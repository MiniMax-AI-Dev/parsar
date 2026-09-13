package claudesdk

import (
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestNullableSystemPrompt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options map[string]any
		want    string
		reject  bool
	}{
		{"omitted", map[string]any{"model": "test-model"}, "", false},
		{"null", map[string]any{"model": "test-model", "system_prompt": nil}, "", false},
		{"empty", map[string]any{"model": "test-model", "system_prompt": ""}, "", false},
		{"text", map[string]any{"model": "test-model", "system_prompt": "instructions"}, "instructions", false},
		{"null-model", map[string]any{"model": nil}, "", true},
		{"null-unknown", map[string]any{"model": "test-model", "unsupported": nil}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state")}
			start, _, err := prepare(config, proto.PromptRequestPayload{RunID: "run", Prompt: "hello", AgentOptions: tc.options})
			if (err != nil) != tc.reject || (!tc.reject && start.SystemPrompt != tc.want) {
				t.Fatalf("system prompt %q, error %v", start.SystemPrompt, err)
			}
		})
	}
}
