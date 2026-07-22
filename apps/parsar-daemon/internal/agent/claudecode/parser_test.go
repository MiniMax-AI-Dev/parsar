package claudecode_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// counterMinter emits perm_001, perm_002, ... for deterministic
// envelope IDs in tests.
func counterMinter() func() string {
	var (
		mu sync.Mutex
		n  int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		n++
		return fmt.Sprintf("perm_%03d", n)
	}
}

func mustDecode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %T: %v\nraw=%s", v, err, raw)
	}
	return v
}

func TestTranslateSystemInitSurfacesSessionID(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_1", nil, counterMinter())
	line := []byte(`{"type":"system","subtype":"init","session_id":"sess_abc","tools":["Bash"]}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if out.SessionID != "sess_abc" {
		t.Errorf("SessionID = %q, want sess_abc", out.SessionID)
	}
	if len(out.Envelopes) != 0 {
		t.Errorf("system init must not emit envelopes, got %d", len(out.Envelopes))
	}
	if out.Terminal {
		t.Errorf("system init is not terminal")
	}
}

func TestTranslateAssistantTextEmitsDelta(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_42", nil, counterMinter())
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"text","text":"hello "},
		{"type":"text","text":"world"}
	]}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 2 {
		t.Fatalf("want 2 envelopes, got %d", len(out.Envelopes))
	}
	for i, env := range out.Envelopes {
		if env.Type != "delta" {
			t.Errorf("env[%d].Type = %q, want delta", i, env.Type)
		}
		if env.ID != "run_42" {
			t.Errorf("env[%d].ID = %q, want run_42", i, env.ID)
		}
	}
	d1 := mustDecode[struct {
		Delta    string `json:"delta"`
		Sequence uint64 `json:"sequence"`
	}](t, out.Envelopes[0].Payload)
	d2 := mustDecode[struct {
		Delta    string `json:"delta"`
		Sequence uint64 `json:"sequence"`
	}](t, out.Envelopes[1].Payload)
	if d1.Delta != "hello " || d2.Delta != "world" {
		t.Errorf("delta texts: %q,%q", d1.Delta, d2.Delta)
	}
	if d1.Sequence == 0 || d2.Sequence <= d1.Sequence {
		t.Errorf("sequence not monotonic: %d,%d", d1.Sequence, d2.Sequence)
	}
}

func TestTranslateAssistantThinkingEmitsThinking(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_x", nil, counterMinter())
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"thinking","thinking":"let me think...","signature":"sig"}
	]}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Type != "thinking" {
		t.Fatalf("want one thinking env, got %#v", out.Envelopes)
	}
	got := mustDecode[struct {
		Text string `json:"text"`
	}](t, out.Envelopes[0].Payload)
	if got.Text != "let me think..." {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestTranslateAssistantToolUseEmitsBeforeStage(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_t", nil, counterMinter())
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"tool_use","id":"toolu_99","name":"Bash","input":{"command":"ls"}}
	]}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 {
		t.Fatalf("want 1 env, got %d", len(out.Envelopes))
	}
	got := mustDecode[struct {
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Stage string         `json:"stage"`
		Args  map[string]any `json:"args"`
	}](t, out.Envelopes[0].Payload)
	if got.ID != "toolu_99" || got.Name != "Bash" || got.Stage != "before" {
		t.Errorf("payload mismatch: %+v", got)
	}
	if got.Args["command"] != "ls" {
		t.Errorf("args missing command: %v", got.Args)
	}
}

func TestTranslateAssistantMixedContentMonotonicSequence(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_seq", nil, counterMinter())
	line := []byte(`{"type":"assistant","message":{"role":"assistant","content":[
		{"type":"text","text":"a"},
		{"type":"thinking","thinking":"b"},
		{"type":"text","text":"c"},
		{"type":"tool_use","id":"t","name":"Read","input":{}}
	]}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 4 {
		t.Fatalf("want 4 envs, got %d", len(out.Envelopes))
	}
	var seqs []uint64
	for _, e := range out.Envelopes {
		if e.Type == "delta" || e.Type == "thinking" {
			d := mustDecode[struct {
				Sequence uint64 `json:"sequence"`
			}](t, e.Payload)
			seqs = append(seqs, d.Sequence)
		}
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Errorf("sequence not strictly increasing: %v", seqs)
		}
	}
}

func TestTranslateUserToolResultEmitsAfterStage(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_r", nil, counterMinter())
	line := []byte(`{"type":"user","message":{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"toolu_99","content":"hello\nworld\n","is_error":false}
	]}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Type != "tool_call" {
		t.Fatalf("want one tool_call after env, got %#v", out.Envelopes)
	}
	got := mustDecode[struct {
		ID     string         `json:"id"`
		Stage  string         `json:"stage"`
		Result map[string]any `json:"result"`
	}](t, out.Envelopes[0].Payload)
	if got.ID != "toolu_99" || got.Stage != "after" {
		t.Errorf("got %+v", got)
	}
	if got.Result["content"] != "hello\nworld\n" {
		t.Errorf("content not preserved, got %v", got.Result["content"])
	}
	if got.Result["is_error"] != false {
		t.Errorf("is_error not preserved: %v", got.Result["is_error"])
	}
}

func TestTranslateUserToolResultPreservesStructuredContent(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_r", nil, counterMinter())
	line := []byte(`{"type":"user","message":{"role":"user","content":[
		{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"x"}],"is_error":true}
	]}}`)
	out, _ := tr.Translate(line)
	got := mustDecode[struct {
		Result map[string]any `json:"result"`
	}](t, out.Envelopes[0].Payload)
	if _, ok := got.Result["content"].([]any); !ok {
		t.Errorf("expected []any content block array, got %T %v", got.Result["content"], got.Result["content"])
	}
}

func TestTranslateControlRequestMintsPermIDAndRecords(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	tr := claudecode.NewTranslatorForTest("run_z", pending, counterMinter())
	line := []byte(`{"type":"control_request","request_id":"req_001","request":{
		"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf /tmp/a"}
	}}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Type != "permission_request" {
		t.Fatalf("want one permission_request env, got %#v", out.Envelopes)
	}
	env := out.Envelopes[0]
	if env.ID != "run_z" {
		t.Errorf("env.ID = %q, want run_z", env.ID)
	}
	pr := mustDecode[struct {
		RequestID string         `json:"request_id"`
		Tool      string         `json:"tool"`
		Title     string         `json:"title"`
		Payload   map[string]any `json:"payload"`
	}](t, env.Payload)
	if pr.RequestID != "perm_001" || pr.Tool != "Bash" || pr.Title != "Bash" {
		t.Errorf("permission payload mismatch: %+v", pr)
	}
	if pr.Payload["command"] != "rm -rf /tmp/a" {
		t.Errorf("payload not preserved: %v", pr.Payload)
	}
	entry, ok := pending.Resolve("perm_001")
	if !ok || entry.CCRequestID != "req_001" {
		t.Errorf("pending table not recorded: %+v ok=%v", entry, ok)
	}
}

