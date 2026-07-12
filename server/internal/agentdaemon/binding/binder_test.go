package binding

import (
	"context"
	"errors"
	"testing"
)

func TestInMemoryBinder_ResolveAndBind(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	if _, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("expected ErrNotBound on empty store, got %v", err)
	}

	in := Binding{
		ConversationID: "conv-1",
		AgentID:        "pa-1",
		DeviceID:       "dev-A",
		WorkDir:        "/home/me/proj",
	}
	if err := b.Bind(ctx, in); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	got, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DeviceID != "dev-A" || got.WorkDir != "/home/me/proj" {
		t.Fatalf("Resolve returned %+v", got)
	}
	if got.AgentKind != "claude_code" {
		t.Fatalf("AgentKind default not applied: %q", got.AgentKind)
	}
}

func TestInMemoryBinder_BindRejectsMissingFields(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	cases := []Binding{
		{AgentID: "pa", DeviceID: "d"},
		{ConversationID: "c", DeviceID: "d"},
		{ConversationID: "c", AgentID: "pa"},
	}
	for i, in := range cases {
		if err := b.Bind(ctx, in); err == nil {
			t.Errorf("case %d: expected error on missing field, got nil", i)
		}
	}
}

func TestInMemoryBinder_RememberSession(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	// RememberSession on a missing binding is a no-op so a stray
	// session callback doesn't materialise a half-formed row.
	if err := b.RememberSession(ctx, Binding{
		ConversationID:   "conv-1",
		AgentID:          "pa-1",
		AgentKind:        "claude_code",
		AgentSessionID:   "claude-sess-xyz",
		AgentSessionType: SessionTypeClaude,
	}); err != nil {
		t.Fatalf("RememberSession on missing binding: %v", err)
	}
	if _, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("RememberSession should not create binding")
	}

	if err := b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-A"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := b.RememberSession(ctx, Binding{
		ConversationID:   "conv-1",
		AgentID:          "pa-1",
		AgentKind:        "claude_code",
		AgentSessionID:   "claude-sess-xyz",
		AgentSessionType: SessionTypeClaude,
	}); err != nil {
		t.Fatalf("RememberSession: %v", err)
	}
	got, _ := b.Resolve(ctx, "conv-1", "pa-1", "claude_code")
	if got.AgentSessionID != "claude-sess-xyz" || got.AgentSessionType != SessionTypeClaude {
		t.Fatalf("agent session not persisted: %+v", got)
	}
	if got.AgentStateKey != "conv-1/pa-1/claude_code" {
		t.Fatalf("AgentStateKey = %q", got.AgentStateKey)
	}
}

func TestInMemoryBinder_RemembersEngineSessionsPerAgentKind(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	if err := b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-A"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := b.RememberSession(ctx, Binding{
		ConversationID:   "conv-1",
		AgentID:          "pa-1",
		AgentKind:        "claude_code",
		AgentSessionID:   "claude-sess-1",
		AgentSessionType: SessionTypeClaude,
	}); err != nil {
		t.Fatalf("RememberSession claude: %v", err)
	}
	if err := b.RememberSession(ctx, Binding{
		ConversationID:   "conv-1",
		AgentID:          "pa-1",
		AgentKind:        "codex",
		AgentSessionID:   "codex-thread-1",
		AgentSessionType: SessionTypeCodex,
	}); err != nil {
		t.Fatalf("RememberSession codex: %v", err)
	}

	claude, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("Resolve claude: %v", err)
	}
	codex, err := b.Resolve(ctx, "conv-1", "pa-1", "codex")
	if err != nil {
		t.Fatalf("Resolve codex: %v", err)
	}
	if claude.AgentSessionID != "claude-sess-1" || claude.AgentSessionType != SessionTypeClaude {
		t.Fatalf("claude session = %+v", claude)
	}
	if codex.AgentSessionID != "codex-thread-1" || codex.AgentSessionType != SessionTypeCodex {
		t.Fatalf("codex session = %+v", codex)
	}
	if claude.DeviceID != "dev-A" || codex.DeviceID != "dev-A" {
		t.Fatalf("runtime binding should be shared, got claude=%q codex=%q", claude.DeviceID, codex.DeviceID)
	}
}

func TestInMemoryBinder_BindWithSessionDoesNotLeakAcrossAgentKinds(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	if err := b.Bind(ctx, Binding{
		ConversationID:   "conv-1",
		AgentID:          "pa-1",
		DeviceID:         "dev-A",
		AgentKind:        "codex",
		AgentSessionID:   "codex-thread-1",
		AgentSessionType: SessionTypeCodex,
	}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	codex, err := b.Resolve(ctx, "conv-1", "pa-1", "codex")
	if err != nil {
		t.Fatalf("Resolve codex: %v", err)
	}
	claude, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("Resolve claude: %v", err)
	}
	if codex.AgentSessionID != "codex-thread-1" {
		t.Fatalf("codex session missing: %+v", codex)
	}
	if claude.AgentSessionID != "" || claude.AgentSessionType != "" {
		t.Fatalf("codex session leaked into claude resolve: %+v", claude)
	}
}

func TestInMemoryBinder_InvalidateConversation(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	must(b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-A"}))
	must(b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-2", DeviceID: "dev-A"}))
	must(b.Bind(ctx, Binding{ConversationID: "conv-2", AgentID: "pa-1", DeviceID: "dev-A"}))

	if err := b.InvalidateConversation(ctx, "conv-1"); err != nil {
		t.Fatalf("InvalidateConversation: %v", err)
	}
	if _, err := b.Resolve(ctx, "conv-1", "pa-1", "claude_code"); !errors.Is(err, ErrNotBound) {
		t.Errorf("conv-1/pa-1 should be evicted")
	}
	if _, err := b.Resolve(ctx, "conv-1", "pa-2", "claude_code"); !errors.Is(err, ErrNotBound) {
		t.Errorf("conv-1/pa-2 should be evicted")
	}
	if _, err := b.Resolve(ctx, "conv-2", "pa-1", "claude_code"); err != nil {
		t.Errorf("conv-2 should survive: %v", err)
	}
}

func TestInMemoryBinder_InvalidateDevice(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()
	_ = b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-A"})
	_ = b.Bind(ctx, Binding{ConversationID: "conv-2", AgentID: "pa-1", DeviceID: "dev-A"})
	_ = b.Bind(ctx, Binding{ConversationID: "conv-3", AgentID: "pa-1", DeviceID: "dev-B"})

	if err := b.InvalidateDevice(ctx, "dev-A"); err != nil {
		t.Fatalf("InvalidateDevice: %v", err)
	}
	for _, conv := range []string{"conv-1", "conv-2"} {
		if _, err := b.Resolve(ctx, conv, "pa-1", "claude_code"); !errors.Is(err, ErrNotBound) {
			t.Errorf("%s should be evicted after dev-A invalidation", conv)
		}
	}
	if _, err := b.Resolve(ctx, "conv-3", "pa-1", "claude_code"); err != nil {
		t.Errorf("dev-B binding should survive: %v", err)
	}
}
