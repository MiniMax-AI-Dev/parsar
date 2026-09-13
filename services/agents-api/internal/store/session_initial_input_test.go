package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestInitialInputCreationRetriesAcrossConnectionsAndLaterTurns(t *testing.T) {
	s, pool := testStore(t)
	other, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	input := CreateSessionInput{Engine: "codex", IdempotencyKey: "initial", InitialInputs: []Input{messageInput("first"), messageInput("second")}}
	var wg sync.WaitGroup
	results := make(chan Session, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := s
			if i%2 == 0 {
				st = other
			}
			session, err := st.CreateSession(ctx, tenant, input)
			if err != nil {
				t.Error(err)
				return
			}
			results <- session
		}()
	}
	wg.Wait()
	close(results)
	var first Session
	for session := range results {
		if first.ID == "" {
			first = session
		}
		if session.ID != first.ID || session.LastTurn == nil || session.LastTurn.ID != first.LastTurn.ID {
			t.Fatal("creation retry duplicated work", session)
		}
	}
	if first.LastTurn == nil {
		t.Fatal("missing initial Turn")
	}
	inputs, err := s.ListTurnInputs(ctx, tenant, first.ID, first.LastTurn.ID, 0, 100)
	if err != nil || len(inputs) != 2 {
		t.Fatal(inputs, err)
	}
	if !strings.Contains(string(inputs[0].Payload), "first") || !strings.Contains(string(inputs[1].Payload), "second") {
		t.Fatal(inputs)
	}
	for _, changed := range [][]Input{nil, {messageInput("changed")}, {input.InitialInputs[1], input.InitialInputs[0]}} {
		request := input
		request.InitialInputs = changed
		if _, err := s.CreateSession(ctx, tenant, request); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("changed initial input accepted", err)
		}
	}
	transition(t, s, tenant, first.ID, first.LastTurn.ID, TurnQueued, TurnInProgress)
	transition(t, s, tenant, first.ID, first.LastTurn.ID, TurnInProgress, TurnCompleted)
	// The same caller key at the events endpoint is an independent request.
	next, err := s.SubmitInputs(ctx, tenant, first.ID, input.IdempotencyKey, []Input{messageInput("later")})
	if err != nil || len(next) != 1 || next[0].TurnID == first.LastTurn.ID {
		t.Fatal(next, err)
	}
	if _, err := s.RequestCancel(ctx, tenant, first.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateSessionMetadata(ctx, tenant, first.ID, map[string]string{"updated": "yes"}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListSessionEvents(ctx, tenant, first.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	restarted, _ := testStore(t)
	retry, err := restarted.CreateSession(ctx, tenant, input)
	if err != nil || retry.ID != first.ID || retry.LastTurn.ID != next[0].TurnID || retry.LastTurn.Status != TurnCancelled || retry.Metadata["updated"] != "yes" {
		t.Fatal(retry, err)
	}
	after, err := restarted.ListSessionEvents(ctx, tenant, first.ID, 0)
	if err != nil || !reflect.DeepEqual(events, after) {
		t.Fatal("retry emitted more events", err)
	}
	foreign, err := restarted.CreateSession(ctx, uuid.NewString(), input)
	if err != nil || foreign.ID == first.ID || foreign.LastTurn.ID == first.LastTurn.ID {
		t.Fatal("tenant creation keys collided", foreign, err)
	}
}

func TestInitialInputFailureRollsBackSessionAndWork(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenant, marker := uuid.NewString(), uuid.NewString()
	constraint := "initial_failure_" + strings.ReplaceAll(marker, "-", "")
	// Fail the second input insert after the Session, Turn and first Item exist.
	_, err := pool.Exec(ctx, "ALTER TABLE turn_inputs ADD CONSTRAINT "+constraint+" CHECK (payload->>'text' <> '"+marker+"')")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "ALTER TABLE turn_inputs DROP CONSTRAINT IF EXISTS "+constraint) })
	input := CreateSessionInput{Engine: "codex", IdempotencyKey: "rollback", InitialInputs: []Input{messageInput("first"), messageInput(marker)}}
	if got, err := s.CreateSession(ctx, tenant, input); err == nil || got.ID != "" {
		t.Fatal("partial creation succeeded", got, err)
	}
	page, err := s.ListSessions(ctx, tenant, "", 100, true)
	if err != nil || len(page.Sessions) != 0 {
		t.Fatal("partial Session survived", page, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE turn_inputs DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	got, err := s.CreateSession(ctx, tenant, input)
	if err != nil || got.LastTurn == nil {
		t.Fatal("retry after rollback failed", got, err)
	}
	items, err := s.ListItems(ctx, tenant, got.ID, "", 100, true)
	if err != nil || len(items.Items) != 2 {
		t.Fatal(items, err)
	}
}
