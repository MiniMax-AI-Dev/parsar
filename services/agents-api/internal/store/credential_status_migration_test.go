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

func TestCredentialStatusMigrationPreservesMetadataAndCiphertext(t *testing.T) {
	_, pool := testStore(t)
	ctx := t.Context()
	schema := "credential_status_" + uuid.NewString()[:8]
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
	if _, err := provider.UpTo(ctx, 32); err != nil {
		t.Fatal(err)
	}
	id, vault, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, "INSERT INTO vaults(id,tenant_id,metadata) VALUES ($1,$2,'{}')", vault, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO vault_credentials(id,vault_id,name,auth_type,mcp_server_url,token_ciphertext) VALUES ($1,$2,'Existing Credential','static_bearer','https://example.invalid/mcp',$3)", id, vault, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := db.QueryRowContext(ctx, "SELECT (to_jsonb(v)-'status')::text FROM vault_credentials v WHERE id=$1", id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	if _, err := provider.UpTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRowContext(ctx, "SELECT status FROM vault_credentials WHERE id=$1", id).Scan(&status); err != nil || status != "active" || snapshot() != before {
		t.Fatal("migration changed historical resource", status, err)
	}
	if err := db.QueryRowContext(ctx, "INSERT INTO vault_credentials(id,vault_id,name,auth_type,mcp_server_url,token_ciphertext) VALUES ($1,$2,'New Credential','static_bearer','https://example.invalid/mcp',$3) RETURNING status", uuid.NewString(), vault, []byte{4, 5, 6}).Scan(&status); err != nil || status != "active" {
		t.Fatal("new Credential default", status, err)
	}
	for _, invalid := range []any{nil, "deleted"} {
		if _, err := db.ExecContext(ctx, "UPDATE vault_credentials SET status=$1 WHERE id=$2", invalid, id); err == nil {
			t.Fatal("invalid classification accepted")
		}
	}
	if _, err := provider.DownTo(ctx, 32); err != nil || snapshot() != before {
		t.Fatal("reversible active-only migration", err)
	}
	if _, err := provider.UpTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE vault_credentials SET status='archived' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 32); err == nil || !strings.Contains(err.Error(), "Cannot remove archived Credential classification") {
		t.Fatal("downgrade lost archived classification", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT status FROM vault_credentials WHERE id=$1", id).Scan(&status); err != nil || status != "archived" || snapshot() != before {
		t.Fatal("failed downgrade changed data", status, err)
	}
}
