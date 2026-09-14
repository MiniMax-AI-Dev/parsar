package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestPreparedDispatchSettlesOnlyReadyInput(t *testing.T) {
	for _, action := range []string{"cancel", "expire", "delete", "prepare-failure", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			h, pending, released := preparedDispatchHarness(t)
			_, pool := store.NewTestStore(t)
			result := runPreparedDispatch(h, t.Context(), pending)
			frame := h.read(proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, frame.ID)
			switch action {
			case "cancel":
				if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID); err != nil {
					t.Fatal(err)
				}
			case "expire":
				if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := h.s.DeleteSession(t.Context(), h.tenant, h.session.ID); err != nil {
					t.Fatal(err)
				}
			case "prepare-failure":
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed", ErrorCode: "preparation_failed"})
			case "disconnect":
				_ = h.conn.Close()
			}
			got := awaitPreparedDispatch(t, result)
			if got.run.Turn.ID != "" || released.Load() != 1 {
				t.Fatal("unready preparation admitted work or retained credentials", got, released.Load())
			}
			if action == "cancel" || action == "expire" {
				want := store.EnvironmentInputCancelled
				if action == "expire" {
					want = store.EnvironmentInputExpired
				}
				if got.err != nil || got.run.Reservation.State != want {
					t.Fatal("terminal reservation outcome changed", got)
				}
			} else if got.err == nil {
				t.Fatal("preparation failure was hidden")
			}
			if action == "delete" && !errors.Is(got.err, store.ErrNotFound) {
				t.Fatal("deleted reservation remained accessible", got.err)
			}
			var history int
			err := pool.QueryRow(t.Context(), `SELECT
				(SELECT count(*) FROM turns WHERE session_id=$1)+
				(SELECT count(*) FROM session_items WHERE session_id=$1)+
				(SELECT count(*) FROM session_events WHERE session_id=$1)`, h.session.ID).Scan(&history)
			if err != nil || history != 0 {
				t.Fatal("unready preparation produced history", history, err)
			}
			if action == "prepare-failure" || action == "disconnect" {
				stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
				if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(pending.Deadline) {
					t.Fatal("preparation failure changed the pending identity", stored, err)
				}
			}
		})
	}
}

func TestPreparedDispatchHandlesStartRejectionAndPendingStartCancellation(t *testing.T) {
	for _, action := range []string{"reject", "cancel", "cancel-no-outcome"} {
		t.Run(action, func(t *testing.T) {
			h, pending, released := preparedDispatchHarness(t)
			result := runPreparedDispatch(h, t.Context(), pending)
			frame := h.read(proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, frame.ID)
			start := readyPreparedDispatch(t, h, frame.ID, handle)
			if action == "reject" {
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{State: "rejected", Operation: proto.TypeExecutionStart, ErrorCode: "preparation_not_ready"})
			} else {
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "starting", RunID: start.RunID})
				if _, err := h.s.RequestCancel(t.Context(), h.tenant, h.session.ID, "cancel-start"); err != nil {
					t.Fatal(err)
				}
				frame := h.read(proto.TypePromptCancel)
				var cancel proto.PromptCancelPayload
				if frame.ID != start.RunID || frame.DecodePayload(&cancel) != nil || cancel.DeliveryID == "" {
					t.Fatal("pending Start cancellation lost Run ownership", frame.ID, cancel)
				}
				ack := proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Content: "stopped"}}
				if action == "cancel-no-outcome" {
					// The current daemon can cancel a starting owner before a Session supplies an outcome.
					ack.Outcome = nil
				}
				h.write(start.RunID, proto.TypeInteractionDecisionAck, ack)
			}
			got := awaitPreparedDispatch(t, result)
			want := store.TurnFailed
			if action == "cancel" {
				want = store.TurnCancelled
			}
			if got.err != nil || got.run.Turn.Status != want || got.run.Turn.ID != start.RunID || released.Load() != 1 {
				t.Fatal("Start control did not settle through ordinary completion", got, released.Load())
			}
			if action == "cancel-no-outcome" {
				var outcome struct {
					ErrorCode string `json:"error_code"`
				}
				if json.Unmarshal(got.run.Turn.Outcome, &outcome) != nil || outcome.ErrorCode != "cancel_outcome_unavailable" {
					t.Fatal("missing native cancellation outcome was treated as complete", got.run.Turn.Status)
				}
			}
		})
	}
}

func TestPreparedDispatchRejectsPooledWriterBeforePreparation(t *testing.T) {
	h, pending, released := preparedDispatchHarness(t)
	h.d.Store = h.s
	got, err := h.d.RunEnvironmentInput(context.Background(), h.tenant, h.session.ID, pending.ID)
	if err == nil || got.Turn.ID != "" || released.Load() != 0 {
		t.Fatal("pooled writer reached native preparation", got, err)
	}
}

func TestPreparedDispatchCancellationReceiptSurvivesStartFailure(t *testing.T) {
	for _, withOutcome := range []bool{false, true} {
		name := "no-outcome"
		if withOutcome {
			name = "with-outcome"
		}
		t.Run(name, func(t *testing.T) {
			h, pending, released := preparedDispatchHarness(t)
			result := runPreparedDispatch(h, t.Context(), pending)
			prepare := h.read(proto.TypeExecutionPrepare)
			handle := acknowledgePreparation(h, prepare.ID)
			start := readyPreparedDispatch(t, h, prepare.ID, handle)
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "starting", RunID: start.RunID})
			if _, err := h.s.RequestCancel(t.Context(), h.tenant, h.session.ID, "cancel-start"); err != nil {
				t.Fatal(err)
			}
			frame := h.read(proto.TypePromptCancel)
			var cancel proto.PromptCancelPayload
			if frame.ID != start.RunID || frame.DecodePayload(&cancel) != nil || cancel.DeliveryID == "" {
				t.Fatal("missing cancellation delivery")
			}
			// Cancelling the starting owner can publish failure before its separate acknowledgment.
			h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 4, State: "failed", RunID: start.RunID, ErrorCode: "start_failed"})
			time.Sleep(100 * time.Millisecond)
			ack := proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true}
			if withOutcome {
				// Also verify preservation when a native adapter can supply a complete outcome.
				ack.Outcome = &proto.DonePayload{Content: "retained cancellation", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancelled-prepared-native"}}
			}
			h.write(start.RunID, proto.TypeInteractionDecisionAck, ack)
			got := awaitPreparedDispatch(t, result)
			var outcome execution.Result
			if json.Unmarshal(got.run.Turn.Outcome, &outcome) != nil {
				t.Fatal("invalid stored cancellation outcome")
			}
			want, code := store.TurnFailed, "cancel_outcome_unavailable"
			if withOutcome {
				want, code = store.TurnCancelled, ""
			}
			if got.err != nil || got.run.Turn.Status != want || outcome.ErrorCode != code || released.Load() != 1 {
				t.Fatal("preparation failure replaced the cancellation receipt", got)
			}
			events, err := h.s.ListTurnEvents(t.Context(), h.tenant, h.session.ID, start.RunID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			receipts := 0
			for _, event := range events {
				if event.Kind == "cancel_receipt" {
					receipts++
				}
			}
			if receipts != 1 {
				t.Fatal("cancellation receipt was not journaled once", receipts)
			}
			bound, err := h.s.GetSessionDevice(t.Context(), h.tenant, h.session.ID)
			if err != nil || (withOutcome && (bound.NativeSessionID != "cancelled-prepared-native" || outcome.Done.Content != "retained cancellation")) {
				t.Fatal("cancellation lost native continuation or final output", err)
			}
		})
	}
}
