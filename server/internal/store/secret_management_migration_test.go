package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretManagementMigrationPreservesLegacyOwnership(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	// Recreate the pre-upgrade shape inside a transaction; rollback preserves
	// the test database schema for the remaining integration tests.
	if _, err := tx.Exec(ctx, `
		ALTER TABLE secrets DROP COLUMN management_workspace_id;
		INSERT INTO workspaces(id, name, slug, config, created_at, updated_at) VALUES
		('aabbccdd-eeff-4abb-8cdd-aabbccddeeff', 'Metadata owner', 'metadata-owner', '{}', now(), now()),
		('10000000-0000-4000-8000-000000000001', 'Runtime owner', 'runtime-owner', '{"runtime_credential_secret_id":"20000000-0000-4000-8000-000000000002"}', now(), now()),
		('10000000-0000-4000-8000-000000000002', 'Conflict A', 'conflict-a', '{"runtime_credential_secret_id":"20000000-0000-4000-8000-000000000003"}', now(), now()),
		('10000000-0000-4000-8000-000000000003', 'Conflict B', 'conflict-b', '{"runtime_credential_secret_id":"20000000-0000-4000-8000-000000000003"}', now(), now()),
		('10000000-0000-4000-8000-000000000004', 'Metadata conflict', 'metadata-conflict', '{"runtime_credential_secret_id":"20000000-0000-4000-8000-000000000004"}', now(), now());
		INSERT INTO secrets(id, slug, name, kind, provider, auth_type, encrypted_payload, key_version, metadata, created_at, updated_at) VALUES
		('20000000-0000-4000-8000-000000000001', 'metadata-secret', 'Metadata secret', 'model', 'test', 'api_key', '"legacy-payload"', 'v1', '{"workspace_id":"AABBCCDD-EEFF-4ABB-8CDD-AABBCCDDEEFF"}', now(), now()),
		('20000000-0000-4000-8000-000000000002', 'runtime-secret', 'Runtime secret', 'runtime', 'test', 'api_key', '"legacy-payload"', 'v1', '{"masked":"test-mask"}', now(), now()),
		('20000000-0000-4000-8000-000000000003', 'ambiguous-secret', 'Ambiguous secret', 'runtime', 'test', 'api_key', '"legacy-payload"', 'v1', '{}', now(), now()),
		('20000000-0000-4000-8000-000000000004', 'conflicting-secret', 'Conflicting secret', 'runtime', 'test', 'api_key', '"legacy-payload"', 'v1', '{"workspace_id":"aabbccdd-eeff-4abb-8cdd-aabbccddeeff"}', now(), now()),
		('20000000-0000-4000-8000-000000000005', 'unowned-secret', 'Unowned secret', 'model', 'test', 'api_key', '"legacy-payload"', 'v1', '{}', now(), now());
	`); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("PARSAR_MIGRATIONS_DIR")
	if dir == "" {
		dir = "../../migrations"
	}
	migration, err := os.ReadFile(filepath.Join(dir, "000014_secret_management_workspace.sql"))
	if err != nil {
		t.Fatal(err)
	}
	up := strings.SplitN(string(migration), "-- +goose Down", 2)[0]
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	for slug, want := range map[string]string{
		"metadata-secret":  "aabbccdd-eeff-4abb-8cdd-aabbccddeeff",
		"runtime-secret":   "10000000-0000-4000-8000-000000000001",
		"ambiguous-secret": "", "conflicting-secret": "", "unowned-secret": "",
	} {
		var owner, status string
		var payload []byte
		if err := tx.QueryRow(ctx, `SELECT coalesce(management_workspace_id::text, ''), status, encrypted_payload FROM secrets WHERE slug = $1`, slug).Scan(&owner, &status, &payload); err != nil {
			t.Fatal(err)
		}
		if owner != want || status != "active" || string(payload) != `"legacy-payload"` {
			t.Fatalf("%s: owner=%q status=%q payload changed=%v", slug, owner, status, string(payload) != `"legacy-payload"`)
		}
	}
}
