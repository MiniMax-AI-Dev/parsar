package store

import (
	"context"
	"testing"
	"time"
)

func TestSandboxPoolPersistence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := s.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, "delete from sandboxes"); err != nil {
		t.Fatalf("reset sandboxes: %v", err)
	}

	const timeoutSec = int32(3600)
	now := time.Now().UTC()
	expires1 := now.Add(time.Duration(timeoutSec) * time.Second)

	if err := s.CreateSandboxPoolEntry(ctx, ids.WorkspaceID, "sbx-pool-1", "parsar-opencode-base", expires1, timeoutSec); err != nil {
		t.Fatalf("CreateSandboxPoolEntry sbx-pool-1: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	expires2 := time.Now().UTC().Add(time.Duration(timeoutSec) * time.Second)
	if err := s.CreateSandboxPoolEntry(ctx, ids.WorkspaceID, "sbx-pool-2", "parsar-opencode-base", expires2, timeoutSec); err != nil {
		t.Fatalf("CreateSandboxPoolEntry sbx-pool-2: %v", err)
	}

	// Re-creating the same id is a no-op (ON CONFLICT DO NOTHING) — a
	// flapping server must not blow up on its own previous lifetime.
	if err := s.CreateSandboxPoolEntry(ctx, ids.WorkspaceID, "sbx-pool-1", "parsar-opencode-base", expires1, timeoutSec); err != nil {
		t.Fatalf("CreateSandboxPoolEntry duplicate must no-op: %v", err)
	}

	rows, err := s.ListActiveSandboxPoolEntries(ctx, ids.WorkspaceID, 10, 0)
	if err != nil {
		t.Fatalf("ListActiveSandboxPoolEntries: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 active rows, got %d", len(rows))
	}
	if rows[0].SandboxID != "sbx-pool-2" || rows[1].SandboxID != "sbx-pool-1" {
		t.Errorf("listing not newest-first: %+v", rows)
	}
	for _, r := range rows {
		if r.Status != SandboxPoolEntryStatusIdle {
			t.Errorf("new row status=%q want %q", r.Status, SandboxPoolEntryStatusIdle)
		}
		if r.KilledAt != nil {
			t.Errorf("new row should have nil killed_at; got %v", r.KilledAt)
		}
		if r.TimeoutSeconds != timeoutSec {
			t.Errorf("new row timeout_seconds=%d want %d", r.TimeoutSeconds, timeoutSec)
		}
		if r.AutoRenewThresholdSeconds != 0 {
			t.Errorf("new row auto_renew_threshold_seconds=%d want 0 (default)", r.AutoRenewThresholdSeconds)
		}
		if r.ExpiresAt.IsZero() {
			t.Errorf("new row expires_at must not be zero")
		}
	}

	beforeRenew := rows[1].LastRenewedAt
	beforeExpires := rows[1].ExpiresAt
	time.Sleep(20 * time.Millisecond)
	renewedExpires := time.Now().UTC().Add(time.Duration(timeoutSec) * time.Second).Add(time.Minute)
	if err := s.TouchSandboxPoolRenewed(ctx, "sbx-pool-1", renewedExpires); err != nil {
		t.Fatalf("TouchSandboxPoolRenewed: %v", err)
	}
	rowsAfter, err := s.ListActiveSandboxPoolEntries(ctx, ids.WorkspaceID, 10, 0)
	if err != nil {
		t.Fatalf("ListActiveSandboxPoolEntries after renew: %v", err)
	}
	var pool1 *SandboxPoolEntryRead
	for i, r := range rowsAfter {
		if r.SandboxID == "sbx-pool-1" {
			pool1 = &rowsAfter[i]
			break
		}
	}
	if pool1 == nil {
		t.Fatal("sbx-pool-1 missing after renew")
	}
	if !pool1.LastRenewedAt.After(beforeRenew) {
		t.Errorf("last_renewed_at not advanced: before=%v after=%v", beforeRenew, pool1.LastRenewedAt)
	}
	if !pool1.ExpiresAt.After(beforeExpires) {
		t.Errorf("expires_at not rolled forward: before=%v after=%v", beforeExpires, pool1.ExpiresAt)
	}

	// Claim transitions status without killed_at so the row stays in active listing.
	if err := s.MarkSandboxPoolEntryClaimed(ctx, ids.WorkspaceID, ids.BackendAgentID, "pool-cache-key", "sbx-pool-2"); err != nil {
		t.Fatalf("MarkSandboxPoolEntryClaimed: %v", err)
	}
	rowsAfterClaim, err := s.ListActiveSandboxPoolEntries(ctx, ids.WorkspaceID, 10, 0)
	if err != nil {
		t.Fatalf("ListActiveSandboxPoolEntries after claim: %v", err)
	}
	if len(rowsAfterClaim) != 2 {
		t.Fatalf("after claim expected 2 active rows (claimed still visible), got %d: %+v", len(rowsAfterClaim), rowsAfterClaim)
	}
	var pool2 *SandboxPoolEntryRead
	for i, r := range rowsAfterClaim {
		if r.SandboxID == "sbx-pool-2" {
			pool2 = &rowsAfterClaim[i]
			break
		}
	}
	if pool2 == nil {
		t.Fatal("sbx-pool-2 missing after claim")
	}
	if pool2.Status != SandboxPoolEntryStatusClaimed {
		t.Errorf("post-claim status=%q want %q", pool2.Status, SandboxPoolEntryStatusClaimed)
	}
	if pool2.KilledAt != nil {
		t.Errorf("claimed row must not set killed_at; got %v", pool2.KilledAt)
	}

	if err := s.SetSandboxPoolAutoRenewThreshold(ctx, "sbx-pool-1", 300); err != nil {
		t.Fatalf("SetSandboxPoolAutoRenewThreshold: %v", err)
	}
	got, ok, err := s.GetSandboxPoolEntry(ctx, ids.WorkspaceID, "sbx-pool-1")
	if err != nil || !ok {
		t.Fatalf("GetSandboxPoolEntry sbx-pool-1: ok=%v err=%v", ok, err)
	}
	if got.AutoRenewThresholdSeconds != 300 {
		t.Errorf("auto_renew_threshold_seconds=%d want 300", got.AutoRenewThresholdSeconds)
	}

	// SQL returns ALL threshold>0 rows; Go-side filter is the contract.
	dueRows, err := s.ListSandboxPoolEntriesDueForAutoRenew(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("ListSandboxPoolEntriesDueForAutoRenew: %v", err)
	}
	if len(dueRows) != 1 || dueRows[0].SandboxID != "sbx-pool-1" {
		t.Errorf("expected sbx-pool-1 in auto-renew scan, got %+v", dueRows)
	}

	if err := s.MarkSandboxPoolEntryKilled(ctx, "sbx-pool-1", SandboxPoolEntryStatusKilled); err != nil {
		t.Fatalf("MarkSandboxPoolEntryKilled: %v", err)
	}
	rowsAfterKill, err := s.ListActiveSandboxPoolEntries(ctx, ids.WorkspaceID, 10, 0)
	if err != nil {
		t.Fatalf("ListActiveSandboxPoolEntries after kill: %v", err)
	}
	if len(rowsAfterKill) != 1 || rowsAfterKill[0].SandboxID != "sbx-pool-2" {
		t.Errorf("after kill expected only sbx-pool-2 active; got %+v", rowsAfterKill)
	}

	cnt, err := s.CountActiveSandboxPoolEntries(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("CountActiveSandboxPoolEntries: %v", err)
	}
	if cnt != 1 {
		t.Errorf("CountActiveSandboxPoolEntries=%d want 1 (sbx-pool-2 claimed-but-alive)", cnt)
	}

	if err := s.MarkSandboxPoolEntryKilled(ctx, "sbx-pool-1", SandboxPoolEntryStatusKilled); err != nil {
		t.Fatalf("MarkSandboxPoolEntryKilled idempotent must no-op: %v", err)
	}

	if err := s.MarkSandboxPoolEntryKilled(ctx, "sbx-pool-2", "spawning"); err == nil {
		t.Error("MarkSandboxPoolEntryKilled with bad status must error")
	}

	if err := s.SetSandboxPoolAutoRenewThreshold(ctx, "sbx-pool-2", -1); err == nil {
		t.Error("SetSandboxPoolAutoRenewThreshold with negative seconds must error")
	}
}

// TestSweepOrphanedSandboxPoolEntries: startup sweep turns every active row
// (idle / renewing / claimed) into killed_orphaned; killed rows are left alone.
func TestSweepOrphanedSandboxPoolEntries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := s.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(ctx, "delete from sandboxes"); err != nil {
		t.Fatalf("reset sandboxes: %v", err)
	}

	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	type seed struct {
		id     string
		status string
		killed *time.Time
	}
	seeds := []seed{
		{"sbx-stale-idle", SandboxPoolEntryStatusIdle, nil},
		{"sbx-stale-renewing", SandboxPoolEntryStatusRenewing, nil},
		{"sbx-stale-claimed", SandboxPoolEntryStatusClaimed, nil},
		{"sbx-already-killed", SandboxPoolEntryStatusKilled, &now},
	}
	for _, sd := range seeds {
		if _, err := db.Exec(ctx,
			`insert into sandboxes (id, workspace_id, agent_id, cache_key, sandbox_id, template_id, lifecycle_status, allocation_status, killed_at, expires_at, timeout_seconds, created_at, last_active_at, last_renewed_at) values (gen_random_uuid(), $1, case when $4 = 'claimed' then $2::uuid else null end, case when $4 = 'claimed' then 'claimed-cache-key' else null end, $3, $5, (case when $4 in ('idle', 'claimed') then 'running' else $4 end), (case when $4 = 'claimed' then 'bound' when $4 = 'killed' then 'released' else 'pooled' end), $6, $7, $8, now(), now(), now())`,
			ids.WorkspaceID, ids.BackendAgentID, sd.id, sd.status, "parsar-opencode-base", sd.killed, expires, int32(3600),
		); err != nil {
			t.Fatalf("seed %s: %v", sd.id, err)
		}
	}

	swept, err := s.SweepOrphanedSandboxPoolEntries(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanedSandboxPoolEntries: %v", err)
	}
	if swept != 3 {
		t.Errorf("swept %d rows; want 3 (idle + renewing + claimed)", swept)
	}

	rows, err := s.ListActiveSandboxPoolEntries(ctx, ids.WorkspaceID, 100, 0)
	if err != nil {
		t.Fatalf("ListActiveSandboxPoolEntries post-sweep: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 active rows after sweep, got %d: %+v", len(rows), rows)
	}

	var status string
	var killedAt *time.Time
	if err := db.QueryRow(ctx,
		`select lifecycle_status, killed_at from sandboxes where sandbox_id = 'sbx-already-killed'`,
	).Scan(&status, &killedAt); err != nil {
		t.Fatalf("query already-killed row: %v", err)
	}
	if status != SandboxPoolEntryStatusKilled {
		t.Errorf("already-killed row touched by sweep: status=%q", status)
	}

	swept2, err := s.SweepOrphanedSandboxPoolEntries(ctx)
	if err != nil {
		t.Fatalf("idempotent sweep: %v", err)
	}
	if swept2 != 0 {
		t.Errorf("second sweep returned %d, want 0", swept2)
	}
}
