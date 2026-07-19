package agentdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeModelResolver struct {
	runtime   store.ModelRuntime
	secret    store.SecretPayload
	modelErr  error
	secretErr error

	userRuntime    store.ModelRuntime
	userErr        error
	userRuntimeSet bool
	userErrSet     bool

	// Call counters let shared-binding tests assert that the per-user
	// resolver is never invoked when an agent has a workspace secret.
	resolveCalls     int
	resolveUserCalls int
}

func (f *fakeModelResolver) ResolveModelRuntime(_ context.Context, _, _ string) (store.ModelRuntime, error) {
	f.resolveCalls++
	if f.modelErr != nil {
		return store.ModelRuntime{}, f.modelErr
	}
	return f.runtime, nil
}

func (f *fakeModelResolver) ResolveModelRuntimeForUser(_ context.Context, _, _ string) (store.ModelRuntime, error) {
	f.resolveUserCalls++
	if f.userErrSet {
		// Mirror the real store, which still returns the partially-resolved
		// mr (CredentialMode + CredentialKindCode) when the per-user lookup
		// fails so callers can emit credential-form notices.
		if f.userRuntimeSet {
			return f.userRuntime, f.userErr
		}
		return f.runtime, f.userErr
	}
	if f.userRuntimeSet {
		return f.userRuntime, nil
	}
	if f.modelErr != nil {
		return store.ModelRuntime{}, f.modelErr
	}
	return f.runtime, nil
}

func (f *fakeModelResolver) GetSecretPayload(_ context.Context, _, _ string) (store.SecretPayload, error) {
	if f.secretErr != nil {
		return store.SecretPayload{}, f.secretErr
	}
	return f.secret, nil
}

func TestStreamPrompt_ManagedAnthropicModelInjection(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-platform"})
	if err != nil {
		t.Fatal(err)
	}

	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()
	defer sess.Close("test done")

	binder := binding.NewInMemoryBinder()
	if err := binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-1",
		AgentID:        "pa-1",
		DeviceID:       "dev-1",
		AgentKind:      "claude_code",
		WorkDir:        "/workspace",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:        "model-1",
			ModelKey:       "claude-opus-4-7",
			ProviderType:   "anthropic-compatible",
			Adapter:        "@ai-sdk/anthropic",
			BaseURL:        "https://api.example.com/anthropic",
			SecretID:       "secret-1",
			ProviderConfig: map[string]any{"auth_scheme": "bearer", "headers": map[string]any{"X-Sub-Module": "claude-code-internal"}},
		},
		secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc},
	}
	c := New(Config{Registry: reg, Binder: binder, ModelResolver: &resolver, Secrets: svc})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{
		"default_model_id": "agent-default",
		"model_id":         "model-1",
		"model":            "manual-model-should-lose",
		"env": map[string]any{
			"PARSAR_KEEP":       "yes",
			"ANTHROPIC_API_KEY": "manual-should-lose",
			"OTHER_FLAG":        "kept",
		},
	}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request never written")
	}

	var payload proto.PromptRequestPayload
	for _, env := range conn.Writes() {
		if env.Type != proto.TypePromptRequest {
			continue
		}
		if err := env.DecodePayload(&payload); err != nil {
			t.Fatalf("decode prompt_request: %v", err)
		}
	}
	if payload.AgentOptions["model"] != "claude-opus-4-7" {
		t.Fatalf("agent_options.model=%v, want platform model key", payload.AgentOptions["model"])
	}
	envMap, ok := payload.AgentOptions["env"].(map[string]any)
	if !ok {
		t.Fatalf("agent_options.env has type %T", payload.AgentOptions["env"])
	}
	checks := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "sk-platform",
		"ANTHROPIC_BASE_URL":   "https://api.example.com/anthropic",
		"OTHER_FLAG":           "kept",
	}
	for k, want := range checks {
		if got := envMap[k]; got != want {
			t.Fatalf("env[%s]=%v, want %q; full env=%+v", k, got, want, envMap)
		}
	}
	if got, _ := envMap["ANTHROPIC_CUSTOM_HEADERS"].(string); !strings.Contains(got, "X-Sub-Module: claude-code-internal") {
		t.Fatalf("ANTHROPIC_CUSTOM_HEADERS=%q", got)
	}
	if _, ok := envMap["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("anthropic-compatible provider must remove ANTHROPIC_API_KEY; full env=%+v", envMap)
	}
	if got := envMap["PARSAR_KEEP"]; got != "yes" {
		t.Fatalf("user-supplied env must pass through managed merge; PARSAR_KEEP=%v full env=%+v", got, envMap)
	}

	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	_, _ = drainEvents(ch, t)
}

