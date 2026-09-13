package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestEnvironmentMigrationPreservesHistoryAndGuardsIdentity(t *testing.T) {
	_, pool := testStore(t)
	ctx := context.Background()
	schema := "environment_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.NewString()
	for _, kind := range []string{"none", "self_hosted"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id,tenant_id,engine,idempotency_key,request_hash,configuration)
            VALUES ($1,$2,'codex',$3,'historical',jsonb_build_object('environment',jsonb_build_object('type',$3::text)))`, uuid.NewString(), tenant, kind); err != nil {
			t.Fatal(err)
		}
	}
	history := func() string {
		t.Helper()
		var snapshot string
		if err := db.QueryRowContext(ctx, "SELECT jsonb_agg(to_jsonb(s) ORDER BY id)::text FROM sessions s").Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := history()
	if _, err := provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if after := history(); after != before {
		t.Fatal("migration changed historical Sessions")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM environments").Scan(&count); err != nil || count != 0 {
		t.Fatal("manufactured historical ownership", count, err)
	}
	if _, err := provider.DownTo(ctx, 19); err != nil {
		t.Fatal("empty downgrade failed", err)
	}
	if _, err := provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	poolConfig := pool.Config()
	poolConfig.ConnConfig = cfg
	migrated, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(migrated.Close)
	s := New(migrated)

	sessionID, environmentID := uuid.NewString(), uuid.NewString()
	tx, err := migrated.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	input := environmentInput("new", "self_hosted", "/workspace")
	if _, err := tx.Exec(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
        VALUES ($1,$2,'codex','new','new',$3)`, sessionID, tenant, input.Configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO environments(id,session_id) VALUES ($1,$2)", environmentID, sessionID); err != nil {
		t.Fatal(err)
	}
	downgradeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	outcome := make(chan error, 1)
	go func() { _, err := provider.DownTo(downgradeCtx, 19); outcome <- err }()
	// The uncommitted creator must block the downgrade before its emptiness decision.
	for {
		var blocked bool
		err := pool.QueryRow(downgradeCtx, `SELECT EXISTS (
            SELECT 1 FROM pg_locks WHERE relation=$1::regclass
              AND mode='AccessExclusiveLock' AND NOT granted)`, quoted+".environments").Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-outcome:
			t.Fatal("downgrade did not wait for creator", err)
		case <-downgradeCtx.Done():
			t.Fatal("downgrade lock was not observed")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-outcome; err == nil || !strings.Contains(err.Error(), "Cannot remove durable Environment identities") {
		t.Fatal("concurrent downgrade discarded identity", err)
	}
	retained, err := s.GetEnvironment(ctx, tenant, environmentID)
	if err != nil || retained.ID != environmentID || retained.SessionID != sessionID {
		t.Fatal("downgrade destroyed ownership", retained, err)
	}
}
