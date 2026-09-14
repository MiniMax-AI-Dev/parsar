package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestExecutorPrincipalMigrationRetiresUnknownAuthority(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "executor_principal_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 25); err != nil {
		t.Fatal(err)
	}
	tenant, session, environment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	input := environmentInput("legacy", "self_hosted", "/workspace")
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash,configuration)
		VALUES ($1,$2,'codex','legacy','historical',$3)`, session, tenant, input.Configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO environments(id,session_id) VALUES ($1,$2)", environment, session); err != nil {
		t.Fatal(err)
	}
	_, digest, err := newExecutorSecret()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO environment_executor_credentials(environment_id,token_sha256,issued_at)
		VALUES ($1,$2,now()-interval '2 days')`, environment, digest); err != nil {
		t.Fatal(err)
	}
	var oldSession, oldCredential, after string
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", session).Scan(&oldSession); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT (to_jsonb(c)-'revoked_at')::text FROM environment_executor_credentials c WHERE environment_id=$1", environment).Scan(&oldCredential); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 26); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", session).Scan(&after); err != nil || oldSession != after {
		t.Fatal("migration changed Session", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT (to_jsonb(c)-ARRAY['key_id','tenant_id','subject_kind','subject_id','created_at','revoked_at'])::text
		FROM environment_executor_credentials c WHERE key_id=$1`, environment).Scan(&after); err != nil || oldCredential != after {
		t.Fatal("migration changed legacy digest/restriction/issuance", err)
	}
	var retired bool
	if err := db.QueryRowContext(ctx, `SELECT key_id=environment_id AND tenant_id IS NULL AND subject_kind IS NULL
		AND subject_id IS NULL AND created_at IS NULL AND revoked_at IS NOT NULL FROM environment_executor_credentials WHERE key_id=$1`, environment).Scan(&retired); err != nil || !retired {
		t.Fatal("migration inferred authority", err)
	}
	var mappings int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM execution_project_scopes").Scan(&mappings); err != nil || mappings != 0 {
		t.Fatal("migration invented project", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE environment_executor_credentials SET revoked_at=NULL WHERE key_id=$1", environment); err == nil {
		t.Fatal("unknown principal regained authority")
	}
	if _, err := provider.DownTo(ctx, 25); err != nil {
		t.Fatal("legacy-only downgrade", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT revoked_at IS NOT NULL FROM environment_executor_credentials WHERE environment_id=$1", environment).Scan(&retired); err != nil || !retired {
		t.Fatal("downgrade undid revocation", err)
	}
	if _, err := provider.UpTo(ctx, 26); err != nil {
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
	p := FixtureExecutorPrincipal(t, s, tenant)
	if _, err := s.AuthenticateEnvironmentExecutor(ctx, environment, digest); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy credential accepted", err)
	}
	if _, err := s.IssueExecutorCredential(ctx, p, environment, ""); !errors.Is(err, ErrExecutorCredentialExists) {
		t.Fatal("legacy key ID claimed", err)
	}
	if _, err := s.RotateExecutorCredential(ctx, p, environment); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy principal claimed", err)
	}
	if err := s.RevokeExecutorCredential(ctx, p, environment); !errors.Is(err, ErrNotFound) {
		t.Fatal("legacy principal manufactured", err)
	}
	key, err := s.IssueExecutorCredential(ctx, p, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 25); err == nil || !strings.Contains(err.Error(), "Cannot remove durable executor principal identities") {
		t.Fatal("downgrade lost principal keys", err)
	}
	if _, err := s.RotateExecutorCredential(ctx, p, key.KeyID); err != nil {
		t.Fatal("failed downgrade damaged identity", err)
	}
}