func TestStreamPrompt_ManagedOpenCodeModelInjection(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-openai-platform"})
	if err != nil {
		t.Fatal(err)
	}

	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()
	defer sess.Close("test done")
	feedAgentKinds(t, conn, sess, []proto.SupportedAgentKind{
		{Kind: "claude_code", Available: true},
		{Kind: "opencode", Available: true, Version: "opencode 1.4.3", Capabilities: proto.AgentKindCapabilities{Streaming: true, Usage: true}},
	})

	binder := binding.NewInMemoryBinder()
	if err := binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-1",
		AgentID:        "pa-1",
		DeviceID:       "dev-1",
		AgentKind:      "opencode",
		WorkDir:        "/workspace",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:      "model-openai",
			ModelKey:     "gpt-4o-mini",
			ModelName:    "GPT-4o mini",
			ProviderType: "openai",
			Adapter:      "@ai-sdk/openai",
			BaseURL:      "https://api.openai-proxy.example/v1",
			SecretID:     "secret-openai",
		},
		secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc},
	}
	c := New(Config{Registry: reg, Binder: binder, ModelResolver: &resolver, Secrets: svc})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{
		"default_model_id": "agent-default",
		"agent_kind":       "opencode",
		"model_id":         "model-openai",
		"env": map[string]any{
			"PARSAR_KEEP": "yes",
			"OTHER_FLAG":  "kept",
		},
	}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request never written")
	}

	var payload proto.PromptRequestPayload
	for _, env := range conn.Writes() {
		if env.Type != proto.TypePromptRequest {
			continue
		}
		if err := env.DecodePayload(&payload); err != nil {
			t.Fatalf("decode prompt_request: %v", err)
		}
	}
	if payload.AgentKind != "opencode" {
		t.Fatalf("prompt_request agent_kind=%q, want opencode", payload.AgentKind)
	}
	if payload.AgentOptions["model"] != "gpt-4o-mini" {
		t.Fatalf("agent_options.model=%v, want platform model key", payload.AgentOptions["model"])
	}
	if payload.AgentOptions["model_selector"] != "gpt-4o-mini" {
		t.Fatalf("agent_options.model_selector=%v, want platform model key", payload.AgentOptions["model_selector"])
	}
	envMap, ok := payload.AgentOptions["env"].(map[string]any)
	if !ok {
		t.Fatalf("agent_options.env has type %T", payload.AgentOptions["env"])
	}
	if got := envMap["OTHER_FLAG"]; got != "kept" {
		t.Fatalf("env[OTHER_FLAG]=%v, want kept; full env=%+v", got, envMap)
	}
	if got := envMap["PARSAR_KEEP"]; got != "yes" {
		t.Fatalf("user-supplied env must pass through managed merge; PARSAR_KEEP=%v full env=%+v", got, envMap)
	}
	rawConfig, ok := payload.AgentOptions["opencode_json"].(string)
	if !ok || strings.TrimSpace(rawConfig) == "" {
		t.Fatalf("agent_options.opencode_json missing: %#v", payload.AgentOptions["opencode_json"])
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(rawConfig), &cfg); err != nil {
		t.Fatalf("unmarshal opencode_json: %v", err)
	}
	providers, ok := cfg["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider block missing: %+v", cfg)
	}
	provider, ok := providers["openai"].(map[string]any)
	if !ok {
		t.Fatalf("openai provider missing: %+v", providers)
	}
	if provider["npm"] != "@ai-sdk/openai" {
		t.Fatalf("provider.npm=%v, want @ai-sdk/openai", provider["npm"])
	}
	options, ok := provider["options"].(map[string]any)
	if !ok {
		t.Fatalf("provider.options missing: %+v", provider)
	}
	if options["apiKey"] != "sk-openai-platform" {
		t.Fatalf("provider.options.apiKey=%v, want injected key", options["apiKey"])
	}
	if options["baseURL"] != "https://api.openai-proxy.example/v1" {
		t.Fatalf("provider.options.baseURL=%v", options["baseURL"])
	}
	models, ok := provider["models"].(map[string]any)
	if !ok {
		t.Fatalf("provider.models missing: %+v", provider)
	}
	if _, ok := models["gpt-4o-mini"]; !ok {
		t.Fatalf("provider.models missing gpt-4o-mini: %+v", models)
	}

	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	_, _ = drainEvents(ch, t)
}

func TestStreamPrompt_ManagedModelUnsupportedProviderDoesNotAcquireSandbox(t *testing.T) {
	svc, err := secrets.New("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-openai"})
	if err != nil {
		t.Fatal(err)
	}
	resolver := fakeModelResolver{runtime: store.ModelRuntime{
		ModelID:      "model-openai",
		ModelKey:     "gpt-4o",
		ProviderType: "openai",
		Adapter:      "@ai-sdk/openai",
		SecretID:     "secret-openai",
	}, secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc}}
	sb := &stubSandboxProvider{deviceID: "dev-sandbox"}
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder(), Sandbox: sb, ModelResolver: &resolver, Secrets: svc})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{"daemon_mode": "sandbox", "model_id": "model-openai"}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt should return error channel, got hard err: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || !strings.Contains(gotErr.Error, ErrManagedModelUnsupported.Error()) {
		t.Fatalf("expected unsupported model error, got err=%+v done=%+v", gotErr, gotDone)
	}
	if gotDone == nil {
		t.Fatalf("expected done event")
	}
	if sb.acquireCalls != 0 {
		t.Fatalf("sandbox Acquire must not fire when model injection fails; saw %d", sb.acquireCalls)
	}
}

// ----------------------------------------------------------------------
// applySpecMemoryInjection
// ----------------------------------------------------------------------

// fakeSpecMemory is a minimal stub of SpecMemoryInjector that records
// the args it was called with and returns canned (text, err). Lets us
// verify both the "happy path appends" and "render error swallowed"
// branches without standing up a real *specmemory.Service.
type fakeSpecMemory struct {
	text  string
	err   error
	calls int
	gotWS string
	gotU  string
}

func (f *fakeSpecMemory) RenderSessionPrompt(_ context.Context, workspaceID, userID string) (string, error) {
	f.calls++
	f.gotWS = workspaceID
	f.gotU = userID
	return f.text, f.err
}

func newInjectionTestConn(t *testing.T, sm SpecMemoryInjector) *Connector {
	t.Helper()
	return New(Config{
		Registry:   gateway.NewRegistry(),
		Binder:     binding.NewInMemoryBinder(),
		SpecMemory: sm,
	})
}

// ----------------------------------------------------------------------
// injectManagedModel auth_scheme
// ----------------------------------------------------------------------

func TestInjectManagedModel_DefaultAuthScheme_InjectsAPIKey(t *testing.T) {
	// When provider config has no auth_scheme (or empty), the API key
	// must be injected as ANTHROPIC_API_KEY (standard x-api-key header).
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-test-default"})
	if err != nil {
		t.Fatal(err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:        "model-default",
			ModelKey:       "claude-sonnet-4-20250514",
			ProviderType:   "anthropic-compatible",
			Adapter:        "@ai-sdk/anthropic",
			BaseURL:        "https://proxy.example.com/v1",
			SecretID:       "secret-d",
			ProviderConfig: map[string]any{}, // no auth_scheme
		},
		secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc},
	}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		ModelResolver: &resolver,
		Secrets:       svc,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{"model_id": "model-default"}
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "claude_code"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}

	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("opts[env] type %T", opts["env"])
	}
	if got := env["ANTHROPIC_API_KEY"]; got != "sk-test-default" {
		t.Fatalf("ANTHROPIC_API_KEY=%v, want sk-test-default", got)
	}
	if _, exists := env["ANTHROPIC_AUTH_TOKEN"]; exists {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be set when auth_scheme is default")
	}
}

