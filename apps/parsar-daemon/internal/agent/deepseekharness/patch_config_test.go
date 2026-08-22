package deepseekharness_test

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	"gopkg.in/yaml.v3"
)

type patchRow struct {
	ID     string         `yaml:"id"`
	Config map[string]any `yaml:"config"`
}

func decodeRows(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var rows []patchRow
	if err := yaml.Unmarshal(body, &rows); err != nil {
		t.Fatalf("unmarshal patch: %v\n%s", err, body)
	}
	out := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Config
	}
	return out
}

func TestRenderPatchManagedProviderDeclaresRoute(t *testing.T) {
	body, err := deepseekharness.RenderPatchForTest(map[string]any{
		"name":        "Parsar Gateway",
		"base_url":    "https://gw.example/v1",
		"api":         "openai-completions",
		"api_key_env": "PARSAR_DSH_API_KEY",
		"model":       "deepseek-v4",
		"headers":     map[string]any{"X-Sub-Module": "parsar"},
	}, "deepseek-v4", "")
	if err != nil {
		t.Fatalf("RenderPatchForTest: %v", err)
	}
	rows := decodeRows(t, body)

	providers, ok := rows["llm-pi-ai"]["providers"].(map[string]any)
	if !ok {
		t.Fatalf("llm-pi-ai row missing providers: %#v", rows["llm-pi-ai"])
	}
	route, ok := providers[deepseekharness.ManagedRouteForTest].(map[string]any)
	if !ok {
		t.Fatalf("missing managed route: %#v", providers)
	}
	// A route pi-ai does not ship is refused unless api, baseURL and a
	// non-empty models list are all declared.
	if route["api"] != "openai-completions" || route["baseURL"] != "https://gw.example/v1" {
		t.Fatalf("route transport fields = %#v", route)
	}
	if route["apiKeyEnv"] != "PARSAR_DSH_API_KEY" {
		t.Fatalf("route must reference the key env var: %#v", route)
	}
	models, ok := route["models"].([]any)
	if !ok || len(models) != 1 {
		t.Fatalf("route models = %#v", route["models"])
	}

	defaultModel := rows["agent-default-model"]
	if defaultModel["provider"] != deepseekharness.ManagedRouteForTest || defaultModel["model"] != "deepseek-v4" {
		t.Fatalf("agent-default-model = %#v", defaultModel)
	}
}

func TestRenderPatchModelOnlyKeepsShippedRoute(t *testing.T) {
	body, err := deepseekharness.RenderPatchForTest(nil, "deepseek-v4-pro", "")
	if err != nil {
		t.Fatalf("RenderPatchForTest: %v", err)
	}
	rows := decodeRows(t, body)
	if _, ok := rows["llm-pi-ai"]; ok {
		t.Fatalf("no managed provider means no llm-pi-ai row: %#v", rows)
	}
	if rows["agent-default-model"]["provider"] != "deepseek-official" {
		t.Fatalf("agent-default-model = %#v", rows["agent-default-model"])
	}
	if rows["agent-default-model"]["model"] != "deepseek-v4-pro" {
		t.Fatalf("agent-default-model = %#v", rows["agent-default-model"])
	}
}

func TestRenderPatchWithoutModelOnlyPinsPermissionPreset(t *testing.T) {
	body, err := deepseekharness.RenderPatchForTest(nil, "", "")
	if err != nil {
		t.Fatalf("RenderPatchForTest: %v", err)
	}
	rows := decodeRows(t, body)
	if len(rows) != 1 {
		t.Fatalf("expected only the permission row, got %#v", rows)
	}
	if rows["permission"]["defaultPreset"] != "parsar-unattended" {
		t.Fatalf("permission row = %#v", rows["permission"])
	}
}

// dsh validates the composed sandbox+approval pair against the preset table
// and re-pins both knobs from defaultPreset on every session creation, so
// patching the approval row alone fails boot with "match no preset". The
// pairing has to arrive as a declared, selected preset.
func TestRenderPatchDeclaresUnattendedPresetPair(t *testing.T) {
	body, err := deepseekharness.RenderPatchForTest(nil, "", "")
	if err != nil {
		t.Fatalf("RenderPatchForTest: %v", err)
	}
	rows := decodeRows(t, body)
	presets, ok := rows["permission"]["presets"].(map[string]any)
	if !ok {
		t.Fatalf("permission row missing presets: %#v", rows["permission"])
	}
	preset, ok := presets["parsar-unattended"].(map[string]any)
	if !ok {
		t.Fatalf("missing parsar-unattended preset: %#v", presets)
	}
	if preset["sandbox"] != "workspace-write" || preset["approval"] != "never" {
		t.Fatalf("preset pair = %#v, want workspace-write + never", preset)
	}
	if _, ok := rows["approval"]; ok {
		t.Fatalf("the approval row must not be patched on its own: %#v", rows)
	}
}

func TestRenderPatchRejectsIncompleteProvider(t *testing.T) {
	cases := map[string]map[string]any{
		"base_url": {"api": "openai-completions", "api_key_env": "K", "model": "m"},
		"api":      {"base_url": "https://x/v1", "api_key_env": "K", "model": "m"},
		"api_key":  {"base_url": "https://x/v1", "api": "openai-completions", "model": "m"},
		"model":    {"base_url": "https://x/v1", "api": "openai-completions", "api_key_env": "K"},
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := deepseekharness.RenderPatchForTest(raw, "", ""); err == nil {
				t.Fatalf("expected rejection for incomplete provider %#v", raw)
			}
		})
	}
}

func TestRenderPatchRejectsBadProviderShape(t *testing.T) {
	_, err := deepseekharness.RenderPatchForTest("not-an-object", "", "")
	if err == nil || !strings.Contains(err.Error(), "dsh_provider") {
		t.Fatalf("err = %v, want dsh_provider shape error", err)
	}
}
