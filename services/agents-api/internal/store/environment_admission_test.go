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
	"github.com/google/uuid"
)

type environmentAdmissionResult struct {
	receipts []store.InputReceipt
	err      error
}

func newEnvironmentAdmission(t *testing.T) (*dispatchHarness, *execution.Worker) {
	t.Helper()
	h := newDispatchHarness(t)
	enableWorkerEnvironment(t, h)
	worker, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = worker.Run(ctx)
	})
	h.session, err = worker.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{
		Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(),
		Configuration: json.RawMessage(`{"agent":{"model":"test-model"},"environment":{"type":"self_hosted","workspace_directory":"/remote"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, worker
}

func submitEnvironmentAdmission(ctx context.Context, h *dispatchHarness, worker *execution.Worker, key string) <-chan environmentAdmissionResult {
	result := make(chan environmentAdmissionResult, 1)
	go func() {
		receipts, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, key, environmentAdmissionInputs())
		result <- environmentAdmissionResult{receipts, err}
	}()
	return result
}

func environmentAdmissionInputs() []store.Input {
	return []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}, {Kind: "message", Payload: json.RawMessage(`{"text":"second"}`)}}
}

func awaitEnvironmentAdmission(t *testing.T, result <-chan environmentAdmissionResult) environmentAdmissionResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("input waiter did not settle")
		return environmentAdmissionResult{}
	}
}

func environmentAdmissionPending(t *testing.T, h *dispatchHarness, key string) store.EnvironmentInputReservation {
	t.Helper()
	_, pool := store.NewTestStore(t)
	var id string
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "input reservation", func() bool {
		return pool.QueryRow(t.Context(), "SELECT id::text FROM environment_input_reservations WHERE session_id=$1 AND idempotency_key=$2", h.session.ID, key).Scan(&id) == nil
	})
	pending, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func TestEnvironmentAdmissionWaitsForPreparedClaimAndRetainsRetry(t *testing.T) {
	h, worker := newEnvironmentAdmission(t)
	first := submitEnvironmentAdmission(t.Context(), h, worker, "wait")
	retry := submitEnvironmentAdmission(t.Context(), h, worker, "wait")
	pending := environmentAdmissionPending(t, h, "wait")
	for _, result := range []<-chan environmentAdmissionResult{first, retry} {
		select {
		case got := <-result:
			t.Fatal("reservation returned before admission", got)
		default:
		}
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity == nil || session.EnvironmentInputActivity.Status != "requires_action" {
		t.Fatal("waiting activity", session, err)
	}
	inputs := environmentAdmissionInputs()
	if _, err = worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "other", inputs); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal("competing batch", err)
	}
	if _, err = worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "wait", inputs[:1]); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatal("changed retry", err)
	}
	if _, err = worker.SubmitInputs(t.Context(), uuid.NewString(), h.session.ID, "wait", inputs); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign tenant", err)
	}
	mixed := append(inputs, store.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
	if _, err = worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "mixed", mixed); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("mixed batch accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	})
	frame := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, frame.ID)
	select {
	case got := <-first:
		t.Fatal("preparing is not admission", got)
	default:
	}
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	for _, result := range []<-chan environmentAdmissionResult{first, retry} {
		got := awaitEnvironmentAdmission(t, result)
		if got.err != nil || len(got.receipts) != 2 || got.receipts[0].TurnID != start.RunID {
			t.Fatal("admission result", got)
		}
	}
	if _, err = worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "active", inputs); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal("active steering accepted", err)
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "done", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "admitted-native"}})
	run := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
	if run.Turn.Status != store.TurnCompleted {
		t.Fatal("completion", run.Turn)
	}
	replay, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "wait", inputs)
	if err != nil || len(replay) != 2 || !replay[0].Replayed || replay[0].TurnID != start.RunID {
		t.Fatal("terminal retry", replay, err)
	}
}

func TestEnvironmentAdmissionSettlementDoesNotCreateTurn(t *testing.T) {
	for _, name := range []string{"expired", "cancelled", "deleted", "disconnected", "ownership_lost"} {
		t.Run(name, func(t *testing.T) {
			h, worker := newEnvironmentAdmission(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			response := submitEnvironmentAdmission(ctx, h, worker, "waiting")
			pending := environmentAdmissionPending(t, h, "waiting")
			_, pool := store.NewTestStore(t)
			expected := execution.ErrEnvironmentInputExpired
			switch name {
			case "expired":
				if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				expected = execution.ErrEnvironmentInputCancelled
				if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				expected = store.ErrNotFound
				if err := h.s.DeleteSession(t.Context(), h.tenant, h.session.ID); err != nil {
					t.Fatal(err)
				}
			case "disconnected":
				expected = context.Canceled
				cancel()
			case "ownership_lost":
				expected = execution.ErrExecutionUnavailable
				var killed bool
				err := pool.QueryRow(t.Context(), `SELECT pg_terminate_backend(pid,1000) FROM pg_locks WHERE locktype='advisory'
      AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
      AND classid=(706172736172::bigint >> 32)::oid
      AND objid=(706172736172::bigint & 4294967295)::oid AND objsubid=1 AND granted`).Scan(&killed)
				if err != nil || !killed {
					t.Fatal("owned lease termination", err)
				}
			}
			got := awaitEnvironmentAdmission(t, response)
			if !errors.Is(got.err, expected) || len(got.receipts) != 0 {
				t.Fatal("settlement", got, expected)
			}
			var turns, inputs int
			if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM turns WHERE session_id=$1),(SELECT count(*) FROM turn_inputs WHERE session_id=$1)", h.session.ID).Scan(&turns, &inputs); err != nil {
				t.Fatal(err)
			}
			if turns != 0 || inputs != 0 {
				t.Fatal("settlement created work", turns, inputs)
			}
			if name == "deleted" {
				return
			}
			retained, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, pending.ID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "disconnected" || name == "ownership_lost" {
				if retained.State != store.EnvironmentInputPending || !retained.Deadline.Equal(pending.Deadline) {
					t.Fatal("observer changed durable outcome", retained)
				}
			} else {
				replay, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "waiting", environmentAdmissionInputs())
				if !errors.Is(err, expected) || len(replay) != 0 {
					t.Fatal("settled retry", replay, err)
				}
			}
		})
	}
}
