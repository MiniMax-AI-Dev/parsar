package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
)

func TestSessionCreatorIsRequiredBeforeCreation(t *testing.T) {
	s, _ := testStore(t)
	tenant := uuid.NewString()
	input := environmentInput("creator-required", "self_hosted", "/workspace")
	input.InitialInputs = []Input{messageInput("initial")}
	for _, invalid := range []identity.Subject{{}, {Kind: "user"}, {ID: "someone"}, {Kind: "workspace", ID: "someone"}} {
		input.Creator = invalid
		if _, err := s.CreateSessionStream(t.Context(), tenant, input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid creator accepted: %v", err)
		}
	}
	page, err := s.ListSessions(t.Context(), tenant, "", 10, false, nil)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatal("invalid creation wrote resources", page, err)
	}
	input.Creator = FixtureCreator()
	created, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || created.Creator == nil || *created.Creator != input.Creator {
		t.Fatal("valid creator did not persist", created, err)
	}
}

func TestConcurrentSessionCreatorsCannotShareCreationRetry(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	request := json.RawMessage(`{"agent_id":"source"}`)
	creators := []identity.Subject{{Kind: "user", ID: "same-id"}, {Kind: "service_account", ID: "same-id"}}
	type outcome struct {
		result SessionCreation
		err    error
	}
	results := make(chan outcome, 8)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(8)
	for i := range 8 {
		go func() {
			input := environmentInput("shared-retry", "self_hosted", "/workspace")
			input.Creator = creators[i%2]
			input.CreationRequest, input.InitialInputs = request, []Input{messageInput("once")}
			ready.Done()
			<-start
			result, err := s.CreateSessionStream(t.Context(), tenant, input)
			results <- outcome{result, err}
		}()
	}
	ready.Wait()
	close(start)
	var winner Session
	succeeded, conflicted, created := 0, 0, 0
	for range 8 {
		got := <-results
		if errors.Is(got.err, ErrIdempotencyConflict) {
			conflicted++
			continue
		}
		if got.err != nil || got.result.Session.Creator == nil {
			t.Fatal("creation failed", got)
		}
		succeeded++
		if got.result.Created {
			created++
		}
		if winner.ID == "" {
			winner = got.result.Session
		}
		if winner.ID != got.result.Session.ID || *winner.Creator != *got.result.Session.Creator {
			t.Fatal("creator or Session changed across retries")
		}
	}
	if succeeded != 4 || conflicted != 4 || created != 1 {
		t.Fatalf("success/conflict/created = %d/%d/%d", succeeded, conflicted, created)
	}
	var environments, turns, inputs int
	if err := pool.QueryRow(t.Context(), `SELECT
        (SELECT count(*) FROM environments WHERE session_id=$1),
        (SELECT count(*) FROM turns WHERE session_id=$1),
        (SELECT count(*) FROM turn_inputs WHERE session_id=$1)`, winner.ID).Scan(&environments, &turns, &inputs); err != nil || environments != 1 || turns != 1 || inputs != 1 {
		t.Fatal("concurrent creators duplicated resources", environments, turns, inputs, err)
	}
	pool.Close()
	restarted, _ := testStore(t)
	for _, creator := range creators {
		found, err := restarted.FindSessionCreation(t.Context(), tenant, "shared-retry", request, creator)
		if creator == *winner.Creator {
			if err != nil || found.Session.ID != winner.ID || found.Session.Creator == nil || *found.Session.Creator != creator {
				t.Fatal("creator retry did not survive restart", found, err)
			}
		} else if !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("early recovery ignored creator kind", err)
		}
	}
	if _, err := restarted.GetSession(t.Context(), uuid.NewString(), winner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("creator bypassed project isolation", err)
	}
}

func TestHistoricalUnknownCreatorCannotBeClaimedByRetry(t *testing.T) {
	for _, recordedIntent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown intent", true: "recorded intent"}[recordedIntent], func(t *testing.T) {
			s, pool := testStore(t)
			ctx := context.Background()
			tenant := uuid.NewString()
			input := environmentInput("historical-owner", "self_hosted", "/workspace")
			input.CreationRequest = json.RawMessage(`{"agent_id":"historical-source"}`)
			created, err := s.CreateSession(ctx, tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			// Explicitly represent pre-migration ownership; production creation has no unknown mode.
			if _, err := pool.Exec(ctx, `UPDATE sessions SET creator_kind=NULL, creator_id=NULL,
                creation_request_hash=CASE WHEN $2 THEN creation_request_hash ELSE NULL END WHERE id=$1`, created.ID, recordedIntent); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err := pool.QueryRow(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", created.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			read, err := s.GetSession(ctx, tenant, created.ID)
			if err != nil || read.Creator != nil {
				t.Fatal("historical ownership was invented", read, err)
			}
			page, err := s.ListSessions(ctx, tenant, "", 10, false, nil)
			if err != nil || len(page.Sessions) != 1 || page.Sessions[0].Creator != nil {
				t.Fatal("historical project reads changed", page, err)
			}
			if _, err := s.FindSessionCreation(ctx, tenant, input.IdempotencyKey, input.CreationRequest, input.Creator); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("early retry claimed historical ownership", err)
			}
			if _, err := s.CreateSessionStream(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal("upsert claimed historical ownership", err)
			}
			for _, statement := range []string{
				"UPDATE sessions SET creator_kind='user' WHERE id=$1",
				"UPDATE sessions SET creator_id='someone' WHERE id=$1",
			} {
				if _, err := pool.Exec(ctx, statement, created.ID); err == nil {
					t.Fatal("partial creator passed the database constraint")
				}
			}
			if err := pool.QueryRow(ctx, "SELECT to_jsonb(s)::text FROM sessions s WHERE id=$1", created.ID).Scan(&after); err != nil || after != before {
				t.Fatal("retry changed historical data", err)
			}
		})
	}
}
