package opencode_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/opencode"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// These tests are the canonical contract for the opencode.json shape
// the connector writes; keeping them co-located with the adapter lets
// new providers be unit-tested without spinning up the httprunner.

func TestRenderBuildsProviderBlock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		runtime   store.ModelRuntime
		wantNPM   string
		wantBase  string
		wantKey   string
		wantOpts  map[string]any // expected merged provider options subset
		wantModel map[string]any // expected fields inside model block
	}{
		{
			name: "anthropic claude opus with headers and effort",
			runtime: store.ModelRuntime{
				ProviderType: "anthropic",
				Adapter:      "@ai-sdk/anthropic",
				BaseURL:      "https://platform-api.example.com/v1",
				ModelKey:     "claude-opus-4-7",
				ModelName:    "claude-opus-4-7",
				Capabilities: map[string]any{"reasoning": true, "tool_call": true, "temperature": true, "attachment": true},
				Limits:       map[string]any{"context": float64(600000), "output": float64(128000)},
				ModelConfig: map[string]any{
					"headers":    map[string]any{"X-Sub-Module": "claude-code-internal"},
					"options":    map[string]any{"effort": "xhigh", "thinking": map[string]any{"type": "adaptive"}},
					"modalities": map[string]any{"input": []any{"text", "image", "pdf"}, "output": []any{"text"}},
				},
			},
			wantNPM:   "@ai-sdk/anthropic",
			wantBase:  "https://platform-api.example.com/v1",
			wantKey:   "claude-opus-4-7",
			wantOpts:  map[string]any{"apiKey": "sk-test-key", "baseURL": "https://platform-api.example.com/v1"},
			wantModel: map[string]any{"reasoning": true, "tool_call": true},
		},
		{
			name: "azure (openai sdk) gpt-5.5",
			runtime: store.ModelRuntime{
				ProviderType: "azure",
				Adapter:      "@ai-sdk/openai",
				BaseURL:      "https://platform-api.example.com/v1",
				ModelKey:     "gpt-5.5",
				ModelName:    "gpt-5.5",
				Capabilities: map[string]any{"reasoning": true, "tool_call": true, "temperature": false},
				Limits:       map[string]any{"context": float64(600000), "output": float64(128000)},
				ModelConfig:  map[string]any{"options": map[string]any{"store": false, "effort": "high"}},
			},
			wantNPM:  "@ai-sdk/openai",
			wantBase: "https://platform-api.example.com/v1",
			wantKey:  "gpt-5.5",
			wantOpts: map[string]any{"apiKey": "sk-test-key", "baseURL": "https://platform-api.example.com/v1"},
		},
		{
			name: "gemini",
			runtime: store.ModelRuntime{
				ProviderType: "gemini",
				Adapter:      "@ai-sdk/google",
				BaseURL:      "https://platform-api.example.com/v1beta",
				ModelKey:     "gemini-3.1-pro-preview",
				Capabilities: map[string]any{"reasoning": true, "tool_call": true},
				Limits:       map[string]any{"context": float64(600000), "output": float64(65536)},
			},
			wantNPM:  "@ai-sdk/google",
			wantBase: "https://platform-api.example.com/v1beta",
			wantKey:  "gemini-3.1-pro-preview",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			configHome, cleanup, err := opencode.Render("run-"+tc.runtime.ProviderType, tc.runtime, "sk-test-key", opencode.RenderInput{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			data, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
			if err != nil {
				t.Fatalf("expected opencode.json under %s, got %v", configHome, err)
			}
			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatal(err)
			}
			providers, _ := parsed["provider"].(map[string]any)
			block, ok := providers[tc.runtime.ProviderType].(map[string]any)
			if !ok {
				t.Fatalf("expected provider block for %q in %v", tc.runtime.ProviderType, providers)
			}
			if block["npm"] != tc.wantNPM {
				t.Fatalf("npm: got %v want %v", block["npm"], tc.wantNPM)
			}
			opts, _ := block["options"].(map[string]any)
			if opts["apiKey"] != "sk-test-key" {
				t.Fatalf("apiKey missing in options: %+v", opts)
			}
			if tc.wantBase != "" && opts["baseURL"] != tc.wantBase {
				t.Fatalf("baseURL: got %v want %v", opts["baseURL"], tc.wantBase)
			}
			models, _ := block["models"].(map[string]any)
			modelBlock, ok := models[tc.wantKey].(map[string]any)
			if !ok {
				t.Fatalf("expected model %q under provider, got %v", tc.wantKey, models)
			}
			for k, v := range tc.wantModel {
				if modelBlock[k] != v {
					t.Fatalf("model[%s]: got %v want %v", k, modelBlock[k], v)
				}
			}
			whitelist, _ := block["whitelist"].([]any)
			if len(whitelist) != 1 || whitelist[0] != tc.wantKey {
				t.Fatalf("whitelist should contain exactly %q, got %v", tc.wantKey, whitelist)
			}
			perms, _ := parsed["permission"].(map[string]any)
			if perms["*"] != "allow" {
				t.Fatalf("expected permission.* = allow, got %v", perms)
			}
		})
	}
}

