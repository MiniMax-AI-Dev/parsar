package pi_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTranslateSessionHeaderSurfacesSessionID(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"session","id":"sess-abc","cwd":"/x","timestamp":"t"}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if tx.SessionID != "sess-abc" {
		t.Fatalf("SessionID = %q, want sess-abc", tx.SessionID)
	}
	if len(tx.Envelopes) != 0 {
		t.Fatalf("session header should emit no envelopes, got %#v", tx.Envelopes)
	}
}

func TestTranslateTextDeltaEmitsDelta(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hello"}}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(tx.Envelopes) != 1 || tx.Envelopes[0].Type != proto.TypeDelta {
		t.Fatalf("delta envelopes = %#v", tx.Envelopes)
	}
	delta := decodePayload[proto.DeltaPayload](t, tx.Envelopes[0])
	if delta.Delta != "hello" || delta.Sequence == 0 {
		t.Fatalf("delta payload = %#v", delta)
	}
}

func TestTranslateThinkingDeltaEmitsThinking(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"pondering"}}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(tx.Envelopes) != 1 || tx.Envelopes[0].Type != proto.TypeThinking {
		t.Fatalf("thinking envelopes = %#v", tx.Envelopes)
	}
	think := decodePayload[proto.ThinkingPayload](t, tx.Envelopes[0])
	if think.Text != "pondering" || think.Sequence == 0 {
		t.Fatalf("thinking payload = %#v", think)
	}
}

func TestTranslateToolExecutionStartEmitsBeforeToolCall(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"tool_execution_start","toolCallId":"t1","toolName":"bash","args":{"cmd":"ls"}}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(tx.Envelopes) != 1 || tx.Envelopes[0].Type != proto.TypeToolCall {
		t.Fatalf("toolcall envelopes = %#v", tx.Envelopes)
	}
	tc := decodePayload[proto.ToolCallPayload](t, tx.Envelopes[0])
	if tc.ID != "t1" || tc.Name != "bash" || tc.Stage != "before" {
		t.Fatalf("toolcall payload = %#v", tc)
	}
	if tc.Args["cmd"] != "ls" {
		t.Fatalf("toolcall args = %#v", tc.Args)
	}
}

func TestTranslateToolExecutionEndEmitsAfterToolCall(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"tool_execution_end","toolCallId":"t1","toolName":"bash","result":{"stdout":"x"},"isError":true}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(tx.Envelopes) != 1 || tx.Envelopes[0].Type != proto.TypeToolCall {
		t.Fatalf("toolcall envelopes = %#v", tx.Envelopes)
	}
	tc := decodePayload[proto.ToolCallPayload](t, tx.Envelopes[0])
	if tc.ID != "t1" || tc.Stage != "after" {
		t.Fatalf("toolcall payload = %#v", tc)
	}
	if tc.Result["is_error"] != true {
		t.Fatalf("toolcall result = %#v", tc.Result)
	}
}

func TestTranslateMessageEndCapturesUsage(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-u")
	line := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"provider":"anthropic","model":"claude-x","usage":{"input":10,"output":7,"cacheRead":2,"cacheWrite":1,"reasoning":3,"totalTokens":23,"cost":{"input":0.1,"output":0.2,"cacheRead":0,"cacheWrite":0,"total":0.3}},"stopReason":"stop"}}`
	if _, err := tr.Translate([]byte(line)); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	var got *proto.UsagePayload
	for _, env := range envs {
		if env.Type == proto.TypeUsage {
			payload := decodePayload[proto.UsagePayload](t, env)
			got = &payload
		}
	}
	if got == nil {
		t.Fatalf("usage env missing: %#v", envs)
	}
	if got.Provider != "anthropic" || got.Model != "claude-x" {
		t.Fatalf("usage provider/model = %#v", got)
	}
	if got.InputTokens != 10 || got.OutputTokens != 7 || got.CostUSD != 0.3 {
		t.Fatalf("usage tokens/cost = %#v", got)
	}
	if got.Raw["cache_read_tokens"] != float64(2) {
		t.Fatalf("usage raw = %#v", got.Raw)
	}
}

// pi exits 0 even when the model errors: it emits a message_end whose
// assistant message carries stopReason "error". The parser MUST surface
// that as TypeError despite the clean process exit.
func TestTranslateMessageEndErrorStopReasonEmitsError(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-err")
	line := `{"type":"message_end","message":{"role":"assistant","content":[],"provider":"anthropic","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","errorMessage":"boom"}}`
	tx, err := tr.Translate([]byte(line))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	var found bool
	for _, env := range tx.Envelopes {
		if env.Type == proto.TypeError {
			ep := decodePayload[proto.ErrorPayload](t, env)
			if strings.Contains(ep.Error, "boom") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("expected TypeError mentioning boom, got %#v", tx.Envelopes)
	}
}

func TestTerminalAlwaysEmitsDoneWithSessionMetadata(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-1")
	if _, err := tr.Translate([]byte(`{"type":"session","id":"sess-abc","cwd":"/x","timestamp":"t"}`)); err != nil {
		t.Fatalf("Translate header: %v", err)
	}
	if _, err := tr.Translate([]byte(`{"type":"message_update","message":{"role":"assistant"},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hello"}}`)); err != nil {
		t.Fatalf("Translate delta: %v", err)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	last := envs[len(envs)-1]
	if last.Type != proto.TypeDone {
		t.Fatalf("last env = %q, want done", last.Type)
	}
	done := decodePayload[proto.DonePayload](t, last)
	if done.Content != "hello" {
		t.Fatalf("done content = %q, want hello", done.Content)
	}
	if done.Metadata["pi_session_id"] != "sess-abc" {
		t.Fatalf("done metadata = %#v, want pi_session_id sess-abc", done.Metadata)
	}
	// Resume hinges on the canonical claude_session_id (see parser.go).
	if done.Metadata["claude_session_id"] != "sess-abc" {
		t.Fatalf("done metadata = %#v, want claude_session_id sess-abc", done.Metadata)
	}
}

func TestTerminalErrorIncludesStderr(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-e")
	envs := tr.TerminalEnvelopes(errors.New("exit status 2"), "bad auth", false)
	if len(envs) < 2 || envs[0].Type != proto.TypeError || envs[len(envs)-1].Type != proto.TypeDone {
		t.Fatalf("error terminal envs = %#v", envs)
	}
	errPayload := decodePayload[proto.ErrorPayload](t, envs[0])
	if !strings.Contains(errPayload.Error, "bad auth") {
		t.Fatalf("error payload = %#v", errPayload)
	}
}

func TestMessageEndContentFallbackWhenNoDeltas(t *testing.T) {
	tr := pi.NewTranslatorForTest("run-f")
	line := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"final answer"}],"provider":"anthropic","model":"m","usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop"}}`
	if _, err := tr.Translate([]byte(line)); err != nil {
		t.Fatalf("Translate: %v", err)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	done := decodePayload[proto.DonePayload](t, envs[len(envs)-1])
	if done.Content != "final answer" {
		t.Fatalf("done content = %q, want final answer", done.Content)
	}
}

func decodePayload[T any](t *testing.T, env proto.Envelope) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		t.Fatalf("decode %s payload: %v", env.Type, err)
	}
	return out
}