func TestInjectManagedModel_BearerAuthScheme_InjectsAuthToken(t *testing.T) {
	// When provider config has auth_scheme="bearer", the API key
	// must be injected as ANTHROPIC_AUTH_TOKEN (Authorization: Bearer).
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-test-bearer"})
	if err != nil {
		t.Fatal(err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:      "model-bearer",
			ModelKey:     "claude-opus-4-7",
			ProviderType: "anthropic-compatible",
			Adapter:      "@ai-sdk/anthropic",
			BaseURL:      "https://api.example.com/anthropic",
			SecretID:     "secret-b",
			ProviderConfig: map[string]any{
				"auth_scheme": "bearer",
			},
		},
		secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc},
	}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		ModelResolver: &resolver,
		Secrets:       svc,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{"model_id": "model-bearer"}
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "claude_code"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}

	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("opts[env] type %T", opts["env"])
	}
	if got := env["ANTHROPIC_AUTH_TOKEN"]; got != "sk-test-bearer" {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN=%v, want sk-test-bearer", got)
	}
	if _, exists := env["ANTHROPIC_API_KEY"]; exists {
		t.Fatalf("ANTHROPIC_API_KEY should not be set when auth_scheme is bearer")
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://api.example.com/anthropic" {
		t.Fatalf("ANTHROPIC_BASE_URL=%v", got)
	}
	if got := opts["model"]; got != "claude-opus-4-7" {
		t.Fatalf("model=%v, want claude-opus-4-7", got)
	}
}

// ----------------------------------------------------------------------
// applySpecMemoryInjection
// ----------------------------------------------------------------------

func TestApplySpecMemoryInjection_NilSpecMemorySkips(t *testing.T) {
	c := newInjectionTestConn(t, nil)
	opts := map[string]any{"system_prompt": "base"}
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	if got := opts["system_prompt"]; got != "base" {
		t.Fatalf("nil injector must be a no-op; system_prompt=%v", got)
	}
}

func TestApplySpecMemoryInjection_MissingIdentityKeysSkips(t *testing.T) {
	cases := []struct {
		name string
		ws   string
		user string
	}{
		{name: "no workspace", ws: "", user: "user-1"},
		{name: "no user", ws: "ws-1", user: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := &fakeSpecMemory{text: "should-not-appear"}
			c := newInjectionTestConn(t, sm)
			opts := map[string]any{"system_prompt": "base"}
			in := basicInput()
			in.WorkspaceID = tc.ws
			in.ConversationInitiatorID = tc.user

			c.applySpecMemoryInjection(context.Background(), opts, in)

			if sm.calls != 0 {
				t.Fatalf("injector must not be called when identity is incomplete; calls=%d", sm.calls)
			}
			if got := opts["system_prompt"]; got != "base" {
				t.Fatalf("system_prompt mutated despite skip; got %v", got)
			}
		})
	}
}

func TestApplySpecMemoryInjection_OverrideWinsAndSkipsInjection(t *testing.T) {
	// plan §2.2: override_system_prompt is the highest-priority signal.
	// When the user has explicitly overridden, we MUST NOT append on
	// top — the override is meant to fully replace the system prompt.
	sm := &fakeSpecMemory{text: "should-not-appear"}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{
		"system_prompt":          "base",
		"override_system_prompt": "user-took-control",
	}
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	if sm.calls != 0 {
		t.Fatalf("injector must not be called when override is set; calls=%d", sm.calls)
	}
	if got := opts["system_prompt"]; got != "base" {
		t.Fatalf("system_prompt mutated despite override; got %v", got)
	}
}

func TestApplySpecMemoryInjection_RenderErrorIsSwallowed(t *testing.T) {
	// plan §9 risk #1 (fail-soft): a render error must NOT break the
	// turn. We log + proceed with the un-injected system_prompt.
	sm := &fakeSpecMemory{err: errors.New("db down")}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{"system_prompt": "base"}
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	if sm.calls != 1 {
		t.Fatalf("expected one render call, got %d", sm.calls)
	}
	if got := opts["system_prompt"]; got != "base" {
		t.Fatalf("system_prompt mutated after render error; got %v", got)
	}
}

func TestApplySpecMemoryInjection_EmptyResultIsNoOp(t *testing.T) {
	sm := &fakeSpecMemory{text: ""}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{"system_prompt": "base"}
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	if got := opts["system_prompt"]; got != "base" {
		t.Fatalf("system_prompt should be untouched when injection is empty; got %v", got)
	}
}

func TestApplySpecMemoryInjection_AppendsOntoBase(t *testing.T) {
	sm := &fakeSpecMemory{text: "<spec>x</spec>"}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{"system_prompt": "base prompt"}
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	want := "base prompt\n\n<spec>x</spec>"
	if got := opts["system_prompt"]; got != want {
		t.Fatalf("system_prompt=%q, want %q", got, want)
	}
	if sm.gotWS != "ws-1" || sm.gotU != "user-1" {
		t.Fatalf("injector got ws=%q user=%q; want ws-1/user-1",
			sm.gotWS, sm.gotU)
	}
}

func TestApplySpecMemoryInjection_ReplacesEmptyBase(t *testing.T) {
	// When there is no agent-side system_prompt, the injection bundle
	// becomes the prompt itself — no leading "\n\n" garbage.
	sm := &fakeSpecMemory{text: "<spec>y</spec>"}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{} // no system_prompt at all
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"

	c.applySpecMemoryInjection(context.Background(), opts, in)

	if got := opts["system_prompt"]; got != "<spec>y</spec>" {
		t.Fatalf("system_prompt=%q, want bare injection", got)
	}
}

