package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestSessionCreationIdentityConvergesOnFrozenSnapshot(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	request := json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740993}}]}}`)
	const count = 8
	results := make(chan SessionCreation, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same", CreationRequest: request, Configuration: json.RawMessage(fmt.Sprintf(`{"resolved":%d}`, i)), InitialInputs: []Input{messageInput("one")}}
			result, err := s.CreateSessionStream(ctx, tenant, input)
			results <- result
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first SessionCreation
	created := 0
	for result := range results {
		if first.Session.ID == "" {
			first = result
		}
		if result.Session.ID != first.Session.ID || string(result.Session.Configuration) != string(first.Session.Configuration) {
			t.Fatal("concurrent resolution changed winner", first, result)
		}
		if result.Created {
			created++
			if result.Cursor != 0 {
				t.Fatal("creation cursor passed initial input")
			}
		}
	}
	var turns, inputs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM turns WHERE session_id=$1`, first.Session.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM turn_inputs WHERE session_id=$1`, first.Session.ID).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if created != 1 || turns != 1 || inputs != 1 {
		t.Fatal(created, turns, inputs)
	}
	restarted := New(pool)
	retry, err := restarted.FindSessionCreation(ctx, tenant, "same", json.RawMessage(`{"agent":{"tools":[{"parameters":{"const":9007199254740993}}]},"agent_id":"source"}`), FixtureCreator())
	if err != nil || retry.Created || retry.Session.ID != first.Session.ID || retry.Cursor == 0 {
		t.Fatal(retry, err)
	}
	if _, err := restarted.FindSessionCreation(ctx, tenant, "same", json.RawMessage(`{"agent_id":"source","agent":{"tools":[{"parameters":{"const":9007199254740992}}]}}`), FixtureCreator()); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("numeric identity collapsed", err)
	}
	if _, err := restarted.FindSessionCreation(ctx, uuid.NewString(), "same", request, FixtureCreator()); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign lookup", err)
	}
	if _, err := restarted.CreateSession(ctx, tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "same", CreationRequest: json.RawMessage(`{"agent_id":"changed"}`), Configuration: first.Session.Configuration}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed caller admitted", err)
	}
}

func TestSessionCreationIdentityDoesNotInventHistoricalIntent(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "historical", Configuration: json.RawMessage(`{"agent":{"id":"source","instructions":"original"}}`)}
	first, err := s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	input.CreationRequest = json.RawMessage(`{"agent_id":"source"}`)
	if _, err := s.FindSessionCreation(ctx, tenant, input.IdempotencyKey, input.CreationRequest, FixtureCreator()); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	retry, err := s.CreateSession(ctx, tenant, input)
	if err != nil || !reflect.DeepEqual(first, retry) {
		t.Fatal(retry, err)
	}
	var hash *string
	if err := pool.QueryRow(ctx, `SELECT creation_request_hash FROM sessions WHERE id=$1`, first.ID).Scan(&hash); err != nil || hash != nil {
		t.Fatal("historical intent was manufactured", hash, err)
	}
	input.Configuration = json.RawMessage(`{"agent":{"id":"source","instructions":"changed"}}`)
	if _, err := s.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("historical retry rules changed", err)
	}
}
