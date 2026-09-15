package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEnvironmentActiveInputSerializesWithCompletion(t *testing.T) {
	for _, completionFirst := range []bool{false, true} {
		name := "input-first"
		if completionFirst {
			name = "completion-first"
		}
		t.Run(name, func(t *testing.T) {
			s, pool := testStore(t)
			writer := executionLease(t, s).Store()
			tenant, session := environmentInputSession(t, s)
			original := submitMessage(t, s, tenant, session.ID, "original")
			transition(t, s, tenant, session.ID, original.TurnID, TurnQueued, TurnInProgress)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			var blockerPID int32
			if err := blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM sessions WHERE id=$1 FOR UPDATE", session.ID).Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			waiter := func(pid int32) int32 {
				t.Helper()
				for ctx.Err() == nil {
					var blocked int32
					if err := pool.QueryRow(ctx, "SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) ORDER BY pid LIMIT 1", pid).Scan(&blocked); err == nil {
						return blocked
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatal("transaction did not wait for the Session lock")
				return 0
			}
			type admission struct {
				value EnvironmentInputReservation
				err   error
			}
			admitted := make(chan admission, 1)
			completed := make(chan error, 1)
			batch := []Input{messageInput("first"), messageInput("second")}
			input := func() {
				value, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "racing-input", batch)
				admitted <- admission{value, err}
			}
			complete := func() {
				_, err := writer.CompleteExecution(ctx, tenant, session.ID, original.TurnID, TurnCompleted, nil, "", original.Sequence)
				completed <- err
			}
			first, second := input, complete
			if completionFirst {
				first, second = complete, input
			}
			go first()
			firstPID := waiter(blockerPID)
			go second()
			waiter(firstPID)
			if err := blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			got, completionErr := <-admitted, <-completed
			if got.err != nil {
				t.Fatal(got.err)
			}
			if completionFirst {
				if completionErr != nil || got.value.State != EnvironmentInputPending || got.value.ID == "" || len(got.value.Receipts) != 0 || got.value.Deadline.Sub(got.value.CreatedAt) != 5*time.Minute {
					t.Fatal("completion winner did not leave new input waiting for preparation", completionErr, got.value)
				}
				environmentInputHistory(t, pool, session.ID, 1, 1)
				prepared, err := writer.PromoteEnvironmentInput(ctx, tenant, session.ID, got.value.ID)
				if err != nil || len(prepared.Receipts) != 2 || prepared.Receipts[0].TurnID == original.TurnID {
					t.Fatal("prepared successor reused terminal work", err)
				}
				retry, err := s.ReserveEnvironmentInput(ctx, tenant, session.ID, "racing-input", batch)
				if err != nil || retry.ID != got.value.ID || !retry.Deadline.Equal(got.value.Deadline) || len(retry.Receipts) != 2 || !retry.Receipts[0].Replayed || retry.Receipts[0].TurnID != prepared.Receipts[0].TurnID {
					t.Fatal("active retry replaced its original reservation", retry, err)
				}
				if _, err := writer.CompleteExecution(ctx, tenant, session.ID, prepared.Receipts[0].TurnID, TurnCompleted, nil, "", prepared.Receipts[1].Sequence); err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(completionErr, ErrUnappliedInputs) || got.value.State != EnvironmentInputAdmitted || got.value.ID != "" || !got.value.Deadline.IsZero() || len(got.value.Receipts) != 2 || got.value.Receipts[0].TurnID != original.TurnID || got.value.Receipts[0].Replayed {
					t.Fatal("admitted input escaped the original Turn or application fence", completionErr, got.value)
				}
				environmentInputHistory(t, pool, session.ID, 1, 3)
				if _, err := writer.CompleteExecution(ctx, tenant, session.ID, original.TurnID, TurnCompleted, nil, "", got.value.Receipts[1].Sequence); err != nil {
					t.Fatal("completion after controlled application failed", err)
				}
			}
		})
	}
}
