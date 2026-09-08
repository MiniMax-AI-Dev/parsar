package agentdaemon

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestOpenCodeManagedEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, adapter, base string
		endpoints           any
		override, want      string
	}{
		{"Anthropic JSON map", "@ai-sdk/anthropic", "https://legacy.test/v1", map[string]any{"anthropic": "https://gateway.test/anthropic"}, "", "https://gateway.test/anthropic/v1"},
		{"Anthropic typed alias", "@ai-sdk/anthropic", "https://legacy.test/v1", map[string]string{"messages": "https://gateway.test/anthropic"}, "", "https://gateway.test/anthropic/v1"},
		{"Anthropic SDK override", "@ai-sdk/anthropic", "https://legacy.test/v1", map[string]any{"anthropic": "https://gateway.test/anthropic"}, "https://override.test/api", "https://override.test/api"},
		{"chat map", "@ai-sdk/openai-compatible", "https://legacy.test/v1", map[string]any{"openai": "https://gateway.test/chat/v1", "openai-response": "https://gateway.test/responses/v1"}, "", "https://gateway.test/chat/v1"},
		{"responses map", "@ai-sdk/openai", "https://legacy.test/v1", map[string]any{"openai": "https://gateway.test/chat/v1", "openai-response": "https://gateway.test/responses/v1"}, "", "https://gateway.test/responses/v1"},
		{"chat typed alias", "@ai-sdk/openai-compatible", "", map[string]string{"chat-completions": "https://gateway.test/api/paas/v4"}, "", "https://gateway.test/api/paas/v4"},
		{"responses typed alias", "@ai-sdk/openai", "", map[string]string{"responses": "https://gateway.test/responses/custom"}, "", "https://gateway.test/responses/custom"},
		{"chat legacy base", "@ai-sdk/openai-compatible", "https://legacy.test/custom", nil, "", "https://legacy.test/custom"},
		{"responses legacy base", "@ai-sdk/openai", "https://legacy.test/custom", map[string]any{"openai-response": " "}, "", "https://legacy.test/custom"},
		{"chat SDK override", "@ai-sdk/openai-compatible", "", map[string]any{"openai": "https://gateway.test/v1"}, "https://override.test/custom", "https://override.test/custom"},
		{"responses SDK override", "@ai-sdk/openai", "", map[string]any{"openai-response": "https://gateway.test/v1"}, "https://override.test/custom", "https://override.test/custom"},
		{"other SDK unchanged", "@ai-sdk/google", "https://legacy.test/custom", map[string]any{"google": "https://gateway.test/new"}, "", "https://legacy.test/custom"},
		{"SDK default", "@ai-sdk/openai", "", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := store.ModelRuntime{
				ProviderType: "qa", ModelKey: "qa-model", Adapter: tc.adapter, BaseURL: tc.base,
				ProviderConfig: map[string]any{"endpoint_base_urls": tc.endpoints},
			}
			if tc.override != "" {
				mr.ProviderConfig["options"] = map[string]any{"baseURL": tc.override}
			}
			opts := map[string]any{}
			if err := injectOpenCodeManagedModel(opts, "qa-model", mr, "synthetic-key"); err != nil {
				t.Fatal(err)
			}
			var config struct {
				Provider map[string]struct {
					Options map[string]any `json:"options"`
				} `json:"provider"`
			}
			if err := json.Unmarshal([]byte(opts["opencode_json"].(string)), &config); err != nil {
				t.Fatal(err)
			}
			got, _ := config.Provider["qa"].Options["baseURL"].(string)
			if got != tc.want {
				t.Fatalf("baseURL = %q, want %q", got, tc.want)
			}
		})
	}
}