// ----------------------------------------------------------------------
// credential_ref model: missing-credential emit
// ----------------------------------------------------------------------

func TestInjectManagedModel_CredentialRef_EmitsNoticeOnUserLookupErr(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "claude-sonnet-4-5",
			ModelName:          "Claude Sonnet 4.5",
			ProviderType:       "anthropic",
			Adapter:            "@ai-sdk/anthropic",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "anthropic_api_key",
		},
		userErrSet: true,
		userErr:    errors.New("user has not configured credential for kind \"anthropic_api_key\""),
	}
	sm := &fakeSystemMessageStore{}
	c := New(Config{
		Registry:       gateway.NewRegistry(),
		Binder:         binding.NewInMemoryBinder(),
		ModelResolver:  &resolver,
		Secrets:        svc,
		SystemMessages: sm,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-b"
	in.AgentConfig = map[string]any{"model_id": "model-byok"}
	opts := renderStaticAgentOptions(in)

	err = c.injectManagedModel(context.Background(), in, opts, "claude_code")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, ErrManagedModelPersonalCredMissing) {
		t.Fatalf("err = %v, want wrapped ErrManagedModelPersonalCredMissing", err)
	}

	if len(sm.runtimeErrors) != 1 {
		t.Fatalf("expected 1 system_message, got %d: %+v", len(sm.runtimeErrors), sm.runtimeErrors)
	}
	msg := sm.runtimeErrors[0]
	if msg.SubKind != CapabilityCredentialMissing {
		t.Errorf("SubKind = %q, want %q", msg.SubKind, CapabilityCredentialMissing)
	}
	if msg.CapabilityID != ModelCredentialMissingCapabilityID {
		t.Errorf("CapabilityID = %q, want sentinel %q", msg.CapabilityID, ModelCredentialMissingCapabilityID)
	}
	if msg.CredentialKind != "anthropic_api_key" {
		t.Errorf("CredentialKind = %q, want anthropic_api_key", msg.CredentialKind)
	}
	if !strings.Contains(msg.CapabilityName, "anthropic/claude-sonnet-4-5") {
		t.Errorf("CapabilityName should contain provider/model_key, got %q", msg.CapabilityName)
	}
	if msg.ConversationID != "conv-1" || msg.RunID != "run-1" {
		t.Errorf("notice missing run scope: %+v", msg)
	}
}

