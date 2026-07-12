package store

import (
	"context"
	"testing"
)

func mustSeedDevFixture(t *testing.T, ctx context.Context, store *Store) DevFixtureIDs {
	t.Helper()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatalf("SeedDevFixture: %v", err)
	}
	return DefaultDevFixtureIDs()
}
