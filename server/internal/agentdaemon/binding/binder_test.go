package binding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInMemoryBinder_ResolveAndBind(t *testing.T) {
	b := NewInMemoryBinder()
	ctx := context.Background()

	if _, err := b.Resolve(ctx, "conv-1", "pa-1"); !errors.Is(err, ErrNotBound) {
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

	got, err := b.Resolve(ctx, "conv-1", "pa-1")
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
	if err := b.RememberSession(ctx, "conv-1", "pa-1", "claude-sess-xyz", time.Time{}); err != nil {
		t.Fatalf("RememberSession on missing binding: %v", err)
	}
	if _, err := b.Resolve(ctx, "conv-1", "pa-1"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("RememberSession should not create binding")
	}

	if err := b.Bind(ctx, Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-A"}); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := b.RememberSession(ctx, "conv-1", "pa-1", "claude-sess-xyz", time.Time{}); err != nil {
		t.Fatalf("RememberSession: %v", err)
	}
	got, _ := b.Resolve(ctx, "conv-1", "pa-1")
	if got.ClaudeSessionID != "claude-sess-xyz" {
		t.Fatalf("ClaudeSessionID not persisted: %+v", got)
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
	if _, err := b.Resolve(ctx, "conv-1", "pa-1"); !errors.Is(err, ErrNotBound) {
		t.Errorf("conv-1/pa-1 should be evicted")
	}
	if _, err := b.Resolve(ctx, "conv-1", "pa-2"); !errors.Is(err, ErrNotBound) {
		t.Errorf("conv-1/pa-2 should be evicted")
	}
	if _, err := b.Resolve(ctx, "conv-2", "pa-1"); err != nil {
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
		if _, err := b.Resolve(ctx, conv, "pa-1"); !errors.Is(err, ErrNotBound) {
			t.Errorf("%s should be evicted after dev-A invalidation", conv)
		}
	}
	if _, err := b.Resolve(ctx, "conv-3", "pa-1"); err != nil {
		t.Errorf("dev-B binding should survive: %v", err)
	}
}
