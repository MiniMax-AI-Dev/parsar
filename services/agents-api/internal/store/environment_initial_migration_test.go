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

func TestEnvironmentInitialMigrationPreservesLaterInputOrigin(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "initial_origin_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
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
	if _, err := provider.UpTo(ctx, 28); err != nil {
		t.Fatal(err)
	}
	session, later := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash) VALUES ($1,$2,'codex','historical','historical')`, session, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO environments(id,session_id) VALUES ($1,$2)", uuid.NewString(), session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,created_at,deadline)
		VALUES ($1,$2,'later','[{"kind":"message","payload":{"text":"historical"}}]',clock_timestamp(),clock_timestamp()+interval '5 minutes')`, later, session); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var result string
		if err := db.QueryRowContext(ctx, "SELECT (to_jsonb(r)-'is_initial')::text FROM environment_input_reservations r WHERE id=$1", later).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	for i := 0; i < 2; i++ {
		if _, err := provider.UpTo(ctx, 29); err != nil {
			t.Fatal(err)
		}
		var initial bool
		if err := db.QueryRowContext(ctx, "SELECT is_initial FROM environment_input_reservations WHERE id=$1", later).Scan(&initial); err != nil || initial || snapshot() != before {
			t.Fatal("migration inferred origin or changed historical data", initial, err)
		}
		if i == 0 {
			if _, err := provider.DownTo(ctx, 28); err != nil {
				t.Fatal("later-only downgrade failed", err)
			}
		}
	}
	initial := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO environment_input_reservations(id,session_id,idempotency_key,batch,is_initial,state,created_at,deadline,settled_at)
		VALUES ($1,$2,'initial','[{"kind":"message","payload":{"text":"initial"}}]',true,'expired',clock_timestamp()-interval '6 minutes',clock_timestamp()-interval '1 minute',clock_timestamp())`, initial, session); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 28); err == nil || !strings.Contains(err.Error(), "Cannot remove initial Environment input origin") {
		t.Fatal("downgrade discarded initial failure meaning", err)
	}
	var retained bool
	if err := db.QueryRowContext(ctx, "SELECT is_initial FROM environment_input_reservations WHERE id=$1", initial).Scan(&retained); err != nil || !retained || snapshot() != before {
		t.Fatal("rejected downgrade changed origin/history", retained, err)
	}
}
