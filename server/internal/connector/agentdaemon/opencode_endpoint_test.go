package agentdaemon

import (
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestOpenCodeManagedAnthropicEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints any
		override  string
		want      string
	}{
		{"JSON map", map[string]any{"anthropic": "https://gateway.test/anthropic"}, "", "https://gateway.test/anthropic/v1"},
		{"typed map alias", map[string]string{"messages": "https://gateway.test/anthropic"}, "", "https://gateway.test/anthropic/v1"},
		{"explicit SDK override", map[string]any{"anthropic": "https://gateway.test/anthropic"}, "https://override.test/api", "https://override.test/api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mr := store.ModelRuntime{
				ProviderType: "qa", ModelKey: "qa-model", Adapter: "@ai-sdk/anthropic", BaseURL: "https://gateway.test/v1",
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
			if got := config.Provider["qa"].Options["baseURL"]; got != tc.want {
				t.Fatalf("baseURL = %v, want %s", got, tc.want)
			}
		})
	}
}
