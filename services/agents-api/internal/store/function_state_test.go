package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestFunctionStateSnapshotsRecoveryAndRetries(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	before, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		for range 2 {
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture(id)); err != nil {
				t.Fatal(err)
			}
		}
	}
	assertFunctionState(t, s, tenant, session.ID, TurnWaiting, 2)
	for _, id := range []string{"first", "second"} {
		if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, id, json.RawMessage(`{"success":true,"output":"private result"}`)); err != nil {
			t.Fatal(err)
		}
	}
	assertFunctionState(t, s, tenant, session.ID, TurnWaiting, 2)
	pool.Close()
	s, _ = testStore(t)
	assertFunctionState(t, s, tenant, session.ID, TurnWaiting, 2)
	if _, err := s.CompleteExecution(t.Context(), tenant, session.ID, turn, TurnCompleted, nil, "", 1); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("waiting execution completed", err)
	}
	for i, id := range []string{"first", "second"} {
		for range 2 {
			if err := s.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, id); err != nil {
				t.Fatal(err)
			}
		}
		status := TurnWaiting
		if i == 1 {
			status = TurnInProgress
		}
		assertFunctionState(t, s, tenant, session.ID, status, 1-i)
	}
	changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, before)
	if err != nil {
		t.Fatal(err)
	}
	counts := []int{1, 2, 1, 0, 0}
	types := []string{"agent.session.requires_action", "agent.session.requires_action", "agent.session.requires_action", "agent.session.turn.in_progress", "agent.session.in_progress"}
	if len(changes) != len(counts) {
		t.Fatalf("duplicate or missing events: %+v", changes)
	}
	for i, change := range changes {
		if change.Event.Type != types[i] || len(change.RequiredActions) != counts[i] {
			t.Fatalf("snapshot %d: %+v", i, change)
		}
		if counts[i] > 0 {
			raw, err := json.Marshal(change.RequiredActions[0].Arguments)
			if err != nil || string(raw) != `{"ticket":9007199254740993}` {
				t.Fatal("argument precision lost", string(raw), err)
			}
			if change.Turn.Status != TurnWaiting {
				t.Fatal(change.Turn)
			}
		}
	}
	if _, err := s.GetSession(t.Context(), uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.ListSessionEvents(t.Context(), uuid.NewString(), session.ID, before); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestFunctionStateCancellationAndTerminalCleanup(t *testing.T) {
	for _, status := range []string{TurnCancelled, TurnFailed, TurnCompleted} {
		t.Run(status, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session := newTurnSession(t, s)
			input := submitMessage(t, s, tenant, session.ID, "start")
			transition(t, s, tenant, session.ID, input.TurnID, TurnQueued, TurnInProgress)
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, input.TurnID, functionCallFixture("call")); err != nil {
				t.Fatal(err)
			}
			if status == TurnCancelled {
				before, _ := s.SessionEventCursor(t.Context(), tenant, session.ID)
				for _, key := range []string{"cancel", "cancel", "another-cancel"} {
					if _, err := s.RequestCancel(t.Context(), tenant, session.ID, key); err != nil {
						t.Fatal(err)
					}
				}
				assertFunctionState(t, s, tenant, session.ID, TurnWaiting, 0)
				changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, before)
				if err != nil || len(changes) != 1 || changes[0].Event.Type != "agent.session.in_progress" || len(changes[0].RequiredActions) != 0 || changes[0].Turn.CancelRequestedAt.IsZero() {
					t.Fatal(changes, err)
				}
			}
			if status == TurnCompleted {
				transition(t, s, tenant, session.ID, input.TurnID, TurnWaiting, status)
			} else if _, err := s.CompleteExecution(t.Context(), tenant, session.ID, input.TurnID, status, nil, "", input.Sequence); err != nil {
				t.Fatal(err)
			}
			assertFunctionState(t, s, tenant, session.ID, status, 0)
			if _, err := s.GetFunctionCall(t.Context(), tenant, session.ID, input.TurnID, "call"); err != nil {
				t.Fatal("history lost", err)
			}
			next := submitMessage(t, s, tenant, session.ID, "next")
			if next.TurnID == input.TurnID {
				t.Fatal("terminal turn reused")
			}
			assertFunctionState(t, s, tenant, session.ID, TurnQueued, 0)
		})
	}
}

func TestFunctionStateReadsRemainConsistentDuringReceipts(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Go(func() {
		defer close(done)
		for i := range 30 {
			id := fmt.Sprint(i)
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, functionCallFixture(id)); err != nil {
				t.Error(err)
				return
			}
			if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, id, json.RawMessage(`{"success":true}`)); err != nil {
				t.Error(err)
				return
			}
			if err := s.ConfirmFunctionResult(t.Context(), tenant, session.ID, turn, id); err != nil {
				t.Error(err)
				return
			}
		}
	})
	defer wg.Wait()
	for {
		current, err := s.GetSession(t.Context(), tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if (current.LastTurn.Status == TurnWaiting) != (len(current.RequiredActions) > 0) {
			t.Fatalf("torn activity snapshot: %+v", current)
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func assertFunctionState(t *testing.T, s *Store, tenant, sessionID, status string, count int) {
	t.Helper()
	current, err := s.GetSession(t.Context(), tenant, sessionID)
	if err != nil || current.LastTurn == nil || current.LastTurn.Status != status || len(current.RequiredActions) != count {
		t.Fatalf("state: %+v; %v", current, err)
	}
	page, err := s.ListSessions(t.Context(), tenant, "", 10, true)
	if err != nil || len(page.Sessions) != 1 || len(page.Sessions[0].RequiredActions) != count || page.Sessions[0].LastTurn.Status != status {
		t.Fatal("list differs from retrieve", page, err)
	}
}
