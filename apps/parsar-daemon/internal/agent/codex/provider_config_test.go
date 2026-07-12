package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCodexProviderConfig_Minimal(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		BaseURL:     "https://platform-api.example.com/v1",
		BearerToken: "sk-test",
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	for _, want := range []string{
		`[model_providers.parsar]`,
		`name = "Parsar"`,
		`base_url = "https://platform-api.example.com/v1"`,
		`experimental_bearer_token = "sk-test"`,
		`wire_api = "responses"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config.toml missing %q\n---\n%s", want, body)
		}
	}
}

// TestWriteCodexProviderConfig_PinsResponsesWire confirms wire_api stays
// "responses" even when the caller forgot to set it. codex-rs removed
// the "chat" variant; emitting empty would fall back to upstream's
// default which today is "responses" but could drift.
func TestWriteCodexProviderConfig_PinsResponsesWire(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		BaseURL:     "https://x/v1",
		BearerToken: "sk-x",
		// WireAPI deliberately empty
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(body, `wire_api = "responses"`) {
		t.Fatalf("wire_api default not pinned: %s", body)
	}
}

func TestWriteCodexProviderConfig_FullProvider(t *testing.T) {
	dir := t.TempDir()
	cfg := providerConfig{
		Name:        "mygw",
		BaseURL:     "https://platform-api.example.com/v1",
		BearerToken: "sk-test-fixture",
		WireAPI:     "responses",
		HTTPHeaders: map[string]string{
			"X-Sub-Module": "codex-internal",
			"X-Request-ID": "abc",
		},
		QueryParams: map[string]string{
			"api-version": "2025-04-01-preview",
		},
		RequestMaxRetries: 4,
		StreamMaxRetries:  3,
	}
	if err := writeCodexProviderConfig(dir, cfg); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	for _, want := range []string{
		`name = "mygw"`,
		`base_url = "https://platform-api.example.com/v1"`,
		`experimental_bearer_token = "sk-test-fixture"`,
		`request_max_retries = 4`,
		`stream_max_retries = 3`,
		`[model_providers.parsar.http_headers]`,
		`"X-Sub-Module" = "codex-internal"`,
		`"X-Request-ID" = "abc"`,
		`[model_providers.parsar.query_params]`,
		`"api-version" = "2025-04-01-preview"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("config.toml missing %q\n---\n%s", want, body)
		}
	}
}

func TestWriteCodexProviderConfig_RejectsMissingFields(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		cfg  providerConfig
	}{
		{"missing base_url", providerConfig{BearerToken: "sk-x"}},
		{"missing bearer_token", providerConfig{BaseURL: "https://x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := writeCodexProviderConfig(dir, tc.cfg); err == nil {
				t.Fatal("expected error for incomplete provider config")
			}
		})
	}
}

// TestWriteCodexProviderConfig_AppendsAlongsideMCP verifies the two
// writers coexist on the same file. Without append semantics one would
// silently overwrite the other depending on call order.
func TestWriteCodexProviderConfig_AppendsAlongsideMCP(t *testing.T) {
	dir := t.TempDir()
	mcpCfg := map[string]mcpServerConfig{
		"docs": {Name: "docs", Command: "docs-server"},
	}
	if err := writeCodexMCPConfig(dir, mcpCfg); err != nil {
		t.Fatalf("mcp write: %v", err)
	}
	if err := writeCodexProviderConfig(dir, providerConfig{
		BaseURL: "https://x", BearerToken: "sk-x",
	}); err != nil {
		t.Fatalf("provider write: %v", err)
	}
	body := mustReadFile(t, filepath.Join(dir, "config.toml"))
	if !strings.Contains(body, `[mcp_servers."docs"]`) {
		t.Errorf("mcp_servers block lost after provider write:\n%s", body)
	}
	if !strings.Contains(body, `[model_providers.parsar]`) {
		t.Errorf("model_providers block missing:\n%s", body)
	}
}

func TestNormaliseProviderConfig_FullRoundTrip(t *testing.T) {
	raw := map[string]any{
		"name":         "mygw",
		"base_url":     "https://x/v1",
		"bearer_token": "sk-x",
		"wire_api":     "responses",
		"http_headers": map[string]any{
			"X-Sub-Module": "codex-internal",
		},
		"query_params": map[string]any{
			"api-version": "2025-04-01-preview",
		},
		"request_max_retries": float64(4), // JSON numbers arrive as float64
	}
	cfg, hasProvider, err := normaliseProviderConfig(raw)
	if err != nil {
		t.Fatalf("normalise: %v", err)
	}
	if !hasProvider {
		t.Fatal("hasProvider must be true for non-nil raw")
	}
	if cfg.Name != "mygw" || cfg.BaseURL != "https://x/v1" || cfg.BearerToken != "sk-x" {
		t.Fatalf("scalar fields wrong: %+v", cfg)
	}
	if cfg.HTTPHeaders["X-Sub-Module"] != "codex-internal" {
		t.Fatalf("headers lost: %+v", cfg.HTTPHeaders)
	}
	if cfg.QueryParams["api-version"] != "2025-04-01-preview" {
		t.Fatalf("query_params lost: %+v", cfg.QueryParams)
	}
	if cfg.RequestMaxRetries != 4 {
		t.Fatalf("request_max_retries = %d, want 4", cfg.RequestMaxRetries)
	}
}

func TestNormaliseProviderConfig_Nil(t *testing.T) {
	cfg, hasProvider, err := normaliseProviderConfig(nil)
	if err != nil {
		t.Fatalf("normalise nil: %v", err)
	}
	if hasProvider {
		t.Fatal("hasProvider must be false for nil")
	}
	_ = cfg
}

func TestBuildSessionPlan_PinsModelProviderWhenProviderSet(t *testing.T) {
	plan, err := BuildSessionPlan("run-x", "conv-1/agent-1/codex", "", map[string]any{
		"codex_provider": map[string]any{
			"base_url":     "https://x/v1",
			"bearer_token": "sk-x",
		},
	})
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.ModelProvider != "parsar" {
		t.Fatalf("plan.ModelProvider = %q, want parsar", plan.ModelProvider)
	}
	found := false
	for _, kv := range plan.ExtraConfig {
		if kv[0] == "model_provider" && kv[1] == `"parsar"` {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ExtraConfig missing model_provider override: %+v", plan.ExtraConfig)
	}
	// And the config.toml was actually written.
	codexHome := ""
	for _, kv := range plan.Env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			codexHome = strings.TrimPrefix(kv, "CODEX_HOME=")
		}
	}
	body := mustReadFile(t, filepath.Join(codexHome, "config.toml"))
	if !strings.Contains(body, `[model_providers.parsar]`) {
		t.Fatalf("config.toml missing provider block:\n%s", body)
	}
}

func TestBuildSessionPlan_NoProviderLeavesBuiltinDefault(t *testing.T) {
	plan, err := BuildSessionPlan("run-y", "conv-1/agent-1/codex", "", nil)
	if err != nil {
		t.Fatalf("BuildSessionPlan: %v", err)
	}
	defer plan.Cleanup()
	if plan.ModelProvider != "" {
		t.Fatalf("plan.ModelProvider = %q, want empty when no provider configured", plan.ModelProvider)
	}
	for _, kv := range plan.ExtraConfig {
		if kv[0] == "model_provider" {
			t.Fatalf("model_provider override leaked into ExtraConfig: %+v", plan.ExtraConfig)
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
