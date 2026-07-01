package main

import (
	"context"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/seed"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Bg().Error("dev seed failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	pool, err := db.OpenPool(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	st := store.New(pool)
	result, err := st.SeedDevFixture(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("seeded dev database: users=%d workspaces=%d workspace_members=%d agents=%d conversations=%d\n",
		result.Users,
		result.Workspaces,
		result.WorkspaceMembers,
		result.Agents,
		result.Conversations,
	)

	// Seed model registry so the admin UI lands on data instead of an empty page.
	ids := store.DefaultDevFixtureIDs()
	mr, err := seed.SeedModelRegistry(ctx, st, ids.WorkspaceID, ids.UserID)
	if err != nil {
		return fmt.Errorf("seed model registry: %w", err)
	}
	fmt.Printf("seeded model registry: providers=%d models=%d (workspace_id=%s)\n",
		mr.CreatedProviders, mr.CreatedModels, ids.WorkspaceID)
	return nil
}
