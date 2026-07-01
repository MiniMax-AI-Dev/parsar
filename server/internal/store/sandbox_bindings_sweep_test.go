package store

import (
	"context"
	"testing"
	"time"
)

// TestSweepOrphanedSandboxBindings asserts that only active/spawning/killing
// rows transition to killed_orphaned; already-killed rows stay untouched.
// Requires PARSAR_TEST_DATABASE_URL — skipped otherwise.
func TestSweepOrphanedSandboxBindings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}
	s := New(db)

	// Use three agents so the partial unique index
	// (one active per agent) doesn't trip.
	type want struct {
		bindingID   string
		finalStatus string
		finalKilled bool // was killed_at set after sweep?
	}
	cases := []struct {
		name          string
		agentID       string
		initStatus    string
		preKilled     bool   // mark killed before sweep so it's not orphaned
		want          string // expected status AFTER sweep
		wantKilledSet bool
	}{
		{"active gets orphaned", ids.ProductAgentID, SandboxBindingStatusActive, false, SandboxBindingStatusKilledOrphaned, true},
		{"spawning gets orphaned", ids.BackendAgentID, SandboxBindingStatusSpawning, false, SandboxBindingStatusKilledOrphaned, true},
		{"killing gets orphaned", ids.TestAgentID, SandboxBindingStatusKilling, false, SandboxBindingStatusKilledOrphaned, true},
	}

	var ws []want
	for _, c := range cases {
		row, err := s.CreateSandboxBinding(ctx, CreateSandboxBindingInput{
			WorkspaceID: ids.WorkspaceID,
			AgentID:     c.agentID,
			CacheKey:    "cache_" + c.name,
			SandboxID:   "sbx_" + c.name,
			TemplateID:  "tpl_test",
			Status:      c.initStatus,
		})
		if err != nil {
			t.Fatalf("CreateSandboxBinding(%s): %v", c.name, err)
		}
		ws = append(ws, want{bindingID: row.ID, finalStatus: c.want, finalKilled: c.wantKilledSet})
	}

	n1, err := s.SweepOrphanedSandboxBindings(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanedSandboxBindings (first): %v", err)
	}
	if n1 != int64(len(cases)) {
		t.Errorf("first sweep marked %d; want %d (one per orphanable case)", n1, len(cases))
	}

	// Verify each row transitioned exactly as expected.
	for i, c := range cases {
		row, found, err := s.GetActiveSandboxBindingForAgent(ctx, ids.WorkspaceID, c.agentID)
		if err != nil {
			t.Errorf("GetActiveSandboxBindingForAgent(%s): %v", c.name, err)
			continue
		}
		if found {
			t.Errorf("case %d (%s): GetActive returned row %+v after sweep; expected no live binding", i, c.name, row)
		}
	}

	// Direct table read — there's no GetByID helper, and we need to
	// see the post-sweep killed_at + status the API surface hides.
	for i, w := range ws {
		var status string
		var killedAt *time.Time
		if err := db.QueryRow(ctx, `select lifecycle_status, killed_at from sandboxes where id = $1`, w.bindingID).Scan(&status, &killedAt); err != nil {
			t.Fatalf("case %d direct lookup: %v", i, err)
		}
		if status != w.finalStatus {
			t.Errorf("case %d (%s): final status = %q; want %q", i, cases[i].name, status, w.finalStatus)
		}
		if w.finalKilled && killedAt == nil {
			t.Errorf("case %d (%s): killed_at should be set after sweep; got nil", i, cases[i].name)
		}
	}

	// Sweep must be idempotent — otherwise restart would re-mark already-killed rows.
	n2, err := s.SweepOrphanedSandboxBindings(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanedSandboxBindings (second): %v", err)
	}
	if n2 != 0 {
		t.Errorf("second sweep marked %d; want 0 (sweep should be idempotent)", n2)
	}
}

// TestSweepOrphanedSandboxBindingsEmptyTableIsNoOp pins the empty-DB startup
// case: sweep must not error when there are no historical bindings.
func TestSweepOrphanedSandboxBindingsEmptyTableIsNoOp(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := New(db).InsertDevFixture(ctx, DefaultDevFixtureIDs()); err != nil {
		t.Fatalf("InsertDevFixture: %v", err)
	}

	n, err := New(db).SweepOrphanedSandboxBindings(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanedSandboxBindings: %v", err)
	}
	if n != 0 {
		t.Errorf("empty-table sweep marked %d; want 0", n)
	}
}
