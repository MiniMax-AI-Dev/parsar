package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/migrations"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("PARSAR_AGENTS_API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PARSAR_AGENTS_API_TEST_DATABASE_URL is not set; dedicated PostgreSQL required")
	}
	cfg, err := testDatabaseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// A separate database, not product fixtures or migrations, is sufficient.
	var database string
	var productTable *string
	if err := pool.QueryRow(ctx, "SELECT current_database(), to_regclass('workspaces')::text").Scan(&database, &productTable); err != nil || productTable != nil || database != cfg.ConnConfig.Database {
		t.Fatal("execution tests require a database without product workspace tables")
	}
	if err := migrations.Apply(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	return New(pool), pool
}

// Validate the driver's effective database, including query parameters and DSNs.
func testDatabaseConfig(dsn string) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid test database configuration")
	}
	database := cfg.ConnConfig.Database
	if !strings.HasPrefix(database, "parsar_agents_api_") || !strings.HasSuffix(database, "_tests") {
		return nil, errors.New("test database must be named parsar_agents_api_*_tests")
	}
	return cfg, nil
}

func TestDatabaseGuardUsesEffectiveDatabase(t *testing.T) {
	for _, dsn := range []string{
		"postgres://localhost/parsar_agents_api_local_tests?dbname=agents_api",
		"host=localhost dbname=agents_api",
		"postgres://localhost/agents_api",
	} {
		if _, err := testDatabaseConfig(dsn); err == nil {
			t.Fatalf("unsafe database accepted: %s", dsn)
		}
	}
	cfg, err := testDatabaseConfig("postgres://localhost/parsar_agents_api_local_tests")
	if err != nil || cfg.ConnConfig.Database != "parsar_agents_api_local_tests" {
		t.Fatalf("valid dedicated database rejected: %v", err)
	}
}

func TestSessionsPersistAndStayTenantScoped(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	input := CreateSessionInput{Engine: "codex", Metadata: map[string]string{"source": "standalone"}, IdempotencyKey: "first",
		Configuration: []byte(`{"agent":{"model":"test-model","instructions":"Keep the snapshot."},"environment":{"type":"none"}}`)}
	first, err := s.CreateSession(ctx, tenantA, input)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, tenantB, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == other.ID {
		t.Fatal("idempotency leaked across tenants")
	}
	if _, err := s.GetSession(ctx, tenantB, first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read: %v", err)
	}
	if _, err := s.ListSessions(ctx, tenantB, first.ID, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant cursor: %v", err)
	}
	for _, key := range []string{"second", "third"} {
		input.IdempotencyKey = key
		if _, err := s.CreateSession(ctx, tenantA, input); err != nil {
			t.Fatal(err)
		}
	}
	// Recreate the pool and Store as a new service process would.
	pool.Close()
	recovered, _ := testStore(t)
	got, err := recovered.GetSession(ctx, tenantA, first.ID)
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("restart read = %+v, %v; want %+v", got, err, first)
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err := recovered.ListSessions(ctx, tenantA, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, session := range page.Sessions {
			if session.TenantID != tenantA || seen[session.ID] {
				t.Fatalf("unexpected/duplicate session: %+v", session)
			}
			seen[session.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 || !seen[first.ID] || seen[other.ID] {
		t.Fatalf("pagination lost or leaked sessions: %+v", seen)
	}
	empty, err := recovered.ListSessions(ctx, uuid.NewString(), "", 10)
	if err != nil || empty.Sessions == nil || len(empty.Sessions) != 0 {
		t.Fatalf("empty tenant = %+v, %v", empty, err)
	}
}

func TestConcurrentSessionCreationIsIdempotent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Engine: "claude_code", Metadata: map[string]string{"b": "2", "a": "1"}, IdempotencyKey: "repeated"}
	const count = 8
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session, err := s.CreateSession(ctx, tenant, input)
			ids <- session.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != 1 {
		t.Fatalf("duplicate sessions: %+v", unique)
	}
	replay, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "claude_code", Metadata: map[string]string{"a": "1", "b": "2"}, IdempotencyKey: "repeated"})
	if err != nil || !unique[replay.ID] {
		t.Fatalf("reordered metadata was not replayed: %+v %v", replay, err)
	}
	for _, changed := range []CreateSessionInput{
		{Engine: "codex", Metadata: input.Metadata, IdempotencyKey: input.IdempotencyKey},
		{Engine: input.Engine, Metadata: map[string]string{"a": "changed"}, IdempotencyKey: input.IdempotencyKey},
	} {
		if _, err := s.CreateSession(ctx, tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed request = %v", err)
		}
	}
	page, err := s.ListSessions(ctx, tenant, "", 10)
	if err != nil || len(page.Sessions) != 1 || !reflect.DeepEqual(page.Sessions[0], replay) {
		t.Fatalf("retry changed stored session: %+v, %v", page, err)
	}
}
