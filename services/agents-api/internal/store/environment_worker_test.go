package store_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestWorkerEnvironmentSharesCapacityThroughClaimAndCleanup(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	released := enableWorkerEnvironment(t, h)
	frames := workerFrames(t, h)
	pending := map[string]store.EnvironmentInputReservation{}
	for range 2 {
		value := workerEnvironmentReservation(t, h)
		pending[value.SessionID] = value
	}
	ordinary := map[string]store.Session{}
	for _, key := range []string{"one", "two", "three"} {
		session := publicSession(t, h, key)
		h.session = session
		receipt := h.message(key, "ordinary")
		ordinary[receipt.TurnID] = session
	}
	_, stop := startEnvironmentExpiryWorker(t, h.d)
	var normal []proto.Envelope
	var preparing []proto.Envelope
	for range 4 {
		select {
		case frame := <-frames:
			switch frame.Type {
			case proto.TypePromptRequest:
				normal = append(normal, frame)
			case proto.TypeExecutionPrepare:
				preparing = append(preparing, frame)
			default:
				t.Fatal("unexpected initial frame", frame.Type)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("mixed queues did not fill capacity")
		}
	}
	if len(normal) != 2 || len(preparing) != 2 {
		t.Fatal("mixed queues did not share capacity", len(normal), len(preparing))
	}
	first := preparing[0]
	handle := acknowledgePreparation(h, first.ID)
	h.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	frame := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.ID != first.ID || frame.DecodePayload(&start) != nil || start.Handle != handle || start.Prompt != "first" {
		t.Fatal("worker changed preparation at Start")
	}
	h.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	second := preparing[1]
	secondHandle := acknowledgePreparation(h, second.ID)
	var prepare proto.ExecutionPreparePayload
	if second.DecodePayload(&prepare) != nil {
		t.Fatal("invalid Prepare")
	}
	waiting := pending[strings.TrimPrefix(prepare.Configuration.AgentStateKey, "agents-api-")]
	if waiting.ID == "" {
		t.Fatal("wrong waiting Session")
	}
	select {
	case frame := <-frames:
		t.Fatal("claim released a slot or duplicated active preparation", frame.Type)
	case <-time.After(time.Second):
	}
	tenant, due := newEnvironmentExpiryReservation(t, h.s)
	makeEnvironmentExpiryDue(t, pool, &due)
	waitEnvironmentExpiry(t, h.s, tenant, due)
	if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, waiting.SessionID, waiting.ID); err != nil {
		t.Fatal(err)
	}
	release := nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	var payload proto.ExecutionReleasePayload
	if release.ID != second.ID || release.DecodePayload(&payload) != nil || payload.Handle != secondHandle {
		t.Fatal("cancel released the wrong preparation")
	}
	normal = append(normal, nextWorkerFrame(t, frames, proto.TypePromptRequest))
	if released.Load() != 1 {
		t.Fatal("next job preceded preparation cleanup")
	}
	for _, request := range normal {
		h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "ordinary complete"})
		h.session = ordinary[request.ID]
		waitTurn(t, h, request.ID, store.TurnCompleted)
	}
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "remote complete"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
	if released.Load() != 2 {
		t.Fatal("connection owners were not released")
	}
	assertEnvironmentExpiryHasNoHistory(t, pool, waiting.SessionID)
	assertEnvironmentExpiryHasNoHistory(t, pool, due.SessionID)
}

func TestWorkerEnvironmentRetriesPendingWithoutExtendingDeadline(t *testing.T) {
	h := newDispatchHarness(t)
	released := enableWorkerEnvironment(t, h)
	frames := workerFrames(t, h)
	pending := workerEnvironmentReservation(t, h)
	_, stop := startEnvironmentExpiryWorker(t, h.d)
	first := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	started := time.Now()
	handle := acknowledgePreparation(h, first.ID)
	h.write(first.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	h.session = publicSession(t, h, "unrelated")
	receipt := h.message("ordinary", "make progress after preparation failure")
	request := nextWorkerFrame(t, frames, proto.TypePromptRequest)
	if request.ID != receipt.TurnID {
		t.Fatal("preparation failure blocked ordinary work")
	}
	h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "complete"})
	waitTurn(t, h, request.ID, store.TurnCompleted)
	second := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	if time.Since(started) < 4*time.Second || first.ID == second.ID || released.Load() != 1 {
		t.Fatal("pending preparation retried rapidly or reused a released owner")
	}
	acknowledgePreparation(h, second.ID)
	select {
	case frame := <-frames:
		t.Fatal("active preparation was duplicated", frame.Type)
	case <-time.After(time.Second):
	}
	stop()
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, pending.SessionID, pending.ID)
	if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(pending.Deadline) || len(stored.Receipts) != 0 {
		t.Fatal("retry or shutdown changed the original reservation", stored, err)
	}
	_, stop = startEnvironmentExpiryWorker(t, h.d)
	third := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
	handle = acknowledgePreparation(h, third.ID)
	h.write(third.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	request = nextWorkerFrame(t, frames, proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if request.ID != third.ID || json.Unmarshal(request.Payload, &start) != nil || start.Handle != handle {
		t.Fatal("restart changed retained preparation")
	}
	h.write(third.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "resumed"})
	run := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
	if run.Turn.Status != store.TurnCompleted || !run.Reservation.Deadline.Equal(pending.Deadline) {
		t.Fatal("restarted worker did not complete original work", run)
	}
	nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
	stop()
	if released.Load() != 3 {
		t.Fatal("worker leaked a preparation owner")
	}
}
