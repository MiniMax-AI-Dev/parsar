package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestWorkerReconcilesEnvironmentPromotionBeforeStart(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "deleted"}[deleted], func(t *testing.T) {
			s, pool := store.NewTestStore(t)
			tenant, pending := newEnvironmentExpiryReservation(t, s)
			lease, err := s.AcquireExecutionLease(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lease.Close(context.Background()) })
			got, err := lease.Store().PromoteEnvironmentInput(t.Context(), tenant, pending.SessionID, pending.ID)
			if err != nil || len(got.Receipts) != 1 || got.Receipts[0].Replayed {
				t.Fatal(got, err)
			}
			turnID := got.Receipts[0].TurnID
			if deleted {
				if err := s.DeleteSession(t.Context(), tenant, pending.SessionID); err != nil {
					t.Fatal(err)
				}
			}
			turn, err := s.GetTurn(t.Context(), tenant, pending.SessionID, turnID)
			if err != nil || turn.Status != store.TurnInProgress || (deleted && turn.CancelRequestedAt.IsZero()) {
				t.Fatal("promotion did not retain the active claim", turn, err)
			}
			// Simulate owner loss after commit, without sending any daemon Start.
			if err := lease.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			restarted, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: gateway.NewRegistry()})
			if err != nil {
				t.Fatal(err)
			}
			stopped, cancel := context.WithCancel(t.Context())
			cancel()
			if err := restarted.Run(stopped); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			turn, err = s.GetTurn(t.Context(), tenant, pending.SessionID, turnID)
			var outcome struct {
				ErrorCode string `json:"error_code"`
			}
			if err != nil || turn.Status != store.TurnFailed || json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.ErrorCode != "execution_interrupted" {
				t.Fatal("restart failed to settle the original claim", turn, err)
			}
			var turns, inputs, queued int
			err = pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM turns WHERE session_id=$1),
				(SELECT count(*) FROM turn_inputs WHERE session_id=$1),
				(SELECT count(*) FROM turns WHERE session_id=$1 AND status='queued')`, pending.SessionID).Scan(&turns, &inputs, &queued)
			if err != nil || turns != 1 || inputs != 1 || queued != 0 {
				t.Fatal("restart duplicated or requeued prepared work", turns, inputs, queued, err)
			}
			successor, err := s.AcquireExecutionLease(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = successor.Close(context.Background()) })
			retry, err := successor.Store().PromoteEnvironmentInput(t.Context(), tenant, pending.SessionID, pending.ID)
			if deleted {
				if !errors.Is(err, store.ErrNotFound) {
					t.Fatal("deleted reservation was exposed", err)
				}
			} else if err != nil || len(retry.Receipts) != 1 || !retry.Receipts[0].Replayed || retry.Receipts[0].Sequence != got.Receipts[0].Sequence || retry.Receipts[0].TurnID != turnID {
				t.Fatal("recovery retry authorized another start", retry, err)
			}
		})
	}
}