func TestRenderMergesProviderHeadersIntoModelBlock(t *testing.T) {
	t.Parallel()
	// Model-level headers override provider-level for same key; other
	// provider-level keys pass through.
	rt := store.ModelRuntime{
		ProviderType: "MiniMax-gpt5",
		Adapter:      "@ai-sdk/openai",
		BaseURL:      "https://platform-api.example.com/v1",
		ModelKey:     "gpt-5.5",
		ModelName:    "gpt-5.5",
		ProviderConfig: map[string]any{
			"headers": map[string]any{
				"X-Sub-Module": "claude-code-internal",
				"X-Tenant":     "team-a",
			},
		},
		ModelConfig: map[string]any{
			"headers": map[string]any{
				"X-Sub-Module": "team-runtime",
			},
		},
	}
	configHome, cleanup, err := opencode.Render("run-headers-merge", rt, "sk-test-key", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	data, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	providers, _ := parsed["provider"].(map[string]any)
	block, _ := providers["MiniMax-gpt5"].(map[string]any)
	models, _ := block["models"].(map[string]any)
	model, _ := models["gpt-5.5"].(map[string]any)
	headers, _ := model["headers"].(map[string]any)
	if headers["X-Sub-Module"] != "team-runtime" {
		t.Fatalf("expected model override to win for X-Sub-Module, got %v", headers["X-Sub-Module"])
	}
	if headers["X-Tenant"] != "team-a" {
		t.Fatalf("expected provider X-Tenant to pass through, got %v", headers["X-Tenant"])
	}
}

func TestRenderProviderHeadersOnly(t *testing.T) {
	t.Parallel()
	// Provider-only headers must still land in the model block.
	rt := store.ModelRuntime{
		ProviderType: "MiniMax-only",
		Adapter:      "@ai-sdk/openai",
		BaseURL:      "https://platform-api.example.com/v1",
		ModelKey:     "gpt-5.5",
		ModelName:    "gpt-5.5",
		ProviderConfig: map[string]any{
			"headers": map[string]any{"X-Sub-Module": "claude-code-internal"},
		},
	}
	configHome, cleanup, err := opencode.Render("run-headers-provider", rt, "sk-test-key", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	data, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	providers, _ := parsed["provider"].(map[string]any)
	block, _ := providers["MiniMax-only"].(map[string]any)
	models, _ := block["models"].(map[string]any)
	model, _ := models["gpt-5.5"].(map[string]any)
	headers, _ := model["headers"].(map[string]any)
	if headers["X-Sub-Module"] != "claude-code-internal" {
		t.Fatalf("expected provider header to land in model block, got %v", headers)
	}
}

func TestRenderSkipsWhenAdapterMissing(t *testing.T) {
	t.Parallel()
	configHome, cleanup, err := opencode.Render("run-x", store.ModelRuntime{ProviderType: "anthropic", ModelKey: "claude-opus-4-7"}, "sk", opencode.RenderInput{})
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if configHome != "" {
		t.Fatalf("expected empty configHome when adapter is missing, got %q", configHome)
	}
}

func TestRenderSkipsWhenAPIKeyMissing(t *testing.T) {
	t.Parallel()
	// Missing apiKey must produce no opencode.json (otherwise the
	// per-run scratch dir would leak with no cleanup path).
	configHome, cleanup, err := opencode.Render("run-no-key", store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}, "", opencode.RenderInput{})
	t.Cleanup(cleanup)
	if err != nil {
		t.Fatal(err)
	}
	if configHome != "" {
		t.Fatalf("expected empty configHome when apiKey is empty, got %q", configHome)
	}
}

func TestRenderCleanupRemovesScratch(t *testing.T) {
	t.Parallel()
	configHome, cleanup, err := opencode.Render("run-cleanup", store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(configHome, "opencode", "opencode.json")); err != nil {
		t.Fatalf("expected config file before cleanup, got %v", err)
	}
	cleanup()
	if _, err := os.Stat(configHome); !os.IsNotExist(err) {
		t.Fatalf("expected configHome removed after cleanup, got err=%v", err)
	}
}

func TestRenderInjectsPluginSpecWhenProvided(t *testing.T) {
	t.Parallel()
	pluginPath := "/abs/path/to/parsar-opencode-plugin.ts"
	configHome, cleanup, err := opencode.Render("run-plugin", store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}, "sk-test", opencode.RenderInput{PluginSpec: pluginPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	raw, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	plugin, ok := parsed["plugin"].([]any)
	if !ok {
		t.Fatalf("plugin field missing or wrong type: %v", parsed["plugin"])
	}
	if len(plugin) != 1 || plugin[0] != pluginPath {
		t.Fatalf("plugin = %v, want [%q]", plugin, pluginPath)
	}
	// Plugin injection must NOT switch opencode into ask mode — Parsar
	// does not own a native approval endpoint.
	perms, ok := parsed["permission"].(map[string]any)
	if !ok {
		t.Fatalf("permission field missing or wrong type: %v", parsed["permission"])
	}
	if perms["*"] != "allow" {
		t.Fatalf("plugin enabled => permission.* = %v, want \"allow\"", perms["*"])
	}
}

func TestRenderOmitsPluginWhenSpecEmpty(t *testing.T) {
	t.Parallel()
	configHome, cleanup, err := opencode.Render("run-no-plugin", store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	raw, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, present := parsed["plugin"]; present {
		t.Fatalf("plugin field should be absent when spec is empty, got %v", parsed["plugin"])
	}
	perms, ok := parsed["permission"].(map[string]any)
	if !ok {
		t.Fatalf("permission field missing or wrong type: %v", parsed["permission"])
	}
	if perms["*"] != "allow" {
		t.Fatalf("plugin disabled => permission.* = %v, want \"allow\"", perms["*"])
	}
}

// TestRenderConfigReturnsRawJSONForSandboxCallers exercises the
// pure-JSON path sandbox callers consume; the bytes MUST match what
// Render writes to disk for the same inputs.
func TestRenderConfigReturnsRawJSONForSandboxCallers(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		BaseURL:      "https://platform-api.example.com/v1",
		ModelKey:     "claude-opus-4-7",
		ModelName:    "claude-opus-4-7",
	}
	bytesOut, err := opencode.RenderConfig(rt, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	if len(bytesOut) == 0 {
		t.Fatal("RenderConfig returned empty bytes for a fully-populated ModelRuntime")
	}
	var parsed map[string]any
	if err := json.Unmarshal(bytesOut, &parsed); err != nil {
		t.Fatalf("RenderConfig produced invalid JSON: %v\n%s", err, bytesOut)
	}
	providerMap, _ := parsed["provider"].(map[string]any)
	anthropic, _ := providerMap["anthropic"].(map[string]any)
	if anthropic["npm"] != "@ai-sdk/anthropic" {
		t.Errorf("provider.anthropic.npm = %v, want @ai-sdk/anthropic", anthropic["npm"])
	}
	opts, _ := anthropic["options"].(map[string]any)
	if opts["apiKey"] != "sk-test" {
		t.Errorf("provider.anthropic.options.apiKey = %v, want sk-test", opts["apiKey"])
	}
	if opts["baseURL"] != "https://platform-api.example.com/v1" {
		t.Errorf("provider.anthropic.options.baseURL = %v, want gateway URL", opts["baseURL"])
	}

	// Render-on-disk and RenderConfig must produce byte-identical JSON.
	configHome, cleanup, err := opencode.Render("test-rendercfg-parity", rt, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	t.Cleanup(cleanup)
	onDisk, err := os.ReadFile(filepath.Join(configHome, "opencode", "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(bytesOut) {
		t.Errorf("Render-on-disk and RenderConfig bytes diverged.\non-disk:\n%s\nRenderConfig:\n%s", onDisk, bytesOut)
	}
}

// TestRenderConfigReturnsNilForIncompleteRuntime pins the "essential
// fields missing → no-op" contract: sandbox callers treat (nil, nil)
// as "fall back to env-var injection".
func TestRenderConfigReturnsNilForIncompleteRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		runtime store.ModelRuntime
		apiKey  string
	}{
		{"no api key", store.ModelRuntime{ProviderType: "p", Adapter: "@", ModelKey: "m"}, ""},
		{"no provider", store.ModelRuntime{Adapter: "@", ModelKey: "m"}, "sk"},
		{"no adapter", store.ModelRuntime{ProviderType: "p", ModelKey: "m"}, "sk"},
		{"no model key", store.ModelRuntime{ProviderType: "p", Adapter: "@"}, "sk"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := opencode.RenderConfig(tc.runtime, tc.apiKey, opencode.RenderInput{})
			if err != nil {
				t.Fatalf("RenderConfig should not error on missing fields, got %v", err)
			}
			if out != nil {
				t.Fatalf("RenderConfig should return nil for missing fields, got %d bytes", len(out))
			}
		})
	}
}

func TestRenderSanitizesRunIDForFilesystem(t *testing.T) {
	t.Parallel()
	// Path separators in runID must not escape the per-run scratch dir.
	configHome, cleanup, err := opencode.Render("evil/../escape", store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expectedPrefix := filepath.Join(home, ".parsar", "runtime", "opencode")
	rel, err := filepath.Rel(expectedPrefix, configHome)
	if err != nil {
		t.Fatalf("configHome %q must live under %q", configHome, expectedPrefix)
	}
	if filepath.Dir(rel) == "." {
		t.Fatalf("configHome %q resolved to top-level; rel=%q", configHome, rel)
	}
	if filepath.IsAbs(rel) || filepath.HasPrefix(rel, "..") {
		t.Fatalf("configHome %q escaped scratch root; rel=%q", configHome, rel)
	}
}

// TestRenderConfigInjectsMCPBlock pins the `mcp` block shape opencode
// upstream expects: each entry becomes one named subprocess with
// command, environment, and enabled fields under `type: "local"`.
func TestRenderConfigInjectsMCPBlock(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}
	in := opencode.RenderInput{
		MCPServers: []opencode.MCPServerSpec{{
			Name:    "github",
			Command: []string{"npx", "-y", "@modelcontextprotocol/server-github"},
			Environment: map[string]string{
				"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_test_token",
			},
			Enabled: true,
		}},
	}
	out, err := opencode.RenderConfig(rt, "sk-test", in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	mcp, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block missing or wrong type: %v", parsed["mcp"])
	}
	github, ok := mcp["github"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.github missing or wrong type: %v", mcp["github"])
	}
	if github["type"] != "local" {
		t.Errorf("mcp.github.type = %v, want \"local\"", github["type"])
	}
	if github["enabled"] != true {
		t.Errorf("mcp.github.enabled = %v, want true", github["enabled"])
	}
	cmd, _ := github["command"].([]any)
	if len(cmd) != 3 || cmd[0] != "npx" || cmd[2] != "@modelcontextprotocol/server-github" {
		t.Errorf("mcp.github.command = %v, want [npx -y @modelcontextprotocol/server-github]", cmd)
	}
	env, _ := github["environment"].(map[string]any)
	if env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_test_token" {
		t.Errorf("mcp.github.environment.GITHUB_PERSONAL_ACCESS_TOKEN = %v, want ghp_test_token", env["GITHUB_PERSONAL_ACCESS_TOKEN"])
	}
}

// TestRenderConfigOmitsMCPWhenEmpty pins that an empty MCPServers list
// leaves the `mcp` field absent (not `{}` — some opencode loader
// versions distinguish the two).
func TestRenderConfigOmitsMCPWhenEmpty(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}
	out, err := opencode.RenderConfig(rt, "sk-test", opencode.RenderInput{})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, present := parsed["mcp"]; present {
		t.Fatalf("mcp field should be absent when MCPServers is empty, got %v", parsed["mcp"])
	}
}

