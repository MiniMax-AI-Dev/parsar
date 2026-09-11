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

var messagePayload = json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

func newTurnSession(t *testing.T, s *Store) (string, Session) {
	t.Helper()
	tenant := uuid.NewString()
	session, err := s.CreateSession(context.Background(), tenant, CreateSessionInput{
		Engine: "codex", IdempotencyKey: "session", Configuration: json.RawMessage(`{"agent":{"model":"test","instructions":"original"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return tenant, session
}

func submitMessage(t *testing.T, s *Store, tenant, session, key string) InputReceipt {
	t.Helper()
	receipt, err := s.SubmitMessage(context.Background(), tenant, session, key, messagePayload)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func transition(t *testing.T, s *Store, tenant, session, turn, from, to string) Turn {
	t.Helper()
	got, err := s.TransitionTurn(context.Background(), tenant, session, turn, TurnTransition{ExpectedStatus: from, Status: to})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestConcurrentInputsUseOneTurnAndOneRetryReceipt(t *testing.T) {
	s, _ := testStore(t)
	otherStore, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	const count = 8
	for _, repeatedKey := range []bool{true, false} {
		t.Run(fmt.Sprintf("repeated-key-%v", repeatedKey), func(t *testing.T) {
			var wg sync.WaitGroup
			receipts := make(chan InputReceipt, count)
			errs := make(chan error, count)
			for i := range count {
				wg.Add(1)
				go func() {
					defer wg.Done()
					key := "same"
					if !repeatedKey {
						key = fmt.Sprintf("distinct-%d", i)
					}
					store := s
					if i%2 == 0 {
						store = otherStore
					}
					r, err := store.SubmitMessage(ctx, tenant, session.ID, key, messagePayload)
					receipts <- r
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
			turns, sequences := map[string]bool{}, map[int64]bool{}
			newReceipts := 0
			for r := range receipts {
				turns[r.TurnID], sequences[r.Sequence] = true, true
				if !r.Replayed {
					newReceipts++
				}
			}
			want := count
			if repeatedKey {
				want = 1
			}
			if len(turns) != 1 || turns[""] || len(sequences) != want || newReceipts != want {
				t.Fatalf("turns=%v sequences=%v new=%d", turns, sequences, newReceipts)
			}
		})
	}
	first := submitMessage(t, s, tenant, session.ID, "same")
	inputs, err := s.ListTurnInputs(ctx, tenant, session.ID, first.TurnID, 0, 100)
	if err != nil || len(inputs) != count+1 {
		t.Fatalf("lost or duplicated inputs: %d, %v", len(inputs), err)
	}
}

func TestTurnInputRetriesAndRestart(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	first := submitMessage(t, s, tenant, session.ID, "first")
	transition(t, s, tenant, session.ID, first.TurnID, TurnQueued, TurnInProgress)
	steer := submitMessage(t, s, tenant, session.ID, "steer")
	if steer.TurnID != first.TurnID || steer.Sequence <= first.Sequence {
		t.Fatalf("active message did not steer: %+v", steer)
	}
	reordered := json.RawMessage(`{ "input": [{"content":[{"text":"hello","type":"input_text"}],"role":"user"}] }`)
	retry, err := s.SubmitMessage(ctx, tenant, session.ID, "first", reordered)
	if err != nil || !retry.Replayed || retry.Sequence != first.Sequence || retry.TurnID != first.TurnID {
		t.Fatalf("equivalent retry = %+v, %v", retry, err)
	}
	if _, err := s.SubmitMessage(ctx, tenant, session.ID, "first", json.RawMessage(`{"text":"changed"}`)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payload accepted: %v", err)
	}
	if _, err := s.RequestCancel(ctx, tenant, session.ID, "first"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed input kind accepted: %v", err)
	}
	completed := transition(t, s, tenant, session.ID, first.TurnID, TurnInProgress, TurnCompleted)
	next := submitMessage(t, s, tenant, session.ID, "next")
	if next.TurnID == first.TurnID {
		t.Fatal("idle message did not start a new Turn")
	}
	pool.Close()
	recovered, _ := testStore(t)
	retry = submitMessage(t, recovered, tenant, session.ID, "first")
	if !retry.Replayed || retry.TurnID != first.TurnID || retry.Sequence != first.Sequence {
		t.Fatalf("restart retry changed target: %+v", retry)
	}
	got, err := recovered.GetTurn(ctx, tenant, session.ID, first.TurnID)
	if err != nil || !reflect.DeepEqual(got, completed) {
		t.Fatalf("restart turn: %+v, %v", got, err)
	}
	var all []TurnInput
	var cursor int64
	for {
		page, err := recovered.ListTurnInputs(ctx, tenant, session.ID, first.TurnID, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if page[0].Sequence <= cursor || page[0].CreatedAt.IsZero() {
			t.Fatalf("invalid ordered input: %+v", page)
		}
		cursor = page[0].Sequence
		all = append(all, page...)
	}
	if len(all) != 2 || all[0].Sequence != first.Sequence || all[1].Sequence != steer.Sequence {
		t.Fatalf("recovered inputs = %+v", all)
	}
	snapshot, err := recovered.GetSession(ctx, tenant, session.ID)
	if err != nil || snapshot.LastTurn == nil || snapshot.LastTurn.ID != next.TurnID || snapshot.LastTurn.Status != TurnQueued {
		t.Fatal("latest Session activity did not survive restart", err)
	}
	snapshot.LastTurn = nil
	if !reflect.DeepEqual(snapshot, session) {
		t.Fatal("turn submission mutated the Session snapshot", err)
	}
}

func TestTurnOperationsAreTenantAndSessionScoped(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	otherTenant, other := newTurnSession(t, s)
	ctx := context.Background()
	first := submitMessage(t, s, tenant, session.ID, "input")
	for _, scope := range []struct{ tenant, session string }{{otherTenant, session.ID}, {tenant, other.ID}, {tenant, uuid.NewString()}} {
		for name, call := range map[string]func() error{
			"submit": func() error {
				_, err := s.SubmitMessage(ctx, scope.tenant, scope.session, "input", messagePayload)
				return err
			},
			"cancel": func() error { _, err := s.RequestCancel(ctx, scope.tenant, scope.session, "cancel"); return err },
			"read":   func() error { _, err := s.GetTurn(ctx, scope.tenant, scope.session, first.TurnID); return err },
			"inputs": func() error {
				_, err := s.ListTurnInputs(ctx, scope.tenant, scope.session, first.TurnID, 0, 10)
				return err
			},
			"transition": func() error {
				_, err := s.TransitionTurn(ctx, scope.tenant, scope.session, first.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnFailed})
				return err
			},
		} {
			if err := call(); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s escaped scope: %v", name, err)
			}
		}
	}
	// Turn IDs cannot be used with another valid Session in the same tenant either.
	second, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTurn(ctx, tenant, second.ID, first.TurnID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session turn read: %v", err)
	}
	if _, err := s.TransitionTurn(ctx, tenant, second.ID, first.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnFailed}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-session turn write: %v", err)
	}
	otherInput := submitMessage(t, s, otherTenant, other.ID, "input")
	if otherInput.TurnID == first.TurnID {
		t.Fatal("retry identity leaked across Sessions")
	}
}
