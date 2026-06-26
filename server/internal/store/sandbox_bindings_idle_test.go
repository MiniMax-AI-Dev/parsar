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
	idle1 := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductProjectAgentID, "sbx-idle-1", now.Add(-2*time.Hour))
	idle2 := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.BackendProjectAgentID, "sbx-idle-2", now.Add(-90*time.Minute))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.TestProjectAgentID, "sbx-fresh", now.Add(-5*time.Minute))

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
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductProjectAgentID, "sbx-cap-1", now.Add(-2*time.Hour))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.BackendProjectAgentID, "sbx-cap-2", now.Add(-90*time.Minute))
	mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.TestProjectAgentID, "sbx-cap-3", now.Add(-1*time.Hour))

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
	mustCreateBindingWithLastActive(t, context.Background(), s, ids.WorkspaceID, ids.ProductProjectAgentID, "sbx-neg-1", time.Now().Add(-2*time.Hour))

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

	original := mustCreateBindingWithLastActive(t, ctx, s, ids.WorkspaceID, ids.ProductProjectAgentID, "sbx-touch", time.Now().Add(-2*time.Hour))

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

// mustCreateBindingWithLastActive seeds an active binding then directly
// UPDATEs last_active_at via raw SQL so tests can simulate aged rows.
func mustCreateBindingWithLastActive(t *testing.T, ctx context.Context, s *Store, workspaceID, projectAgentID, sandboxID string, lastActive time.Time) SandboxBindingRead {
	t.Helper()
	row, err := s.CreateSandboxBinding(ctx, CreateSandboxBindingInput{
		WorkspaceID:    workspaceID,
		ProjectAgentID: projectAgentID,
		CacheKey:       "cache-" + sandboxID,
		SandboxID:      sandboxID,
		TemplateID:     "tpl_test",
		Status:         SandboxBindingStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateSandboxBinding %s: %v", sandboxID, err)
	}
	if _, err := s.db.Exec(ctx, `update sandboxes set last_active_at = $1 where id = $2::uuid`, lastActive, row.ID); err != nil {
		t.Fatalf("backdate last_active_at: %v", err)
	}
	return row
}