func TestInjectManagedModel_CredentialRef_EmitsNoticeOnEmptyPayload(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "gpt-4o",
			ProviderType:       "openai",
			Adapter:            "@ai-sdk/openai",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "openai_api_key",
		},
		userRuntimeSet: true,
		userRuntime: store.ModelRuntime{
			ModelID:            "model-byok",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "openai_api_key",
		},
	}
	sm := &fakeSystemMessageStore{}
	c := New(Config{
		Registry:       gateway.NewRegistry(),
		Binder:         binding.NewInMemoryBinder(),
		ModelResolver:  &resolver,
		Secrets:        svc,
		SystemMessages: sm,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-b"
	in.AgentConfig = map[string]any{"model_id": "model-byok"}
	opts := renderStaticAgentOptions(in)

	err = c.injectManagedModel(context.Background(), in, opts, "claude_code")
	if !errors.Is(err, ErrManagedModelPersonalCredMissing) {
		t.Fatalf("err = %v, want wrapped ErrManagedModelPersonalCredMissing", err)
	}
	if len(sm.runtimeErrors) != 1 {
		t.Fatalf("expected 1 system_message, got %d", len(sm.runtimeErrors))
	}
	if got := sm.runtimeErrors[0].CredentialKind; got != "openai_api_key" {
		t.Errorf("CredentialKind = %q, want openai_api_key", got)
	}
}

func TestInjectManagedModel_CredentialRef_UserIDMissingDoesNotEmit(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}

	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "claude-sonnet-4-5",
			ProviderType:       "anthropic",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "anthropic_api_key",
		},
	}
	sm := &fakeSystemMessageStore{}
	c := New(Config{
		Registry:       gateway.NewRegistry(),
		Binder:         binding.NewInMemoryBinder(),
		ModelResolver:  &resolver,
		Secrets:        svc,
		SystemMessages: sm,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = ""
	in.AgentConfig = map[string]any{"model_id": "model-byok"}
	opts := renderStaticAgentOptions(in)

	err = c.injectManagedModel(context.Background(), in, opts, "claude_code")
	if !errors.Is(err, ErrManagedModelUserIDMissing) {
		t.Fatalf("err = %v, want wrapped ErrManagedModelUserIDMissing", err)
	}
	if len(sm.runtimeErrors) != 0 {
		t.Fatalf("expected 0 system_messages, got %d: %+v", len(sm.runtimeErrors), sm.runtimeErrors)
	}
}

func TestBuildModelCredentialCapabilityName_Fallbacks(t *testing.T) {
	cases := []struct {
		name string
		mr   store.ModelRuntime
		want string
	}{
		{
			name: "provider and model_key",
			mr:   store.ModelRuntime{ProviderType: "anthropic", ModelKey: "claude-sonnet-4-5"},
			want: "Model · anthropic/claude-sonnet-4-5",
		},
		{
			name: "only model_key",
			mr:   store.ModelRuntime{ModelKey: "claude-sonnet-4-5"},
			want: "Model · claude-sonnet-4-5",
		},
		{
			name: "only model_name",
			mr:   store.ModelRuntime{ModelName: "Claude Sonnet 4.5"},
			want: "Model · Claude Sonnet 4.5",
		},
		{
			name: "no identifiers",
			mr:   store.ModelRuntime{},
			want: "Model",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildModelCredentialCapabilityName(tc.mr); got != tc.want {
				t.Errorf("buildModelCredentialCapabilityName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestIsOpenAICompatibleRuntime pins which provider_type / adapter
// slugs the codex injector accepts. Anything that isn't OpenAI's
// Responses API shape must fall through to ErrManagedModelUnsupported.
func TestIsOpenAICompatibleRuntime(t *testing.T) {
	cases := []struct {
		name string
		mr   store.ModelRuntime
		want bool
	}{
		{"openai provider", store.ModelRuntime{ProviderType: "openai"}, true},
		{"openai-compatible provider", store.ModelRuntime{ProviderType: "openai-compatible"}, true},
		{"azure-openai provider", store.ModelRuntime{ProviderType: "azure-openai"}, true},
		{"@ai-sdk/openai adapter only", store.ModelRuntime{Adapter: "@ai-sdk/openai"}, true},
		{"@ai-sdk/azure adapter only", store.ModelRuntime{Adapter: "@ai-sdk/azure"}, true},
		{"endpoint types openai", store.ModelRuntime{ProviderConfig: map[string]any{"supported_endpoint_types": []any{"openai"}}}, true},
		{"endpoint types openai response", store.ModelRuntime{ProviderConfig: map[string]any{"supported_endpoint_types": []any{"openai-response"}}}, true},
		{"whitespace + case-insensitive", store.ModelRuntime{ProviderType: " Openai "}, true},
		{"anthropic provider rejected", store.ModelRuntime{ProviderType: "anthropic", Adapter: "@ai-sdk/anthropic"}, false},
		{"empty", store.ModelRuntime{}, false},
		{"unknown provider", store.ModelRuntime{ProviderType: "cohere"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOpenAICompatibleRuntime(tc.mr); got != tc.want {
				t.Fatalf("isOpenAICompatibleRuntime(%+v) = %v, want %v", tc.mr, got, tc.want)
			}
		})
	}
}

func TestIsAnthropicRuntimeSupportsEndpointTypes(t *testing.T) {
	if !isAnthropicRuntime(store.ModelRuntime{
		Adapter:        "@ai-sdk/openai-compatible",
		ProviderConfig: map[string]any{"supported_endpoint_types": []any{"anthropic", "openai"}},
	}) {
		t.Fatalf("expected supported_endpoint_types to allow anthropic runtime")
	}
}

// TestInjectCodexManagedModel_OpenAIHappyPath asserts the contract the
// daemon's codex adapter relies on:
//
//   - opts["model"] = mr.ModelKey
//   - opts["codex_provider"] carries the full provider block (base_url,
//     bearer_token, wire_api) the daemon writes to
//     CODEX_HOME/config.toml.
//   - http_headers + query_params flatten from mr.ProviderConfig
func TestInjectCodexManagedModel_OpenAIHappyPath(t *testing.T) {
	opts := map[string]any{
		"env": map[string]any{
			"OTHER_FLAG": "kept",
		},
	}
	mr := store.ModelRuntime{
		ModelID:      "model-openai",
		ModelKey:     "gpt-5.5",
		ModelName:    "GPT-5.5",
		ProviderType: "openai-compatible",
		Adapter:      "@ai-sdk/openai-compatible",
		BaseURL:      "https://platform-api.example.com/v1",
		ProviderConfig: map[string]any{
			"headers": map[string]any{
				"X-Sub-Module": "codex-internal",
			},
		},
	}

	if err := injectCodexManagedModel(opts, mr.ModelID, mr, "sk-platform"); err != nil {
		t.Fatalf("injectCodexManagedModel: %v", err)
	}

	if got := opts["model"]; got != "gpt-5.5" {
		t.Fatalf("opts[model] = %v, want gpt-5.5", got)
	}
	provider, ok := opts["codex_provider"].(map[string]any)
	if !ok {
		t.Fatalf("opts[codex_provider] has type %T", opts["codex_provider"])
	}
	if got := provider["base_url"]; got != "https://platform-api.example.com/v1" {
		t.Fatalf("codex_provider.base_url = %v", got)
	}
	if got := provider["bearer_token"]; got != "sk-platform" {
		t.Fatalf("codex_provider.bearer_token = %v, want platform secret", got)
	}
	if got := provider["wire_api"]; got != "responses" {
		t.Fatalf("codex_provider.wire_api = %v, want responses", got)
	}
	if got := provider["name"]; got != "GPT-5.5" {
		t.Fatalf("codex_provider.name = %v, want display name", got)
	}
	headers, ok := provider["http_headers"].(map[string]string)
	if !ok {
		t.Fatalf("codex_provider.http_headers has type %T", provider["http_headers"])
	}
	if got := headers["X-Sub-Module"]; got != "codex-internal" {
		t.Fatalf("custom header lost: %+v", headers)
	}
	// env is left alone for non-auth keys.
	env, _ := opts["env"].(map[string]any)
	if got := env["OTHER_FLAG"]; got != "kept" {
		t.Fatalf("unrelated env key lost: env=%+v", env)
	}
	if _, hasKey := env["OPENAI_API_KEY"]; hasKey {
		t.Fatalf("OPENAI_API_KEY must NOT be set in env — auth flows through codex_provider.bearer_token: %+v", env)
	}
}

// TestInjectCodexManagedModel_AzureQueryParams pins that an Azure
// model_provider with query_params (api-version=...) survives the
// injection round-trip. Without this, codex would route to Azure with
// no api-version and Azure returns 400.
func TestInjectCodexManagedModel_AzureQueryParams(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{
		ModelID:      "model-azure",
		ModelKey:     "gpt-4o",
		ProviderType: "azure-openai",
		Adapter:      "@ai-sdk/azure",
		BaseURL:      "https://my-resource.openai.azure.com/openai",
		ProviderConfig: map[string]any{
			"query_params": map[string]any{
				"api-version": "2025-04-01-preview",
			},
		},
	}
	if err := injectCodexManagedModel(opts, mr.ModelID, mr, "sk-azure"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	provider := opts["codex_provider"].(map[string]any)
	params, ok := provider["query_params"].(map[string]string)
	if !ok {
		t.Fatalf("codex_provider.query_params has type %T", provider["query_params"])
	}
	if got := params["api-version"]; got != "2025-04-01-preview" {
		t.Fatalf("Azure api-version lost: %+v", params)
	}
}

// TestInjectCodexManagedModel_NonOpenAIRejected confirms the gate
// reviewer asked for: a codex agent pointed at an Anthropic model
// must surface ErrManagedModelUnsupported so the credential-form flow
// can guide the admin to a compatible provider.
func TestInjectCodexManagedModel_NonOpenAIRejected(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{
		ModelID:      "model-anthropic",
		ModelKey:     "claude-opus-4-7",
		ProviderType: "anthropic-compatible",
		Adapter:      "@ai-sdk/anthropic",
		BaseURL:      "https://api.anthropic.com",
	}
	err := injectCodexManagedModel(opts, mr.ModelID, mr, "sk-x")
	if err == nil {
		t.Fatal("expected ErrManagedModelUnsupported for anthropic provider, got nil")
	}
	if !errors.Is(err, ErrManagedModelUnsupported) {
		t.Fatalf("expected ErrManagedModelUnsupported, got %v", err)
	}
	// On rejection, opts must not be partially mutated — the caller
	// should observe a clean failure, not "model set, provider unset".
	if _, ok := opts["model"]; ok {
		t.Fatalf("opts[model] must not be set on rejection: %+v", opts)
	}
	if _, ok := opts["codex_provider"]; ok {
		t.Fatalf("opts[codex_provider] must not be set on rejection: %+v", opts)
	}
}

// TestInjectCodexManagedModel_NoBaseURLRejected guards against the
// half-built provider block: without base_url, the codex daemon can't
// write a valid [model_providers.parsar] section, so we fail loud at
// the server boundary rather than push a broken config to the runtime.
func TestInjectCodexManagedModel_NoBaseURLRejected(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{
		ModelID:      "model-openai",
		ModelKey:     "gpt-4o-mini",
		ProviderType: "openai",
		Adapter:      "@ai-sdk/openai",
		// BaseURL deliberately empty
	}
	err := injectCodexManagedModel(opts, mr.ModelID, mr, "sk-platform")
	if err == nil {
		t.Fatal("expected ErrManagedModelConfigInvalid for missing base_url, got nil")
	}
	if !errors.Is(err, ErrManagedModelConfigInvalid) {
		t.Fatalf("expected ErrManagedModelConfigInvalid, got %v", err)
	}
}

// ----------------------------------------------------------------------
// injectPiManagedModel
// ----------------------------------------------------------------------

// TestInjectPiManagedModel_AnthropicHappyPath pins the Option-B contract
// pi's daemon adapter relies on. The previous design emitted pi's builtin
// "<provider>/<model>" selector and dropped mr.BaseURL, so the platform
// proxy key was sent to api.anthropic.com → 401 invalid x-api-key.
//
// The injector now mirrors codex: it stamps a structured opts["pi_provider"]
// block (base_url + api protocol + headers) that the daemon materialises
// into a models.json "parsar" provider, sets opts["model"] to the
// provider-qualified "parsar/<model_key>" selector, and rides the secret on
// opts["env"][PARSAR_PI_API_KEY] (referenced by api_key_env) so it never
// leaks through --api-key argv.
func TestInjectPiManagedModel_AnthropicHappyPath(t *testing.T) {
	opts := map[string]any{
		"env": map[string]any{"OTHER_FLAG": "kept"},
	}
	mr := store.ModelRuntime{
		ModelID:      "model-anthropic",
		ModelKey:     "claude-opus-4-7",
		ModelName:    "Claude Opus 4.7",
		ProviderType: "anthropic-compatible",
		Adapter:      "@ai-sdk/anthropic",
		BaseURL:      "https://platform-api.example.com",
		ProviderConfig: map[string]any{
			"headers": map[string]any{
				"X-Sub-Module": "claude-code-internal",
			},
		},
	}
	if err := injectPiManagedModel(opts, mr.ModelID, mr, "sk-pi"); err != nil {
		t.Fatalf("injectPiManagedModel: %v", err)
	}
	// model selects the materialised "parsar" provider (which carries
	// base_url + headers), NOT pi's builtin anthropic provider.
	if got := opts["model"]; got != "parsar/claude-opus-4-7" {
		t.Fatalf("opts[model] = %v, want parsar/claude-opus-4-7", got)
	}
	provider, ok := opts["pi_provider"].(map[string]any)
	if !ok {
		t.Fatalf("opts[pi_provider] has type %T, want map[string]any", opts["pi_provider"])
	}
	if got := provider["base_url"]; got != "https://platform-api.example.com" {
		t.Fatalf("pi_provider.base_url = %v, want the platform base_url", got)
	}
	if got := provider["api"]; got != "anthropic-messages" {
		t.Fatalf("pi_provider.api = %v, want anthropic-messages", got)
	}
	if got := provider["api_key_env"]; got != "PARSAR_PI_API_KEY" {
		t.Fatalf("pi_provider.api_key_env = %v, want PARSAR_PI_API_KEY", got)
	}
	headers, ok := provider["headers"].(map[string]string)
	if !ok {
		t.Fatalf("pi_provider.headers has type %T, want map[string]string", provider["headers"])
	}
	if got := headers["X-Sub-Module"]; got != "claude-code-internal" {
		t.Fatalf("custom header lost: %+v", headers)
	}
	// secret rides env (referenced by api_key_env), never --api-key argv.
	env, _ := opts["env"].(map[string]any)
	if got := env["PARSAR_PI_API_KEY"]; got != "sk-pi" {
		t.Fatalf("env[PARSAR_PI_API_KEY] = %v, want sk-pi", got)
	}
	if got := env["OTHER_FLAG"]; got != "kept" {
		t.Fatalf("unrelated env key lost: %+v", env)
	}
	if _, hasKey := opts["api_key"]; hasKey {
		t.Fatalf("opts[api_key] must be absent — pi auth flows through models.json apiKey env ref, not --api-key: %+v", opts)
	}
}

// TestInjectPiManagedModel_ProviderMapping pins the provider_type/adapter
// → pi `api` protocol mapping. opts["model"] is ALWAYS the fixed "parsar"
// provider — the daemon materialises one provider per run regardless of the
// upstream family; the family only selects which wire protocol that
// provider speaks (anthropic-messages / openai-completions /
// google-generative-ai — the three custom-provider APIs pi documents).
func TestInjectPiManagedModel_ProviderMapping(t *testing.T) {
	cases := []struct {
		name    string
		mr      store.ModelRuntime
		wantAPI string
	}{
		{"openai", store.ModelRuntime{ModelKey: "gpt-4o", ProviderType: "openai", BaseURL: "https://x/v1"}, "openai-completions"},
		{"openai-compatible adapter", store.ModelRuntime{ModelKey: "gpt-4o-mini", Adapter: "@ai-sdk/openai", BaseURL: "https://x/v1"}, "openai-completions"},
		{"anthropic", store.ModelRuntime{ModelKey: "claude-sonnet-4-5", ProviderType: "anthropic", BaseURL: "https://x"}, "anthropic-messages"},
		{"google", store.ModelRuntime{ModelKey: "gemini-2.5-pro", ProviderType: "google", BaseURL: "https://x"}, "google-generative-ai"},
		{"gemini alias", store.ModelRuntime{ModelKey: "gemini-2.5-flash", ProviderType: "gemini", BaseURL: "https://x"}, "google-generative-ai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := map[string]any{}
			if err := injectPiManagedModel(opts, "model-x", tc.mr, "sk-x"); err != nil {
				t.Fatalf("inject: %v", err)
			}
			if got := opts["model"]; got != "parsar/"+tc.mr.ModelKey {
				t.Fatalf("opts[model] = %v, want parsar/%s", got, tc.mr.ModelKey)
			}
			provider, ok := opts["pi_provider"].(map[string]any)
			if !ok {
				t.Fatalf("opts[pi_provider] has type %T", opts["pi_provider"])
			}
			if got := provider["api"]; got != tc.wantAPI {
				t.Fatalf("pi_provider.api = %v, want %v", got, tc.wantAPI)
			}
		})
	}
}

// TestInjectPiManagedModel_UnmappedProviderRejected confirms an
// unrecognised provider surfaces ErrManagedModelUnsupported (so the
// credential-form flow can steer the admin) and leaves opts untouched.
func TestInjectPiManagedModel_UnmappedProviderRejected(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{
		ModelID:      "model-cohere",
		ModelKey:     "command-r",
		ProviderType: "cohere",
	}
	err := injectPiManagedModel(opts, mr.ModelID, mr, "sk-x")
	if err == nil {
		t.Fatal("expected ErrManagedModelUnsupported for unmapped provider, got nil")
	}
	if !errors.Is(err, ErrManagedModelUnsupported) {
		t.Fatalf("expected ErrManagedModelUnsupported, got %v", err)
	}
	if _, ok := opts["model"]; ok {
		t.Fatalf("opts[model] must not be set on rejection: %+v", opts)
	}
	if _, ok := opts["api_key"]; ok {
		t.Fatalf("opts[api_key] must not be set on rejection: %+v", opts)
	}
}

// TestInjectPiManagedModel_MissingModelKeyRejected guards the
// half-built selector: pi requires --api-key to be paired with --model,
// so an empty model_key must fail loud rather than emit "<provider>/".
func TestInjectPiManagedModel_MissingModelKeyRejected(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{ModelID: "model-x", ProviderType: "anthropic"}
	err := injectPiManagedModel(opts, mr.ModelID, mr, "sk-x")
	if err == nil {
		t.Fatal("expected ErrManagedModelConfigInvalid for missing model_key, got nil")
	}
	if !errors.Is(err, ErrManagedModelConfigInvalid) {
		t.Fatalf("expected ErrManagedModelConfigInvalid, got %v", err)
	}
}

// TestInjectPiManagedModel_NoBaseURLRejected guards the half-built
// provider, mirroring the codex rule: without base_url the daemon can't
// materialise a models.json "parsar" provider, and pi would silently fall
// back to the upstream default endpoint (api.anthropic.com) with the
// platform proxy key → 401. Fail loud at the server boundary instead.
func TestInjectPiManagedModel_NoBaseURLRejected(t *testing.T) {
	opts := map[string]any{}
	mr := store.ModelRuntime{
		ModelID:      "model-anthropic",
		ModelKey:     "claude-opus-4-7",
		ProviderType: "anthropic-compatible",
		Adapter:      "@ai-sdk/anthropic",
		// BaseURL deliberately empty
	}
	err := injectPiManagedModel(opts, mr.ModelID, mr, "sk-pi")
	if err == nil {
		t.Fatal("expected ErrManagedModelConfigInvalid for missing base_url, got nil")
	}
	if !errors.Is(err, ErrManagedModelConfigInvalid) {
		t.Fatalf("expected ErrManagedModelConfigInvalid, got %v", err)
	}
	if _, ok := opts["model"]; ok {
		t.Fatalf("opts[model] must not be set on rejection: %+v", opts)
	}
	if _, ok := opts["pi_provider"]; ok {
		t.Fatalf("opts[pi_provider] must not be set on rejection: %+v", opts)
	}
}

// TestInjectManagedModel_PiSwitchWired drives the full
// c.injectManagedModel path with agent_kind="pi" to prove the switch
// dispatches to injectPiManagedModel (without a case "pi" it falls
// through to ErrUnsupportedAgentKind).
func TestInjectManagedModel_PiSwitchWired(t *testing.T) {
	const masterKey = "test-master-key"
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-pi"})
	if err != nil {
		t.Fatal(err)
	}
	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:      "model-pi",
			ModelKey:     "claude-opus-4-7",
			ProviderType: "anthropic-compatible",
			Adapter:      "@ai-sdk/anthropic",
			BaseURL:      "https://platform-api.example.com",
			SecretID:     "secret-pi",
		},
		secret: store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: enc},
	}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		ModelResolver: &resolver,
		Secrets:       svc,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.AgentConfig = map[string]any{"model_id": "model-pi"}
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "pi"); err != nil {
		t.Fatalf("injectManagedModel(pi): %v", err)
	}
	if got := opts["model"]; got != "parsar/claude-opus-4-7" {
		t.Fatalf("opts[model] = %v, want parsar/claude-opus-4-7", got)
	}
	if _, ok := opts["pi_provider"].(map[string]any); !ok {
		t.Fatalf("opts[pi_provider] must be set, got %T", opts["pi_provider"])
	}
	env, _ := opts["env"].(map[string]any)
	if got := env["PARSAR_PI_API_KEY"]; got != "sk-pi" {
		t.Fatalf("env[PARSAR_PI_API_KEY] = %v, want sk-pi (secret must ride env, not --api-key)", got)
	}
	if _, ok := opts["api_key"]; ok {
		t.Fatalf("opts[api_key] must be absent for pi: %+v", opts)
	}
}

// ----------------------------------------------------------------------
// model_credential_binding (shared API key on the agent)
// ----------------------------------------------------------------------

// sharedBindingResolver is the canonical setup for the three shared-key
// cases below: a credential_ref model (Anthropic) the agent has bound to
// a workspace secret. The decrypted payload returns "sk-shared".
func sharedBindingResolver(t *testing.T, svc *secrets.Service) *fakeModelResolver {
	t.Helper()
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-shared"})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "claude-opus-4-7",
			ProviderType:       "anthropic-compatible",
			Adapter:            "@ai-sdk/anthropic",
			BaseURL:            "https://api.example.com/anthropic",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "anthropic_api_key",
		},
		secret: store.SecretPayload{
			SecretRead:       store.SecretRead{Status: "active"},
			EncryptedPayload: enc,
		},
	}
}

