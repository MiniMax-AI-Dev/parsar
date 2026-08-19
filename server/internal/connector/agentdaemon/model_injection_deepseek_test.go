package agentdaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestInjectDeepseekHarnessManagedModel_HappyPath pins the contract the dsh
// adapter materialises into its `--patch` overlay: a declared llm-pi-ai route
// carrying base_url + wire protocol + headers, the model key pinned for
// agent-default-model, and the secret delivered only through opts["env"]
// under the name the route references.
func TestInjectDeepseekHarnessManagedModel_HappyPath(t *testing.T) {
	opts := map[string]any{
		"env": map[string]any{"OTHER_FLAG": "kept"},
	}
	mr := store.ModelRuntime{
		ModelID:      "model-ds",
		ModelKey:     "deepseek-v4",
		ModelName:    "DeepSeek V4",
		ProviderType: "openai-compatible",
		Adapter:      "@ai-sdk/openai-compatible",
		BaseURL:      "https://gateway.example.com/v1",
		ProviderConfig: map[string]any{
			"headers": map[string]any{"X-Sub-Module": "parsar"},
		},
	}
	if err := injectDeepseekHarnessManagedModel(opts, mr.ModelID, mr, "sk-dsh"); err != nil {
		t.Fatalf("injectDeepseekHarnessManagedModel: %v", err)
	}
	if got := opts["model"]; got != "deepseek-v4" {
		t.Fatalf("opts[model] = %v, want deepseek-v4", got)
	}
	provider, ok := opts["dsh_provider"].(map[string]any)
	if !ok {
		t.Fatalf("opts[dsh_provider] has type %T, want map[string]any", opts["dsh_provider"])
	}
	if got := provider["base_url"]; got != "https://gateway.example.com/v1" {
		t.Fatalf("dsh_provider.base_url = %v, want the platform base_url", got)
	}
	if got := provider["api"]; got != "openai-completions" {
		t.Fatalf("dsh_provider.api = %v, want openai-completions", got)
	}
	if got := provider["api_key_env"]; got != "PARSAR_DSH_API_KEY" {
		t.Fatalf("dsh_provider.api_key_env = %v, want PARSAR_DSH_API_KEY", got)
	}
	if got := provider["model"]; got != "deepseek-v4" {
		t.Fatalf("dsh_provider.model = %v, want deepseek-v4", got)
	}
	if got := provider["name"]; got != "DeepSeek V4" {
		t.Fatalf("dsh_provider.name = %v, want DeepSeek V4", got)
	}
	headers, ok := provider["headers"].(map[string]string)
	if !ok {
		t.Fatalf("dsh_provider.headers has type %T, want map[string]string", provider["headers"])
	}
	if got := headers["X-Sub-Module"]; got != "parsar" {
		t.Fatalf("dsh_provider.headers[X-Sub-Module] = %q", got)
	}
	// The key rides the environment only: the overlay file the daemon
	// writes references it by name, so it never lands on dsh's argv.
	if _, ok := provider["api_key"]; ok {
		t.Fatalf("dsh_provider must not carry the raw key: %+v", provider)
	}
	env, ok := opts["env"].(map[string]any)
	if !ok {
		t.Fatalf("opts[env] has type %T", opts["env"])
	}
	if got := env["PARSAR_DSH_API_KEY"]; got != "sk-dsh" {
		t.Fatalf("env[PARSAR_DSH_API_KEY] = %v, want sk-dsh", got)
	}
	if got := env["OTHER_FLAG"]; got != "kept" {
		t.Fatalf("existing env must survive the merge: %+v", env)
	}
}

