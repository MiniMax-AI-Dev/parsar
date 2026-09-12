package store_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionFunctionInputBatchStillSteersMessages(t *testing.T) {
	h := newFunctionHarness(t)
	input := h.message("start", "Run")
	running := h.run(t.Context(), input.TurnID)
	h.read(proto.TypePromptRequest)
	h.write(input.TurnID, proto.TypeFunctionCall, proto.FunctionCallPayload{CallID: "a", Name: "lookup_ticket", Arguments: json.RawMessage(`{}`)})
	state := functionState(t, h, 1)
	raw, _ := json.Marshal(store.FunctionResultInput{TurnID: input.TurnID, CallID: state.RequiredActions[0].CallID, Result: json.RawMessage(`{"success":true,"output":"answer"}`)})
	batch := []store.Input{{Kind: "tool_result", Payload: raw}, {Kind: "message", Payload: json.RawMessage(`{"text":"Follow up"}`)}}
	receipts, err := h.s.SubmitInputs(t.Context(), h.tenant, h.session.ID, "mixed", batch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.SubmitInputs(t.Context(), h.tenant, h.session.ID, "mixed", batch); err != nil {
		t.Fatal(err)
	}
	resultSeen, messageSeen := false, false
	_ = h.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !resultSeen || !messageSeen {
		var env proto.Envelope
		if err := h.conn.ReadJSON(&env); err != nil {
			t.Fatal(err)
		}
		switch env.Type {
		case proto.TypeFunctionResult:
			var result proto.FunctionResultPayload
			if env.DecodePayload(&result) != nil || resultSeen || result.CallID != "a" {
				t.Fatal(result)
			}
			resultSeen = true
			h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: result.DeliveryID, Applied: true})
		case proto.TypePromptSteer:
			var steer proto.PromptSteerPayload
			if env.DecodePayload(&steer) != nil || messageSeen || steer.Text != "Follow up" || steer.InputID != strconv.FormatInt(receipts[1].Sequence, 10) {
				t.Fatal(steer)
			}
			messageSeen = true
			h.write(input.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: steer.InputID, Accepted: true})
		}
	}
	h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
	h.finished(running, store.TurnCompleted)
	saved, err := h.s.GetFunctionCall(t.Context(), h.tenant, h.session.ID, input.TurnID, state.RequiredActions[0].CallID)
	if err != nil || !saved.Applied {
		t.Fatal(saved, err)
	}
}
