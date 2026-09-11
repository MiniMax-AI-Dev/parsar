package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func messageInput(text string) Input {
	payload, _ := json.Marshal(map[string]string{"text": text})
	return Input{Kind: "message", Payload: payload}
}

func TestInputBatchesAreOrderedAndIdempotentAcrossConnections(t *testing.T) {
	s, _ := testStore(t)
	other, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	const count = 8
	batch := []Input{messageInput("first"), messageInput("second")}
	for _, repeated := range []bool{true, false} {
		var wg sync.WaitGroup
		receipts := make(chan []InputReceipt, count)
		errs := make(chan error, count)
		for i := range count {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st := s
				if i%2 == 0 {
					st = other
				}
				key := "same-batch"
				if !repeated {
					key = fmt.Sprintf("batch-%d", i)
				}
				got, err := st.SubmitInputs(ctx, tenant, session.ID, key, batch)
				receipts <- got
				errs <- err
			}()
		}
		wg.Wait()
		close(receipts)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		newBatches := 0
		for got := range receipts {
			if len(got) != 2 || got[0].TurnID == "" || got[0].TurnID != got[1].TurnID || got[0].Sequence >= got[1].Sequence || got[0].Replayed != got[1].Replayed {
				t.Fatalf("invalid batch receipt: %+v", got)
			}
			if !got[0].Replayed {
				newBatches++
			}
		}
		want := count
		if repeated {
			want = 1
		}
		if newBatches != want {
			t.Fatalf("accepted %d batches, want %d", newBatches, want)
		}
	}
	got, err := s.SubmitInputs(ctx, tenant, session.ID, "same-batch", batch)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := s.ListTurnInputs(ctx, tenant, session.ID, got[0].TurnID, 0, 100)
	if err != nil || len(inputs) != 2*(count+1) {
		t.Fatalf("inputs=%d err=%v", len(inputs), err)
	}
	for i, input := range inputs {
		var payload map[string]string
		if err := json.Unmarshal(input.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		want := "first"
		if i%2 == 1 {
			want = "second"
		}
		if payload["text"] != want {
			t.Fatalf("batch interleaved at %d: %v", i, payload)
		}
	}
}

func TestBatchRetriesCompareTheWholeRequestAndRetainTargets(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	cancel := Input{Kind: "cancel", Payload: json.RawMessage(`{}`)}
	batch := []Input{cancel, messageInput("one"), cancel, messageInput("two")}
	first, err := s.SubmitInputs(ctx, tenant, session.ID, "mixed", batch)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].TurnID != "" || first[1].TurnID == "" || first[1].TurnID != first[2].TurnID || first[1].TurnID == first[3].TurnID {
		t.Fatalf("cancellation targets: %+v", first)
	}
	cancelled, err := s.GetTurn(ctx, tenant, session.ID, first[1].TurnID)
	if err != nil || cancelled.Status != TurnCancelled {
		t.Fatalf("cancelled turn=%+v err=%v", cancelled, err)
	}
	transition(t, s, tenant, session.ID, first[3].TurnID, TurnQueued, TurnInProgress)
	transition(t, s, tenant, session.ID, first[3].TurnID, TurnInProgress, TurnCompleted)
	next := submitMessage(t, s, tenant, session.ID, "next")
	for _, changed := range [][]Input{batch[:3], append(append([]Input{}, batch...), cancel), {batch[0], batch[3], batch[2], batch[1]}} {
		if _, err := s.SubmitInputs(ctx, tenant, session.ID, "mixed", changed); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatalf("changed batch accepted: %v", err)
		}
	}
	batch[1].Payload = json.RawMessage(`{ "text" : "one" }`)
	pool.Close()
	restarted, _ := testStore(t)
	retry, err := restarted.SubmitInputs(ctx, tenant, session.ID, "mixed", batch)
	for i := range first {
		first[i].Replayed = true
	}
	if err != nil || !reflect.DeepEqual(retry, first) {
		t.Fatalf("restart changed receipts: %+v, %v", retry, err)
	}
	current, err := restarted.GetTurn(ctx, tenant, session.ID, next.TurnID)
	if err != nil || current.Status != TurnQueued || !current.CancelRequestedAt.IsZero() {
		t.Fatalf("retry cancelled later work: %+v, %v", current, err)
	}
	otherTenant, _ := newTurnSession(t, restarted)
	if _, err := restarted.SubmitInputs(ctx, otherTenant, session.ID, "mixed", batch); !errors.Is(err, ErrNotFound) {
		t.Fatalf("batch retry escaped tenant: %v", err)
	}
}

func TestFailedBatchRollsBackEarlierCancellationAndInputs(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	initial := submitMessage(t, s, tenant, session.ID, "initial")
	transition(t, s, tenant, session.ID, initial.TurnID, TurnQueued, TurnInProgress)
	key := uuid.NewString()
	constraint := "batch_failure_" + strings.ReplaceAll(key, "-", "")
	// Inject a storage error on the second insert, after cancellation has run.
	_, err := pool.Exec(ctx, "ALTER TABLE turn_inputs ADD CONSTRAINT "+constraint+" CHECK (idempotency_key <> '"+key+"' OR batch_position = 0)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "ALTER TABLE turn_inputs DROP CONSTRAINT IF EXISTS "+constraint) })
	batch := []Input{{Kind: "cancel", Payload: json.RawMessage(`{}`)}, messageInput("after cancel")}
	if got, err := s.SubmitInputs(ctx, tenant, session.ID, key, batch); err == nil || got != nil {
		t.Fatalf("partial batch succeeded: %+v %v", got, err)
	}
	turn, err := s.GetTurn(ctx, tenant, session.ID, initial.TurnID)
	if err != nil || turn.Status != TurnInProgress || !turn.CancelRequestedAt.IsZero() {
		t.Fatalf("cancellation escaped rollback: %+v %v", turn, err)
	}
	inputs, err := s.ListTurnInputs(ctx, tenant, session.ID, initial.TurnID, 0, 100)
	if err != nil || len(inputs) != 1 {
		t.Fatalf("partial inputs survived: %v %v", inputs, err)
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE turn_inputs DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	got, err := s.SubmitInputs(ctx, tenant, session.ID, key, batch)
	if err != nil || len(got) != 2 || got[0].Replayed || got[1].Replayed {
		t.Fatalf("retry after rollback: %+v %v", got, err)
	}
}

func TestInputBatchValidation(t *testing.T) {
	for _, input := range [][]Input{
		nil, make([]Input, 65), {messageInput("ok"), {Kind: "unsupported", Payload: json.RawMessage(`{}`)}},
		{{Kind: "message"}}, {{Kind: "message", Payload: json.RawMessage(`[]`)}},
		{{Kind: "cancel", Payload: json.RawMessage(`{"target":"other"}`)}},
		{messageInput(strings.Repeat("x", 300*1024)), messageInput(strings.Repeat("y", 300*1024))},
	} {
		if _, _, err := validateInputs(input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid batch accepted: %v", err)
		}
	}
}
