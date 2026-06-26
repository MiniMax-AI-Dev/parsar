package agentdaemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestMergeSystemPromptsIntoOptions_AppendPrependsToBase(t *testing.T) {
	opts := map[string]any{"system_prompt": "user base"}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "cap-a", Mode: canonical.SystemPromptModeAppend, Content: "rule one"},
	})
	want := "rule one\n\nuser base"
	if got, _ := opts["system_prompt"].(string); got != want {
		t.Fatalf("system_prompt=%q, want %q", got, want)
	}
	if _, ok := opts["override_system_prompt"]; ok {
		t.Fatalf("override_system_prompt should not be set in append-only path")
	}
}

func TestMergeSystemPromptsIntoOptions_AppendMultipleJoined(t *testing.T) {
	opts := map[string]any{"system_prompt": "base"}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "a", Mode: canonical.SystemPromptModeAppend, Content: "first"},
		{Name: "b", Mode: canonical.SystemPromptModeAppend, Content: "second"},
	})
	want := "first\n\nsecond\n\nbase"
	if got, _ := opts["system_prompt"].(string); got != want {
		t.Fatalf("system_prompt=%q, want %q", got, want)
	}
}

func TestMergeSystemPromptsIntoOptions_AppendWithoutBase(t *testing.T) {
	opts := map[string]any{}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "a", Mode: canonical.SystemPromptModeAppend, Content: "only"},
	})
	if got, _ := opts["system_prompt"].(string); got != "only" {
		t.Fatalf("system_prompt=%q, want %q", got, "only")
	}
}

func TestMergeSystemPromptsIntoOptions_OverrideDropsExistingAndAppend(t *testing.T) {
	opts := map[string]any{"system_prompt": "user base"}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "append-cap", Mode: canonical.SystemPromptModeAppend, Content: "append-ignored"},
		{Name: "override-cap", Mode: canonical.SystemPromptModeOverride, Content: "you are pirate"},
	})
	if _, ok := opts["system_prompt"]; ok {
		t.Fatalf("system_prompt should be dropped when override is active")
	}
	if got, _ := opts["override_system_prompt"].(string); got != "you are pirate" {
		t.Fatalf("override_system_prompt=%q, want %q", got, "you are pirate")
	}
}

func TestMergeSystemPromptsIntoOptions_MultipleOverrideJoined(t *testing.T) {
	opts := map[string]any{}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "a", Mode: canonical.SystemPromptModeOverride, Content: "first"},
		{Name: "b", Mode: canonical.SystemPromptModeOverride, Content: "second"},
	})
	if got, _ := opts["override_system_prompt"].(string); got != "first\n\nsecond" {
		t.Fatalf("override_system_prompt=%q, want %q", got, "first\n\nsecond")
	}
}

func TestMergeSystemPromptsIntoOptions_EmptyContentSkipped(t *testing.T) {
	opts := map[string]any{"system_prompt": "base"}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "blank", Mode: canonical.SystemPromptModeAppend, Content: "   "},
	})
	if got, _ := opts["system_prompt"].(string); got != "base" {
		t.Fatalf("system_prompt=%q, want %q", got, "base")
	}
}

func TestMergeSystemPromptsIntoOptions_OverrideThenSpecMemorySkipsAppend(t *testing.T) {
	// Sanity-check the cross-function contract: when override sets
	// override_system_prompt, the subsequent applySpecMemoryInjection
	// no-ops via its existing guard (model_injection.go:423).
	sm := &fakeSpecMemory{text: "<mem>x</mem>"}
	c := newInjectionTestConn(t, sm)
	opts := map[string]any{"system_prompt": "user base"}
	mergeSystemPromptsIntoOptions(opts, []ResolvedSystemPrompt{
		{Name: "ov", Mode: canonical.SystemPromptModeOverride, Content: "pirate"},
	})
	in := basicInput()
	in.WorkspaceID = "ws-1"
	in.ConversationInitiatorID = "user-1"
	c.applySpecMemoryInjection(t.Context(), opts, in)

	if _, ok := opts["system_prompt"]; ok {
		t.Fatalf("system_prompt should stay absent when override is set")
	}
	if got, _ := opts["override_system_prompt"].(string); got != "pirate" {
		t.Fatalf("override_system_prompt=%q, want pirate", got)
	}
}

func TestResolveCapabilityAdditions_SystemPromptKindResolved(t *testing.T) {
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSystemPrompt,
		SystemPrompt: &canonical.SystemPromptSpec{
			Prompt: "Always answer in Chinese.",
			Mode:   canonical.SystemPromptModeAppend,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "cap-sp",
		Name:          "house-style",
		Type:          "system_prompt",
		CanonicalSpec: raw,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.SystemPrompts) != 1 {
		t.Fatalf("want 1 system_prompt, got %d: %+v", len(got.SystemPrompts), got.SystemPrompts)
	}
	resolved := got.SystemPrompts[0]
	if resolved.Mode != canonical.SystemPromptModeAppend {
		t.Fatalf("mode=%q, want append", resolved.Mode)
	}
	if resolved.Content != "Always answer in Chinese." {
		t.Fatalf("content=%q", resolved.Content)
	}
	if resolved.Name != "house-style" {
		t.Fatalf("name=%q, want house-style", resolved.Name)
	}
}

func TestResolveCapabilityAdditions_SystemPromptKindMismatchErrors(t *testing.T) {
	mismatched := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{Name: "x", Command: "true"}},
		},
	}
	raw, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "bad",
		Name:          "bad-row",
		Type:          "system_prompt",
		CanonicalSpec: raw,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	if _, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code"); err == nil {
		t.Fatal("expected error for system_prompt row with mcp canonical_spec")
	}
}
