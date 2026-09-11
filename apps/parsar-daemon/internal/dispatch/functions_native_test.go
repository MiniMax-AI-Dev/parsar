package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type nativeFunctionSender chan proto.Envelope

func (s nativeFunctionSender) Send(ctx context.Context, e proto.Envelope) error {
	select {
	case s <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNativeFunctionBridge(t *testing.T) {
	root := os.Getenv("PARSAR_NATIVE_PROOF_DIR")
	if root == "" {
		t.Skip("explicit native Codex binary and proof directory required")
	}
	home, err := os.MkdirTemp(root, "daemon-functions-")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARSAR_HOME", home)
	var count atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := count.Add(1)
		raw, _ := json.MarshalIndent(body, "", "  ")
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("request-%d.json", n)), raw, 0600)
		var item map[string]any
		if n%2 == 1 {
			if !strings.Contains(string(raw), "lookup_ticket") {
				t.Error("tool was not registered")
			}
			item = map[string]any{"id": fmt.Sprintf("fc_%d", n), "type": "function_call", "call_id": fmt.Sprintf("call_%d", n), "name": "lookup_ticket", "arguments": `{"ticket":"42"}`, "status": "completed"}
		} else {
			if !strings.Contains(string(raw), "TICKET-RESULT") {
				t.Error("native model did not receive function result")
			}
			item = map[string]any{"id": fmt.Sprintf("msg_%d", n), "type": "message", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "FUNCTION-OK", "annotations": []any{}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(kind string, data map[string]any) {
			data["type"] = kind
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
			w.(http.Flusher).Flush()
		}
		send("response.created", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "status": "in_progress", "output": []any{}}})
		send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		send("response.completed", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "gpt-5.5", "output": []any{item}}})
	}))
	defer model.Close()
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{FunctionTools: true}}, codex.Factory)
	sender := make(nativeFunctionSender, 256)
	router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	await := func(kind string) proto.Envelope {
		t.Helper()
		for {
			select {
			case env := <-sender:
				if env.Type == proto.TypeError {
					t.Fatalf("native error: %s", env.Payload)
				}
				if env.Type == kind {
					return env
				}
			case <-ctx.Done():
				t.Fatalf("waiting for %s; evidence %s", kind, home)
			}
		}
	}
	nativeID := ""
	for index := 0; index < 3; index++ {
		run := fmt.Sprintf("run-%d", index)
		request := proto.PromptRequestPayload{AgentKind: "codex", Prompt: "Look up ticket 42.", RunID: run, AgentStateKey: "native-functions", AgentSessionID: nativeID, StrictResume: true, ReleaseOnCompletion: true, DisableExecutionEnvironment: true, ObserveTools: true,
			FunctionTools: []proto.FunctionTool{{Name: "lookup_ticket", Description: "Read a synthetic ticket", Parameters: json.RawMessage(`{"type":"object","properties":{"ticket":{"type":"string"}},"required":["ticket"],"additionalProperties":false}`)}},
			AgentOptions:  map[string]any{"model": "gpt-5.5", "codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-local-token"}}}
		env, _ := proto.NewEnvelope(proto.TypePromptRequest, run, request)
		if err := router.Handle(ctx, env); err != nil {
			t.Fatal(err)
		}
		call := await(proto.TypeFunctionCall)
		var payload proto.FunctionCallPayload
		if err := call.DecodePayload(&payload); err != nil || call.ID != run || payload.Name != "lookup_ticket" {
			t.Fatal(call, err)
		}
		if index == 2 {
			cancelFrame, _ := proto.NewEnvelope(proto.TypePromptCancel, run, proto.PromptCancelPayload{DeliveryID: "cancel"})
			if err := router.Handle(ctx, cancelFrame); err != nil {
				t.Fatal(err)
			}
			ack := await(proto.TypeInteractionDecisionAck)
			var receipt proto.InteractionDecisionAckPayload
			_ = ack.DecodePayload(&receipt)
			if !receipt.Applied {
				t.Fatal(receipt)
			}
			late, _ := proto.NewEnvelope(proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: payload.CallID, Success: true, Text: "late", DeliveryID: "late"})
			if err := router.Handle(ctx, late); err != nil {
				t.Fatal(err)
			}
			_ = await(proto.TypeInteractionDecisionAck).DecodePayload(&receipt)
			if receipt.Applied || receipt.ErrorCode != "not_pending" {
				t.Fatal(receipt)
			}
			break
		}
		result, _ := proto.NewEnvelope(proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: payload.CallID, Success: index == 0, Text: "TICKET-RESULT", DeliveryID: "result"})
		if err := router.Handle(ctx, result); err != nil {
			t.Fatal(err)
		}
		ack := await(proto.TypeInteractionDecisionAck)
		var receipt proto.InteractionDecisionAckPayload
		_ = ack.DecodePayload(&receipt)
		if !receipt.Applied {
			t.Fatal(receipt)
		}
		done := await(proto.TypeDone)
		var output proto.DonePayload
		_ = done.DecodePayload(&output)
		id, _ := output.Metadata[proto.DoneMetaAgentSessionID].(string)
		if output.Content != "FUNCTION-OK" || id == "" || (nativeID != "" && id != nativeID) {
			t.Fatal(output)
		}
		nativeID = id
	}
	t.Logf("Native function success/failure, fresh-process resume, cancellation and late-result rejection passed; evidence %s", home)
}