func TestTranslateControlCancelLooksUpPerm(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	tr := claudecode.NewTranslatorForTest("run_z", pending, counterMinter())
	// Seed by translating the original control_request.
	_, _ = tr.Translate([]byte(`{"type":"control_request","request_id":"req_5","request":{"subtype":"can_use_tool","tool_name":"Write","input":{}}}`))
	out, err := tr.Translate([]byte(`{"type":"control_cancel_request","request_id":"req_5"}`))
	if err != nil {
		t.Fatalf("Translate cancel: %v", err)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Type != "permission_cancel" {
		t.Fatalf("want one permission_cancel env, got %#v", out.Envelopes)
	}
	if out.Envelopes[0].ID != "perm_001" {
		t.Errorf("cancel env.ID = %q, want perm_001", out.Envelopes[0].ID)
	}
}

func TestTranslateControlCancelUnknownCCIDDropped(t *testing.T) {
	pending := claudecode.NewPendingTableForTest()
	tr := claudecode.NewTranslatorForTest("run_z", pending, counterMinter())
	out, err := tr.Translate([]byte(`{"type":"control_cancel_request","request_id":"req_nope"}`))
	if err != nil {
		t.Fatalf("Translate cancel: %v", err)
	}
	if len(out.Envelopes) != 0 {
		t.Errorf("unknown cancel should be dropped, got %#v", out.Envelopes)
	}
}

func TestTranslateResultSuccessEmitsUsageThenDone(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	line := []byte(`{
		"type":"result","subtype":"success","is_error":false,
		"result":"final answer text","session_id":"sess_abc",
		"total_cost_usd":0.01234,
		"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":7},
		"modelUsage":{"claude-opus-4-7-thinking-medium":{"inputTokens":100,"outputTokens":50,"contextWindow":200000,"costUSD":0.01234}}
	}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !out.Terminal {
		t.Error("result must be terminal")
	}
	if out.SessionID != "sess_abc" {
		t.Errorf("SessionID = %q, want sess_abc", out.SessionID)
	}
	if len(out.Envelopes) != 2 {
		t.Fatalf("want 2 envs (usage, done), got %d %#v", len(out.Envelopes), out.Envelopes)
	}
	if out.Envelopes[0].Type != "usage" || out.Envelopes[1].Type != "done" {
		t.Errorf("env order wrong, got %s,%s", out.Envelopes[0].Type, out.Envelopes[1].Type)
	}
	usage := mustDecode[struct {
		Provider     string         `json:"provider"`
		Model        string         `json:"model"`
		InputTokens  int32          `json:"input_tokens"`
		OutputTokens int32          `json:"output_tokens"`
		CostUSD      float64        `json:"cost_usd"`
		Raw          map[string]any `json:"raw"`
	}](t, out.Envelopes[0].Payload)
	if usage.Provider != "claude_code" {
		t.Errorf("usage.Provider = %q", usage.Provider)
	}
	// Model flows through from modelUsage's map key — no top-level
	// "model" field in the result frame.
	if usage.Model != "claude-opus-4-7-thinking-medium" {
		t.Errorf("usage.Model = %q, want claude-opus-4-7-thinking-medium", usage.Model)
	}
	if usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Errorf("usage tokens: %+v", usage)
	}
	if usage.CostUSD != 0.01234 {
		t.Errorf("usage cost = %v", usage.CostUSD)
	}
	if _, ok := usage.Raw["cache_read_input_tokens"]; !ok {
		t.Errorf("usage.Raw missing cache stats: %v", usage.Raw)
	}
	done := mustDecode[struct {
		Content  string         `json:"content"`
		Metadata map[string]any `json:"metadata"`
	}](t, out.Envelopes[1].Payload)
	if done.Content != "final answer text" {
		t.Errorf("done.Content = %q", done.Content)
	}
	if done.Metadata == nil {
		t.Fatalf("done.Metadata missing")
	}
	if got, _ := done.Metadata[proto.DoneMetaAgentSessionID].(string); got != "sess_abc" {
		t.Errorf("done.Metadata.agent_session_id = %q, want sess_abc", got)
	}
	if got, _ := done.Metadata[proto.DoneMetaAgentSessionType].(string); got != "claude_session" {
		t.Errorf("done.Metadata.agent_session_type = %q", got)
	}
}

func TestTranslateResultSuccessNoUsageOmitsUsage(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	line := []byte(`{"type":"result","subtype":"success","result":"hi"}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(out.Envelopes) != 1 || out.Envelopes[0].Type != "done" {
		t.Errorf("want only done when no usage, got %#v", out.Envelopes)
	}
}

func TestTranslateResultErrorSubtypeEmitsError(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	line := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"error":"boom"}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if !out.Terminal {
		t.Error("result error must be terminal")
	}
	if len(out.Envelopes) != 2 {
		t.Fatalf("want error+done, got %#v", out.Envelopes)
	}
	if out.Envelopes[0].Type != "error" || out.Envelopes[1].Type != "done" {
		t.Errorf("env order: %s,%s", out.Envelopes[0].Type, out.Envelopes[1].Type)
	}
	got := mustDecode[struct {
		Error string `json:"error"`
	}](t, out.Envelopes[0].Payload)
	if got.Error != "boom" {
		t.Errorf("error text = %q", got.Error)
	}
}

func TestTranslateResultErrorWithoutMessageFallsBackToSubtype(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	line := []byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`)
	out, _ := tr.Translate(line)
	got := mustDecode[struct {
		Error string `json:"error"`
	}](t, out.Envelopes[0].Payload)
	if !strings.Contains(got.Error, "error_max_turns") {
		t.Errorf("error fallback should mention subtype, got %q", got.Error)
	}
}

func TestTranslateUnknownTypeIsNoOp(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	out, err := tr.Translate([]byte(`{"type":"some_future_thing","foo":1}`))
	if err != nil {
		t.Fatalf("unknown type should not error: %v", err)
	}
	if len(out.Envelopes) != 0 || out.Terminal {
		t.Errorf("unknown type emitted envelopes/terminal: %#v term=%v", out.Envelopes, out.Terminal)
	}
}

func TestTranslateBlankLineIsNoOp(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	for _, s := range []string{"", "   ", "\n", "\t \n"} {
		out, err := tr.Translate([]byte(s))
		if err != nil {
			t.Errorf("blank line %q errored: %v", s, err)
		}
		if len(out.Envelopes) != 0 {
			t.Errorf("blank line %q emitted envs", s)
		}
	}
}

func TestTranslateMalformedJSONReturnsError(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	_, err := tr.Translate([]byte(`{"type":"assistant", broken`))
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}
