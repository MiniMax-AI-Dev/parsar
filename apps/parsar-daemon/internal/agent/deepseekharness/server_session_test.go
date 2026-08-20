package deepseekharness

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/enginehost"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/gorilla/websocket"
)

func quietConfig() sessionConfig {
	cfg := defaultConfig()
	cfg.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return cfg
}

// harness wires a serverSession to a fakeGateway without the supervisor,
// standing in for the lease the real Factory holds.
type harness struct {
	gateway *fakeGateway
	session *serverSession
	out     chan proto.Envelope
	exited  chan struct{}
	conn    *websocket.Conn

	released bool
}

func newHarness(t *testing.T, req proto.PromptRequestPayload) *harness {
	t.Helper()
	gateway := newFakeGateway(t)
	out := make(chan proto.Envelope, 128)
	h := &harness{gateway: gateway, out: out, exited: make(chan struct{})}

	s := &serverSession{
		runID:        req.RunID,
		cfg:          quietConfig(),
		api:          newAPIClient(enginehost.NewClient(gateway.srv.URL, 10*time.Second)),
		out:          out,
		engineExited: h.exited,
		release:      func() { h.released = true },
		diagnostics:  func() string { return "fake engine output" },
	}
	h.session = s

	if err := s.attachAndPrompt(context.Background(), req, "/tmp/fake-workspace"); err != nil {
		t.Fatalf("attachAndPrompt: %v", err)
	}
	h.conn = gateway.conn(t)
	go s.pump()
	return h
}

// collect drains out until it closes, which the session does exactly once
// after its terminal done frame.
func (h *harness) collect(t *testing.T) []proto.Envelope {
	t.Helper()
	var got []proto.Envelope
	deadline := time.After(15 * time.Second)
	for {
		select {
		case env, ok := <-h.out:
			if !ok {
				return got
			}
			got = append(got, env)
		case <-deadline:
			t.Fatalf("session never closed its out channel; collected %d frames", len(got))
			return got
		}
	}
}

func decodeEnv[T any](t *testing.T, env proto.Envelope) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		t.Fatalf("decode %s payload: %v", env.Type, err)
	}
	return out
}

func framesOfType(envs []proto.Envelope, typ string) []proto.Envelope {
	var out []proto.Envelope
	for _, env := range envs {
		if env.Type == typ {
			out = append(out, env)
		}
	}
	return out
}

func baseRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		AgentKind:      "deepseek_harness",
		ConversationID: "conv-1",
		RunID:          "run-1",
		Prompt:         "hello",
		AgentOptions:   map[string]any{},
	}
}

func TestServerSessionCreatesASessionAndQueuesTheTurn(t *testing.T) {
	h := newHarness(t, baseRequest())

	creates := h.gateway.createCalls()
	if len(creates) != 1 {
		t.Fatalf("expected one session.create, got %d", len(creates))
	}
	if !strings.Contains(creates[0], "/tmp/fake-workspace") {
		t.Errorf("session.create did not carry the workspace cwd: %s", creates[0])
	}

	prompts := h.gateway.promptCalls()
	if len(prompts) != 1 {
		t.Fatalf("expected one session.prompt, got %d", len(prompts))
	}
	// mode is required by the gateway schema; omitting it is a hard
	// bad-request, so it is asserted rather than assumed.
	if prompts[0].Mode != promptModeQueue {
		t.Errorf("prompt mode = %q, want %q", prompts[0].Mode, promptModeQueue)
	}
	if prompts[0].SessionID != h.gateway.nextSessionID {
		t.Errorf("prompt session = %q, want the created %q", prompts[0].SessionID, h.gateway.nextSessionID)
	}
	if len(prompts[0].Content) != 1 || prompts[0].Content[0].Text != "hello" {
		t.Errorf("prompt content = %+v", prompts[0].Content)
	}

	emitEvent(t, h.conn, h.gateway.nextSessionID, eventTurnEnd, 9, turnEnd("completed"))
	h.collect(t)
}

func TestServerSessionResumesTheGivenSessionWithoutCreating(t *testing.T) {
	req := baseRequest()
	req.AgentSessionID = "session-prior-42"
	h := newHarness(t, req)

	if creates := h.gateway.createCalls(); len(creates) != 0 {
		t.Fatalf("a resumed turn must not create a session, got %v", creates)
	}
	prompts := h.gateway.promptCalls()
	if len(prompts) != 1 || prompts[0].SessionID != "session-prior-42" {
		t.Fatalf("prompt did not target the prior session: %+v", prompts)
	}

	emitEvent(t, h.conn, "session-prior-42", eventTurnEnd, 3, turnEnd("completed"))
	envs := h.collect(t)
	done := decodeEnv[proto.DonePayload](t, envs[len(envs)-1])
	if done.Metadata[proto.DoneMetaAgentSessionID] != "session-prior-42" {
		t.Errorf("done metadata = %#v, want the resumed session id", done.Metadata)
	}
}

