package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestFunctionCallWaitsAndRepliesOnce(t *testing.T) {
	for _, success := range []bool{true, false} {
		t.Run(map[bool]string{true: "success", false: "failure"}[success], func(t *testing.T) {
			tc, srv, cleanup := NewTestClient()
			defer cleanup()
			s, out := newInteractionTestSession(tc.JSONRPCClient)
			var err error
			s.functions, err = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			s.setThreadID("thread")
			params := map[string]any{"threadId": "thread", "turnId": "turn", "callId": "call", "tool": "lookup", "arguments": map[string]string{"ticket": "42"}}
			if err := SendServerRequest(srv, "rpc-call", "item/tool/call", params); err != nil {
				t.Fatal(err)
			}
			select {
			case env := <-out:
				var call proto.FunctionCallPayload
				if err := env.DecodePayload(&call); err != nil || env.Type != proto.TypeFunctionCall || env.ID != "run-test" || call.CallID != "call" || call.Name != "lookup" {
					t.Fatal(env, err)
				}
			case <-time.After(time.Second):
				t.Fatal("missing function call")
			}
			text, image, empty := "answer", "https://example.com/result.png", ""
			content := []proto.FunctionResultContent{{Type: "input_text", Text: &text}, {Type: "input_image", ImageURL: &image}, {Type: "input_text", Text: &empty}}
			if err := s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Content: []proto.FunctionResultContent{{Type: "input_audio"}}}); err == nil {
				t.Fatal("invalid result consumed the pending call")
			}
			finished := make(chan error, 1)
			go func() {
				finished <- s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call", Success: success, Content: content})
			}()
			var reply struct {
				ID     string          `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if err := json.NewDecoder(srv.FromClient).Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.ID != "rpc-call" {
				t.Fatal(reply.ID)
			}
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			var result struct {
				Success bool              `json:"success"`
				Content []functionContent `json:"contentItems"`
			}
			if err := json.Unmarshal(reply.Result, &result); err != nil || result.Success != success || !reflect.DeepEqual(result.Content, []functionContent{{Type: "inputText", Text: &text}, {Type: "inputImage", ImageURL: &image}, {Type: "inputText", Text: &empty}}) {
				t.Fatal(string(reply.Result), err)
			}
			if err := s.SubmitFunctionResult(t.Context(), proto.FunctionResultPayload{CallID: "call"}); !errors.Is(err, agent.ErrUnknownFunctionCall) {
				t.Fatal(err)
			}
		})
	}
}

func TestFunctionCallRejectsUnregisteredAndClosedRuns(t *testing.T) {
	tc, _, cleanup := NewTestClient()
	defer cleanup()
	s, _ := newInteractionTestSession(tc.JSONRPCClient)
	s.setThreadID("thread")
	s.functions, _ = prepareFunctionTools([]proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}})
	for _, params := range []string{
		`{"threadId":"other","turnId":"turn","callId":"a","tool":"lookup","arguments":{}}`,
		`{"threadId":"thread","turnId":"turn","callId":"a","tool":"unknown","arguments":{}}`,
		`{"threadId":"thread","turnId":"turn","callId":"a","tool":"lookup","namespace":"foreign","arguments":{}}`,
	} {
		if _, err := s.handleFunctionCall(json.RawMessage(params), "rpc"); err == nil {
			t.Fatal("unexpected call accepted", params)
		}
	}
	s.stopFunctionCalls()
	if _, err := s.handleFunctionCall(json.RawMessage(`{"threadId":"thread","turnId":"turn","callId":"a","tool":"lookup","arguments":{}}`), "rpc"); err == nil {
		t.Fatal("closed run accepted call")
	}
	if err := s.SubmitFunctionResult(context.Background(), proto.FunctionResultPayload{CallID: "a"}); !errors.Is(err, agent.ErrUnknownFunctionCall) {
		t.Fatal(err)
	}
}

func TestFunctionToolDefinitionsRejectAmbiguousInput(t *testing.T) {
	for _, tools := range [][]proto.FunctionTool{
		{{Name: "", Parameters: json.RawMessage(`{}`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`[]`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`null`)}},
		{{Name: "lookup", Parameters: json.RawMessage(`{}`)}, {Name: "lookup", Parameters: json.RawMessage(`{}`)}},
	} {
		if _, err := prepareFunctionTools(tools); err == nil {
			t.Fatal("invalid function accepted", tools)
		}
	}
}
