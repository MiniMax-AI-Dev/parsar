package seed

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestSeedModelRegistryIdempotent verifies SeedModelRegistry can be
// called repeatedly without growing the models table.
func TestSeedModelRegistryIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := store.New(db)

	ids := store.DefaultDevFixtureIDs()

	// SeedDevFixture creates the user that models.created_by references via FK.
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}

	first, err := SeedModelRegistry(ctx, st, ids.WorkspaceID, ids.UserID)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if first.CreatedModels != ExpectedModels {
		t.Fatalf("first run models: got %d, want %d", first.CreatedModels, ExpectedModels)
	}

	second, err := SeedModelRegistry(ctx, st, ids.WorkspaceID, ids.UserID)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.CreatedModels != 0 {
		t.Fatalf("expected idempotent second run, got models=%d", second.CreatedModels)
	}

	models, err := st.ListModels(ctx, ids.WorkspaceID, 200)
	if err != nil {
		t.Fatalf("list models: %v", err)
	}
	if len(models) != ExpectedModels {
		t.Fatalf("total models after 2 runs: got %d, want %d",
			len(models), ExpectedModels)
	}
}

// openTestDB mirrors the helper in server/internal/store/store_test.go;
// duplicated here so store doesn't depend on testing infra in prod builds.
func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)

	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	resetTestDB(t, db)
	return db
}

// resetTestDB truncates all mutable tables so each test starts clean.
// Order respects FK dependencies (children before parents).
func resetTestDB(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `
		truncate table
			usage_logs,
			audit_records,
						agent_run_artifacts,
			agent_runs,
			messages,
			conversations,
			project_agents,
			agents,
			models,
			secrets,
			projects,
			workspace_members,
			workspaces,
			auth_identities,
			users
		restart identity cascade
	`); err != nil {
		t.Fatalf("reset test db: %v", err)
	}
}
