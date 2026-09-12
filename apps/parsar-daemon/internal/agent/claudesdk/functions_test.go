//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestFunctionFactoryNativeReceipts(t *testing.T) {
	for _, mode := range []string{"functions-success", "functions-wrong-receipt", "functions-no-receipt", "functions-cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=" + mode, "GORACE=atexit_sleep_ms=0"}}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", AgentSessionID: "native-session", ObserveToolObservations: true, AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}, FunctionTools: []proto.FunctionTool{{Name: "lookup", Description: "Lookup.", Parameters: json.RawMessage(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"}}}}`)}}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			running, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			defer running.Cancel(context.Background())
			submitter := running.(agent.FunctionResultSubmitter)
			submissions := make(chan error, 2)
			calls := 0
			failed := false
			complete := map[string]proto.ToolObservation{}
			for event := range out {
				if event.ID != "run" {
					t.Fatal("wrong run")
				}
				switch event.Type {
				case proto.TypeFunctionCall:
					var call proto.FunctionCallPayload
					if err := event.DecodePayload(&call); err != nil {
						t.Fatal(err)
					}
					calls++
					invalid := proto.FunctionResultPayload{DeliveryID: "delivery-" + call.CallID, CallID: call.CallID, Success: true}
					if err := submitter.SubmitFunctionResult(ctx, invalid); err == nil {
						t.Fatal("missing content consumed call")
					}
					image := "https://example.invalid/image"
					invalid.Content = []proto.FunctionResultContent{{Type: "input_image", ImageURL: &image}}
					if err := submitter.SubmitFunctionResult(ctx, invalid); err == nil {
						t.Fatal("image should fail before delivery")
					}
					first, second := "first-"+call.CallID, "second-"+call.CallID
					value := proto.FunctionResultPayload{DeliveryID: "delivery-" + call.CallID, CallID: call.CallID, Success: call.CallID == "b", Content: []proto.FunctionResultContent{{Type: "input_text", Text: &first}, {Type: "input_text", Text: &second}}}
					go func() { submissions <- submitter.SubmitFunctionResult(ctx, value) }()
				case proto.TypeToolCall:
					var tool proto.ToolCallPayload
					if err := event.DecodePayload(&tool); err != nil {
						t.Fatal(err)
					}
					if tool.NativeItem != nil || tool.Observation == nil || tool.Observation.Kind != "function" {
						t.Fatal("expected neutral observation")
					}
					if tool.Stage != "before" && tool.Stage != "after" {
						t.Fatal("invalid shared tool stage")
					}
					if tool.Stage == "after" {
						complete[tool.ID] = *tool.Observation
					}
				case proto.TypeDelta:
					if mode == "functions-cancel" {
						if err := running.Cancel(ctx); err != nil {
							t.Fatal(err)
						}
					}
				case proto.TypeError:
					failed = true
				}
			}
			if calls != 2 || failed != (mode != "functions-success") {
				t.Fatalf("calls=%d failed=%v", calls, failed)
			}
			for range calls {
				if err := <-submissions; (err == nil) != (mode == "functions-success") {
					t.Fatalf("unexpected receipt: %v", err)
				}
			}
			if mode == "functions-success" {
				if len(complete) != 2 || complete["a"].Status != "failed" || complete["b"].Status != "completed" {
					t.Fatalf("bad observations: %+v", complete)
				}
				for id, value := range complete {
					if value.Content == nil || len(*value.Content) != 2 || *(*value.Content)[0].Text != "first-"+id || *(*value.Content)[1].Text != "second-"+id {
						t.Fatal("lost ordered result")
					}
				}
			} else if len(complete) != 0 {
				t.Fatal("unconfirmed result acquired a completed observation")
			}
		})
	}
}

func runFunctionHelper(request startRequest, mode string, scanner *bufio.Scanner, emit func(bridgeEvent)) {
	if len(request.Functions) != 1 || request.Functions[0].Name != "lookup" || !strings.Contains(string(request.Functions[0].Parameters), `"items":{"type":"string"}`) {
		os.Exit(4)
	}
	for _, id := range []string{"a", "b"} {
		emit(bridgeEvent{Type: "function_call", Call: &proto.FunctionCallPayload{CallID: id, Name: "lookup", Arguments: json.RawMessage(`{"ids":["same"]}`)}})
	}
	results := map[string]proto.FunctionResultPayload{}
	for range 2 {
		if !scanner.Scan() {
			os.Exit(5)
		}
		var value proto.FunctionResultPayload
		if json.Unmarshal(scanner.Bytes(), &value) != nil || value.ValidateContent() != nil || len(value.Content) != 2 {
			os.Exit(6)
		}
		results[value.CallID] = value
	}
	if mode == "functions-cancel" {
		emit(bridgeEvent{Type: "delta", Delta: "waiting for confirmation"})
		time.Sleep(time.Hour)
		return
	}
	if mode == "functions-no-receipt" {
		return
	}
	for _, id := range []string{"b", "a"} {
		delivery := results[id].DeliveryID
		if mode == "functions-wrong-receipt" {
			delivery = "wrong"
		}
		emit(bridgeEvent{Type: "function_applied", CallID: id, DeliveryID: delivery})
	}
	emit(bridgeEvent{Type: "result", SessionID: request.Resume, Text: "done"})
}
