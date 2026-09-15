package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestEnvironmentInputMigrationRetainsHistoryAndRetryIdentity(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	schema := "environment_input_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["search_path"] = schema
	db := sql.OpenDB(stdlib.GetConnector(*cfg))
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 21); err != nil {
		t.Fatal(err)
	}
	tenant, session := uuid.NewString(), uuid.NewString()
	// Historical schemas must be seeded without the current Store's creation contract.
	input := environmentInput("session", "self_hosted", "/workspace")
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
		VALUES ($1,$2,'codex','session','historical',$3)`, session, tenant, input.Configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO environments(id,session_id) VALUES ($1,$2)", uuid.NewString(), session); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", session).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM environment_input_reservations").Scan(&count); err != nil || count != 0 {
		t.Fatal("migration manufactured reservations", count, err)
	}
	if _, err := provider.DownTo(ctx, 21); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if _, err := provider.UpTo(ctx, 22); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", session).Scan(&after); err != nil || before != after {
		t.Fatal("migration changed Session", err)
	}
	// Seed the historical schema directly; current queries require later columns.
	pending := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,created_at,deadline)
		VALUES ($1,$2,'pending','[{"kind":"message","payload":{"text":"pending"}}]',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, pending, session); err != nil {
		t.Fatal(err)
	}
	var original, retained string
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(r)::text FROM environment_input_reservations r WHERE id=$1", pending).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 21); err == nil || !strings.Contains(err.Error(), "Cannot remove durable Environment input identities") {
		t.Fatal("downgrade discarded retry identity", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(r)::text FROM environment_input_reservations r WHERE id=$1", pending).Scan(&retained); err != nil || retained != original {
		t.Fatal("downgrade lost reservation", retained, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", session).Scan(&after); err != nil || before != after {
		t.Fatal("migration changed Session", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM turns WHERE session_id=$1", session).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration manufactured a Turn", count, err)
	}
}

func TestEnvironmentInputPromotionUsesCurrentExecutionWriter(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := environmentInputSession(t, s)
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	if _, err := s.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID); err == nil {
		t.Fatal("pooled Store promoted input without execution ownership")
	}
	closed := executionLease(t, s)
	if err := closed.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := closed.Store().PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID); err == nil {
		t.Fatal("closed execution writer promoted pending input")
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	old := executionLease(t, s)
	writer := old.Store()
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", old.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	successor := executionLease(t, s)
	if _, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID); err == nil {
		t.Fatal("stale execution writer promoted pending input")
	}
	environmentInputHistory(t, pool, session.ID, 0, 0)
	got, err := successor.Store().PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
	if err != nil || got.State != EnvironmentInputAdmitted {
		t.Fatal("successor could not promote", got, err)
	}
	environmentInputHistory(t, pool, session.ID, 1, 2)
}
