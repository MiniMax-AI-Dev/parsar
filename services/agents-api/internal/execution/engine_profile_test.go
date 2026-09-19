package execution

import (
	"encoding/json"
	"testing"
)

func TestAcceptedEnginePlacements(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "unregistered"} {
		for _, placement := range []string{"none", "openai_hosted", "self_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture"},"environment":{"type":"` + placement + `"`)
			if placement == "self_hosted" {
				raw = append(raw, []byte(`,"workspace_directory":"/work"`)...)
			}
			raw = append(raw, []byte(`}}`)...)
			want := engine != "unregistered" && !(engine == "claude_sdk" && placement == "self_hosted")
			if err := ValidateSessionConfiguration(engine, raw); (err == nil) != want {
				t.Fatalf("%s/%s: %v", engine, placement, err)
			}
		}
	}
}

func TestClaudeHostedToolsRequireSeparateQualification(t *testing.T) {
	for _, tools := range []string{`[{"type":"function","name":"f","parameters":{"type":"object"},"defer_loading":false}]`, `[{"type":"mcp","server_label":"s","transport":{"type":"http","server_url":"https://example.test/mcp"},"connection_origin":"service"}]`} {
		for _, placement := range []string{"none", "openai_hosted"} {
			raw := json.RawMessage(`{"agent":{"model":"fixture","tools":` + tools + `},"environment":{"type":"` + placement + `"}}`)
			if err := ValidateSessionConfiguration("claude_sdk", raw); (err == nil) != (placement == "none") {
				t.Fatalf("%s: %v", placement, err)
			}
		}
	}
}
