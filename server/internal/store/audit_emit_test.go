package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type recordingSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *recordingSink) Write(ctx context.Context, ev audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *recordingSink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestNewWithoutAuditIsNilSafe(t *testing.T) {
	st := New(nil)
	st.emitAuditEvent(audit.Event{Source: audit.SourceAdmin, EventType: "test.nil_safe", ActorType: audit.ActorTypeSystem})
}

func TestWithAuditRoutesEventsToIngester(t *testing.T) {
	sink := &recordingSink{}
	ing := audit.NewIngester(sink, audit.Options{BufferCapacity: 4})
	ing.Start(context.Background())
	t.Cleanup(func() { _ = ing.Stop(context.Background()) })

	st := New(nil, WithAudit(ing))

	st.emitAuditEvent(audit.Event{
		Source:    audit.SourceAdmin,
		EventType: "test.routed",
		ActorType: audit.ActorTypeUser,
		ActorID:   "00000000-0000-0000-0000-000000000001",
	})

	// Stop drains the sink; cleanup will Stop() again, which is idempotent.
	if err := ing.Stop(context.Background()); err != nil {
		t.Fatalf("ing.Stop: %v", err)
	}

	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 routed event, got %d", len(got))
	}
	if got[0].EventType != "test.routed" {
		t.Errorf("unexpected event_type: %q", got[0].EventType)
	}
}

func TestWithAuditNilIngesterIsNilSafe(t *testing.T) {
	st := New(nil, WithAudit(nil))
	st.emitAuditEvent(audit.Event{Source: audit.SourceAdmin, EventType: "test.opt_nil", ActorType: audit.ActorTypeSystem})
}

// TestEmitAuditEventSwallowsErrors: business code must not fail because audit failed.
func TestEmitAuditEventSwallowsErrors(t *testing.T) {
	sink := &recordingSink{}
	ing := audit.NewIngester(sink, audit.Options{BufferCapacity: 1})
	ing.Start(context.Background())
	if err := ing.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	st := New(nil, WithAudit(ing))
	st.emitAuditEvent(audit.Event{Source: audit.SourceAdmin, EventType: "test.closed", ActorType: audit.ActorTypeSystem})

	if err := ing.Emit(audit.Event{Source: audit.SourceAdmin, EventType: "test.direct_after_close", ActorType: audit.ActorTypeSystem}); !errors.Is(err, audit.ErrClosed) {
		t.Errorf("ingester should report ErrClosed after Stop; got %v", err)
	}
}

func TestAddWorkspaceMemberEmitsAdminAuditRecord(t *testing.T) {
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `truncate table audit_records restart identity`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `truncate table audit_records restart identity`)
	})

	queries := sqlc.New(pool)
	sink := audit.NewPostgresSink(queries)
	ing := audit.NewIngester(sink, audit.Options{BufferCapacity: 32})
	ing.Start(ctx)

	st := New(pool, WithAudit(ing))
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatalf("SeedDevFixture: %v", err)
	}

	added, err := st.AddWorkspaceMember(ctx, AddWorkspaceMemberInput{
		WorkspaceID: ids.WorkspaceID,
		Email:       "audit-emit@example.com",
		Name:        "Audit Emit",
		Role:        "member",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("AddWorkspaceMember: %v", err)
	}

	// Stop drains the ingester so the insert has landed before we query.
	if err := ing.Stop(ctx); err != nil {
		t.Fatalf("ing.Stop: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`select count(*) from audit_records where source = 'admin' and event_type = $1 and target_id::text = $2`,
		"workspace_member.added", added.Member.ID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit_records row for workspace_member.added/%s, got %d", added.Member.ID, count)
	}
}

func TestSecretAndModelLifecycleEmitsAuditRecords(t *testing.T) {
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `truncate table audit_records restart identity`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `truncate table audit_records restart identity`)
	})

	queries := sqlc.New(pool)
	sink := audit.NewPostgresSink(queries)
	ing := audit.NewIngester(sink, audit.Options{BufferCapacity: 32})
	ing.Start(ctx)

	st := New(pool, WithAudit(ing))
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatalf("SeedDevFixture: %v", err)
	}

	// encrypted_payload is jsonb; audit producer doesn't inspect contents
	// so a minimal valid JSON object suffices.
	secret, err := st.CreateSecret(ctx, CreateSecretInput{
		WorkspaceID: ids.WorkspaceID,
		Name:        "audit-emit-secret",
		Kind:        "api_key",
		Provider:    "anthropic",
		AuthType:    "api_key",
		Masked:      "****",
		CreatedBy:   ids.UserID,
	}, []byte(`{"stub":true}`))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	model, err := st.CreateModel(ctx, CreateModelInput{
		Name:           "audit-emit-model",
		ProviderType:   "openai",
		Adapter:        "@ai-sdk/openai",
		BaseURL:        "https://example.invalid",
		ModelKey:       "audit-emit-model-v1",
		CredentialMode: "inline_secret",
		SecretID:       secret.ID,
		CreatedBy:      ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	if _, err := st.DisableModel(ctx, ids.WorkspaceID, model.ID); err != nil {
		t.Fatalf("DisableModel: %v", err)
	}
	if _, err := st.DisableSecret(ctx, ids.WorkspaceID, secret.ID); err != nil {
		t.Fatalf("DisableSecret: %v", err)
	}

	if err := ing.Stop(ctx); err != nil {
		t.Fatalf("ing.Stop: %v", err)
	}

	cases := []struct {
		eventType  string
		targetType string
		targetID   string
	}{
		{"secret.created", "secret", secret.ID},
		{"model.created", "model", model.ID},
		{"model.disabled", "model", model.ID},
		{"secret.disabled", "secret", secret.ID},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.eventType, func(t *testing.T) {
			var count int
			if err := pool.QueryRow(ctx,
				`select count(*) from audit_records
				 where source = 'admin'
				   and event_type = $1
				   and target_type = $2
				   and target_id::text = $3`,
				tc.eventType, tc.targetType, tc.targetID,
			).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("expected 1 audit_records row for %s/%s/%s, got %d",
					tc.eventType, tc.targetType, tc.targetID, count)
			}
		})
	}
}
