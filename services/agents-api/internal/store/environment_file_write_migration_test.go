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
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestEnvironmentFileWriteMigrationRetainsUnresolvedIdentity(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "file_write_" + uuid.NewString()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
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
	if _, err := provider.UpTo(ctx, 35); err != nil {
		t.Fatal(err)
	}
	session, tenant, environment, device, write := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
		VALUES ($1,$2,'codex','old','old','{}')`, session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environments(id,session_id) VALUES ($1,$2)`, environment, session); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO devices(id,tenant_id,name,credential_hash) VALUES ($1,$2,'old',$3)`, device, tenant, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(e)::text FROM environments e WHERE id=$1`, environment).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := provider.UpTo(ctx, 36); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM environment_file_writes").Scan(&count); err != nil || count != 0 {
			t.Fatal("migration manufactured write authority", count, err)
		}
		if _, err := provider.DownTo(ctx, 35); err != nil {
			t.Fatal("empty downgrade", err)
		}
	}
	if _, err := provider.UpTo(ctx, 36); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environment_file_writes(id,environment_id,device_id,request_sha256)
		VALUES ($1,$2,$3,$4)`, write, environment, device, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	var original, retained string
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(w)::text FROM environment_file_writes w WHERE id=$1`, write).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 35); err == nil || !strings.Contains(err.Error(), "Cannot remove durable Environment file write identities") {
		t.Fatal("downgrade removed unresolved identity", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(w)::text FROM environment_file_writes w WHERE id=$1`, write).Scan(&retained); err != nil || retained != original {
		t.Fatal("failed downgrade changed write", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT to_jsonb(e)::text FROM environments e WHERE id=$1`, environment).Scan(&after); err != nil || after != before {
		t.Fatal("migration changed Environment", err)
	}
}
