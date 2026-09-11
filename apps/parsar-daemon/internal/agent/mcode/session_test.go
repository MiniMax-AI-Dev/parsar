package mcode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func testRequest(t *testing.T) proto.PromptRequestPayload {
	t.Helper()
	t.Setenv("PARSAR_HOME", t.TempDir())
	return proto.PromptRequestPayload{RunID: "run-1", ConversationID: "conversation-1", AgentStateKey: "conversation-1/agent-1/mcode", Prompt: "Hello", AgentOptions: map[string]any{
		"model": "fixture", "mcode_provider": map[string]any{"kind": "custom", "enabled": true}, "system_prompt": "Current instructions",
	}}
}

func helperSession(t *testing.T, scenario string, resume bool) (*Session, <-chan proto.Envelope) {
	t.Helper()
	req := testRequest(t)
	if resume {
		req.AgentSessionID = "native-1"
	}
	t.Setenv("PARSAR_MCODE_TEST_HELPER", scenario)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "mcode")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestMCodeProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	out := make(chan proto.Envelope, 32)
	session, err := newSession(ctx, req, out, binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Cancel(context.Background())
		select {
		case <-session.exited:
		case <-time.After(3 * time.Second):
			t.Error("CLI was not reaped")
		}
	})
	return session, out
}

func TestSessionStreamsCurrentTurnAndResumes(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "resume"}[resume], func(t *testing.T) {
			_, out := helperSession(t, "happy", resume)
			var content string
			tools := map[string]int{}
			doneCount := 0
			for event := range out {
				switch event.Type {
				case proto.TypeError:
					t.Fatalf("error: %s", event.Payload)
				case proto.TypeDelta:
					var p proto.DeltaPayload
					_ = json.Unmarshal(event.Payload, &p)
					content += p.Delta
				case proto.TypeToolCall:
					var p proto.ToolCallPayload
					_ = json.Unmarshal(event.Payload, &p)
					tools[p.Stage]++
				case proto.TypeDone:
					doneCount++
					var p proto.DonePayload
					_ = json.Unmarshal(event.Payload, &p)
					if p.Content != "Hello world" || p.Metadata[proto.DoneMetaAgentSessionID] != "native-1" {
						t.Fatalf("done = %#v", p)
					}
					if p.Usage.InputTokens != 0 || p.Usage.OutputTokens != 0 || p.Usage.CostUSD != 0 {
						t.Fatalf("context occupancy reported as usage: %#v", p.Usage)
					}
				}
			}
			if content != "Hello world" || doneCount != 1 || tools["before"] != 1 || tools["after"] != 1 {
				t.Fatalf("content=%q done=%d tools=%v", content, doneCount, tools)
			}
		})
	}
}

func TestSessionInteractionRoundTrip(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(map[bool]string{true: "allow", false: "deny"}[approved], func(t *testing.T) {
			session, out := helperSession(t, "interaction", false)
			permissions, questions := 0, 0
			for event := range out {
				switch event.Type {
				case proto.TypeError:
					t.Fatalf("error: %s", event.Payload)
				case proto.TypePermissionRequest:
					permissions++
					var p proto.PermissionRequestPayload
					_ = json.Unmarshal(event.Payload, &p)
					if err := session.SubmitPermission(context.Background(), p.RequestID, proto.PermissionDecisionPayload{Approved: approved}); err != nil {
						t.Fatal(err)
					}
					if err := session.SubmitPermission(context.Background(), p.RequestID, proto.PermissionDecisionPayload{Approved: true}); !errors.Is(err, agent.ErrUnknownPermission) {
						t.Fatalf("duplicate approval: %v", err)
					}
				case proto.TypePromptForUserChoice:
					questions++
					var p proto.PromptForUserChoicePayload
					_ = json.Unmarshal(event.Payload, &p)
					decision := proto.PromptForUserChoiceDecisionPayload{QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: "region", Answers: []string{"Europe"}}}}
					if err := session.SubmitPromptForUserChoice(context.Background(), p.AskID, decision); err != nil {
						t.Fatal(err)
					}
				}
			}
			if permissions != 1 || questions != 1 {
				t.Fatalf("permissions=%d questions=%d", permissions, questions)
			}
		})
	}
}

func TestResumeSelectsModelWhenNativeSelectorIsMissing(t *testing.T) {
	_, out := helperSession(t, "resume-model", true)
	completed := false
	for event := range out {
		if event.Type == proto.TypeError {
			t.Fatalf("resume failed: %s", event.Payload)
		}
		if event.Type == proto.TypeDone {
			completed = true
		}
	}
	if !completed {
		t.Fatal("resume did not complete")
	}
}