// TestRenderConfigSkipsInvalidMCPEntries pins that entries with empty
// Name or empty Command are silently skipped (so callers can build the
// list with conditional inclusions). All-invalid → no mcp block.
func TestRenderConfigSkipsInvalidMCPEntries(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}
	in := opencode.RenderInput{
		MCPServers: []opencode.MCPServerSpec{
			{Name: "", Command: []string{"npx", "x"}, Enabled: true},   // no name → skip
			{Name: "  ", Command: []string{"npx", "x"}, Enabled: true}, // whitespace-only name → skip
			{Name: "no-command", Command: nil, Enabled: true},          // no command → skip
			{Name: "valid", Command: []string{"echo", "ok"}, Enabled: true},
		},
	}
	out, err := opencode.RenderConfig(rt, "sk-test", in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	mcp, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block missing: %v", parsed["mcp"])
	}
	if len(mcp) != 1 {
		t.Fatalf("expected exactly 1 mcp entry (only \"valid\" survives filter), got %d: %v", len(mcp), mcp)
	}
	if _, ok := mcp["valid"]; !ok {
		t.Fatalf("expected mcp.valid to survive filter, got %v", mcp)
	}
}

// TestRenderConfigOmitsMCPWhenAllInvalid: when every entry is invalid,
// `mcp` must be absent (matching the empty-list path) so downstream
// can `_, ok := parsed["mcp"]` as an "any MCP wired" check.
func TestRenderConfigOmitsMCPWhenAllInvalid(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}
	in := opencode.RenderInput{
		MCPServers: []opencode.MCPServerSpec{
			{Name: "", Command: []string{"npx"}, Enabled: true},
			{Name: "no-command", Command: nil, Enabled: true},
		},
	}
	out, err := opencode.RenderConfig(rt, "sk-test", in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, present := parsed["mcp"]; present {
		t.Fatalf("mcp field should be absent when every entry is invalid, got %v", parsed["mcp"])
	}
}

