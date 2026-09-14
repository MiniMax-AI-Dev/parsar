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

func TestEnvironmentConnectionMigrationPreservesStateAndGuardsFencing(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "connection_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	session, environment, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration) VALUES ($1,$2,'codex','historical','historical','{"environment":{"type":"self_hosted"}}')`, session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO environments(id,session_id) VALUES ($1,$2)", environment, session); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT to_jsonb(e)::text FROM environments e WHERE id=$1", environment).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	if _, err := provider.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("migration invented connection state")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM environment_connections").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err := provider.DownTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 27); err != nil {
		t.Fatal(err)
	}
	generation := uuid.NewString()
	if _, err := db.ExecContext(ctx, "INSERT INTO environment_connections(environment_id,generation) VALUES ($1,$2)", environment, generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE environment_connections SET revision=-1 WHERE environment_id=$1", environment); err == nil {
		t.Fatal("negative observation revision accepted")
	}
	if _, err := provider.DownTo(ctx, 26); err == nil || !strings.Contains(err.Error(), "Cannot remove active Environment observation fencing") {
		t.Fatal("downgrade lost generation", err)
	}
	var retained string
	if err := db.QueryRowContext(ctx, "SELECT generation::text FROM environment_connections WHERE environment_id=$1", environment).Scan(&retained); err != nil || retained != generation {
		t.Fatal(retained, err)
	}
	if snapshot() != before {
		t.Fatal("migration changed Environment ownership")
	}
}
