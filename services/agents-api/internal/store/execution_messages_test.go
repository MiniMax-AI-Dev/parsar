package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionNegotiatesAndPersistsMessageObservations(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	first := h.message("legacy", "legacy observation policy")
	result := h.run(ctx, first.TurnID)
	var request proto.PromptRequestPayload
	env := h.read(proto.TypePromptRequest)
	_ = env.DecodePayload(&request)
	if request.ObserveMessages {
		t.Fatal("unadvertised observation capability requested")
	}
	h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
	h.finished(result, store.TurnCompleted)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, Resume: true, DurableTurns: true, WebSearchControl: true, MessageItems: true}}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			t.Fatal(err)
		}
		info, _, _ := peer.AgentKindStatus("codex")
		if info.Capabilities.MessageItems {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("message capability lost in gateway")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := h.message("observed", "stream separate messages")
	result = h.run(ctx, input.TurnID)
	env = h.read(proto.TypePromptRequest)
	_ = env.DecodePayload(&request)
	if !request.ObserveMessages {
		t.Fatal("advertised capability was not requested")
	}
	text := "complete"
	h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "a", Status: "in_progress", Phase: "commentary"})
	h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{ItemID: "a", Delta: text, Sequence: 1})
	h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "a", Status: "completed", Phase: "commentary", Text: &text})
	h.write(input.TurnID, proto.TypeOutputMessage, proto.OutputMessagePayload{ID: "b", Status: "in_progress", Phase: "final_answer"})
	h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{ItemID: "b", Delta: "partial", Sequence: 2})
	if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	env = h.read(proto.TypePromptCancel)
	var cancel proto.PromptCancelPayload
	_ = env.DecodePayload(&cancel)
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: &proto.DonePayload{}})
	h.finished(result, store.TurnCancelled)
	reopened, pool := store.NewTestStore(t)
	defer pool.Close()
	events, err := reopened.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 {
		t.Fatalf("lost message observations: %+v", events)
	}
	var complete proto.OutputMessagePayload
	if err = json.Unmarshal(events[2].Payload, &complete); err != nil || complete.ID != "a" || complete.Text == nil || *complete.Text != "complete" || complete.Phase != "commentary" {
		t.Fatalf("completed snapshot: %+v %v", complete, err)
	}
	var delta proto.DeltaPayload
	if err = json.Unmarshal(events[4].Payload, &delta); err != nil || delta.ItemID != "b" || delta.Delta != "partial" {
		t.Fatalf("cancelled partial identity lost: %+v %v", delta, err)
	}
	if events[5].Kind != "cancel_receipt" || events[6].Kind != "execution_cancelled" {
		t.Fatal("terminal ordering changed")
	}
}
