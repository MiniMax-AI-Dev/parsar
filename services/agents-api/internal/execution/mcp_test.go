package execution

import (
	"encoding/json"
	"testing"
)

func TestMCPRequiresSupportedServicePlacement(t *testing.T) {
	tool := json.RawMessage(`{"type":"mcp","server_label":"tickets","transport":{"type":"http","server_url":"https://mcp.example/mcp"},"connection_origin":"service","allowed_tools":[]}`)
	for _, profile := range []struct {
		engine, environment string
		valid               bool
	}{
		{"codex", `{"type":"none"}`, true},
		{"claude_sdk", `{"type":"none"}`, false},
		{"unavailable", `{"type":"none"}`, false},
		{"codex", `null`, false},
		{"codex", `{"type":"self_hosted","workspace_directory":"/work"}`, false},
	} {
		raw, err := json.Marshal(map[string]any{"agent": map[string]any{"model": "model", "tools": []json.RawMessage{tool}}, "environment": json.RawMessage(profile.environment)})
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateSessionConfiguration(profile.engine, raw); (err == nil) != profile.valid {
			t.Fatalf("%s/%s: %v", profile.engine, profile.environment, err)
		}
	}
	function := json.RawMessage(`{"type":"function","name":"lookup","description":"Read","parameters":{"type":"object"},"defer_loading":false}`)
	functions, servers, err := executionTools([]json.RawMessage{tool, function})
	if err != nil || len(functions) != 1 || functions[0].Name != "lookup" || len(servers) != 1 || servers[0].AllowedTools == nil || len(*servers[0].AllowedTools) != 0 {
		t.Fatal("mixed tool configuration lost the deny-all declaration", functions, servers, err)
	}
}
