package claudecode_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTranslateResultErrorDetails(t *testing.T) {
	for _, tt := range []struct {
		name, fields, want string
	}{
		{"missing session", `"errors":["No conversation found with session ID: missing-session"]`, "No conversation found with session ID: missing-session"},
		{"multiple details", `"errors":[" first error ","", "  ","second error"]`, "first error\nsecond error"},
		{"empty details", `"errors":["", "  "]`, "claude_code: error_during_execution"},
		{"legacy error", `"error":" legacy error ","errors":["array detail"]`, "legacy error"},
		{"legacy result", `"result":" legacy result ","errors":["array detail"]`, "legacy result"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tr := claudecode.NewTranslatorForTest("run_details", nil, counterMinter())
			line := []byte(`{"type":"result","subtype":"error_during_execution","is_error":true,` + tt.fields + `}`)
			out, err := tr.Translate(line)
			if err != nil {
				t.Fatal(err)
			}
			if !out.Terminal || len(out.Envelopes) != 2 || out.Envelopes[0].Type != proto.TypeError || out.Envelopes[1].Type != proto.TypeDone {
				t.Fatalf("want terminal error then done, got %+v", out)
			}
			var detail proto.ErrorPayload
			if err := json.Unmarshal(out.Envelopes[0].Payload, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Error != tt.want {
				t.Fatalf("error = %q, want %q", detail.Error, tt.want)
			}
		})
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

func TestTranslateResultIsErrorSuccessSubtypeUsesResultMessage(t *testing.T) {
	tr := claudecode.NewTranslatorForTest("run_99", nil, counterMinter())
	line := []byte(`{"type":"result","subtype":"success","is_error":true,"result":"API Error: 400 content rejected"}`)
	out, err := tr.Translate(line)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	got := mustDecode[struct {
		Error string `json:"error"`
	}](t, out.Envelopes[0].Payload)
	if got.Error != "API Error: 400 content rejected" {
		t.Errorf("error text = %q", got.Error)
	}
}
