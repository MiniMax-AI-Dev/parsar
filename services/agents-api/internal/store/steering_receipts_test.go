package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionDurableInputReceiptLifetime(t *testing.T) {
	for _, mode := range []string{"delayed-acceptance", "missing-at-done", "retry-after-write", "cancel-unknown-first"} {
		t.Run(mode, func(t *testing.T) {
			h := newDispatchHarness(t)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			first := h.message("first", "initial")
			result := h.run(ctx, first.TurnID)
			h.read(proto.TypePromptRequest)
			extra := h.message("extra", "additional")
			var input proto.PromptSteerPayload
			if err := h.read(proto.TypePromptSteer).DecodePayload(&input); err != nil || !input.DurableReceipt {
				t.Fatal("durable receipt was not requested", err)
			}
			h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, Written: true})
			status := store.TurnFailed
			switch mode {
			case "delayed-acceptance":
				select {
				case got := <-result:
					t.Fatalf("write phase ended execution: %+v", got)
				case <-time.After(31 * time.Second):
				}
				h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, Accepted: true})
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Content: "completed"})
				status = store.TurnCompleted
			case "missing-at-done":
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Content: "unconfirmed input"})
			case "retry-after-write":
				h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, ErrorCode: "not_ready"})
			case "cancel-unknown-first":
				if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "cancel"); err != nil {
					t.Fatal(err)
				}
				var request proto.PromptCancelPayload
				if err := h.read(proto.TypePromptCancel).DecodePayload(&request); err != nil {
					t.Fatal(err)
				}
				h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, ErrorCode: "outcome_unknown"})
				select {
				case got := <-result:
					t.Fatalf("input preempted cancellation: %+v", got)
				case <-time.After(300 * time.Millisecond):
				}
				h.write(first.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, Applied: true, Outcome: &proto.DonePayload{Content: "partial"}})
				status = store.TurnCancelled
			}
			done := h.finished(result, status)
			var outcome execution.Result
			if err := json.Unmarshal(done.Outcome, &outcome); err != nil {
				t.Fatal(err)
			}
			want := first.Sequence
			if mode == "delayed-acceptance" {
				want = extra.Sequence
			}
			if outcome.AppliedThrough != want {
				t.Fatalf("unconfirmed cursor advancement: %+v", outcome)
			}
			if mode == "missing-at-done" && outcome.ErrorCode != "input_outcome_unknown" {
				t.Fatalf("missing receipt accepted: %+v", outcome)
			}
		})
	}
}