// TestInjectDeepseekHarnessManagedModel_ProviderMapping pins the
// provider_type / endpoint-type → pi-ai wire protocol mapping the harness's
// llm-pi-ai adapter shares with the pi CLI.
func TestInjectDeepseekHarnessManagedModel_ProviderMapping(t *testing.T) {
	cases := []struct {
		name    string
		mr      store.ModelRuntime
		wantAPI string
	}{
		{
			name:    "anthropic",
			mr:      store.ModelRuntime{ModelKey: "claude-opus-4-7", ProviderType: "anthropic", BaseURL: "https://x.example/anthropic"},
			wantAPI: "anthropic-messages",
		},
		{
			name:    "openai",
			mr:      store.ModelRuntime{ModelKey: "gpt-4o", ProviderType: "openai", BaseURL: "https://x.example/v1"},
			wantAPI: "openai-completions",
		},
		{
			name:    "google",
			mr:      store.ModelRuntime{ModelKey: "gemini-2.5-pro", ProviderType: "google", BaseURL: "https://x.example"},
			wantAPI: "google-generative-ai",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := map[string]any{}
			if err := injectDeepseekHarnessManagedModel(opts, "model-x", tc.mr, "sk-x"); err != nil {
				t.Fatalf("inject: %v", err)
			}
			provider, ok := opts["dsh_provider"].(map[string]any)
			if !ok {
				t.Fatalf("opts[dsh_provider] has type %T", opts["dsh_provider"])
			}
			if got := provider["api"]; got != tc.wantAPI {
				t.Fatalf("dsh_provider.api = %v, want %v", got, tc.wantAPI)
			}
		})
	}
}

// A route the harness has to declare itself needs base_url, api and a model
// id, so each missing piece fails at the server boundary and leaves opts
// clean rather than shipping an overlay dsh refuses at boot.
func TestInjectDeepseekHarnessManagedModel_RejectsIncompleteRuntime(t *testing.T) {
	cases := []struct {
		name    string
		mr      store.ModelRuntime
		wantErr error
	}{
		{
			name:    "unmapped provider",
			mr:      store.ModelRuntime{ModelKey: "cmd-r", ProviderType: "cohere", BaseURL: "https://x.example"},
			wantErr: ErrManagedModelUnsupported,
		},
		{
			name:    "missing model key",
			mr:      store.ModelRuntime{ProviderType: "openai", BaseURL: "https://x.example/v1"},
			wantErr: ErrManagedModelConfigInvalid,
		},
		{
			name:    "missing base url",
			mr:      store.ModelRuntime{ModelKey: "gpt-4o", ProviderType: "openai"},
			wantErr: ErrManagedModelConfigInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := map[string]any{}
			err := injectDeepseekHarnessManagedModel(opts, "model-x", tc.mr, "sk-x")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if len(opts) != 0 {
				t.Fatalf("rejection must leave opts clean: %+v", opts)
			}
		})
	}
}

// TestInjectManagedModel_DeepseekHarnessSwitchWired drives the full
// c.injectManagedModel path so a missing `case "deepseek_harness"` (which
// would fall through to ErrUnsupportedAgentKind and break every run) is
// caught here.
func TestInjectManagedModel_DeepseekHarnessSwitchWired(t *testing.T) {
	svc, err := secrets.New("test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-dsh-platform"})
	if err != nil {
		t.Fatal(err)
	}
	resolver := fakeModelResolver{
		runtime: store.ModelRuntime{
			ModelID:      "model-ds",
			ModelKey:     "deepseek-v4",
			ProviderType: "openai-compatible",
			Adapter:      "@ai-sdk/openai-compatible",
			BaseURL:      "https://gateway.example.com/v1",
			SecretID:     "secret-ds",
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
	in.AgentConfig = map[string]any{"agent_kind": "deepseek_harness", "model_id": "model-ds"}
	opts := renderStaticAgentOptions(in)

	if err := c.injectManagedModel(context.Background(), in, opts, "deepseek_harness"); err != nil {
		t.Fatalf("injectManagedModel: %v", err)
	}
	if got := opts["model"]; got != "deepseek-v4" {
		t.Fatalf("opts[model] = %v, want deepseek-v4", got)
	}
	if _, ok := opts["dsh_provider"].(map[string]any); !ok {
		t.Fatalf("opts[dsh_provider] must be set, got %T", opts["dsh_provider"])
	}
	env, _ := opts["env"].(map[string]any)
	if got := env["PARSAR_DSH_API_KEY"]; got != "sk-dsh-platform" {
		t.Fatalf("env[PARSAR_DSH_API_KEY] = %v, want the decrypted key", got)
	}
}
