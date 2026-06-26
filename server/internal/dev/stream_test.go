package dev

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
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

func (f *fakeStreamConnector) Type() string                         { return "agent_daemon" }
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

func newStreamRouter(t *testing.T, conn connector.AgentConnector) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	if conn == nil {
		RegisterRoutesWithStore(r, nil)
		return r
	}
	RegisterRoutesWithStore(r, nil, WithOpenCodeConnector(conn))
	return r
}

func minimalValidPromptBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(connector.PromptInput{
		RunID:                 "00000000-0000-0000-0000-000000000999",
		WorkspaceID:           "00000000-0000-0000-0000-000000000002",
		ProjectID:             "00000000-0000-0000-0000-000000000004",
		ConversationID:        "00000000-0000-0000-0000-000000000012",
		ProjectAgentID:        "00000000-0000-0000-0000-000000000010",
		AgentID:               "00000000-0000-0000-0000-000000000007",
		AgentName:             "后端Agent",
		TriggerMessageContent: "stream me",
		AgentConfig: map[string]any{
			"model_id": "00000000-0000-0000-0000-0000000000aa",
			"workdir":  "/tmp/wd-stream-test",
		},
		ProjectAgentConfig: map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestStreamEndpoint503WhenConnectorMissing(t *testing.T) {
	t.Parallel()
	r := newStreamRouter(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/dev/connectors/opencode/stream", bytes.NewReader(minimalValidPromptBody(t)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no AgentConnector wired)", res.Code)
	}
	if !strings.Contains(res.Body.String(), "not registered") {
		t.Errorf("body should explain why it's 503, got %s", res.Body.String())
	}
}

func TestStreamEndpoint400OnMissingRequiredFields(t *testing.T) {
	t.Parallel()
	fc := &fakeStreamConnector{caps: connector.Capabilities{Sync: true, Streaming: true}}
	r := newStreamRouter(t, fc)
	cases := []struct {
		name string
		body string
		hint string
	}{
		{"no workspace", `{"run_id":"r","conversation_id":"c"}`, "workspace_id"},
		{"no conversation", `{"run_id":"r","workspace_id":"w"}`, "conversation_id"},
		{"no run id", `{"workspace_id":"w","conversation_id":"c"}`, "run_id"},
		{"no model id", `{"run_id":"r","workspace_id":"w","conversation_id":"c","agent_config":{"workdir":"/tmp/x"}}`, "model_id"},
		{"no workdir", `{"run_id":"r","workspace_id":"w","conversation_id":"c","agent_config":{"model_id":"m"}}`, "workdir"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// PromptInput JSON tags are CamelCase — map to a struct that mirrors them.
			body, err := json.Marshal(rebuildPromptInputFromJSON(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/dev/connectors/opencode/stream", bytes.NewReader(body))
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s, want 400 mentioning %s", res.Code, res.Body.String(), tc.hint)
			}
			if !strings.Contains(res.Body.String(), tc.hint) {
				t.Errorf("400 body should mention %q, got %s", tc.hint, res.Body.String())
			}
		})
	}
}

func TestStreamEndpointSurfacesSpawnTimeErrorAsHTTP(t *testing.T) {
	t.Parallel()
	fc := &fakeStreamConnector{
		caps:      connector.Capabilities{Sync: true, Streaming: true},
		errReturn: fmt.Errorf("opencode: model 1234 has no adapter"),
	}
	r := newStreamRouter(t, fc)
	req := httptest.NewRequest(http.MethodPost, "/dev/connectors/opencode/stream", bytes.NewReader(minimalValidPromptBody(t)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d body=%s, want 500 (spawn-time error must NOT open SSE)", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "no adapter") {
		t.Errorf("body should include the underlying error, got %s", res.Body.String())
	}
}

func TestStreamEndpointEmitsSSEFramesForEachEvent(t *testing.T) {
	t.Parallel()
	final := connector.PromptOutput{Content: "hello world"}
	fc := &fakeStreamConnector{
		caps: connector.Capabilities{Sync: true, Streaming: true},
		events: []connector.PromptEvent{
			{Type: connector.EventDelta, Sequence: 1, Delta: "hello "},
			{Type: connector.EventDelta, Sequence: 2, Delta: "world"},
			{Type: connector.EventDone, Sequence: 3, Final: &final},
		},
	}
	r := newStreamRouter(t, fc)

	req := httptest.NewRequest(http.MethodPost, "/dev/connectors/opencode/stream", bytes.NewReader(minimalValidPromptBody(t)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	events := parseSSEFrames(t, res.Body)
	if len(events) != 3 {
		t.Fatalf("got %d SSE events, want 3 (events=%v)", len(events), events)
	}
	if events[0]["type"] != "delta" || events[0]["delta"] != "hello " {
		t.Errorf("event 0 = %+v, want delta='hello '", events[0])
	}
	if events[1]["delta"] != "world" {
		t.Errorf("event 1 = %+v, want delta='world'", events[1])
	}
	if events[2]["type"] != "done" {
		t.Errorf("event 2 = %+v, want type=done", events[2])
	}
	finalObj, ok := events[2]["final"].(map[string]any)
	if !ok {
		t.Fatalf("event 2.final = %+v, want object with content", events[2]["final"])
	}
	if finalObj["content"] != "hello world" {
		t.Errorf("Final.content = %v, want 'hello world'", finalObj["content"])
	}
}

func TestStreamEndpointEmittedAtFieldStamped(t *testing.T) {
	t.Parallel()
	fc := &fakeStreamConnector{
		caps:   connector.Capabilities{Sync: true, Streaming: true},
		events: []connector.PromptEvent{{Type: connector.EventDelta, Sequence: 1, Delta: "x"}},
	}
	r := newStreamRouter(t, fc)
	req := httptest.NewRequest(http.MethodPost, "/dev/connectors/opencode/stream", bytes.NewReader(minimalValidPromptBody(t)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	events := parseSSEFrames(t, res.Body)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0]["emitted_at"] == nil {
		t.Fatal("emitted_at should be populated for client-side latency observation")
	}
}

// parseSSEFrames reads an SSE body and returns the JSON-decoded
// payload of each event as a generic map. It assumes the dev
// endpoint's exact framing (one "event: <name>\ndata: <json>\n\n"
// per event) so any framing regression is caught loudly.
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
		WorkspaceID        string         `json:"workspace_id"`
		ConversationID     string         `json:"conversation_id"`
		RunID              string         `json:"run_id"`
		AgentConfig        map[string]any `json:"agent_config"`
		ProjectAgentConfig map[string]any `json:"project_agent_config"`
	}
	_ = json.Unmarshal([]byte(raw), &lift)
	return connector.PromptInput{
		WorkspaceID:        lift.WorkspaceID,
		ConversationID:     lift.ConversationID,
		RunID:              lift.RunID,
		AgentConfig:        lift.AgentConfig,
		ProjectAgentConfig: lift.ProjectAgentConfig,
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
func TestWriteSSEEventUsesWireNamesForToolAndPermission(t *testing.T) {
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

// writeFlusherRecorder lets writeSSEEvent write into a buffer while
// still satisfying http.ResponseWriter so the function under test
// behaves exactly like the real handler.
type writeFlusherRecorder struct {
	*httptest.ResponseRecorder
	buf *bytes.Buffer
}

func (r *writeFlusherRecorder) Write(p []byte) (int, error) { return r.buf.Write(p) }
