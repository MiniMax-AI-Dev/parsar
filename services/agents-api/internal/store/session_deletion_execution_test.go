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

func TestDeletedSessionWaitingTurnSettlesWithoutStoppingWorker(t *testing.T) {
	h := newFunctionHarness(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	input := h.message("start", "Run")
	h.read(proto.TypePromptRequest)
	h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "pending", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
	state := functionState(t, h, 1)
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); err != nil {
		t.Fatal(err)
	}
	var request proto.PromptCancelPayload
	if err := h.read(proto.TypePromptCancel).DecodePayload(&request); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(store.FunctionResultInput{TurnID: input.TurnID, CallID: state.RequiredActions[0].CallID, Result: json.RawMessage(`{"success":true,"output":"late"}`)})
	if _, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "late", []store.Input{{Kind: "tool_result", Payload: raw}}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "deleted-native"}}})
	waitTurn(t, h, input.TurnID, store.TurnCancelled)
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "deleted-native" {
		t.Fatal(bound, err)
	}
	if _, err := h.s.GetSession(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := h.d.Run(ctx, h.tenant, h.session.ID, input.TurnID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted Session must be unavailable to new dispatch", err)
	}
	h.session = publicSession(t, h, "unrelated")
	next := h.message("next", "Unrelated work")
	h.read(proto.TypePromptRequest)
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{Content: "unaffected"})
	waitTurn(t, h, next.TurnID, store.TurnCompleted)
}

func TestDeletedSessionRestartStillReconcilesHiddenClaim(t *testing.T) {
	h := newDispatchHarness(t)
	input := h.message("interrupted", "Run")
	ctx := t.Context()
	if _, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, input.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.DeleteSession(ctx, h.tenant, h.session.ID); err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.Run(stopped); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	turn, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != store.TurnFailed {
		t.Fatal(turn, err)
	}
	if _, err := h.s.GetSession(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
}
