package gateway

import (
	"strings"
	"testing"
)

// TestContextWindowForModel covers the prefix-matching lookup table so
// drift between the runtime's model ids and the footer's `NNk` figure
// is caught at PR time. New rows in modelContextWindowTable should add
// a case here.
func TestContextWindowForModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		model string
		want  int
	}{
		{"empty", "", 0},
		{"unknown", "some-custom-model", 0},
		{"claude-opus-4-1", "claude-opus-4-1-20250805", 200_000},
		{"claude-opus-4-8", "claude-opus-4-8", 200_000},
		{"claude-sonnet-4-5", "claude-sonnet-4-5-20250101", 200_000},
		{"claude-3-5-sonnet", "claude-3-5-sonnet-20241022", 200_000},
		// Generic claude- prefix catches one-off model ids.
		{"claude-mystery-model", "claude-mystery-model", 200_000},
		// Suffix variations the runtime appends (`[1m]`, thinking max)
		// should not knock the lookup off the row.
		{"claude-opus-4-thinking-max", "claude-opus-4-thinking-max", 200_000},
		// OpenAI family.
		{"gpt-4o", "gpt-4o-2024-08-06", 128_000},
		{"gpt-4o-mini", "gpt-4o-mini", 128_000},
		{"gpt-5", "gpt-5", 128_000},
		// Internal gateway models.
		{"deepseek-chat", "deepseek-chat", 65_536},
		{"qwen-2.5-72b", "qwen2.5-72b-instruct", 32_768},
		// Case-insensitive (uppercase input is a real footer case
		// because the runtime sometimes echoes the user's typed model
		// id rather than the canonical slug).
		{"case-insensitive", "Claude-Opus-4-1", 200_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ContextWindowForModel(tc.model)
			if got != tc.want {
				t.Errorf("ContextWindowForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

// TestStepsFromToolCallEvents covers the event-fold that turns
// agent_run_events tool.call payloads into the StepInfo list the
// renderer eats. The fold has to filter on stage="before" / empty
// stage (the "after" variant is the same tool reported back with a
// result; rendering both would double every step on the card), and
// summarise selected args (file_path / command / pattern / url).
func TestStepsFromToolCallEvents(t *testing.T) {
	t.Parallel()
	events := []ToolCallEvent{
		// stage="" — the common case, accept it.
		{EventKind: "tool.call", Payload: map[string]any{"name": "Bash", "args": map[string]any{"command": "ls -la /tmp"}}},
		// stage="before" — explicit accept.
		{EventKind: "tool.call", Payload: map[string]any{"stage": "before", "name": "Read", "args": map[string]any{"file_path": "/Users/x/main.go"}}},
		// stage="after" — reject, it's a duplicate.
		{EventKind: "tool.call", Payload: map[string]any{"stage": "after", "name": "Read"}},
		// Non-tool.call kind — reject.
		{EventKind: "message.delta", Payload: map[string]any{"delta": "hello"}},
		// Missing name — reject (defensive).
		{EventKind: "tool.call", Payload: map[string]any{"args": map[string]any{}}},
		// MCP tool — accept (the icon table falls back to robot_outlined
		// for unknown prefixes, but the step itself still renders).
		{EventKind: "tool.call", Payload: map[string]any{"name": "mcp__github__create_issue"}},
	}
	steps := StepsFromToolCallEvents(events)
	if len(steps) != 3 {
		t.Fatalf("StepsFromToolCallEvents: got %d steps, want 3 (Bash, Read, mcp)", len(steps))
	}
	if steps[0].Tool != "Bash" || !strings.Contains(steps[0].Label, "ls -la") {
		t.Errorf("step[0] = %+v, want Bash + ls -la summary", steps[0])
	}
	if steps[1].Tool != "Read" || !strings.Contains(steps[1].Label, "main.go") {
		t.Errorf("step[1] = %+v, want Read + main.go summary", steps[1])
	}
	if steps[2].Tool != "mcp__github__create_issue" {
		t.Errorf("step[2] tool = %q, want mcp__github__create_issue", steps[2].Tool)
	}
}

// TestStepsFromToolCallEvents_TruncatesLongArgs covers the
// summarise-args fallback for paths that overflow the 60-char
// budget. The renderer reads Label verbatim, so the truncation has
// to happen here.
func TestStepsFromToolCallEvents_TruncatesLongArgs(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 50) + "/" + strings.Repeat("b", 50) + "/file.go"
	events := []ToolCallEvent{
		{EventKind: "tool.call", Payload: map[string]any{"name": "Read", "args": map[string]any{"file_path": long}}},
	}
	steps := StepsFromToolCallEvents(events)
	if len(steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(steps))
	}
	if !strings.Contains(steps[0].Label, "…") {
		t.Errorf("expected middle-truncation ellipsis in label, got %q", steps[0].Label)
	}
	// Label is `Read · <trimmed-path>`. trimMiddleForCard caps the
	// path itself at 60 BYTES (the ellipsis is 3 UTF-8 bytes but only
	// 1 rune), so the upper bound is len("Read · ") + 60 + a small
	// margin for the ellipsis encoding.
	if l := len(steps[0].Label); l > len("Read · ")+62 {
		t.Errorf("label too long: %d chars (label=%q)", l, steps[0].Label)
	}
}

// TestStepsFromToolCallEvents_SkillLabel locks down the Skill row
// label. Regression: the row used to collapse to a bare "Skill"
// because summariseToolArgsForCard had no Skill case — the user
// couldn't tell which skill ran from the card.
func TestStepsFromToolCallEvents_SkillLabel(t *testing.T) {
	t.Parallel()
	events := []ToolCallEvent{
		{EventKind: "tool.call", Payload: map[string]any{"name": "Skill", "args": map[string]any{"skill": "parsar-debug"}}},
		// Skill with no args still degrades cleanly to bare "Skill"
		// rather than crashing.
		{EventKind: "tool.call", Payload: map[string]any{"name": "Skill"}},
	}
	steps := StepsFromToolCallEvents(events)
	if len(steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(steps))
	}
	if steps[0].Tool != "Skill" || steps[0].Label != "Skill · parsar-debug" {
		t.Errorf("step[0] = %+v, want Skill + `Skill · parsar-debug` label", steps[0])
	}
	if steps[1].Tool != "Skill" || steps[1].Label != "Skill" {
		t.Errorf("step[1] = %+v, want bare `Skill` label when args missing", steps[1])
	}
}
