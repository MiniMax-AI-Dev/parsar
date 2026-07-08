package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// modelsFile mirrors the subset of pi's models.json schema this adapter
// emits (packages/coding-agent/src/core/model-registry.ts ProviderConfigSchema).
type modelsFile struct {
	Providers map[string]struct {
		Name       string            `json:"name"`
		BaseURL    string            `json:"baseUrl"`
		APIKey     string            `json:"apiKey"`
		API        string            `json:"api"`
		Headers    map[string]string `json:"headers"`
		AuthHeader bool              `json:"authHeader"`
		Models     []struct {
			ID string `json:"id"`
		} `json:"models"`
	} `json:"providers"`
}

func readModelsJSON(t *testing.T, dir string) modelsFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		t.Fatalf("read models.json: %v", err)
	}
	var mf modelsFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatalf("models.json is not valid JSON: %v\n%s", err, raw)
	}
	return mf
}

func TestWritePiModelsJSON_AnthropicProvider(t *testing.T) {
	dir := t.TempDir()
	cfg := piProviderConfig{
		Name:      "Parsar Anthropic",
		BaseURL:   "https://platform-api.example.com",
		API:       "anthropic-messages",
		APIKeyEnv: "PARSAR_PI_API_KEY",
		Model:     "claude-opus-4-6-thinking-max",
		Headers:   map[string]string{"X-Sub-Module": "claude-code-internal"},
	}
	if err := writePiModelsJSON(dir, cfg); err != nil {
		t.Fatalf("writePiModelsJSON: %v", err)
	}

	p, ok := readModelsJSON(t, dir).Providers[piManagedProviderSlug]
	if !ok {
		t.Fatalf("models.json missing provider %q", piManagedProviderSlug)
	}
	if p.BaseURL != cfg.BaseURL {
		t.Errorf("baseUrl = %q, want %q", p.BaseURL, cfg.BaseURL)
	}
	if p.API != "anthropic-messages" {
		t.Errorf("api = %q, want anthropic-messages", p.API)
	}
	// pi's resolveConfigValue() only resolves a "$NAME" template from
	// process.env; a bare string is a literal key. So the env var name must
	// be written with a "$" prefix.
	if p.APIKey != "$PARSAR_PI_API_KEY" {
		t.Errorf("apiKey = %q, want $PARSAR_PI_API_KEY (env ref, with $)", p.APIKey)
	}
	if p.Headers["X-Sub-Module"] != "claude-code-internal" {
		t.Errorf("headers[X-Sub-Module] = %q, want claude-code-internal", p.Headers["X-Sub-Module"])
	}
	if len(p.Models) != 1 || p.Models[0].ID != cfg.Model {
		t.Errorf("models = %+v, want one model id %q", p.Models, cfg.Model)
	}
	if p.Name != cfg.Name {
		t.Errorf("name = %q, want %q", p.Name, cfg.Name)
	}
	// anthropic-messages carries auth via x-api-key (the resolved apiKey),
	// not an Authorization bearer header, so authHeader must stay false.
	if p.AuthHeader {
		t.Errorf("authHeader = true, want false for anthropic-messages")
	}
}

func TestWritePiModelsJSON_OpenAIAuthHeader(t *testing.T) {
	dir := t.TempDir()
	cfg := piProviderConfig{
		BaseURL:    "https://gw.example.com/v1",
		API:        "openai-completions",
		APIKeyEnv:  "PARSAR_PI_API_KEY",
		Model:      "gpt-5.5",
		AuthHeader: true,
	}
	if err := writePiModelsJSON(dir, cfg); err != nil {
		t.Fatalf("writePiModelsJSON: %v", err)
	}
	p := readModelsJSON(t, dir).Providers[piManagedProviderSlug]
	if p.API != "openai-completions" {
		t.Errorf("api = %q, want openai-completions", p.API)
	}
	if !p.AuthHeader {
		t.Errorf("authHeader = false, want true so pi sends Authorization: Bearer")
	}
}

func TestWritePiModelsJSON_RejectsMissingFields(t *testing.T) {
	base := piProviderConfig{
		BaseURL:   "https://x/v1",
		API:       "anthropic-messages",
		APIKeyEnv: "PARSAR_PI_API_KEY",
		Model:     "m",
	}
	cases := map[string]func(*piProviderConfig){
		"missing base_url":    func(c *piProviderConfig) { c.BaseURL = "" },
		"missing api":         func(c *piProviderConfig) { c.API = "" },
		"missing api_key_env": func(c *piProviderConfig) { c.APIKeyEnv = "" },
		"missing model":       func(c *piProviderConfig) { c.Model = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			dir := t.TempDir()
			if err := writePiModelsJSON(dir, cfg); err == nil {
				t.Fatalf("expected error for %s, got nil", name)
			}
			if _, err := os.Stat(filepath.Join(dir, "models.json")); err == nil {
				t.Fatalf("%s: models.json must not be written on invalid config", name)
			}
		})
	}
}

