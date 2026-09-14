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
	got, err := canonicalJSONObject([]byte(input))
	if err != nil || string(got) != want {
		t.Fatalf("canonical = %s, %v; want %s", got, err, want)
	}
	for _, raw := range []string{`null`, `[]`, `"text"`, `{} {}`, `{`} {
		if _, err := canonicalJSONObject([]byte(raw)); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid JSON object accepted (length %d): %v", len(raw), err)
		}
	}
}

func TestConfigurationSizeLimitSurvivesJSONBRoundTrip(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	empty := `{"agent":{"model":"example","instructions":""},"environment":{"type":"none"}}`
	raw := strings.Replace(empty, `"instructions":""`, `"instructions":"`+strings.Repeat("x", 512*1024-len(empty))+`"`, 1)
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "size-limit", Configuration: []byte(raw)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(ctx, tenant, first.ID)
	if err != nil || string(got.Configuration) != string(first.Configuration) {
		t.Fatalf("configuration failed round trip: %v", err)
	}
	page, err := s.ListSessions(ctx, tenant, "", 10, false, nil)
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
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "configured",
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

func TestOriginalSessionHashRespectsRecordedCreator(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	legacyHash := sha256.Sum256([]byte(`{"Engine":"codex","Metadata":{}}`))
	for _, known := range []bool{false, true} {
		tenant, id := uuid.NewString(), uuid.NewString()
		var kind, creatorID any
		if known {
			kind, creatorID = FixtureCreator().Kind, FixtureCreator().ID
		}
		// Seed each ownership state explicitly; neither the migration nor a retry assigns it.
		_, err := pool.Exec(ctx, `INSERT INTO sessions (id, tenant_id, engine, metadata, idempotency_key, request_hash, creator_kind, creator_id)
   VALUES ($1, $2, 'codex', '{}', 'legacy', $3, $4, $5)`, id, tenant, hex.EncodeToString(legacyHash[:]), kind, creatorID)
		if err != nil {
			t.Fatal(err)
		}
		for _, configuration := range [][]byte{nil, []byte(`{}`), []byte(` { } `)} {
			got, err := s.CreateSession(ctx, tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "legacy", Configuration: configuration})
			if !known {
				if !errors.Is(err, ErrIdempotencyConflict) {
					t.Fatal("unknown historical creator was claimed", err)
				}
			} else if err != nil || got.ID != id || string(got.Configuration) != "{}" || got.Creator == nil || *got.Creator != FixtureCreator() {
				t.Fatalf("original hash retry = %+v, %v", got, err)
			}
		}
		read, err := s.GetSession(ctx, tenant, id)
		if err != nil || (read.Creator != nil) != known {
			t.Fatal("retry changed recorded creator", read, err)
		}
	}
}
