package dev

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// fakeStreamConnector lets the stream-handler tests drive a
// connector-shaped response without spinning up a real OpenCode
// subprocess. Each StreamPrompt call returns a fresh channel that
// emits the pre-configured events and then closes — exactly the
// contract the dev handler treats as the canonical "end of stream"
// signal.
type fakeStreamConnector struct {
	caps   connector.Capabilities
	events []connector.PromptEvent
	// errReturn forces StreamPrompt to return (nil, err) synchronously
	// without ever opening the channel — covers the spawn-time error
	// path (missing model, secret, etc.) the handler must surface as
	// plain HTTP 5xx before any SSE framing.
	errReturn error
	// blockUntilCtxDone makes StreamPrompt hold the channel open
	// until ctx cancels, so the cancellation test can assert client
	// disconnect propagates to the connector ctx.
	blockUntilCtxDone bool
	// gotInput captures the PromptInput the handler dispatched, so
	// tests can verify body-decode + validation paths.
	gotInput connector.PromptInput
}

func (f *fakeStreamConnector) Type() string                         { return "agents_api" }
func (f *fakeStreamConnector) Capabilities() connector.Capabilities { return f.caps }
func (f *fakeStreamConnector) Prompt(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	return connector.PromptOutput{}, connector.ErrNotSupported
}
func (f *fakeStreamConnector) Cancel(ctx context.Context, conversationID string) error { return nil }
func (f *fakeStreamConnector) Abort(ctx context.Context, input connector.AbortInput) error {
	return nil
}
func (f *fakeStreamConnector) SubmitPermission(ctx context.Context, decision connector.PermissionDecision) error {
	return connector.ErrNotSupported
}
func (f *fakeStreamConnector) SubmitPromptForUserChoice(ctx context.Context, decision connector.PromptForUserChoiceDecision) error {
	return connector.ErrNotSupported
}
func (f *fakeStreamConnector) Close(ctx context.Context, conversationID string) error { return nil }
func (f *fakeStreamConnector) StreamPrompt(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	f.gotInput = in
	if f.errReturn != nil {
		return nil, f.errReturn
	}
	out := make(chan connector.PromptEvent, len(f.events)+1)
	go func() {
		defer close(out)
		for _, ev := range f.events {
			select {
			case <-ctx.Done():
				return
			case out <- ev:
			}
		}
		if f.blockUntilCtxDone {
			<-ctx.Done()
		}
	}()
	return out, nil
}

func parseSSEFrames(t *testing.T, r io.Reader) []map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
	var out []map[string]any
	var data string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		case line == "" && data != "":
			var ev map[string]any
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Fatalf("malformed SSE data %q: %v", data, err)
			}
			out = append(out, ev)
			data = ""
		}
	}
	// Trailing event without blank-line terminator (shouldn't
	// happen with our handler but defensive).
	if data != "" {
		var ev map[string]any
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			t.Fatalf("malformed trailing SSE data %q: %v", data, err)
		}
		out = append(out, ev)
	}
	return out
}

// rebuildPromptInputFromJSON parses a minimal partial JSON body into
// a connector.PromptInput and re-serializes it. We need this because
// connector.PromptInput uses Go-CamelCase JSON tags by default; the
// test cases above are written in snake_case-ish for readability, so
// we round-trip through the actual struct to ensure the dev handler
// sees the same shape a real caller would send.
func rebuildPromptInputFromJSON(raw string) connector.PromptInput {
	var lift struct {
		WorkspaceID    string         `json:"workspace_id"`
		ConversationID string         `json:"conversation_id"`
		RunID          string         `json:"run_id"`
		AgentConfig    map[string]any `json:"agent_config"`
	}
	_ = json.Unmarshal([]byte(raw), &lift)
	return connector.PromptInput{
		WorkspaceID:    lift.WorkspaceID,
		ConversationID: lift.ConversationID,
		RunID:          lift.RunID,
		AgentConfig:    lift.AgentConfig,
	}
}

