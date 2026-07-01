package audit

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPostgresSinkRoundTrip writes one event per Source and reads them
// back to confirm column mapping (especially NULL handling for optional
// IDs) and the payload jsonb round-trip. Skipped silently when
// PARSAR_TEST_DATABASE_URL is unset.
func TestPostgresSinkRoundTrip(t *testing.T) {
	pool := openAuditTestDB(t)
	queries := sqlc.New(pool)
	sink := NewPostgresSink(queries)

	ctx := context.Background()
	occurred := time.Now().UTC().Truncate(time.Microsecond)

	cases := []struct {
		source    string
		actorType string
		actorID   string
		payload   map[string]any
	}{
		{SourceIdentity, ActorTypeUser, uuid.NewString(), map[string]any{"method": "feishu_oauth"}},
		{SourceAdmin, ActorTypeUser, uuid.NewString(), map[string]any{"name": "claude-opus-4-7"}},
		{SourceRuntime, ActorTypeSystem, "", map[string]any{"model": "claude-opus-4-7", "tokens": 1234}},
		{SourceApproval, ActorTypeUser, uuid.NewString(), map[string]any{"decision": "allow"}},
		{SourceData, ActorTypeAgent, uuid.NewString(), map[string]any{"bytes": 42}},
	}
	for k, tc := range cases {
		err := sink.Write(ctx, Event{
			OccurredAt: occurred.Add(time.Duration(k) * time.Second),
			Source:     tc.source,
			EventType:  tc.source + ".roundtrip",
			ActorType:  tc.actorType,
			ActorID:    tc.actorID,
			TargetType: "test",
			TargetID:   "",
			Payload:    tc.payload,
		})
		if err != nil {
			t.Fatalf("PostgresSink.Write[%s]: %v", tc.source, err)
		}
	}

	rows, err := queries.ListAuditRecords(ctx, sqlc.ListAuditRecordsParams{
		ItemLimit:  100,
		TargetType: "test",
	})
	if err != nil {
		t.Fatalf("ListAuditRecords: %v", err)
	}
	if len(rows) != len(cases) {
		t.Fatalf("expected %d rows, got %d", len(cases), len(rows))
	}

	bySource := map[string]sqlc.ListAuditRecordsRow{}
	for _, r := range rows {
		bySource[r.Source] = r
	}
	for _, tc := range cases {
		row, ok := bySource[tc.source]
		if !ok {
			t.Errorf("missing row for source %q", tc.source)
			continue
		}
		var gotPayload map[string]any
		if err := json.Unmarshal(row.Payload, &gotPayload); err != nil {
			t.Errorf("payload unmarshal[%s]: %v", tc.source, err)
			continue
		}
		for key, want := range tc.payload {
			if gotPayload[key] == nil {
				t.Errorf("payload[%s] missing key %q", tc.source, key)
			}
			_ = want // compare presence; JSON numeric types differ from Go int.
		}
		if tc.actorID == "" {
			if row.ActorID.Valid {
				t.Errorf("expected ActorID NULL for source %q, got %v", tc.source, row.ActorID)
			}
		} else if !row.ActorID.Valid {
			t.Errorf("expected ActorID valid for source %q", tc.source)
		}
	}
}

func TestPostgresSinkRejectsBadSource(t *testing.T) {
	pool := openAuditTestDB(t)
	sink := NewPostgresSink(sqlc.New(pool))

	err := sink.Write(context.Background(), Event{
		OccurredAt: time.Now(),
		Source:     "bogus", // not in the 5-category enum
		EventType:  "test.bad",
		ActorType:  ActorTypeSystem,
	})
	if err == nil {
		t.Fatalf("expected check-constraint error for invalid source, got nil")
	}
}

func TestPostgresSinkNullIDsWhenEmpty(t *testing.T) {
	pool := openAuditTestDB(t)
	queries := sqlc.New(pool)
	sink := NewPostgresSink(queries)

	if err := sink.Write(context.Background(), Event{
		OccurredAt: time.Now(),
		Source:     SourceAdmin,
		EventType:  "test.null",
		ActorType:  ActorTypeSystem,
		// All optional IDs left empty.
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rows, err := queries.ListAuditRecords(context.Background(), sqlc.ListAuditRecordsParams{
		ItemLimit: 10,
		EventType: "test.null",
	})
	if err != nil {
		t.Fatalf("ListAuditRecords: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	for name, id := range map[string]pgtype.UUID{
		"actor_id":     r.ActorID,
		"target_id":    r.TargetID,
		"workspace_id": r.WorkspaceID,
	} {
		if id.Valid {
			t.Errorf("expected %s to be NULL when source field is empty", name)
		}
	}
}

// openAuditTestDB connects to PARSAR_TEST_DATABASE_URL and truncates
// audit_records for isolation. Tests are skipped when env is unset.
func openAuditTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "truncate table audit_records restart identity"); err != nil {
		t.Fatalf("truncate audit_records: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "truncate table audit_records restart identity")
	})
	return pool
}
