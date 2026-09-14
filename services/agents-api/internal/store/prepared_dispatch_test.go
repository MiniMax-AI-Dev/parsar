package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type preparedDispatchResult struct {
	run execution.EnvironmentRun
	err error
}

func preparedDispatchHarness(t *testing.T) (*dispatchHarness, store.EnvironmentInputReservation, *atomic.Int32) {
	t.Helper()
	h := newDispatchHarness(t)
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "prepared", Configuration: json.RawMessage(`{"agent":{"model":"test-model","instructions":"Keep this instruction."},"environment":{"type":"self_hosted","workspace_directory":"/executor-workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := h.s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close(context.Background()) })
	h.d.Store = lease.Store()
	peer, err := h.registry.LookupDevice(h.device.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: true, SubagentControl: true, ToolObservations: true, Preparation: true, RemoteEnvironment: true}}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "preparation capability", func() bool {
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.Preparation && info.Capabilities.RemoteEnvironment
	})
	released := &atomic.Int32{}
	h.d.EnvironmentConnection = func(owner context.Context, session store.Session, environment store.Environment) (execution.EnvironmentConnection, error) {
		if owner.Err() != nil || environment.TenantID != h.tenant || environment.SessionID != session.ID || session.ID != h.session.ID {
			return execution.EnvironmentConnection{}, errors.New("incorrect connection owner")
		}
		var once sync.Once
		return execution.EnvironmentConnection{URL: "http://private-registry.test", Token: "synthetic-connection-token", Release: func() { once.Do(func() { released.Add(1) }) }}, nil
	}
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "pending", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}, {Kind: "message", Payload: json.RawMessage(`{"text":"second"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	return h, pending, released
}

func runPreparedDispatch(h *dispatchHarness, ctx context.Context, pending store.EnvironmentInputReservation) <-chan preparedDispatchResult {
	out := make(chan preparedDispatchResult, 1)
	go func() {
		result, err := h.d.RunEnvironmentInput(ctx, h.tenant, h.session.ID, pending.ID)
		out <- preparedDispatchResult{result, err}
	}()
	return out
}

func awaitPreparedDispatch(t *testing.T, result <-chan preparedDispatchResult) preparedDispatchResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("prepared dispatcher did not finish")
		return preparedDispatchResult{}
	}
}

func acknowledgePreparation(h *dispatchHarness, request string) string {
	handle := uuid.NewString()
	h.write(request, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 1, State: "preparing", ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli()})
	return handle
}

func readyPreparedDispatch(t *testing.T, h *dispatchHarness, request, handle string) proto.ExecutionStartPayload {
	t.Helper()
	h.write(request, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready", ExpiresAt: time.Now().Add(5 * time.Minute).UnixMilli()})
	frame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.ID != request || frame.DecodePayload(&start) != nil || start.Handle != handle || start.RunID == "" || start.Prompt != "first\n\nsecond" {
		t.Fatal("Start changed preparation or original batch", frame.ID, start)
	}
	turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
	if err != nil || turn.Status != store.TurnInProgress {
		t.Fatal("Start preceded atomic claim", turn, err)
	}
	return start
}

func TestPreparedDispatchPromotesOriginalBatchAndPersistsCompletion(t *testing.T) {
	h, pending, released := preparedDispatchHarness(t)
	result := runPreparedDispatch(h, t.Context(), pending)
	frame := h.read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || prepare.Configuration.Prompt != "" || prepare.Configuration.RunID != "" || prepare.Configuration.ConversationID != "" || prepare.Configuration.RemoteEnvironment == nil || prepare.Configuration.RemoteEnvironment.WorkspaceDirectory != "/executor-workspace" || prepare.Configuration.DisableExecutionEnvironment {
		t.Fatal("invalid preparation configuration", prepare)
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("preparation created work before readiness", session, err)
	}
	items, err := h.s.ListItems(t.Context(), h.tenant, h.session.ID, "", 100, true)
	if err != nil || len(items.Items) != 0 {
		t.Fatal("preparation published input history", items, err)
	}
	handle := acknowledgePreparation(h, frame.ID)
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	late := h.message("later", "third")
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	steering := h.read(proto.TypePromptSteer)
	var steer proto.PromptSteerPayload
	if steering.ID != start.RunID || steering.DecodePayload(&steer) != nil || steer.Text != "third" || !steer.DurableReceipt {
		t.Fatal("later input bypassed ordinary steering", steer)
	}
	h.write(start.RunID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: steer.InputID, Accepted: true})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "answer", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "retained-prepared-native"}})
	got := awaitPreparedDispatch(t, result)
	if got.err != nil || got.run.Turn.Status != store.TurnCompleted || len(got.run.Reservation.Receipts) != 2 || got.run.Reservation.Receipts[0].Replayed || got.run.Reservation.Receipts[1].Sequence >= late.Sequence || released.Load() != 1 {
		t.Fatal("prepared completion", got, released.Load())
	}
	bound, err := h.s.GetSessionDevice(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "retained-prepared-native" {
		t.Fatal("native identity was not committed", bound, err)
	}
	h.d.EnvironmentConnection = func(context.Context, store.Session, store.Environment) (execution.EnvironmentConnection, error) {
		t.Error("replay resolved another native connection")
		return execution.EnvironmentConnection{}, errors.New("unexpected replay")
	}
	retry, err := h.d.RunEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || len(retry.Reservation.Receipts) != 2 || !retry.Reservation.Receipts[0].Replayed || retry.Reservation.Receipts[0].TurnID != start.RunID || retry.Turn.ID != "" {
		t.Fatal("replay executed again", retry, err)
	}
}

func TestPreparedDispatchOwnerOutlivesReservationDeadline(t *testing.T) {
	h, pending, released := preparedDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	connection := h.d.EnvironmentConnection
	owners := make(chan context.Context, 1)
	h.d.EnvironmentConnection = func(owner context.Context, session store.Session, environment store.Environment) (execution.EnvironmentConnection, error) {
		owners <- owner
		return connection(owner, session, environment)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := runPreparedDispatch(h, parent, pending)
	frame := h.read(proto.TypeExecutionPrepare)
	owner := <-owners
	if _, ok := owner.Deadline(); ok {
		t.Fatal("reservation deadline was imposed on the execution owner")
	}
	handle := acknowledgePreparation(h, frame.ID)
	start := readyPreparedDispatch(t, h, frame.ID, handle)
	if _, err := pool.Exec(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE id=$1", pending.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := h.d.Store.ExpireEnvironmentInput(t.Context(), h.tenant, h.session.ID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputAdmitted || owner.Err() != nil || released.Load() != 0 {
		t.Fatal("admitted execution lost its owner to the pending-input deadline", err)
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "completed after the reservation deadline"})
	got := awaitPreparedDispatch(t, result)
	if got.err != nil || got.run.Turn.Status != store.TurnCompleted || owner.Err() == nil || released.Load() != 1 {
		t.Fatal("completion did not settle and release the execution owner", got, released.Load())
	}
}
