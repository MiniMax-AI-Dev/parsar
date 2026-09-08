package opencode_test

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/runtime/opencode"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestRenderAnthropicSDKBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name, adapter, base, override, want string
	}{
		{"service root", "@ai-sdk/anthropic", "https://example.test/anthropic", "", "https://example.test/anthropic/v1"},
		{"versioned base", "@ai-sdk/anthropic", "https://example.test/v1/", "", "https://example.test/v1"},
		{"messages endpoint", "@ai-sdk/anthropic", "https://example.test/anthropic/v1/messages", "", "https://example.test/anthropic/v1"},
		{"custom messages endpoint", "@ai-sdk/anthropic", "https://example.test/custom/messages", "", "https://example.test/custom"},
		{"explicit SDK override", "@ai-sdk/anthropic", "https://example.test/anthropic", "https://override.test/api", "https://override.test/api"},
		{"other adapter", "@ai-sdk/openai", "https://example.test/custom", "", "https://example.test/custom"},
		{"default SDK endpoint", "@ai-sdk/anthropic", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := store.ModelRuntime{ProviderType: "qa-provider", ModelKey: "qa-model", Adapter: tc.adapter, BaseURL: tc.base}
			if tc.override != "" {
				mr.ProviderConfig = map[string]any{"options": map[string]any{"baseURL": tc.override}}
			}
			raw, err := opencode.RenderConfig(mr, "synthetic-key", opencode.RenderInput{})
			if err != nil {
				t.Fatal(err)
			}
			var config struct {
				Providers map[string]struct {
					Options struct {
						BaseURL string `json:"baseURL"`
					} `json:"options"`
				} `json:"provider"`
			}
			if err := json.Unmarshal(raw, &config); err != nil {
				t.Fatal(err)
			}
			if got := config.Providers["qa-provider"].Options.BaseURL; got != tc.want {
				t.Fatalf("baseURL = %q, want %q", got, tc.want)
			}
		})
	}
}
