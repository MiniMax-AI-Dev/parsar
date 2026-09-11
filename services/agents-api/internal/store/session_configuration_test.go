package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestConfigurationCanonicalization(t *testing.T) {
	input := ` {"environment":{"type":"none"},"agent":{"revision":9007199254740993,"model":"example"}} `
	want := `{"agent":{"model":"example","revision":9007199254740993},"environment":{"type":"none"}}`
	got, err := canonicalConfiguration([]byte(input))
	if err != nil || string(got) != want {
		t.Fatalf("canonical = %s, %v; want %s", got, err, want)
	}
	for _, raw := range []string{`null`, `[]`, `"text"`, `{} {}`, `{`} {
		if _, err := canonicalConfiguration([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid configuration accepted (length %d): %v", len(raw), err)
		}
	}
}

func TestConfigurationSizeLimitSurvivesJSONBRoundTrip(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	empty := `{"agent":{"model":"example","instructions":""},"environment":{"type":"none"}}`
	raw := strings.Replace(empty, `"instructions":""`, `"instructions":"`+strings.Repeat("x", 512*1024-len(empty))+`"`, 1)
	input := CreateSessionInput{Engine: "codex", IdempotencyKey: "size-limit", Configuration: []byte(raw)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || string(got.Configuration) != string(first.Configuration) {
		t.Fatalf("configuration failed round trip: %v", err)
	}
	page, err := s.ListSessions(ctx, tenant, "", 10)
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].ID != first.ID {
		t.Fatalf("configuration broke listing: %v", err)
	}
	input.Configuration = append(input.Configuration, ' ')
	if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized request accepted: %v", err)
	}
}

func TestConfigurationIsPartOfSessionIdentity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Engine: "codex", IdempotencyKey: "configured",
		Configuration: []byte(`{"agent":{"model":"example","instructions":"First"},"environment":{"type":"none"}}`)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Configuration = []byte(`{"environment": {"type":"none"}, "agent":{"instructions":"First", "model":"example"}}`)
	replay, err := s.CreateSession(ctx, tenant, input)
	if err != nil || replay.ID != first.ID || string(replay.Configuration) != string(first.Configuration) {
		t.Fatalf("equivalent configuration changed identity: %+v, %v", replay, err)
	}
	for _, configuration := range []string{
		`{"agent":{"model":"different","instructions":"First"},"environment":{"type":"none"}}`,
		`{"agent":{"model":"example","instructions":"Changed"},"environment":{"type":"none"}}`,
		`{"agent":{"model":"example","instructions":"First"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`,
	} {
		input.Configuration = []byte(configuration)
		if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed snapshot was accepted: %v", err)
		}
	}
	stored, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || string(stored.Configuration) != string(first.Configuration) {
		t.Fatalf("retry mutated snapshot: %+v, %v", stored, err)
	}
}

func TestLegacySessionIdempotencySurvivesConfigurationMigration(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant, id := uuid.NewString(), uuid.NewString()
	legacyHash := sha256.Sum256([]byte(`{"Engine":"codex","Metadata":{}}`))
	_, err := pool.Exec(ctx, `INSERT INTO sessions (id, tenant_id, engine, metadata, idempotency_key, request_hash)
		VALUES ($1, $2, 'codex', '{}', 'legacy', $3)`, id, tenant, hex.EncodeToString(legacyHash[:]))
	if err != nil {
		t.Fatal(err)
	}
	for _, configuration := range [][]byte{nil, []byte(`{}`), []byte(` { } `)} {
		got, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "legacy", Configuration: configuration})
		if err != nil || got.ID != id || string(got.Configuration) != "{}" {
			t.Fatalf("legacy retry = %+v, %v", got, err)
		}
	}
}
