package store

import (
	"context"
	"testing"
)

// TestBuiltinCapabilityEnabled_RoundTrip pins the default-ON semantics of the
// per-agent built-in flag: no row reads as enabled, an explicit false disables,
// and toggling back to true re-enables (via the ON CONFLICT upsert).
func TestBuiltinCapabilityEnabled_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatalf("SeedDevFixture: %v", err)
	}

	const key = "parsar_chat_history"
	agentID := ids.ProductAgentID

	// 1. No row => default ON.
	enabled, err := st.IsBuiltinCapabilityEnabled(ctx, agentID, key)
	if err != nil {
		t.Fatalf("IsBuiltinCapabilityEnabled (no row): %v", err)
	}
	if !enabled {
		t.Fatalf("expected default ON when no row exists, got disabled")
	}

	// 2. Set false => OFF.
	if err := st.SetBuiltinCapabilityEnabled(ctx, agentID, key, false); err != nil {
		t.Fatalf("SetBuiltinCapabilityEnabled(false): %v", err)
	}
	enabled, err = st.IsBuiltinCapabilityEnabled(ctx, agentID, key)
	if err != nil {
		t.Fatalf("IsBuiltinCapabilityEnabled (after false): %v", err)
	}
	if enabled {
		t.Fatalf("expected OFF after setting false, got enabled")
	}

	// 3. Set true => ON again (exercises the ON CONFLICT upsert path).
	if err := st.SetBuiltinCapabilityEnabled(ctx, agentID, key, true); err != nil {
		t.Fatalf("SetBuiltinCapabilityEnabled(true): %v", err)
	}
	enabled, err = st.IsBuiltinCapabilityEnabled(ctx, agentID, key)
	if err != nil {
		t.Fatalf("IsBuiltinCapabilityEnabled (after true): %v", err)
	}
	if !enabled {
		t.Fatalf("expected ON after setting true, got disabled")
	}

	// 4. A different agent is unaffected by the first agent's flag.
	other, err := st.IsBuiltinCapabilityEnabled(ctx, ids.BackendAgentID, key)
	if err != nil {
		t.Fatalf("IsBuiltinCapabilityEnabled (other agent): %v", err)
	}
	if !other {
		t.Fatalf("per-agent flag leaked: other agent should still default ON")
	}
}
