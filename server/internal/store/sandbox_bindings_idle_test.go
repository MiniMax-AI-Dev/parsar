package store

import (
	"context"
	"testing"
	"time"
)

// TestListIdleSandboxBindings: idle-list filters to active+older-than-cutoff,
// ordered oldest first, capped at limit. Requires PARSAR_TEST_DATABASE_URL.
func TestListIdleSandboxBindings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)

	now := time.Now().UTC()
	idle1 := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductAgentID, "sbx-idle-1", now.Add(-2*time.Hour))
	idle2 := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.BackendAgentID, "sbx-idle-2", now.Add(-90*time.Minute))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.TestAgentID, "sbx-fresh", now.Add(-5*time.Minute))

	cutoff := now.Add(-30 * time.Minute)
	rows, err := s.ListIdleSandboxBindings(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("ListIdleSandboxBindings: %v", err)
	}
	if len(rows) != 2 {
		var got []string
		for _, r := range rows {
			got = append(got, r.SandboxID)
		}
		t.Fatalf("idle rows: got %d (%v) want 2 (sbx-idle-1, sbx-idle-2)", len(rows), got)
	}
	if rows[0].ID != idle1.ID {
		t.Errorf("oldest first: got %s want %s", rows[0].ID, idle1.ID)
	}
	if rows[1].ID != idle2.ID {
		t.Errorf("second: got %s want %s", rows[1].ID, idle2.ID)
	}

	if err := s.MarkSandboxBindingKilled(ctx, idle1.ID, SandboxBindingStatusKilled); err != nil {
		t.Fatalf("MarkSandboxBindingKilled: %v", err)
	}
	rows, err = s.ListIdleSandboxBindings(ctx, cutoff, 100)
	if err != nil {
		t.Fatalf("ListIdleSandboxBindings post-kill: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != idle2.ID {
		t.Errorf("after killing idle1: got %d rows want [idle2 only]", len(rows))
	}
}

func TestListIdleSandboxBindings_LimitCap(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)

	now := time.Now().UTC()
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductAgentID, "sbx-cap-1", now.Add(-2*time.Hour))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.BackendAgentID, "sbx-cap-2", now.Add(-90*time.Minute))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.TestAgentID, "sbx-cap-3", now.Add(-1*time.Hour))

	cutoff := now.Add(-30 * time.Minute)
	rows, err := s.ListIdleSandboxBindings(ctx, cutoff, 2)
	if err != nil {
		t.Fatalf("ListIdleSandboxBindings: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("limit=2: got %d want 2", len(rows))
	}
}

func TestListIdleSandboxBindings_NegativeLimitNormalized(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)
	mustCreateBindingWithLastActive(t, context.Background(), s, ids.WorkspaceID, ids.ProductAgentID, "sbx-neg-1", time.Now().Add(-2*time.Hour))

	// limit=0 must normalize to 1, not error.
	rows, err := s.ListIdleSandboxBindings(ctx, time.Now(), 0)
	if err != nil {
		t.Fatalf("ListIdleSandboxBindings limit=0: %v", err)
	}
	if len(rows) > 1 {
		t.Errorf("limit=0 normalized: got %d rows want <= 1", len(rows))
	}
}

func TestTouchSandboxBinding(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)

	original := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductAgentID, "sbx-touch", time.Now().Add(-2*time.Hour))

	if err := s.TouchSandboxBinding(ctx, original.ID); err != nil {
		t.Fatalf("TouchSandboxBinding: %v", err)
	}

	rows, err := s.ListIdleSandboxBindings(ctx, time.Now().Add(-5*time.Minute), 100)
	if err != nil {
		t.Fatalf("ListIdleSandboxBindings post-touch: %v", err)
	}
	for _, r := range rows {
		if r.ID == original.ID {
			t.Errorf("after touch, idle list still contains the touched row %s (last_active_at=%s)", r.ID, r.LastActiveAt)
		}
	}
}

func TestTouchSandboxBinding_EmptyID(t *testing.T) {
	db := openTestDB(t)
	s := New(db)
	if err := s.TouchSandboxBinding(context.Background(), "  "); err == nil {
		t.Error("empty binding id: want error, got nil")
	}
}

func TestTouchSandboxBinding_UnknownIDIsNoOp(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)
	if err := s.TouchSandboxBinding(ctx, "00000000-0000-0000-0000-000000009999"); err != nil {
		t.Errorf("unknown binding id: want nil error, got %v", err)
	}
}

func TestSandboxBindingAutoRenewClaimLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)
	row := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductAgentID, "sbx-auto-renew", time.Now().UTC())
	now := time.Now().UTC()
	if err := s.ConfigureSandboxBindingLease(ctx, row.ID, 3600, 600, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("ConfigureSandboxBindingLease: %v", err)
	}
	claims, err := s.ClaimSandboxBindingsDueForAutoRenew(ctx, now, 10)
	if err != nil {
		t.Fatalf("ClaimSandboxBindingsDueForAutoRenew: %v", err)
	}
	if len(claims) != 1 || claims[0].BindingID != row.ID || claims[0].TimeoutSeconds != 3600 {
		t.Fatalf("claims = %+v, want binding %s", claims, row.ID)
	}
	claimsAgain, err := s.ClaimSandboxBindingsDueForAutoRenew(ctx, now, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(claimsAgain) != 0 {
		t.Fatalf("renewing binding was claimed twice: %+v", claimsAgain)
	}
	if _, err := s.db.Exec(ctx, `update sandboxes set last_renewed_at = $1 where id = $2::uuid`, now.Add(-3*time.Minute), row.ID); err != nil {
		t.Fatalf("backdate abandoned renewal claim: %v", err)
	}
	reclaimed, err := s.ClaimSandboxBindingsDueForAutoRenew(ctx, now, 10)
	if err != nil {
		t.Fatalf("reclaim abandoned renewal: %v", err)
	}
	if len(reclaimed) != 1 || reclaimed[0].BindingID != row.ID {
		t.Fatalf("abandoned renewal claim was not reclaimed: %+v", reclaimed)
	}
	newExpiry := now.Add(time.Hour)
	if err := s.CompleteSandboxBindingRenew(ctx, row.ID, newExpiry); err != nil {
		t.Fatalf("CompleteSandboxBindingRenew: %v", err)
	}
	got, found, err := s.GetActiveSandboxBindingByAgentID(ctx, ids.ProductAgentID)
	if err != nil || !found {
		t.Fatalf("GetActiveSandboxBindingByAgentID found=%v err=%v", found, err)
	}
	if got.Status != SandboxBindingStatusActive || got.ExpiresAt == nil || got.ExpiresAt.Before(newExpiry.Add(-time.Second)) {
		t.Fatalf("renewed binding = %+v", got)
	}
	if err := s.ConfigureSandboxBindingLease(ctx, row.ID, 3600, 600, now.Add(5*time.Minute)); err != nil {
		t.Fatalf("reconfigure lease: %v", err)
	}
	claims, err = s.ClaimSandboxBindingsDueForAutoRenew(ctx, now, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim before failure = %+v, err=%v", claims, err)
	}
	if err := s.FailSandboxBindingRenew(ctx, row.ID); err != nil {
		t.Fatalf("FailSandboxBindingRenew: %v", err)
	}
	claims, err = s.ClaimSandboxBindingsDueForAutoRenew(ctx, now.Add(time.Hour), 10)
	if err != nil || len(claims) != 0 {
		t.Fatalf("failed renewal should be disabled, claims=%+v err=%v", claims, err)
	}
}

func TestReclaimAbandonedSandboxBindingCAS(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)

	stale, won, err := s.ReserveSandboxBindingSlot(ctx, ReserveSandboxBindingSlotInput{
		WorkspaceID: ids.WorkspaceID, AgentID: ids.ProductAgentID,
		CacheKey: "agent_daemon:" + ids.ProductAgentID, TemplateID: "tpl_test",
	})
	if err != nil || !won {
		t.Fatalf("reserve stale row: won=%v err=%v", won, err)
	}
	reclaimed, err := s.ReclaimAbandonedSandboxBinding(ctx, stale.ID, time.Now().UTC().Add(time.Minute))
	if err != nil || !reclaimed {
		t.Fatalf("reclaim stale spawning row: reclaimed=%v err=%v", reclaimed, err)
	}
	reclaimedAgain, err := s.ReclaimAbandonedSandboxBinding(ctx, stale.ID, time.Now().UTC().Add(time.Minute))
	if err != nil || reclaimedAgain {
		t.Fatalf("second CAS must lose: reclaimed=%v err=%v", reclaimedAgain, err)
	}

	finalized, won, err := s.ReserveSandboxBindingSlot(ctx, ReserveSandboxBindingSlotInput{
		WorkspaceID: ids.WorkspaceID, AgentID: ids.BackendAgentID,
		CacheKey: "agent_daemon:" + ids.BackendAgentID, TemplateID: "tpl_test",
	})
	if err != nil || !won {
		t.Fatalf("reserve finalized row: won=%v err=%v", won, err)
	}
	if err := s.FinalizeSandboxBindingSpawning(ctx, FinalizeSandboxBindingSpawningInput{
		BindingID: finalized.ID, SandboxID: "sbx-finalized-cas",
		Metadata: map[string]any{"sandbox_kind": "agent_daemon", "device_id": "dev-finalized"},
	}); err != nil {
		t.Fatalf("finalize row: %v", err)
	}
	reclaimedFinalized, err := s.ReclaimAbandonedSandboxBinding(ctx, finalized.ID, time.Now().UTC().Add(time.Minute))
	if err != nil || reclaimedFinalized {
		t.Fatalf("CAS must not retire finalized row: reclaimed=%v err=%v", reclaimedFinalized, err)
	}
}

// mustCreateBindingWithLastActive seeds an active binding then directly
// UPDATEs last_active_at via raw SQL so tests can simulate aged rows.
func mustCreateBindingWithLastActive(t *testing.T, ctx context.Context, s *Store, workspaceID, agentID, sandboxID string, lastActive time.Time) SandboxBindingRead {
	t.Helper()
	row, err := s.CreateSandboxBinding(ctx, CreateSandboxBindingInput{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		CacheKey:    "cache-" + sandboxID,
		SandboxID:   sandboxID,
		TemplateID:  "tpl_test",
		Status:      SandboxBindingStatusActive,
		Metadata:    map[string]any{"sandbox_kind": "agent_daemon"},
	})
	if err != nil {
		t.Fatalf("CreateSandboxBinding %s: %v", sandboxID, err)
	}
	if _, err := s.db.Exec(ctx, `update sandboxes set last_active_at = $1 where id = $2::uuid`, lastActive, row.ID); err != nil {
		t.Fatalf("backdate last_active_at: %v", err)
	}
	return row
}
