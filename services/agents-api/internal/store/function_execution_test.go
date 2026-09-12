package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const functionConfiguration = `{"agent":{"model":"gpt-5.5","tools":[{"type":"function","name":"lookup_ticket","description":"Read a synthetic ticket","parameters":{"type":"object","properties":{"ticket":{"type":"string"}},"required":["ticket"],"additionalProperties":false},"defer_loading":false}]},"environment":{"type":"none"}}`

func newFunctionHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	h := newDispatchHarness(t)
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "functions", Configuration: json.RawMessage(functionConfiguration)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, Resume: true, DurableTurns: true, WebSearchControl: true, TextVerbosity: true, EnvironmentNone: true, FunctionTools: true}}}})
	deadline := time.Now().Add(time.Second)
	for {
		peer, _ := h.registry.LookupDevice(h.device.ID)
		info, _, _ := peer.AgentKindStatus("codex")
		if info.Capabilities.FunctionTools {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatal("function heartbeat missing")
		}
		time.Sleep(time.Millisecond)
	}
}

func functionState(t *testing.T, h *dispatchHarness, count int) store.Session {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		state, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(state.RequiredActions) == count {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %d function actions: %+v", count, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecutionFunctionsWaitForEveryApplicationReceipt(t *testing.T) {
	h := newFunctionHarness(t)
	input := h.message("start", "Run functions")
	result := h.run(t.Context(), input.TurnID)
	var prompt proto.PromptRequestPayload
	_ = h.read(proto.TypePromptRequest).DecodePayload(&prompt)
	if len(prompt.FunctionTools) != 1 || prompt.FunctionTools[0].Name != "lookup_ticket" {
		t.Fatal(prompt.FunctionTools)
	}
	for _, id := range []string{"a", "b"} {
		for range 2 {
			h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: id, Name: "lookup_ticket", Arguments: json.RawMessage(`{"ticket":"42"}`)})
		}
	}
	state := functionState(t, h, 2)
	if state.LastTurn.Status != store.TurnWaiting {
		t.Fatal(state.LastTurn)
	}
	for _, id := range []string{"a", "b"} {
		public := items.Identity(input.TurnID, "tool:"+id)
		if err := h.s.SubmitFunctionResult(t.Context(), h.tenant, h.session.ID, input.TurnID, public, json.RawMessage(`{"success":true,"output":"saved"}`)); err != nil {
			t.Fatal(err)
		}
	}
	for index := range 2 {
		var reply proto.FunctionResultPayload
		_ = h.read(proto.TypeFunctionResult).DecodePayload(&reply)
		public := items.Identity(input.TurnID, "tool:"+reply.CallID)
		saved, err := h.s.GetFunctionCall(t.Context(), h.tenant, h.session.ID, input.TurnID, public)
		if err != nil || saved.Applied || reply.DeliveryID != "function:"+public || len(reply.Content) != 1 || *reply.Content[0].Text != "saved" {
			t.Fatal(saved, reply, err)
		}
		if index == 1 {
			h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-functions"}})
			select {
			case got := <-result:
				t.Fatal("Done bypassed outstanding receipt", got)
			case <-time.After(40 * time.Millisecond):
			}
		}
		for range 2 {
			h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: reply.DeliveryID, Applied: true})
		}
	}
	h.finished(result, store.TurnCompleted)
	functionState(t, h, 0)
	next := h.message("next", "Resume")
	result = h.run(t.Context(), next.TurnID)
	_ = h.read(proto.TypePromptRequest).DecodePayload(&prompt)
	if prompt.AgentSessionID != "native-functions" || len(prompt.FunctionTools) != 1 {
		t.Fatal(prompt)
	}
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{Content: "resumed"})
	h.finished(result, store.TurnCompleted)
}

func TestExecutionFunctionsCancellationAndUnconfirmedResults(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfirmed", true: "cancel"}[cancel], func(t *testing.T) {
			h := newFunctionHarness(t)
			input := h.message("start", "Run")
			result := h.run(t.Context(), input.TurnID)
			h.read(proto.TypePromptRequest)
			h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "a", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
			state := functionState(t, h, 1)
			id := state.RequiredActions[0].CallID
			if err := h.s.SubmitFunctionResult(t.Context(), h.tenant, h.session.ID, input.TurnID, id, json.RawMessage(`{"success":false,"error":"tool failed"}`)); err != nil {
				t.Fatal(err)
			}
			var reply proto.FunctionResultPayload
			_ = h.read(proto.TypeFunctionResult).DecodePayload(&reply)
			if reply.Success || len(reply.Content) != 1 || *reply.Content[0].Text != "tool failed" {
				t.Fatal(reply)
			}
			status := store.TurnFailed
			if cancel {
				if _, err := h.s.RequestCancel(t.Context(), h.tenant, h.session.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				var request proto.PromptCancelPayload
				_ = h.read(proto.TypePromptCancel).DecodePayload(&request)
				h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: reply.DeliveryID, ErrorCode: "not_pending"})
				select {
				case got := <-result:
					t.Fatal("function rejection bypassed outstanding cancellation", got)
				case <-time.After(40 * time.Millisecond):
				}
				h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-cancelled-functions"}}})
				status = store.TurnCancelled
			} else {
				h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: reply.DeliveryID, ErrorCode: "not_pending"})
			}
			h.finished(result, status)
			if cancel {
				bound, err := h.s.GetSessionDevice(t.Context(), h.tenant, h.session.ID)
				if err != nil || bound.NativeSessionID != "native-cancelled-functions" {
					t.Fatal(bound, err)
				}
			}
			saved, err := h.s.GetFunctionCall(t.Context(), h.tenant, h.session.ID, input.TurnID, id)
			if err != nil || saved.Applied || len(saved.Result) == 0 {
				t.Fatal(saved, err)
			}
			if err := h.s.ConfirmFunctionResult(t.Context(), h.tenant, h.session.ID, input.TurnID, id); !errors.Is(err, store.ErrTurnConflict) {
				t.Fatal(err)
			}
			functionState(t, h, 0)
		})
	}
}

func TestExecutionFunctionsRejectUndeclaredCallsAndPrematureDone(t *testing.T) {
	for _, name := range []string{"undeclared", "lookup_ticket"} {
		t.Run(name, func(t *testing.T) {
			h := newFunctionHarness(t)
			input := h.message("start", "Run")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := h.run(ctx, input.TurnID)
			h.read(proto.TypePromptRequest)
			h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "a", Name: name, Arguments: json.RawMessage(`{}`)})
			h.write(input.TurnID, proto.TypeDone, proto.DonePayload{})
			h.finished(result, store.TurnFailed)
		})
	}
}

func TestExecutionFunctionsRequireAdvertisedCapability(t *testing.T) {
	h := newDispatchHarness(t)
	session, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "functions", Configuration: json.RawMessage(functionConfiguration)})
	if err != nil {
		t.Fatal(err)
	}
	h.session = session
	if err := h.s.BindSessionDevice(t.Context(), h.tenant, session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	input := h.message("start", "Run")
	result := <-h.run(t.Context(), input.TurnID)
	if result.err == nil || !strings.Contains(result.err.Error(), "function_tools") {
		t.Fatal(result)
	}
	turn, err := h.s.GetTurn(t.Context(), h.tenant, session.ID, input.TurnID)
	if err != nil || turn.Status != store.TurnQueued {
		t.Fatal(turn, err)
	}
}