func TestServerSessionStreamsTextThinkingToolsAndUsage(t *testing.T) {
	h := newHarness(t, baseRequest())
	sid := h.gateway.nextSessionID

	emitEvent(t, h.conn, sid, eventAssistantChunk, 1, reasoningDelta(0, "let me think"))
	emitEvent(t, h.conn, sid, eventAssistantChunk, 2, textDelta(1, "Hel"))
	emitEvent(t, h.conn, sid, eventAssistantChunk, 3, textDelta(1, "lo"))
	emitEvent(t, h.conn, sid, eventToolCall, 4, map[string]any{
		"turn": 1, "step": 1, "callId": "call-7", "name": "read",
		"arguments": `{"file_path":"note.txt"}`,
	})
	emitEvent(t, h.conn, sid, eventToolResult, 5, map[string]any{
		"turn": 1, "step": 1,
		"message": map[string]any{
			"role":   "user",
			"source": map[string]any{"kind": "tool", "callId": "call-7"},
			"content": []map[string]any{{
				"type": "tool-result", "toolCallId": "call-7", "isError": false,
				"content": []map[string]any{{"type": "text", "text": "file body"}},
			}},
		},
	})
	emitEvent(t, h.conn, sid, eventAssistantChunk, 6, usageChunk(100, 20, 64))
	emitEvent(t, h.conn, sid, eventAssistantChunk, 7, usageChunk(50, 5, 0))
	emitEvent(t, h.conn, sid, eventAssistantMessage, 8,
		assistantMessage(reasoningBlock("let me think"), textBlock("Hello")))
	emitEvent(t, h.conn, sid, eventTurnEnd, 9, turnEnd("completed"))

	envs := h.collect(t)

	deltas := framesOfType(envs, proto.TypeDelta)
	var text strings.Builder
	for _, env := range deltas {
		text.WriteString(decodeEnv[proto.DeltaPayload](t, env).Delta)
	}
	if text.String() != "Hello" {
		t.Errorf("streamed text = %q, want Hello", text.String())
	}

	thinking := framesOfType(envs, proto.TypeThinking)
	if len(thinking) != 1 || decodeEnv[proto.ThinkingPayload](t, thinking[0]).Text != "let me think" {
		t.Errorf("thinking frames = %d, want the reasoning delta", len(thinking))
	}

	tools := framesOfType(envs, proto.TypeToolCall)
	if len(tools) != 2 {
		t.Fatalf("tool frames = %d, want a before and an after", len(tools))
	}
	before := decodeEnv[proto.ToolCallPayload](t, tools[0])
	if before.Stage != "before" || before.Name != "read" || before.ID != "call-7" {
		t.Errorf("before frame = %+v", before)
	}
	if before.Args["file_path"] != "note.txt" {
		t.Errorf("tool args not parsed from the JSON string: %+v", before.Args)
	}
	after := decodeEnv[proto.ToolCallPayload](t, tools[1])
	if after.Stage != "after" || after.ID != "call-7" {
		t.Errorf("after frame = %+v", after)
	}
	if after.Result["output"] != "file body" || after.Result["is_error"] != false {
		t.Errorf("after result = %+v", after.Result)
	}

	// Usage arrives per model request; a multi-step turn must report the
	// whole turn's cost, not the last request's.
	usage := framesOfType(envs, proto.TypeUsage)
	if len(usage) != 2 {
		t.Fatalf("usage frames = %d, want one per usage chunk", len(usage))
	}
	last := decodeEnv[proto.UsagePayload](t, usage[1])
	if last.InputTokens != 150 || last.OutputTokens != 25 {
		t.Errorf("usage = %+v, want summed 150/25", last.Usage)
	}

	done := decodeEnv[proto.DonePayload](t, envs[len(envs)-1])
	if envs[len(envs)-1].Type != proto.TypeDone {
		t.Fatalf("last frame = %s, want done", envs[len(envs)-1].Type)
	}
	// The answer comes from assistant/message, and reasoning must not leak
	// into it.
	if done.Content != "Hello" {
		t.Errorf("done content = %q, want Hello", done.Content)
	}
	if strings.Contains(done.Content, "let me think") {
		t.Errorf("reasoning leaked into the answer: %q", done.Content)
	}
	if done.Usage.InputTokens != 150 {
		t.Errorf("done usage = %+v", done.Usage)
	}
	if done.Metadata[proto.DoneMetaAgentSessionID] != sid {
		t.Errorf("done metadata = %#v", done.Metadata)
	}
	if done.Metadata[proto.DoneMetaAgentSessionType] != "dsh_session" {
		t.Errorf("done metadata session type = %#v", done.Metadata)
	}
	if !h.released {
		t.Error("the engine lease was not released when the run ended")
	}
}