// TestWireEventNameMapsConnectorEnumsToShortNames pins the SSE wire
// contract: public event names are `delta / done / error / tool /
// permission`, even though the connector-internal enum keeps the
// longer `tool_call` / `permission_request` values.
func TestWireEventNameMapsConnectorEnumsToShortNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   connector.PromptEventType
		want string
	}{
		{connector.EventDelta, "delta"},
		{connector.EventDone, "done"},
		{connector.EventError, "error"},
		{connector.EventToolCall, "tool"},
		{connector.EventPermissionRequest, "permission"},
		{connector.EventPromptForUserChoice, "prompt_for_user_choice"},
		{"", "message"},
		{"unknown_future_type", "unknown_future_type"},
	}
	for _, tc := range cases {
		if got := wireEventName(tc.in); got != tc.want {
			t.Errorf("wireEventName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestWriteSSEEventUsesWireNamesForToolAndPermission exercises the
// full writeSSEEvent path (not just the mapping helper) to confirm
// the SSE `event:` header AND the payload `type` field both use the
// short names, not the connector enum strings. A regression that
// only fixes one of the two surfaces would still ship a broken UI.
func TestWriteSSEEventUsesWireNamesForInteractiveEvents(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ev   connector.PromptEvent
		want string
	}{
		{
			ev:   connector.PromptEvent{Type: connector.EventToolCall, Tool: &connector.ToolCallEvent{ID: "t1", Name: "bash", Stage: "started"}},
			want: "tool",
		},
		{
			ev:   connector.PromptEvent{Type: connector.EventPermissionRequest, Permission: &connector.PermissionRequest{ID: "p1", Tool: "bash"}},
			want: "permission",
		},
		{
			ev: connector.PromptEvent{Type: connector.EventPromptForUserChoice, PromptForUserChoice: &connector.PromptForUserChoiceRequest{
				ID: "ask1", Questions: []connector.PromptForUserChoiceQuestion{{
					Header: "Deploy", Question: "Where?", Options: []connector.PromptForUserChoiceOption{{Label: "Staging"}},
				}},
			}},
			want: "prompt_for_user_choice",
		},
	} {
		buf := &bytes.Buffer{}
		rec := &writeFlusherRecorder{ResponseRecorder: httptest.NewRecorder(), buf: buf}
		if err := writeSSEEvent(rec, tc.ev); err != nil {
			t.Fatalf("writeSSEEvent err = %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "event: "+tc.want+"\n") {
			t.Errorf("SSE frame missing `event: %s` header: %q", tc.want, out)
		}
		// Payload `"type":` must also use the short name.
		if !strings.Contains(out, `"type":"`+tc.want+`"`) {
			t.Errorf("SSE payload missing `\"type\":\"%s\"`: %q", tc.want, out)
		}
	}
}

func TestWriteSSEEventSerializesUserChoiceQuestions(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	rec := &writeFlusherRecorder{ResponseRecorder: httptest.NewRecorder(), buf: buf}
	autoResolutionMs := uint64(90_000)
	ev := connector.PromptEvent{Type: connector.EventPromptForUserChoice, PromptForUserChoice: &connector.PromptForUserChoiceRequest{
		AutoResolutionMs: &autoResolutionMs,
		ID:               "ask1", Questions: []connector.PromptForUserChoiceQuestion{{
			ID: "checks", Header: "Checks", Question: "Which checks?", MultiSelect: true, IsOther: true, IsSecret: true,
			Options: []connector.PromptForUserChoiceOption{{Label: "Unit"}, {Label: "E2E", Description: "Browser flow"}},
		}},
	}}
	if err := writeSSEEvent(rec, ev); err != nil {
		t.Fatalf("writeSSEEvent: %v", err)
	}
	frames := parseSSEFrames(t, buf)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	choice, ok := frames[0]["prompt_for_user_choice"].(map[string]any)
	if !ok || choice["id"] != "ask1" {
		t.Fatalf("choice payload = %#v", frames[0]["prompt_for_user_choice"])
	}
	if choice["auto_resolution_ms"] != float64(autoResolutionMs) {
		t.Fatalf("auto_resolution_ms = %#v", choice["auto_resolution_ms"])
	}
	questions, ok := choice["questions"].([]any)
	if !ok || len(questions) != 1 {
		t.Fatalf("questions = %#v", choice["questions"])
	}
	question, _ := questions[0].(map[string]any)
	if question["id"] != "checks" || question["header"] != "Checks" || question["multi_select"] != true || question["is_other"] != true || question["is_secret"] != true {
		t.Fatalf("question = %#v", question)
	}
}

func TestWriteSSEEventPreservesClosedListQuestionMetadata(t *testing.T) {
	t.Parallel()
	buf := &bytes.Buffer{}
	rec := &writeFlusherRecorder{ResponseRecorder: httptest.NewRecorder(), buf: buf}
	ev := connector.PromptEvent{Type: connector.EventPromptForUserChoice, PromptForUserChoice: &connector.PromptForUserChoiceRequest{
		ID: "ask-closed", Questions: []connector.PromptForUserChoiceQuestion{{
			ID: "environment", Question: "Where?", IsOther: false, IsSecret: false,
			Options: []connector.PromptForUserChoiceOption{{Label: "Staging"}},
		}},
	}}
	if err := writeSSEEvent(rec, ev); err != nil {
		t.Fatalf("writeSSEEvent: %v", err)
	}
	frames := parseSSEFrames(t, buf)
	choice, _ := frames[0]["prompt_for_user_choice"].(map[string]any)
	questions, _ := choice["questions"].([]any)
	question, _ := questions[0].(map[string]any)
	if other, ok := question["is_other"]; !ok || other != false {
		t.Fatalf("is_other = %#v, present = %v, want explicit false", other, ok)
	}
	if secret, ok := question["is_secret"]; !ok || secret != false {
		t.Fatalf("is_secret = %#v, present = %v, want explicit false", secret, ok)
	}
}

// writeFlusherRecorder lets writeSSEEvent write into a buffer while
// still satisfying http.ResponseWriter so the function under test
// behaves exactly like the real handler.
type writeFlusherRecorder struct {
	*httptest.ResponseRecorder
	buf *bytes.Buffer
}

func (r *writeFlusherRecorder) Write(p []byte) (int, error) { return r.buf.Write(p) }