func TestNormalisePiProvider_FullRoundTrip(t *testing.T) {
	raw := map[string]any{
		"name":        "Parsar Anthropic",
		"base_url":    "https://platform-api.example.com",
		"api":         "openai-completions",
		"api_key_env": "PARSAR_PI_API_KEY",
		"model":       "gpt-5.5",
		"auth_header": true,
		// Headers cross the daemon boundary as JSON, so they arrive as
		// map[string]any even though the server typed them map[string]string.
		"headers": map[string]any{"X-Sub-Module": "codex-internal"},
	}
	cfg, ok, err := normalisePiProvider(raw)
	if err != nil {
		t.Fatalf("normalisePiProvider: %v", err)
	}
	if !ok {
		t.Fatal("hasProvider must be true for non-nil raw")
	}
	if cfg.Name != "Parsar Anthropic" || cfg.BaseURL != "https://platform-api.example.com" {
		t.Fatalf("scalar fields wrong: %+v", cfg)
	}
	if cfg.API != "openai-completions" || cfg.APIKeyEnv != "PARSAR_PI_API_KEY" || cfg.Model != "gpt-5.5" {
		t.Fatalf("scalar fields wrong: %+v", cfg)
	}
	if !cfg.AuthHeader {
		t.Fatalf("auth_header lost: %+v", cfg)
	}
	if cfg.Headers["X-Sub-Module"] != "codex-internal" {
		t.Fatalf("headers lost: %+v", cfg.Headers)
	}
}

func TestNormalisePiProvider_Nil(t *testing.T) {
	cfg, ok, err := normalisePiProvider(nil)
	if err != nil {
		t.Fatalf("normalisePiProvider nil: %v", err)
	}
	if ok {
		t.Fatal("hasProvider must be false for nil")
	}
	_ = cfg
}

func TestNormalisePiProvider_WrongType(t *testing.T) {
	if _, _, err := normalisePiProvider("not-an-object"); err == nil {
		t.Fatal("expected error for non-object pi_provider")
	}
}

func TestResolveAgentDirConversationScoped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := resolveAgentDir("conv-abc", "run-1")
	if err != nil {
		t.Fatalf("resolveAgentDir: %v", err)
	}
	// Sibling of resolveSkillsRoot's conv-<id>/skills so one conversation's
	// pi runtime state (models.json, sessions) co-locates under one dir.
	want := filepath.Join(tmp, ".parsar", "runtime", "pi", "conv-conv-abc", "agent")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveAgentDirRunScopedFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := resolveAgentDir("", "run-9")
	if err != nil {
		t.Fatalf("resolveAgentDir: %v", err)
	}
	want := filepath.Join(tmp, ".parsar", "runtime", "pi", "run-run-9", "agent")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyPiManagedProvider_WritesModelsAndSetsEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	callerEnv := map[string]any{"PARSAR_PI_API_KEY": "sk-proxy", "OTHER": "x"}
	opts := map[string]any{
		"model": "parsar/claude-opus-4-6-thinking-max",
		"env":   callerEnv,
		"pi_provider": map[string]any{
			"base_url":    "https://platform-api.example.com",
			"api":         "anthropic-messages",
			"api_key_env": "PARSAR_PI_API_KEY",
			"model":       "claude-opus-4-6-thinking-max",
			"headers":     map[string]any{"X-Sub-Module": "claude-code-internal"},
		},
	}

	out, err := applyPiManagedProvider(opts, "conv-xyz", "run-1")
	if err != nil {
		t.Fatalf("applyPiManagedProvider: %v", err)
	}

	agentDir := filepath.Join(tmp, ".parsar", "runtime", "pi", "conv-conv-xyz", "agent")
	env, ok := out["env"].(map[string]any)
	if !ok {
		t.Fatalf("out[env] not a map: %T", out["env"])
	}
	if env["PI_CODING_AGENT_DIR"] != agentDir {
		t.Errorf("PI_CODING_AGENT_DIR = %v, want %q", env["PI_CODING_AGENT_DIR"], agentDir)
	}
	if env["PARSAR_PI_API_KEY"] != "sk-proxy" || env["OTHER"] != "x" {
		t.Errorf("pre-existing env not preserved: %+v", env)
	}

	p := readModelsJSON(t, agentDir).Providers[piManagedProviderSlug]
	if p.APIKey != "$PARSAR_PI_API_KEY" || p.BaseURL != "https://platform-api.example.com" {
		t.Errorf("models.json not materialised correctly: %+v", p)
	}

	// The caller's env map must be untouched — buildEnv reads opts["env"]
	// and a shared reference would leak PI_CODING_AGENT_DIR back to the
	// server-owned options map across turns.
	if _, leaked := callerEnv["PI_CODING_AGENT_DIR"]; leaked {
		t.Error("applyPiManagedProvider mutated the caller's env map")
	}
}

func TestApplyPiManagedProvider_NoProviderNoop(t *testing.T) {
	opts := map[string]any{"model": "anthropic/x"}
	out, err := applyPiManagedProvider(opts, "conv-1", "run-1")
	if err != nil {
		t.Fatalf("applyPiManagedProvider: %v", err)
	}
	if env, ok := out["env"].(map[string]any); ok {
		if _, set := env["PI_CODING_AGENT_DIR"]; set {
			t.Fatal("PI_CODING_AGENT_DIR must not be set when no pi_provider present")
		}
	}
}