func TestServerSessionIgnoresOtherSessionsOnTheMux(t *testing.T) {
	h := newHarness(t, baseRequest())
	sid := h.gateway.nextSessionID

	// The downlink is multiplexed: another conversation's turn shares the
	// connection and must not bleed into this run.
	emitEvent(t, h.conn, "session-someone-else", eventAssistantChunk, 1, textDelta(0, "NOT MINE"))
	emitEvent(t, h.conn, "session-someone-else", eventTurnEnd, 2, turnEnd("completed"))
	emitEvent(t, h.conn, sid, eventAssistantChunk, 3, textDelta(0, "MINE"))
	emitEvent(t, h.conn, sid, eventTurnEnd, 4, turnEnd("completed"))

	envs := h.collect(t)
	for _, env := range framesOfType(envs, proto.TypeDelta) {
		if strings.Contains(decodeEnv[proto.DeltaPayload](t, env).Delta, "NOT MINE") {
			t.Fatal("another session's delta reached this run")
		}
	}
	if len(framesOfType(envs, proto.TypeDelta)) != 1 {
		t.Errorf("delta frames = %d, want only this session's", len(framesOfType(envs, proto.TypeDelta)))
	}
}

func TestServerSessionFailsAnIncompleteTurn(t *testing.T) {
	h := newHarness(t, baseRequest())
	emitEvent(t, h.conn, h.gateway.nextSessionID, eventTurnEnd, 5,
		map[string]any{"turn": 1, "reason": map[string]any{"kind": "aborted", "message": "context limit"}})

	envs := h.collect(t)
	errs := framesOfType(envs, proto.TypeError)
	if len(errs) != 1 {
		t.Fatalf("error frames = %d, want 1", len(errs))
	}
	msg := decodeEnv[proto.ErrorPayload](t, errs[0]).Error
	if !strings.Contains(msg, "aborted") || !strings.Contains(msg, "context limit") {
		t.Errorf("error message lost the reason: %q", msg)
	}
	// A brand-new session whose first turn failed must not be handed back
	// as resumable.
	done := decodeEnv[proto.DonePayload](t, envs[len(envs)-1])
	if _, ok := done.Metadata[proto.DoneMetaAgentSessionID]; ok {
		t.Errorf("a failed first turn must not persist a session id: %#v", done.Metadata)
	}
}

func TestServerSessionKeepsAResumedSessionIDAfterAFailedTurn(t *testing.T) {
	req := baseRequest()
	req.AgentSessionID = "session-prior-9"
	h := newHarness(t, req)
	emitEvent(t, h.conn, "session-prior-9", eventTurnEnd, 5,
		map[string]any{"turn": 1, "reason": map[string]any{"kind": "aborted"}})

	envs := h.collect(t)
	done := decodeEnv[proto.DonePayload](t, envs[len(envs)-1])
	// The session already exists on disk; forgetting it would strand the
	// conversation on a fresh one.
	if done.Metadata[proto.DoneMetaAgentSessionID] != "session-prior-9" {
		t.Errorf("resumed session id was dropped after a failure: %#v", done.Metadata)
	}
}

func TestServerSessionFailsWhenApprovalIsAsked(t *testing.T) {
	h := newHarness(t, baseRequest())
	emitEvent(t, h.conn, h.gateway.nextSessionID, eventApprovalAsked, 4,
		map[string]any{"requestId": "ap-1", "tool": "bash"})

	envs := h.collect(t)
	errs := framesOfType(envs, proto.TypeError)
	if len(errs) != 1 {
		t.Fatalf("an approval ask with no approver must fail the run, got %d error frames", len(errs))
	}
	if msg := decodeEnv[proto.ErrorPayload](t, errs[0]).Error; !strings.Contains(msg, "approv") {
		t.Errorf("error message = %q", msg)
	}
}