func TestSessionFailuresAreReported(t *testing.T) {
	for _, scenario := range []string{"malformed", "exit", "rpc-error", "unknown-model"} {
		t.Run(scenario, func(t *testing.T) {
			_, out := helperSession(t, scenario, false)
			reported := false
			for event := range out {
				if event.Type == proto.TypeError {
					reported = true
				}
			}
			if !reported {
				t.Fatal("failure was not emitted")
			}
		})
	}
}

func TestCancelStopsWaitingCLI(t *testing.T) {
	session, out := helperSession(t, "hang", false)
	if err := session.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.exited:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel hung")
	}
	for range out {
	}
}

func TestMCodeProcess(t *testing.T) {
	scenario := os.Getenv("PARSAR_MCODE_TEST_HELPER")
	if scenario == "" {
		return
	}
	encoder := json.NewEncoder(os.Stdout)
	send := func(value any) {
		if err := encoder.Encode(value); err != nil {
			os.Exit(2)
		}
	}
	update := func(kind string, fields map[string]any) {
		fields["sessionUpdate"] = kind
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-1", "update": fields}})
	}
	scanner := bufio.NewScanner(os.Stdin)
	var promptID json.RawMessage
	for scanner.Scan() {
		var frame rpcFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(3)
		}
		if scenario == "malformed" {
			os.Stdout.WriteString("invalid JSON\n")
			os.Exit(0)
		}
		if scenario == "exit" {
			os.Exit(1)
		}
		if scenario == "hang" {
			continue
		}
		result := any(map[string]any{})
		switch frame.Method {
		case "initialize":
			result = map[string]int{"protocolVersion": 1}
		case "session/new", "session/load":
			if frame.Method == "session/load" {
				update("agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "OLD HISTORY"}})
			}
			model := "m:custom_provider%3Aparsar:fixture:v:"
			if scenario == "unknown-model" {
				model = "m:minimax:native:u"
			}
			result = map[string]any{"sessionId": "native-1", "configOptions": []map[string]any{{"id": "model", "options": []map[string]string{{"value": model}}}}}
			if scenario == "resume-model" {
				result = map[string]any{"configOptions": []map[string]any{{"id": "permissionMode"}}}
			}
		case "session/set_config_option":
			var params map[string]string
			_ = json.Unmarshal(frame.Params, &params)
			if params["configId"] == "model" && params["value"] != "m:custom_provider%3Aparsar:fixture:v:" {
				os.Exit(4)
			}
		case "session/prompt":
			if scenario == "rpc-error" {
				send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32603, Message: "Fixture provider unavailable"}})
				continue
			}
			if scenario == "interaction" {
				promptID = frame.ID
				send(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{"sessionId": "native-1", "toolCall": map[string]any{"toolCallId": "tool-1", "name": "Bash", "title": "Run fixture", "rawInput": map[string]any{"command": "echo fixture"}}, "options": []map[string]string{{"optionId": "once", "kind": "allow_once"}, {"optionId": "always", "kind": "allow_always"}, {"optionId": "deny", "kind": "reject_once"}}}})
				continue
			}
			update("agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "Hello "}})
			update("agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "world"}})
			update("tool_call", map[string]any{"toolCallId": "tool-1", "name": "Read", "status": "in_progress", "rawInput": map[string]any{"path": "fixture.txt"}})
			for range 2 {
				update("tool_call_update", map[string]any{"toolCallId": "tool-1", "status": "completed", "rawOutput": "fixture"})
			}
			update("usage_update", map[string]any{"used": 2000, "size": 64000, "cost": map[string]any{"amount": 2, "currency": "USD"}})
			result = map[string]string{"stopReason": "end_turn"}
		case "":
			if string(frame.ID) == `"permission-1"` {
				var reply struct {
					Outcome struct {
						Option string `json:"optionId"`
					} `json:"outcome"`
				}
				_ = json.Unmarshal(frame.Result, &reply)
				if reply.Outcome.Option != "once" && reply.Outcome.Option != "deny" {
					os.Exit(5)
				}
				send(map[string]any{"jsonrpc": "2.0", "id": "question-1", "method": "elicitation/create", "params": map[string]any{"sessionId": "native-1", "mode": "form", "requestedSchema": map[string]any{"type": "object", "required": []string{"region"}, "properties": map[string]any{"region": map[string]any{"type": "string", "title": "Region?", "oneOf": []map[string]string{{"const": "eu", "title": "Europe"}, {"const": "us", "title": "America"}}}}}}})
			} else {
				var reply struct {
					Action  string            `json:"action"`
					Content map[string]string `json:"content"`
				}
				_ = json.Unmarshal(frame.Result, &reply)
				if reply.Action != "accept" || reply.Content["region"] != "eu" {
					os.Exit(6)
				}
				raw, _ := json.Marshal(map[string]string{"stopReason": "end_turn"})
				send(rpcFrame{JSONRPC: "2.0", ID: promptID, Result: raw})
			}
			continue
		}
		raw, _ := json.Marshal(result)
		send(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Result: raw})
	}
	os.Exit(0)
}
