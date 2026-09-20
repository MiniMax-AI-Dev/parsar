package store

import "testing"

func TestCoreAgentExecutionDefaults(t *testing.T) {
	for _, config := range []map[string]any{
		{"model": "fixture", "x_agents_core": map[string]any{"harness": "claude_sdk"}, "environment": map[string]any{"type": "openai_hosted", "environment_template_id": "tmpl"}},
		{"model": "fixture", "x_agents_core": nil, "environment": map[string]any{"type": "none"}},
	} {
		if err := ValidateCoreAgentConfig(config, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, config := range []map[string]any{
		{"model": "fixture", "x_agents_core": map[string]any{"harness": "unknown"}},
		{"model": "fixture", "x_agents_core": map[string]any{"harness": "codex", "other": true}},
		{"model": "fixture", "environment": map[string]any{"type": "openai_hosted", "env": map[string]any{"SECRET": "private"}}},
		{"model": "fixture", "environment": nil},
	} {
		if err := ValidateCoreAgentConfig(config, true); err == nil {
			t.Fatalf("accepted %#v", config)
		}
	}
	metadata, err := conversationCoreMetadata(CreateWorkspaceConversationInput{})
	if err != nil || metadata["core_environment"] != nil {
		t.Fatalf("empty chat froze environment: %#v %v", metadata, err)
	}
}