func sharedBindingConfig() map[string]any {
	return map[string]any{
		"model_id": "model-byok",
		"model_credential_binding": map[string]any{
			"source":    "shared",
			"secret_id": "sec-shared",
		},
	}
}

// Shared binding + caller user_id present (typical: logged-in user
// chatting with a public agent). Must NOT fall back to the per-user
// resolver — the shared key is the agent's contract.
func TestInjectManagedModel_SharedBinding_BypassesUserResolver(t *testing.T) {
	svc, err := secrets.New("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	resolver := *sharedBindingResolver(t, svc)
	sm := &fakeSystemMessageStore{}
	c := New(Config{
		Registry:       gateway.NewRegistry(),
		Binder:         binding.NewInMemoryBinder(),
		ModelResolver:  &resolver,
		Secrets:        svc,
		SystemMessages: sm,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-bound"
	in.AgentConfig = sharedBindingConfig()
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "claude_code"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}
	if resolver.resolveUserCalls != 0 {
		t.Fatalf("shared binding must not query user_credentials; resolveUserCalls=%d", resolver.resolveUserCalls)
	}
	if resolver.resolveCalls != 1 {
		t.Fatalf("expected 1 ResolveModelRuntime call, got %d", resolver.resolveCalls)
	}
	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("opts[env] has type %T", opts["env"])
	}
	if got := env["ANTHROPIC_API_KEY"]; got != "sk-shared" {
		t.Fatalf("ANTHROPIC_API_KEY=%v, want sk-shared", got)
	}
	if len(sm.runtimeErrors) != 0 {
		t.Fatalf("shared-binding happy path must not emit credential-form notice, got %+v", sm.runtimeErrors)
	}
}

// Shared binding + empty initiator (lark guest on a public agent).
// Must succeed via the shared secret — this is exactly what public
// agents are designed for.
func TestInjectManagedModel_SharedBinding_GuestCallerSucceeds(t *testing.T) {
	svc, err := secrets.New("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	resolver := *sharedBindingResolver(t, svc)
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		ModelResolver: &resolver,
		Secrets:       svc,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "" // lark guest, no platform user_id
	in.AgentConfig = sharedBindingConfig()
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "claude_code"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}
	if resolver.resolveUserCalls != 0 {
		t.Fatalf("guest + shared binding must not query user_credentials; resolveUserCalls=%d", resolver.resolveUserCalls)
	}
	env, _ := opts["env"].(map[string]any)
	if got := env["ANTHROPIC_API_KEY"]; got != "sk-shared" {
		t.Fatalf("ANTHROPIC_API_KEY=%v, want sk-shared", got)
	}
}

// No binding + caller user_id present: the personal-credential path
// stays intact. Regression guard for the personal IM flow.
func TestInjectManagedModel_NoBinding_PersonalPathUsesUserResolver(t *testing.T) {
	svc, err := secrets.New("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"value": "sk-personal"})
	if err != nil {
		t.Fatal(err)
	}
	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "claude-opus-4-7",
			ProviderType:       "anthropic-compatible",
			Adapter:            "@ai-sdk/anthropic",
			BaseURL:            "https://api.example.com/anthropic",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "anthropic_api_key",
		},
		userRuntimeSet: true,
		userRuntime: store.ModelRuntime{
			ModelID:            "model-byok",
			ModelKey:           "claude-opus-4-7",
			ProviderType:       "anthropic-compatible",
			Adapter:            "@ai-sdk/anthropic",
			BaseURL:            "https://api.example.com/anthropic",
			CredentialMode:     "credential_ref",
			CredentialKindCode: "anthropic_api_key",
			EncryptedPayload:   enc,
		},
	}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		ModelResolver: &resolver,
		Secrets:       svc,
	})

	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-byok"
	in.AgentConfig = map[string]any{"model_id": "model-byok"}
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "claude_code"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}
	if resolver.resolveUserCalls != 1 {
		t.Fatalf("personal path must use ResolveModelRuntimeForUser exactly once; got %d", resolver.resolveUserCalls)
	}
	if resolver.resolveCalls != 0 {
		t.Fatalf("personal path must NOT touch the workspace-only resolver; got %d", resolver.resolveCalls)
	}
	env, _ := opts["env"].(map[string]any)
	if got := env["ANTHROPIC_API_KEY"]; got != "sk-personal" {
		t.Fatalf("ANTHROPIC_API_KEY=%v, want sk-personal", got)
	}
}
