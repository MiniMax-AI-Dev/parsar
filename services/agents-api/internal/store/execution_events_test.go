package store_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionPersistsLiveAndCancelledPartialOutput(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	input := h.message("start", "Stream then cancel")
	result := h.run(ctx, input.TurnID)
	h.read(proto.TypePromptRequest)
	h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{Delta: "已输出", Sequence: 1})
	h.write(input.TurnID, proto.TypeToolCall, proto.ToolCallPayload{ID: "tool-1", Name: "Bash", Stage: "before", Observation: &proto.ToolObservation{Kind: "command", Command: "pwd", Status: "in_progress"}})
	h.write(input.TurnID, proto.TypeToolCall, proto.ToolCallPayload{ID: "tool-1", Name: "Bash", Stage: "after", Observation: &proto.ToolObservation{Kind: "command", Command: "pwd", Status: "completed"}})
	h.write(input.TurnID, proto.TypeUsage, proto.UsagePayload{Usage: proto.Usage{InputTokens: 11, OutputTokens: 2}})
	reopened, pool := store.NewTestStore(t)
	defer pool.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, err := reopened.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("events were not persisted during execution")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	cancelEnv := h.read(proto.TypePromptCancel)
	var cancel proto.PromptCancelPayload
	_ = cancelEnv.DecodePayload(&cancel)
	for i := range 30 {
		h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{Delta: "片段", Sequence: uint64(i + 2)})
	}
	h.write(input.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: &proto.DonePayload{}})
	h.finished(result, store.TurnCancelled)
	events, err := reopened.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var kinds []string
	for i, event := range events {
		if event.Ordinal != int32(i+1) {
			t.Fatal("non-contiguous events")
		}
		kinds = append(kinds, event.Kind)
		if event.Kind == proto.TypeDelta {
			var delta proto.DeltaPayload
			_ = json.Unmarshal(event.Payload, &delta)
			text.WriteString(delta.Delta)
		}
	}
	if text.String() != "已输出"+strings.Repeat("片段", 30) {
		t.Fatalf("partial text lost: %q", text.String())
	}
	if len(events) != 36 || kinds[34] != "cancel_receipt" || kinds[35] != "execution_cancelled" {
		t.Fatalf("event order=%v", kinds)
	}
}

func TestExecutionDoesNotCompleteAfterEventPersistenceFailure(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	input := h.message("start", "Output beyond storage budget")
	result := h.run(ctx, input.TurnID)
	h.read(proto.TypePromptRequest)
	_, pool := store.NewTestStore(t)
	defer pool.Close()
	if _, err := pool.Exec(ctx, "UPDATE turns SET event_bytes=33554432 WHERE id=$1", input.TurnID); err != nil {
		t.Fatal(err)
	}
	h.write(input.TurnID, proto.TypeDelta, proto.DeltaPayload{Delta: "cannot be stored", Sequence: 1})
	h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "Do not report success", Usage: proto.Usage{InputTokens: 13}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "failed-native"}})
	turn := h.finished(result, store.TurnFailed)
	var outcome execution.Result
	_ = json.Unmarshal(turn.Outcome, &outcome)
	if outcome.ErrorCode != "event_persistence_failed" {
		t.Fatal(outcome.ErrorCode)
	}
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "failed-native" || outcome.Done.Usage.InputTokens != 13 {
		t.Fatalf("terminal failure lost native continuity or usage: %+v %+v %v", bound, outcome, err)
	}
	events, err := h.s.ListTurnEvents(ctx, h.tenant, h.session.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 1 || events[0].Kind != "execution_failed" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}
