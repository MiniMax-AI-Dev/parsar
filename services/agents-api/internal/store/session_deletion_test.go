package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSessionDeletionPreservesExecutionAndRejectsAdmission(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	for _, status := range []string{TurnQueued, TurnInProgress, TurnCompleted} {
		t.Run(status, func(t *testing.T) {
			input := CreateSessionInput{Engine: "codex", IdempotencyKey: status}
			session, err := s.CreateSession(ctx, tenant, input)
			if err != nil {
				t.Fatal(err)
			}
			receipt, err := s.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"retained"}`))
			if err != nil {
				t.Fatal(err)
			}
			if status != TurnQueued {
				_, err = s.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress})
				if err != nil {
					t.Fatal(err)
				}
			}
			if status == TurnCompleted {
				_, err = s.CompleteExecution(ctx, tenant, session.ID, receipt.TurnID, TurnCompleted, nil, "", receipt.Sequence)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DeleteSession(ctx, uuid.NewString(), session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
				t.Fatal(err)
			}
			fresh := New(pool)
			if _, err := fresh.GetSession(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal(err)
			}
			if _, err := fresh.CreateSessionStream(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
				t.Fatal(err)
			}
			if _, err := fresh.SubmitMessage(ctx, tenant, session.ID, "input", json.RawMessage(`{"text":"retained"}`)); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.ListItems(ctx, tenant, session.ID, "", 20, true); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			turn, err := fresh.GetTurn(ctx, tenant, session.ID, receipt.TurnID)
			if err != nil {
				t.Fatal(err)
			}
			if status == TurnQueued {
				if turn.Status != TurnCancelled {
					t.Fatal(turn)
				}
				if _, err := fresh.TransitionTurn(ctx, tenant, session.ID, receipt.TurnID, TurnTransition{ExpectedStatus: TurnQueued, Status: TurnInProgress}); !errors.Is(err, ErrTurnConflict) {
					t.Fatal(err)
				}
			} else if status == TurnInProgress {
				if turn.CancelRequestedAt.IsZero() {
					t.Fatal("missing internal cancellation")
				}
				if _, err := fresh.CompleteExecution(ctx, tenant, session.ID, receipt.TurnID, TurnCancelled, nil, "", receipt.Sequence); err != nil {
					t.Fatal(err)
				}
			} else if turn.Status != TurnCompleted {
				t.Fatal(turn)
			}
			inputs, err := fresh.ListTurnInputs(ctx, tenant, session.ID, receipt.TurnID, 0, 20)
			if err != nil || len(inputs) != 1 {
				t.Fatal(inputs, err)
			}
			if _, err := fresh.SessionEventCursor(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			if _, err := fresh.ListSessionEvents(ctx, tenant, session.ID, 0); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
		})
	}
}

func TestSessionDeletionSerializesAdmissionBeforeRetryLookup(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: "creation"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestCancel(ctx, tenant, session.ID, "existing"); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "UPDATE sessions SET deleted_at=clock_timestamp() WHERE id=$1", session.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.RequestCancel(ctx, tenant, session.ID, "existing"); done <- err }()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrNotFound) {
		t.Fatal("retry admitted after deletion", err)
	}
}
