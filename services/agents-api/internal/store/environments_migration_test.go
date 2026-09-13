package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

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
	session, err := s.CreateSession(ctx, tenant, environmentInput("new", "self_hosted", "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 19); err == nil || !strings.Contains(err.Error(), "Cannot remove durable Environment identities") {
		t.Fatal("unsafe downgrade", err)
	}
	retained, err := s.GetEnvironment(ctx, tenant, environment.ID)
	if err != nil || retained.ID != environment.ID || retained.SessionID != session.ID {
		t.Fatal("downgrade destroyed ownership", retained, err)
	}
}
