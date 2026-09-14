package store

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestCreationStreamStartsBeforeOwnInputsAndRetriesAtUpsertCursor(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "stream", InitialInputs: []Input{messageInput("first")}}
	var wg sync.WaitGroup
	results := make(chan SessionCreation, 8)
	for i := range 8 {
		wg.Go(func() {
			st := s
			if i%2 == 0 {
				st = other
			}
			result, err := st.CreateSessionStream(ctx, tenant, input)
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		})
	}
	wg.Wait()
	close(results)
	var created SessionCreation
	var retries []SessionCreation
	for result := range results {
		if result.Created {
			if created.Created {
				t.Fatal("two creation owners")
			}
			created = result
		} else {
			retries = append(retries, result)
		}
	}
	if !created.Created || created.Cursor != 0 || created.Session.LastTurn != nil || len(retries) != 7 {
		t.Fatal("invalid pre-input creation snapshot", created, retries)
	}
	id := created.Session.ID
	initial, err := s.ListSessionEvents(ctx, tenant, id, created.Cursor)
	if err != nil || len(initial) != 3 || initial[0].Event.Type != "agent.session.turn.created" {
		t.Fatal("lost initial events", initial, err)
	}
	for _, retry := range retries {
		if retry.Session.ID != id || retry.Cursor != initial[len(initial)-1].Sequence {
			t.Fatal(retry)
		}
		if events, err := s.ListSessionEvents(ctx, tenant, id, retry.Cursor); err != nil || len(events) != 0 {
			t.Fatal("retry replayed initial events", events, err)
		}
	}
	ordinary, err := s.CreateSession(ctx, tenant, input)
	if err != nil || ordinary.ID != id || ordinary.LastTurn == nil {
		t.Fatal(ordinary, err)
	}
	transition(t, s, tenant, id, ordinary.LastTurn.ID, TurnQueued, TurnInProgress)
	transition(t, s, tenant, id, ordinary.LastTurn.ID, TurnInProgress, TurnCompleted)
	// Completing before the HTTP observer drains does not change its start point.
	all, err := s.ListSessionEvents(ctx, tenant, id, created.Cursor)
	if err != nil || len(all) <= len(initial) || all[0].Event.EventID != initial[0].Event.EventID {
		t.Fatal(all, err)
	}
	late, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || late.Created || late.Cursor != all[len(all)-1].Sequence {
		t.Fatal(late, err)
	}
	next, err := s.SubmitInputs(ctx, tenant, id, "next", []Input{messageInput("later")})
	if err != nil {
		t.Fatal(err)
	}
	future, err := s.ListSessionEvents(ctx, tenant, id, late.Cursor)
	if err != nil || len(future) != 3 || future[0].Turn.ID != next[0].TurnID {
		t.Fatal(future, err)
	}
	turns, err := s.ListTurns(ctx, tenant, id, "", 100, true)
	if err != nil || len(turns.Turns) != 2 {
		t.Fatal(turns, err)
	}
	foreign, err := s.CreateSessionStream(ctx, uuid.NewString(), input)
	if err != nil || !foreign.Created || foreign.Session.ID == id || foreign.Cursor != 0 {
		t.Fatal(foreign, err)
	}
}

func TestCreationStreamIdleAndNonstreamRetry(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "idle"}
	first, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || !first.Created || first.Cursor != 0 || first.Session.LastTurn != nil {
		t.Fatal(first, err)
	}
	if _, err := s.SubmitInputs(ctx, tenant, first.Session.ID, "message", []Input{messageInput("later")}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateSessionStream(ctx, tenant, input)
	if err != nil || retry.Created || retry.Cursor == 0 {
		t.Fatal(retry, err)
	}
	ordinary, err := s.CreateSession(ctx, tenant, input)
	if err != nil || ordinary.ID != first.Session.ID || ordinary.LastTurn == nil {
		t.Fatal(ordinary, err)
	}
	input.IdempotencyKey = "nonstream-first"
	ordinary, err = s.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err = s.CreateSessionStream(ctx, tenant, input)
	if err != nil || retry.Created || retry.Session.ID != ordinary.ID || retry.Cursor != 0 {
		t.Fatal(retry, err)
	}
}
