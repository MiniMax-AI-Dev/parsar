package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionNegotiatesAndPersistsToolObservations(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	first := h.message("legacy", "legacy observation policy")
	result := h.run(ctx, first.TurnID)
	var request proto.PromptRequestPayload
	env := h.read(proto.TypePromptRequest)
	_ = env.DecodePayload(&request)
	if request.ObserveTools {
		t.Fatal("unadvertised observation capability requested")
	}
	h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
	h.finished(result, store.TurnCompleted)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, Resume: true, DurableTurns: true, WebSearchControl: true, TextVerbosity: true, SubagentControl: true, ToolItems: true}}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			t.Fatal(err)
		}
		info, _, _ := peer.AgentKindStatus("codex")
		if info.Capabilities.ToolItems {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("tool capability lost in gateway")
		}
		time.Sleep(10 * time.Millisecond)
	}
	input := h.message("observed", "run tools")
	result = h.run(ctx, input.TurnID)
	env = h.read(proto.TypePromptRequest)
	_ = env.DecodePayload(&request)
	if !request.ObserveTools || request.ObserveMessages {
		t.Fatal("advertised capability was not requested")
	}
	start := json.RawMessage(`{"type":"mcpToolCall","id":"a","server":"reference","tool":"lookup","arguments":{"key":"value"},"status":"inProgress","result":null,"error":null}`)
	complete := json.RawMessage(`{"type":"mcpToolCall","id":"a","server":"reference","tool":"lookup","arguments":{"key":"value"},"status":"completed","result":{"content":[{"type":"text","text":"answer"}],"structuredContent":{"version":9007199254740993}},"error":null}`)
	partial := json.RawMessage(`{"type":"commandExecution","id":"b","command":"long-running","status":"inProgress","aggregatedOutput":null,"exitCode":null}`)
	for i, raw := range []json.RawMessage{start, complete, partial} {
		id, stage := "a", "before"
		if i == 1 {
			stage = "after"
		}
		if i == 2 {
			id = "b"
		}
		h.write(input.TurnID, proto.TypeToolCall, proto.ToolCallPayload{ID: id, Stage: stage, NativeItem: raw})
	}
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
	if len(events) != 5 {
		t.Fatalf("lost tool observations: %+v", events)
	}
	for i, expected := range []json.RawMessage{start, complete, partial} {
		var tool proto.ToolCallPayload
		if err = json.Unmarshal(events[i].Payload, &tool); err != nil {
			t.Fatal(err)
		}
		// PostgreSQL canonicalizes object order; compare JSON values without floating-point coercion.
		var actualValue, expectedValue any
		decode := func(raw []byte, target *any) {
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if e := d.Decode(target); e != nil {
				t.Fatal(e)
			}
		}
		decode(tool.NativeItem, &actualValue)
		decode(expected, &expectedValue)
		if !reflect.DeepEqual(actualValue, expectedValue) {
			t.Fatalf("tool snapshot changed: %s", tool.NativeItem)
		}
		if i == 2 && tool.Stage != "before" {
			t.Fatal("unfinished call acquired a completion")
		}
	}
	if events[3].Kind != "cancel_receipt" || events[4].Kind != "execution_cancelled" {
		t.Fatal("terminal ordering changed")
	}
}
