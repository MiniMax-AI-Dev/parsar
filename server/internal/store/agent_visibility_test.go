package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// openAuditedTestDB is openTestDB plus a real audit ingester.
func openAuditedTestDB(t *testing.T) (*pgxpool.Pool, *Store) {
	t.Helper()
	db := openTestDB(t)
	ing := audit.NewIngester(audit.NewPostgresSink(sqlc.New(db)), audit.Options{BufferCapacity: 32})
	ing.Start(context.Background())
	t.Cleanup(func() { _ = ing.Stop(context.Background()) })
	return db, New(db, WithAudit(ing))
}

// TestUpdateAgentVisibility_HappyPathAndAudit verifies the column
// flips and an audit event lands on a real change.
func TestUpdateAgentVisibility_HappyPathAndAudit(t *testing.T) {
	db, st := openAuditedTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	var initial string
	if err := db.QueryRow(ctx, "select visibility from agents where id = $1::uuid", ids.ProductAgentID).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	if initial != "workspace" {
		t.Fatalf("seed default visibility = %q, want workspace", initial)
	}

	change, err := st.UpdateAgentVisibility(ctx, ids.ProductAgentID, "tenant", ids.UserID)
	if err != nil {
		t.Fatalf("UpdateAgentVisibility: %v", err)
	}
	if change.OldVisibility != "workspace" || change.NewVisibility != "tenant" {
		t.Errorf("change = %+v", change)
	}
	if change.Noop {
		t.Error("expected noop=false on real change")
	}

	var after string
	if err := db.QueryRow(ctx, "select visibility from agents where id = $1::uuid", ids.ProductAgentID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != "tenant" {
		t.Errorf("after update visibility = %q, want tenant", after)
	}

	// Ingester is async — poll up to ~1s before failing.
	deadline := time.Now().Add(time.Second)
	var auditCount int
	for time.Now().Before(deadline) {
		if err := db.QueryRow(ctx, `
			select count(*) from audit_records
			where event_type = 'agent.visibility.changed'
			  and target_id = $1::uuid
			  and payload->>'from' = 'workspace'
			  and payload->>'to' = 'tenant'
		`, ids.ProductAgentID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if auditCount != 1 {
		t.Errorf("expected 1 audit record, got %d", auditCount)
	}
}

// TestUpdateAgentVisibility_NoopSuppressesAudit: PATCH'ing the same
// value must not pollute the audit log.
func TestUpdateAgentVisibility_NoopSuppressesAudit(t *testing.T) {
	db, st := openAuditedTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	change, err := st.UpdateAgentVisibility(ctx, ids.ProductAgentID, "workspace", ids.UserID)
	if err != nil {
		t.Fatalf("UpdateAgentVisibility: %v", err)
	}
	if !change.Noop {
		t.Errorf("expected noop=true when value unchanged; got %+v", change)
	}

	// Give the (would-be) audit emit time to land before asserting absence.
	time.Sleep(150 * time.Millisecond)

	var auditCount int
	if err := db.QueryRow(ctx, `
		select count(*) from audit_records
		where event_type = 'agent.visibility.changed'
		  and target_id = $1::uuid
	`, ids.ProductAgentID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Errorf("expected 0 audit records on noop, got %d", auditCount)
	}
}

// TestUpdateAgentVisibility_InvalidEnumRejected: both the wrapper's
// up-front validation and the DB check constraint must behave the same.
func TestUpdateAgentVisibility_InvalidEnumRejected(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "Workspace", "private", "everyone", "PUBLIC", "  "} {
		if _, err := New(db).UpdateAgentVisibility(ctx, ids.ProductAgentID, bad, ids.UserID); !errors.Is(err, ErrInvalidAgentVisibility) {
			t.Errorf("UpdateAgentVisibility(%q) error = %v, want ErrInvalidAgentVisibility", bad, err)
		}
	}
}

// TestUpdateAgentVisibility_UnknownAgentReturnsTypedError gives the
// dev handler's 404 mapping a stable underlying signal.
func TestUpdateAgentVisibility_UnknownAgentReturnsTypedError(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, err := New(db).UpdateAgentVisibility(ctx, "00000000-0000-0000-0000-000000099999", "tenant", "00000000-0000-0000-0000-000000000001")
	if !errors.Is(err, ErrUnknownAgent) {
		t.Errorf("unknown agent error = %v, want ErrUnknownAgent", err)
	}
}
