package opencode_test

import (
	"fmt"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTranslateCompletedToolsPreservesTraceAndReply(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-tools")
	for i, tool := range []string{"skill", "bash", "qa-service-desk_get_test_ticket", "read", "read"} {
		id := fmt.Sprintf("call_%d", i)
		line := []byte(fmt.Sprintf(`{"type":"tool_use","part":{"type":"tool","callID":%q,"tool":%q,"state":{"status":"completed","input":{"name":"qa-onboarding-zip","limit":3,"enabled":true},"output":"synthetic result"}}}`, id, tool))
		tx, err := tr.Translate(line)
		if err != nil || len(tx.Envelopes) != 2 {
			t.Fatalf("tool %s: envelopes=%#v err=%v", id, tx.Envelopes, err)
		}
		for j, stage := range []string{"before", "after"} {
			env := tx.Envelopes[j]
			call := decodePayload[proto.ToolCallPayload](t, env)
			if env.Type != proto.TypeToolCall || env.ID != "run-tools" || call.ID != id || call.Name != tool || call.Stage != stage {
				t.Fatalf("tool envelope=%#v payload=%#v", env, call)
			}
			if call.Args["name"] != "qa-onboarding-zip" || call.Args["limit"] != float64(3) || call.Args["enabled"] != true {
				t.Fatalf("typed args = %#v", call.Args)
			}
			if stage == "before" && call.Result != nil {
				t.Fatalf("call already has result: %#v", call.Result)
			}
			if stage == "after" && (call.Result["output"] != "synthetic result" || call.Result["is_error"] != false) {
				t.Fatalf("tool result = %#v", call.Result)
			}
		}
		duplicate, err := tr.Translate(line)
		if err != nil || len(duplicate.Envelopes) != 0 {
			t.Fatalf("duplicate call %s: %#v, %v", id, duplicate, err)
		}
	}
	if _, err := tr.Translate([]byte(`{"type":"text","part":{"type":"text","text":"third working day"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Translate([]byte(`{"type":"step_finish","part":{"tokens":{"input":12,"output":4},"cost":0.01}}`)); err != nil {
		t.Fatal(err)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	done := decodePayload[proto.DonePayload](t, envs[len(envs)-1])
	if done.Content != "third working day" || done.Usage.InputTokens != 12 || done.Usage.OutputTokens != 4 || done.Usage.CostUSD != 0.01 {
		t.Fatalf("done = %#v", done)
	}
}

func TestTranslateFailedToolRemainsToolFailure(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-error")
	tx, err := tr.Translate([]byte(`{"type":"tool_use","part":{"type":"tool","callID":"call_error","tool":"read","state":{"status":"error","input":{"filePath":"/missing"},"error":"File not found"}}}`))
	if err != nil || len(tx.Envelopes) != 2 {
		t.Fatalf("envelopes=%#v err=%v", tx.Envelopes, err)
	}
	result := decodePayload[proto.ToolCallPayload](t, tx.Envelopes[1])
	if result.Result["is_error"] != true || result.Result["output"] != "File not found" || result.Args["filePath"] != "/missing" {
		t.Fatalf("failed tool = %#v", result)
	}
	// A recoverable tool error must not become a run error or contaminate text.
	_, _ = tr.Translate([]byte(`{"type":"text","part":{"type":"text","text":"Please supply a valid path."}}`))
	for _, env := range tr.TerminalEnvelopes(nil, "", false) {
		if env.Type == proto.TypeError {
			t.Fatal("tool failure became run failure")
		}
		if env.Type == proto.TypeDone && decodePayload[proto.DonePayload](t, env).Content != "Please supply a valid path." {
			t.Fatal("tool failure leaked into final text")
		}
	}
}

func TestTranslateToolRequiresTerminalIdentity(t *testing.T) {
	for _, part := range []string{
		`null`,
		`{"type":"text","callID":"c","tool":"read","state":{"status":"completed"}}`,
		`{"type":"tool","tool":"read","state":{"status":"completed"}}`,
		`{"type":"tool","callID":"c","state":{"status":"completed"}}`,
		`{"type":"tool","callID":"c","tool":"read","state":{"status":"pending"}}`,
		`{"type":"tool","callID":"c","tool":"read","state":{"status":"running"}}`,
	} {
		tr := opencode.NewTranslatorForTest("run-ignored")
		tx, err := tr.Translate([]byte(`{"type":"tool_use","part":` + part + `}`))
		if err != nil || len(tx.Envelopes) != 0 {
			t.Fatalf("unexpected events for %s: %#v, %v", part, tx, err)
		}
		// An incomplete frame must not consume the eventual terminal call ID.
		tx, err = tr.Translate([]byte(`{"type":"tool_use","part":{"type":"tool","callID":"c","tool":"read","state":{"status":"completed","output":""}}}`))
		if err != nil || len(tx.Envelopes) != 2 {
			t.Fatalf("terminal events missing: %#v, %v", tx, err)
		}
	}
}