func TestServerSessionFailsWhenTheEngineDiesMidTurn(t *testing.T) {
	h := newHarness(t, baseRequest())
	emitEvent(t, h.conn, h.gateway.nextSessionID, eventAssistantChunk, 1, textDelta(0, "par"))
	close(h.exited)

	envs := h.collect(t)
	errs := framesOfType(envs, proto.TypeError)
	if len(errs) != 1 {
		t.Fatalf("error frames = %d, want 1", len(errs))
	}
	msg := decodeEnv[proto.ErrorPayload](t, errs[0]).Error
	if !strings.Contains(msg, "exited mid-turn") || !strings.Contains(msg, "fake engine output") {
		t.Errorf("error should name the death and carry diagnostics, got %q", msg)
	}
	if envs[len(envs)-1].Type != proto.TypeDone {
		t.Error("a failed run still has to end with done")
	}
}

func TestServerSessionFailsWhenTheStreamEndsEarly(t *testing.T) {
	h := newHarness(t, baseRequest())
	// A server-side close with no turn/end: the turn's outcome is unknown,
	// which must not be reported as success.
	_ = h.conn.Close()

	envs := h.collect(t)
	if len(framesOfType(envs, proto.TypeError)) != 1 {
		t.Fatalf("expected one error frame, got %d", len(framesOfType(envs, proto.TypeError)))
	}
}

func TestServerSessionCancelTargetsTheSessionNotTheProcess(t *testing.T) {
	h := newHarness(t, baseRequest())
	if err := h.session.Cancel(context.Background()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// Idempotent: the router may cancel a run that already finished.
	if err := h.session.Cancel(context.Background()); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}

	cancels := h.gateway.cancelCalls()
	if len(cancels) != 1 || cancels[0] != h.gateway.nextSessionID {
		t.Fatalf("session.cancel calls = %v, want exactly one for this session", cancels)
	}

	envs := h.collect(t)
	errs := framesOfType(envs, proto.TypeError)
	if len(errs) != 1 || !strings.Contains(decodeEnv[proto.ErrorPayload](t, errs[0]).Error, "cancelled") {
		t.Errorf("cancelled run should report cancellation, got %d error frames", len(errs))
	}
	select {
	case <-h.exited:
		t.Error("cancelling a run must not take the shared engine down")
	default:
	}
}

func TestServerSessionSurfacesAPromptRejection(t *testing.T) {
	gateway := newFakeGateway(t)
	gateway.promptErr = &rpcError{Code: "bad-request", Message: "invalid payload for session.prompt"}
	out := make(chan proto.Envelope, 8)
	s := &serverSession{
		runID:        "run-x",
		cfg:          quietConfig(),
		api:          newAPIClient(enginehost.NewClient(gateway.srv.URL, 5*time.Second)),
		out:          out,
		engineExited: make(chan struct{}),
		release:      func() {},
		diagnostics:  func() string { return "" },
	}
	err := s.attachAndPrompt(context.Background(), baseRequest(), "/tmp/x")
	if err == nil {
		t.Fatal("expected the gateway rejection to fail the start")
	}
	// The gateway reports schema rejections as a 200 with ok=false, so the
	// union has to be unwrapped or this error would be swallowed.
	if !strings.Contains(err.Error(), "bad-request") || !strings.Contains(err.Error(), "invalid payload") {
		t.Errorf("error lost the gateway's reason: %v", err)
	}
}

func TestPromptContentCarriesSystemPromptAndSupportedImages(t *testing.T) {
	req := baseRequest()
	req.Prompt = "do the thing"
	req.AgentOptions = map[string]any{"system_prompt": "be terse"}
	req.Attachments = []proto.PromptAttachment{
		{Kind: "image", MIME: "image/png", DataBase64: "AAA"},
		{Kind: "image", MIME: "image/tiff", DataBase64: "BBB"},
	}

	parts, err := promptContent(req)
	if err != nil {
		t.Fatalf("promptContent: %v", err)
	}
	if parts[0].Type != "text" || !strings.HasPrefix(parts[0].Text, "be terse") ||
		!strings.Contains(parts[0].Text, "do the thing") {
		t.Errorf("text part = %+v", parts[0])
	}
	// image/tiff is outside the gateway's accepted raster set; forwarding
	// it would fail the whole turn on a schema error.
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want text plus the png only", len(parts))
	}
	if parts[1].MediaType != "image/png" || parts[1].Data != "AAA" {
		t.Errorf("image part = %+v", parts[1])
	}

	req.AgentOptions = map[string]any{"system_prompt": "be terse", "override_system_prompt": "override wins"}
	parts, err = promptContent(req)
	if err != nil {
		t.Fatalf("promptContent: %v", err)
	}
	if !strings.HasPrefix(parts[0].Text, "override wins") {
		t.Errorf("override_system_prompt did not win: %q", parts[0].Text)
	}

	req.Prompt = "   "
	if _, err := promptContent(req); err == nil {
		t.Error("an empty prompt must be rejected")
	}
}
