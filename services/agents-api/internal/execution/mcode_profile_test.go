package execution

import "testing"

func TestMCodeOperationQualification(t *testing.T) {
	p := Policy{}
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"text", `{"agent":{"model":"real-model"},"environment":{"type":"none"}}`, true},
		{"hosted", `{"agent":{"model":"real-model"},"environment":{"type":"openai_hosted"}}`, true},
		{"verbosity", `{"agent":{"model":"real-model","text":{"verbosity":"high"}},"environment":{"type":"none"}}`, false},
		{"subagents", `{"agent":{"model":"real-model","multi_agent":{"enabled":true}},"environment":{"type":"none"}}`, false},
		{"functions", `{"agent":{"model":"real-model","tools":[{"type":"function","name":"f","parameters":{"type":"object"}}]},"environment":{"type":"none"}}`, false},
		{"mcp", `{"agent":{"model":"real-model","tools":[{"type":"mcp","server_label":"x","server_url":"https://example.invalid/mcp"}]},"environment":{"type":"none"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := p.ValidateSessionConfiguration("mcode", []byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