// TestRenderConfigPreservesDisabledMCPEntry pins that Enabled is
// forwarded verbatim, NOT used as a filter — opencode upstream treats
// `enabled: false` as "declared but dormant".
func TestRenderConfigPreservesDisabledMCPEntry(t *testing.T) {
	t.Parallel()
	rt := store.ModelRuntime{
		ProviderType: "anthropic",
		Adapter:      "@ai-sdk/anthropic",
		ModelKey:     "claude-opus-4-7",
	}
	in := opencode.RenderInput{
		MCPServers: []opencode.MCPServerSpec{
			{Name: "github", Command: []string{"npx", "github-mcp"}, Enabled: false},
			{Name: "slack", Command: []string{"npx", "slack-mcp"}, Enabled: true},
		},
	}
	out, err := opencode.RenderConfig(rt, "sk-test", in)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	mcp, ok := parsed["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp block missing: %v", parsed["mcp"])
	}
	if len(mcp) != 2 {
		t.Fatalf("expected both entries to be emitted (disabled is not filtered), got %d: %v", len(mcp), mcp)
	}
	github, _ := mcp["github"].(map[string]any)
	if got := github["enabled"]; got != false {
		t.Errorf("github.enabled = %v, want false (verbatim forward)", got)
	}
	slack, _ := mcp["slack"].(map[string]any)
	if got := slack["enabled"]; got != true {
		t.Errorf("slack.enabled = %v, want true (verbatim forward)", got)
	}
}
