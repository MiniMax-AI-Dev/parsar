package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestTurnEventBatchesAreOrderedIsolatedAndDurable(t *testing.T) {
	ctx := context.Background()
	s, _ := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "events"})
	if err != nil {
		t.Fatal(err)
	}
	input, err := s.SubmitMessage(ctx, tenant, session.ID, "start", json.RawMessage(`{"text":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TransitionTurn(ctx, tenant, session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	batch := []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"部分内容","sequence":1}`)}, {Kind: "usage", Payload: json.RawMessage(`{"input_tokens":10}`)}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	conflict := []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"changed"}`)}}
	if err := s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, conflict); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if err := s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, conflict); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal(err)
	}
	for _, owner := range []string{uuid.NewString()} {
		if err := s.AppendTurnEvents(ctx, owner, session.ID, input.TurnID, 1, batch); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := s.ListTurnEvents(ctx, owner, session.ID, input.TurnID, 0, 100); !errors.Is(err, store.ErrNotFound) {
			t.Fatal(err)
		}
	}
	other, _ := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "other"})
	if _, err := s.ListTurnEvents(ctx, tenant, other.ID, input.TurnID, 0, 100); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	// A failed native binding write must roll back both the terminal event and status.
	if _, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCompleted, json.RawMessage(`{}`), "missing-binding", input.Sequence); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	events, _ := s.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, 0, 100)
	if len(events) != 2 {
		t.Fatal("terminal event survived rollback")
	}
	if _, err = s.CompleteExecution(ctx, tenant, session.ID, input.TurnID, store.TurnCancelled, json.RawMessage(`{"done":{"content":""}}`), "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
		t.Fatal("retry after terminal", err)
	}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, conflict); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal(err)
	}
	reopened, pool := store.NewTestStore(t)
	defer pool.Close()
	page, err := reopened.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, 0, 1)
	if err != nil || len(page) != 1 || page[0].Ordinal != 1 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
	page, err = reopened.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, page[0].Ordinal, 100)
	if err != nil || len(page) != 2 || page[1].Kind != "execution_cancelled" || page[1].Ordinal != 3 {
		t.Fatalf("page=%+v error=%v", page, err)
	}
}

func TestEventLimitStillAllowsTerminalFailure(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	input := h.message("start", "Test output budget")
	_, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	_, pool := store.NewTestStore(t)
	defer pool.Close()
	if _, err := pool.Exec(ctx, "UPDATE turns SET event_bytes=33554432 WHERE id=$1", input.TurnID); err != nil {
		t.Fatal(err)
	}
	events := []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"delta":"more"}`)}}
	if err = h.s.AppendTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 1, events); !errors.Is(err, store.ErrEventLimit) {
		t.Fatal(err)
	}
	if _, err = h.s.CompleteExecution(ctx, h.tenant, h.session.ID, input.TurnID, store.TurnFailed, json.RawMessage(`{"error_code":"event_limit"}`), "", input.Sequence); err != nil {
		t.Fatal(err)
	}
	got, err := h.s.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
	if err != nil || len(got) != 1 || got[0].Kind != "execution_failed" {
		t.Fatalf("events=%+v error=%v", got, err)
	}
}
