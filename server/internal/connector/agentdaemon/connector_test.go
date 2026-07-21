package agentdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeConn is the gateway.WSConn implementation used by these tests.
// Concurrency-safe via a sync.Cond-gated incoming queue.
type fakeConn struct {
	mu       sync.Mutex
	incoming []fakeFrame
	cond     *sync.Cond
	closed   bool

	writes   [][]byte
	writeErr error
}

type fakeFrame struct {
	data []byte
	err  error
}

func newFakeConn() *fakeConn {
	c := &fakeConn{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *fakeConn) Feed(env proto.Envelope) {
	raw, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	c.mu.Lock()
	c.incoming = append(c.incoming, fakeFrame{data: raw})
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	for len(c.incoming) == 0 && !c.closed {
		c.cond.Wait()
	}
	if c.closed && len(c.incoming) == 0 {
		c.mu.Unlock()
		return 0, nil, io.EOF
	}
	f := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	return 1, f.data, nil
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return c.writeErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.writes = append(c.writes, cp)
	return nil
}

func (c *fakeConn) SetReadLimit(int64)               {}
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) Writes() []proto.Envelope {
	c.mu.Lock()
	raw := make([][]byte, len(c.writes))
	copy(raw, c.writes)
	c.mu.Unlock()
	out := make([]proto.Envelope, 0, len(raw))
	for _, r := range raw {
		var env proto.Envelope
		if err := json.Unmarshal(r, &env); err == nil {
			out = append(out, env)
		}
	}
	return out
}

type fakeExecutionRecorder struct {
	mu     sync.Mutex
	inputs []store.RecordAgentRunExecutionSnapshotInput
	err    error
}

func (f *fakeExecutionRecorder) RecordAgentRunExecutionSnapshot(_ context.Context, input store.RecordAgentRunExecutionSnapshotInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, input)
	return f.err
}

func (f *fakeExecutionRecorder) Inputs() []store.RecordAgentRunExecutionSnapshotInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.RecordAgentRunExecutionSnapshotInput, len(f.inputs))
	copy(out, f.inputs)
	return out
}

// ----------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------

// newWiredHarness stands up a Registry, a Session backed by fakeConn, an
// InMemoryBinder pre-seeded with one binding, and the Connector under
// test.
func newWiredHarness(t *testing.T, deviceID, conversationID, agentID string) (*Connector, *gateway.Registry, *gateway.Session, *fakeConn, binding.Binder) {
	t.Helper()
	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, deviceID, "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()

	binder := binding.NewInMemoryBinder()
	if err := binder.Bind(context.Background(), binding.Binding{
		ConversationID: conversationID,
		AgentID:        agentID,
		DeviceID:       deviceID,
		AgentKind:      "claude_code",
		WorkDir:        "/workspace",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	c := New(Config{Registry: reg, Binder: binder})
	return c, reg, sess, conn, binder
}

func basicInput() connector.PromptInput {
	return connector.PromptInput{
		RunID:                 "run-1",
		ConversationID:        "conv-1",
		AgentID:               "pa-1",
		TriggerMessageContent: "hello",
		AgentConfig:           map[string]any{},
	}
}

// ----------------------------------------------------------------------
// agent_kind gate
// ----------------------------------------------------------------------

func TestStreamPrompt_AllowsOpenCodeWhenDeviceAdvertisesKind(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	feedAgentKinds(t, conn, sess, []proto.SupportedAgentKind{
		{Kind: "claude_code", Available: true},
		{Kind: "opencode", Available: true, Version: "opencode 1.4.3", Capabilities: proto.AgentKindCapabilities{Streaming: true, Usage: true}},
	})

	in := basicInput()
	in.AgentConfig = map[string]any{"agent_kind": "opencode"}
	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request was not sent")
	}
	var req proto.PromptRequestPayload
	for _, env := range conn.Writes() {
		if env.Type == proto.TypePromptRequest {
			if err := env.DecodePayload(&req); err != nil {
				t.Fatalf("decode prompt_request: %v", err)
			}
		}
	}
	if req.AgentKind != "opencode" {
		t.Fatalf("prompt_request AgentKind = %q, want opencode", req.AgentKind)
	}
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	_, done := drainEvents(ch, t)
	if done == nil || done.Final == nil || done.Final.Content != "ok" {
		t.Fatalf("expected final ok, got %+v", done)
	}
}

func TestStreamPrompt_RecordsExecutionSnapshotAfterAgentKindValidation(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	recorder := &fakeExecutionRecorder{}
	c.executionRecorder = recorder
	feedAgentKinds(t, conn, sess, []proto.SupportedAgentKind{
		{Kind: "claude_code", Available: true},
		{Kind: "opencode", Available: true, Version: "opencode 1.4.3", Capabilities: proto.AgentKindCapabilities{Streaming: true, Permissions: true, Usage: true, Resume: true}},
	})

	in := basicInput()
	in.AgentConfig = map[string]any{"agent_kind": "opencode"}
	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request was not sent")
	}

	inputs := recorder.Inputs()
	if len(inputs) != 1 {
		t.Fatalf("expected one execution snapshot record, got %+v", inputs)
	}
	got := inputs[0]
	if got.RunID != "run-1" || got.ConnectorType != ConnectorType || got.RuntimeID != "dev-1" || got.DeviceID != "dev-1" {
		t.Fatalf("unexpected execution snapshot identity: %+v", got)
	}
	if got.AgentKind != "opencode" || got.RuntimeMode != "local" || got.WorkingDirectory != "/workspace" || got.ManagedModelID != "" {
		t.Fatalf("unexpected execution snapshot fields: %+v", got)
	}
	if got.Capabilities["streaming"] != true || got.Capabilities["permissions"] != true || got.Capabilities["usage"] != true || got.Capabilities["resume"] != true || got.Capabilities["cancellation"] != true {
		t.Fatalf("unexpected execution snapshot capabilities: %+v", got.Capabilities)
	}

	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	_, done := drainEvents(ch, t)
	if done == nil || done.Final == nil || done.Final.Content != "ok" {
		t.Fatalf("expected final ok, got %+v", done)
	}
}

func TestStreamPrompt_RejectsUnavailableAgentKindFromDeviceHeartbeat(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	feedAgentKinds(t, conn, sess, []proto.SupportedAgentKind{
		{Kind: "claude_code", Available: true},
		{Kind: "opencode", Available: false, Version: "missing", Capabilities: proto.AgentKindCapabilities{Streaming: true}},
	})

	in := basicInput()
	in.AgentConfig = map[string]any{"agent_kind": "opencode"}
	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if !contains(gotErr.Error, ErrUnsupportedAgentKind.Error()) || !contains(gotErr.Error, "unavailable") {
		t.Fatalf("expected unsupported unavailable error, got %q", gotErr.Error)
	}
	for _, env := range conn.Writes() {
		if env.Type == proto.TypePromptRequest {
			t.Fatalf("prompt_request must not be sent for unavailable kind: %+v", env)
		}
	}
}

func TestStreamPrompt_RejectsUnadvertisedAgentKindFromDeviceHeartbeat(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	feedAgentKinds(t, conn, sess, []proto.SupportedAgentKind{
		{Kind: "claude_code", Available: true},
	})

	in := basicInput()
	in.AgentConfig = map[string]any{"agent_kind": "opencode"}
	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if !contains(gotErr.Error, ErrUnsupportedAgentKind.Error()) || !contains(gotErr.Error, "not advertised") {
		t.Fatalf("expected unsupported not-advertised error, got %q", gotErr.Error)
	}
	for _, env := range conn.Writes() {
		if env.Type == proto.TypePromptRequest {
			t.Fatalf("prompt_request must not be sent for unadvertised kind: %+v", env)
		}
	}
}

func TestStreamPrompt_DefaultsToClaudeCodeWhenMissing(t *testing.T) {
	// No agent_kind anywhere — connector defaults to claude_code rather
	// than reject. Binder is empty, so ErrNotBound surfaces as a clean
	// errorChannel rather than a setup failure.
	reg := gateway.NewRegistry()
	c := New(Config{Registry: reg, Binder: binding.NewInMemoryBinder()})
	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("expected nil err, got %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
}

// ----------------------------------------------------------------------
// pre-flight failures
// ----------------------------------------------------------------------

func TestStreamPrompt_NoBindingReturnsErrorChannel(t *testing.T) {
	reg := gateway.NewRegistry()
	c := New(Config{Registry: reg, Binder: binding.NewInMemoryBinder()})
	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil {
		t.Fatalf("expected EventError, got nothing")
	}
	if gotDone == nil {
		t.Fatalf("expected EventDone, got nothing")
	}
	// Message should hint at binding a Runtime.
	if !contains(gotErr.Error, "Runtime") {
		t.Fatalf("error message should mention Runtime binding, got %q", gotErr.Error)
	}
}

func TestStreamPrompt_DeviceOfflineReturnsErrorChannel(t *testing.T) {
	// Bind a conversation to a device that isn't registered. Connector
	// must surface a clean offline error rather than propagating the
	// lookup error.
	reg := gateway.NewRegistry()
	binder := binding.NewInMemoryBinder()
	_ = binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-offline",
		AgentKind: "claude_code",
	})
	c := New(Config{Registry: reg, Binder: binder})
	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	gotErr, _ := drainEvents(ch, t)
	if gotErr == nil || !contains(gotErr.Error, "offline") {
		t.Fatalf("expected offline error, got %+v", gotErr)
	}
}

func TestStreamPrompt_MissingRunIDIsHardError(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	in := basicInput()
	in.RunID = ""
	_, err := c.StreamPrompt(context.Background(), in)
	if err == nil {
		t.Fatal("expected hard error on missing RunID")
	}
}

// ----------------------------------------------------------------------
// happy path
// ----------------------------------------------------------------------

func TestStreamPrompt_HappyPathStreamsDeltasAndDone(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}

	// Wait until the connector has sent the prompt_request frame.
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	// Simulate the daemon's reply: delta, delta, done.
	deltaEnv, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "hello "})
	conn.Feed(deltaEnv)
	deltaEnv2, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "world"})
	conn.Feed(deltaEnv2)
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "hello world"})
	conn.Feed(doneEnv)

	var deltas []string
	var final *connector.PromptOutput
	var seqs []uint64
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if len(deltas) != 2 {
					t.Fatalf("expected 2 deltas, got %d (%v)", len(deltas), deltas)
				}
				if final == nil || final.Content != "hello world" {
					t.Fatalf("expected final content 'hello world', got %+v", final)
				}
				// Sequence must be strictly increasing across delta + done.
				for i := 1; i < len(seqs); i++ {
					if seqs[i] <= seqs[i-1] {
						t.Fatalf("sequences not monotonic: %v", seqs)
					}
				}
				return
			}
			switch ev.Type {
			case connector.EventDelta:
				deltas = append(deltas, ev.Delta)
				seqs = append(seqs, ev.Sequence)
			case connector.EventDone:
				final = ev.Final
				seqs = append(seqs, ev.Sequence)
			case connector.EventError:
				t.Fatalf("unexpected EventError: %s", ev.Error)
			}
		case <-deadline:
			t.Fatal("stream never closed")
		}
	}
}

// TestStreamPrompt_ThinkingRoutedAsEventThinking verifies the
// connector surfaces daemon-side thinking blocks as EventThinking
// rather than collapsing them into EventDelta. Routing thinking into
// the delta channel would make the IM gateway splice the model's
// reasoning into the user-facing reply body.
func TestStreamPrompt_ThinkingRoutedAsEventThinking(t *testing.T) {
	c, reg, sess, conn, _ := newWiredHarness(t, "dev-th", "conv-1", "pa-1")
	defer sess.Close("test done")
	_ = reg

	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	thinkEnv, _ := proto.NewEnvelope(proto.TypeThinking, "run-1", proto.ThinkingPayload{
		Text: "The user wants X. I should respond by Y.",
	})
	conn.Feed(thinkEnv)
	deltaEnv, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "ok"})
	conn.Feed(deltaEnv)
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)

	var thinking, deltas []string
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if len(thinking) != 1 || thinking[0] != "The user wants X. I should respond by Y." {
					t.Fatalf("thinking events = %v, want exactly the daemon-side text", thinking)
				}
				if len(deltas) != 1 || deltas[0] != "ok" {
					t.Fatalf("delta events = %v, want a single 'ok' (thinking must NOT leak into delta)", deltas)
				}
				return
			}
			switch ev.Type {
			case connector.EventThinking:
				thinking = append(thinking, ev.Thinking)
			case connector.EventDelta:
				deltas = append(deltas, ev.Delta)
			case connector.EventError:
				t.Fatalf("unexpected EventError: %s", ev.Error)
			}
		case <-deadline:
			t.Fatal("stream never closed")
		}
	}
}

func TestStreamPrompt_PermissionRequestPropagates(t *testing.T) {
	c, reg, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	// Daemon emits a permission_request. Envelope.ID is the perm id,
	// not the runID. The gateway dispatch path indexes it in the
	// registry's perm map but does NOT fan it to the runID subscriber.
	permEnv, _ := proto.NewEnvelope(proto.TypePermissionRequest, "perm-xyz", proto.PermissionRequestPayload{
		Tool: "Bash", Title: "rm -rf /tmp/scratch",
	})
	conn.Feed(permEnv)

	// Confirm the registry sees the perm so SubmitPermission can find it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, lookupErr := reg.LookupPermission("perm-xyz"); lookupErr == nil && got == sess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("perm-xyz never indexed in registry")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Daemon ends the run; the subscriber should see EventDone and close.
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: ""})
	conn.Feed(doneEnv)

	gotDone := false
	dl := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if !gotDone {
					t.Fatal("channel closed before EventDone")
				}
				return
			}
			if ev.Type == connector.EventDone {
				gotDone = true
			}
		case <-dl:
			t.Fatal("EventDone never delivered")
		}
	}
}

func TestStreamPrompt_UserChoiceMetadataPropagates(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)
	autoResolutionMs := uint64(90_000)
	askEnv, _ := proto.NewEnvelope(proto.TypePromptForUserChoice, "run-1", proto.PromptForUserChoicePayload{
		AskID: "ask-metadata", AutoResolutionMs: &autoResolutionMs,
		Questions: []proto.PromptForUserChoiceQuestion{{
			ID: "token", Question: "Token?", IsOther: true, IsSecret: true,
		}},
	})
	conn.Feed(askEnv)
	select {
	case ev := <-ch:
		if ev.PromptForUserChoice == nil || len(ev.PromptForUserChoice.Questions) != 1 {
			t.Fatalf("user choice event = %+v", ev)
		}
		question := ev.PromptForUserChoice.Questions[0]
		if !question.IsOther || !question.IsSecret || ev.PromptForUserChoice.AutoResolutionMs == nil || *ev.PromptForUserChoice.AutoResolutionMs != autoResolutionMs {
			t.Fatalf("user choice metadata = %+v", ev.PromptForUserChoice)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("user choice event timed out")
	}
}

// ----------------------------------------------------------------------
// Abort / SubmitPermission / Close
// ----------------------------------------------------------------------

func TestAbort_SendsPromptCancelEnvelope(t *testing.T) {
	c, reg, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	// Start a stream so the registry has a run mapping for "run-1".
	if _, err := c.StreamPrompt(context.Background(), basicInput()); err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	// Sanity check: run-1 indexed in registry.
	if got := reg.LookupRun("run-1"); got != sess {
		t.Fatalf("run-1 not indexed, got %p", got)
	}

	if err := c.Abort(context.Background(), connector.AbortInput{RunID: "run-1"}); err != nil {
		t.Fatalf("Abort: %v", err)
	}
	// A prompt_cancel envelope must hit the wire (in addition to the
	// earlier prompt_request).
	if !waitForWrite(t, conn, proto.TypePromptCancel, 2*time.Second) {
		t.Fatal("prompt_cancel never written")
	}
}

func TestAbort_UnknownRunIDIsNoOp(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	if err := c.Abort(context.Background(), connector.AbortInput{RunID: "nope"}); err != nil {
		t.Fatalf("unknown runID should be no-op, got %v", err)
	}
}

func TestSubmitPermission_RoutesThroughRegistry(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	// Open a stream and let the daemon emit a permission_request.
	if _, err := c.StreamPrompt(context.Background(), basicInput()); err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	permEnv, _ := proto.NewEnvelope(proto.TypePermissionRequest, "perm-xyz", proto.PermissionRequestPayload{
		Tool: "Bash", Title: "ls",
	})
	conn.Feed(permEnv)

	// Wait for the perm to be indexed.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := sess.Subscribe("dummy-poll"); err == nil {
			sess.Unsubscribe("dummy-poll")
		}
		if time.Now().After(deadline) {
			break
		}
		// Cheap poll for the perm registration.
		_, lookupErr := c.registry.LookupPermission("perm-xyz")
		if lookupErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := submitPermissionAndAck(t, c, conn, connector.PermissionDecision{
		RequestID: "perm-xyz", Approved: true, Note: "lgtm",
	}, true, ""); err != nil {
		t.Fatalf("SubmitPermission: %v", err)
	}

	// The decision must hit the wire.
	if !waitForWrite(t, conn, proto.TypePermissionDecision, 2*time.Second) {
		t.Fatal("permission_decision never written")
	}

	// A second SubmitPermission for the same id must fail (perm cleared).
	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-xyz", Approved: true,
	})
	if err == nil {
		t.Fatal("expected error on duplicate SubmitPermission, got nil")
	}
}

func TestSubmitPermission_DoesNotSucceedBeforeRuntimeAck(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	if _, err := c.StreamPrompt(context.Background(), basicInput()); err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)
	permEnv, _ := proto.NewEnvelope(proto.TypePermissionRequest, "perm-rejected", proto.PermissionRequestPayload{Tool: "Bash"})
	conn.Feed(permEnv)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := c.registry.LookupPermission("perm-rejected"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("permission registration timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}

	err := submitPermissionAndAck(t, c, conn, connector.PermissionDecision{
		RequestID: "perm-rejected", Approved: true,
	}, false, "runtime_error")
	if !errors.Is(err, connector.ErrInteractionRuntimeUnavailable) {
		t.Fatalf("SubmitPermission error = %v, want ErrInteractionRuntimeUnavailable", err)
	}
}

func TestSubmitPermission_UnknownIDReturnsError(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "ghost", Approved: true,
	})
	if err == nil {
		t.Fatal("expected error for unknown perm id, got nil")
	}
}

func TestSubmitPermission_RequiresRequestID(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{Approved: true})
	if err == nil {
		t.Fatal("expected error for empty RequestID, got nil")
	}
}

func TestSubmitPromptForUserChoice_WaitsForRuntimeAck(t *testing.T) {
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	askEnv, _ := proto.NewEnvelope(proto.TypePromptForUserChoice, "run-1", proto.PromptForUserChoicePayload{
		AskID: "ask-xyz", Questions: []proto.PromptForUserChoiceQuestion{{ID: "q0", Question: "Continue?"}},
	})
	conn.Feed(askEnv)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := c.registry.LookupPromptForUserChoice("ask-xyz"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("user-input registration timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan error, 1)
	go func() {
		done <- c.SubmitPromptForUserChoice(context.Background(), connector.PromptForUserChoiceDecision{
			RequestID: "ask-xyz", QuestionAnswers: []connector.PromptForUserChoiceQuestionAnswer{{QuestionID: "q0", Answers: []string{"yes"}}},
		})
	}()
	var decisionEnv proto.Envelope
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range conn.Writes() {
			if env.Type == proto.TypePromptForUserChoiceDecision && env.ID == "ask-xyz" {
				decisionEnv = env
				break
			}
		}
		if decisionEnv.Type != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if decisionEnv.Type == "" {
		t.Fatal("prompt_for_user_choice_decision never written")
	}
	select {
	case err := <-done:
		t.Fatalf("SubmitPromptForUserChoice returned before ack: %v", err)
	default:
	}
	var payload proto.PromptForUserChoiceDecisionPayload
	if err := decisionEnv.DecodePayload(&payload); err != nil {
		t.Fatalf("decode user-input decision: %v", err)
	}
	ack, _ := proto.NewEnvelope(proto.TypeInteractionDecisionAck, "ask-xyz", proto.InteractionDecisionAckPayload{
		DeliveryID: payload.DeliveryID, Applied: true,
	})
	conn.Feed(ack)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SubmitPromptForUserChoice: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitPromptForUserChoice did not return after ack")
	}
}

func TestSubmitPermission_AckTimeoutIsRetryableAndLateAckIsIsolated(t *testing.T) {
	oldTimeout := gateway.InteractionAckTimeout
	gateway.InteractionAckTimeout = 25 * time.Millisecond
	defer func() { gateway.InteractionAckTimeout = oldTimeout }()
	c, _, sess, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")
	permEnv, _ := proto.NewEnvelope(proto.TypePermissionRequest, "perm-timeout", proto.PermissionRequestPayload{Tool: "Bash"})
	conn.Feed(permEnv)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := c.registry.LookupPermission("perm-timeout"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("permission registration timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{RequestID: "perm-timeout", Approved: false})
	if !errors.Is(err, connector.ErrInteractionRuntimeUnavailable) {
		t.Fatalf("SubmitPermission error = %v, want retryable runtime unavailable", err)
	}
	if _, err := c.registry.LookupPermission("perm-timeout"); err != nil {
		t.Fatalf("permission mapping was detached after missing ack: %v", err)
	}
	var firstDeliveryID string
	for _, env := range conn.Writes() {
		if env.Type != proto.TypePermissionDecision || env.ID != "perm-timeout" {
			continue
		}
		var payload proto.PermissionDecisionPayload
		if err := env.DecodePayload(&payload); err != nil {
			t.Fatalf("decode permission decision: %v", err)
		}
		firstDeliveryID = payload.DeliveryID
	}
	if !strings.HasPrefix(firstDeliveryID, "permission:perm-timeout:") {
		t.Fatalf("first delivery id = %q, want request-derived base plus unique attempt", firstDeliveryID)
	}

	// A retry uses a different transport id. An ack for the timed-out first
	// attempt must not satisfy this newer waiter, especially when the human
	// changed the decision while the first result was uncertain.
	gateway.InteractionAckTimeout = 2 * time.Second
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- c.SubmitPermission(context.Background(), connector.PermissionDecision{
			RequestID: "perm-timeout", Approved: true,
		})
	}()
	var secondDeliveryID string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range conn.Writes() {
			if env.Type != proto.TypePermissionDecision || env.ID != "perm-timeout" {
				continue
			}
			var payload proto.PermissionDecisionPayload
			if err := env.DecodePayload(&payload); err != nil {
				t.Fatalf("decode retry permission decision: %v", err)
			}
			if payload.DeliveryID != firstDeliveryID {
				secondDeliveryID = payload.DeliveryID
			}
		}
		if secondDeliveryID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if secondDeliveryID == "" || secondDeliveryID == firstDeliveryID {
		t.Fatalf("retry delivery id = %q, first = %q", secondDeliveryID, firstDeliveryID)
	}
	lateAck, _ := proto.NewEnvelope(proto.TypeInteractionDecisionAck, "perm-timeout", proto.InteractionDecisionAckPayload{
		DeliveryID: firstDeliveryID, Applied: true,
	})
	conn.Feed(lateAck)
	select {
	case err := <-secondDone:
		t.Fatalf("new attempt consumed stale ack: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	conflictAck, _ := proto.NewEnvelope(proto.TypeInteractionDecisionAck, "perm-timeout", proto.InteractionDecisionAckPayload{
		DeliveryID: secondDeliveryID, Applied: false, ErrorCode: "decision_conflict", Error: "decision_conflict",
	})
	conn.Feed(conflictAck)
	select {
	case err := <-secondDone:
		if !errors.Is(err, connector.ErrInteractionNoLongerPending) {
			t.Fatalf("retry error = %v, want no longer pending conflict", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not return after its own ack")
	}
}

func TestClose_InvalidatesBinder(t *testing.T) {
	c, _, sess, _, binder := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer sess.Close("test done")

	if err := c.Close(context.Background(), "conv-1"); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code"); !errors.Is(err, binding.ErrNotBound) {
		t.Fatalf("expected ErrNotBound after Close, got %v", err)
	}
}

// ----------------------------------------------------------------------
// Capabilities / metadata
// ----------------------------------------------------------------------

func TestCapabilities_AdvertisesFullFeatureSet(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	caps := c.Capabilities()
	if !caps.Sync || !caps.Streaming || !caps.Cancellation || !caps.Permissions || !caps.Usage || !caps.Audit {
		t.Fatalf("expected all primary caps true, got %+v", caps)
	}
	if caps.Auth {
		t.Fatal("Auth must be false (daemon does not refresh credentials)")
	}
}

func TestType_ReturnsAgentDaemon(t *testing.T) {
	c := New(Config{Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder()})
	if c.Type() != "agent_daemon" {
		t.Fatalf("Type() = %q; want agent_daemon", c.Type())
	}
}

// ----------------------------------------------------------------------
// small helpers
// ----------------------------------------------------------------------

// drainEvents reads one EventError + one EventDone (in either order)
// from a pre-flight failure channel, then waits for the channel to
// close. Used by no-binding / device-offline assertions.
func drainEvents(ch <-chan connector.PromptEvent, t *testing.T) (*connector.PromptEvent, *connector.PromptEvent) {
	t.Helper()
	var gotErr, gotDone *connector.PromptEvent
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return gotErr, gotDone
			}
			cp := ev
			switch ev.Type {
			case connector.EventError:
				gotErr = &cp
			case connector.EventDone:
				gotDone = &cp
			}
		case <-deadline:
			t.Fatal("drainEvents: channel never closed")
			return nil, nil
		}
	}
}

// waitForWrite polls fakeConn.Writes() for an envelope of the given
// type and returns true once found. Used by tests that need to confirm
// the connector wrote something on the wire (prompt_request,
// prompt_cancel, permission_decision).
func waitForWrite(t *testing.T, conn *fakeConn, typ string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, env := range conn.Writes() {
			if env.Type == typ {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func submitPermissionAndAck(t *testing.T, c *Connector, conn *fakeConn, decision connector.PermissionDecision, applied bool, errorCode string) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- c.SubmitPermission(context.Background(), decision) }()
	var decisionEnv proto.Envelope
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range conn.Writes() {
			if env.Type == proto.TypePermissionDecision && env.ID == decision.RequestID {
				decisionEnv = env
				break
			}
		}
		if decisionEnv.Type != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if decisionEnv.Type == "" {
		t.Fatal("permission_decision never written")
	}
	var payload proto.PermissionDecisionPayload
	if err := decisionEnv.DecodePayload(&payload); err != nil {
		t.Fatalf("decode permission decision: %v", err)
	}
	ack, _ := proto.NewEnvelope(proto.TypeInteractionDecisionAck, decision.RequestID, proto.InteractionDecisionAckPayload{
		DeliveryID: payload.DeliveryID, Applied: applied, ErrorCode: errorCode, Error: errorCode,
	})
	conn.Feed(ack)
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitPermission did not return after runtime ack")
		return nil
	}
}

// contains is a tiny inline strings.Contains so we don't import strings
// for one assertion.
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ----------------------------------------------------------------------
// Sandbox lazy-create branch
// ----------------------------------------------------------------------

// stubSandboxProvider lets tests inject scripted Acquire/Release behaviour
// for the connector's sandbox lazy-create path. Records every call so
// assertions can check that Acquire fired (and only fired when the
// agent was sandbox-mode).
type stubSandboxProvider struct {
	mu           sync.Mutex
	acquireCalls int
	releaseCalls int
	deviceID     string
	acquireErr   error
}

func (s *stubSandboxProvider) Acquire(_ context.Context, _ connector.PromptInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquireCalls++
	if s.acquireErr != nil {
		return "", s.acquireErr
	}
	return s.deviceID, nil
}

func (s *stubSandboxProvider) Release(_ context.Context, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseCalls++
	return nil
}

func (s *stubSandboxProvider) SandboxStatus(_ context.Context, _ string) (connector.SandboxInfo, bool, error) {
	return connector.SandboxInfo{}, false, nil
}

func (s *stubSandboxProvider) Renew(_ context.Context, _ string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (s *stubSandboxProvider) SandboxRuntimeInfo(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}

func (s *stubSandboxProvider) Reap(_ context.Context) (int, error) { return 0, nil }

// TestStreamPrompt_LocalModeNoBindingFallsThroughToErrorChannel: when
// the agent has no binding and no configured device, the
// connector returns the "Please bind a Runtime" hint and the sandbox provider
// is never asked.
func TestStreamPrompt_LocalModeNoBindingFallsThroughToErrorChannel(t *testing.T) {
	reg := gateway.NewRegistry()
	sb := &stubSandboxProvider{}
	c := New(Config{Registry: reg, Binder: binding.NewInMemoryBinder(), Sandbox: sb})

	in := basicInput()
	// daemon_mode unset → local picker mode, sandbox path should NOT fire
	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if !contains(gotErr.Error, "Runtime") {
		t.Fatalf("expected 'Runtime' hint, got %q", gotErr.Error)
	}
	if sb.acquireCalls != 0 {
		t.Fatalf("sandbox Acquire MUST NOT fire in local mode; saw %d calls", sb.acquireCalls)
	}
}

func TestStreamPrompt_LocalModeConfiguredDeviceBindsConversation(t *testing.T) {
	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, "dev-picked", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()
	defer sess.Close("test done")

	binder := binding.NewInMemoryBinder()
	c := New(Config{Registry: reg, Binder: binder, Sandbox: &stubSandboxProvider{deviceID: "should-not-be-used"}})

	in := basicInput()
	in.AgentConfig = map[string]any{
		"device_id":  "dev-picked",
		"agent_kind": "claude_code",
		"work_dir":   "/repo/parsar",
	}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request was not sent to configured device")
	}
	writes := conn.Writes()
	if len(writes) == 0 {
		t.Fatal("expected at least one write")
	}
	var req proto.PromptRequestPayload
	if err := writes[0].DecodePayload(&req); err != nil {
		t.Fatalf("decode prompt_request: %v", err)
	}
	if req.WorkDir != "/repo/parsar" || req.AgentKind != "claude_code" {
		t.Fatalf("prompt_request mismatch: %+v", req)
	}

	b, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("binder.Resolve after configured device: %v", err)
	}
	if b.DeviceID != "dev-picked" || b.WorkDir != "/repo/parsar" {
		t.Fatalf("binding mismatch: %+v", b)
	}

	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	var final *connector.PromptOutput
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if final == nil || final.Content != "ok" {
					t.Fatalf("expected final ok, got %+v", final)
				}
				return
			}
			if ev.Type == connector.EventDone {
				final = ev.Final
			}
		case <-deadline:
			t.Fatal("stream never closed")
		}
	}
}

// TestStreamPrompt_LocalModeConfiguredDeviceBindsWithWorkdirKey is the
// sibling of LocalModeConfiguredDeviceBindsConversation but uses the
// `workdir` (no underscore) key that store.ConfigureDevAgentConnector
// actually writes into agents.config. Locking this in keeps the
// "user sets work_dir once in the UI, all conversations use it" path from
// silently breaking if someone re-tightens configuredDeviceBinding to only
// read the snake_case alias.
func TestStreamPrompt_LocalModeConfiguredDeviceBindsWithWorkdirKey(t *testing.T) {
	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, "dev-picked", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()
	defer sess.Close("test done")

	binder := binding.NewInMemoryBinder()
	c := New(Config{Registry: reg, Binder: binder, Sandbox: &stubSandboxProvider{deviceID: "should-not-be-used"}})

	in := basicInput()
	in.AgentConfig = map[string]any{
		"device_id":  "dev-picked",
		"agent_kind": "claude_code",
		"workdir":    "/repo/parsar",
	}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt_request was not sent to configured device")
	}
	writes := conn.Writes()
	if len(writes) == 0 {
		t.Fatal("expected at least one write")
	}
	var req proto.PromptRequestPayload
	if err := writes[0].DecodePayload(&req); err != nil {
		t.Fatalf("decode prompt_request: %v", err)
	}
	if req.WorkDir != "/repo/parsar" {
		t.Fatalf("prompt_request work_dir mismatch: got %q, want %q (workdir alias should be honored)", req.WorkDir, "/repo/parsar")
	}

	b, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("binder.Resolve after configured device: %v", err)
	}
	if b.WorkDir != "/repo/parsar" {
		t.Fatalf("binding work_dir mismatch: got %q, want %q", b.WorkDir, "/repo/parsar")
	}

	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	conn.Feed(doneEnv)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("stream never closed")
		}
	}
}

// TestStreamPrompt_SandboxModeIsNoLongerAutoAcquired pins the
// contract: even when AgentConfig.daemon_mode == "sandbox",
// the connector does NOT auto-acquire a sandbox. The default dispatch
// path requires an explicit runtime binding
// (agents.runtime_id) surfaced via AgentConfig.device_id.
// The sandbox provider stays compiled for a future conversation-scoped
// ephemeral path but is disconnected from the default first-prompt flow.
//
// Sandbox-mode agents that hit dispatch before runtime_id is written
// see a sandbox-aware hint ("sandbox is preparing") instead of the generic
// "no Runtime bound" copy.
func TestStreamPrompt_SandboxModeIsNoLongerAutoAcquired(t *testing.T) {
	reg := gateway.NewRegistry()
	sb := &stubSandboxProvider{deviceID: "dev-sbx-abc"}
	binder := binding.NewInMemoryBinder()
	c := New(Config{Registry: reg, Binder: binder, Sandbox: sb})

	in := basicInput()
	// daemon_mode=sandbox alone is no longer a free pass to auto-Acquire.
	in.AgentConfig = map[string]any{"daemon_mode": "sandbox"}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if sb.acquireCalls != 0 {
		t.Fatalf("sandbox Acquire MUST NOT fire on the default path; saw %d calls", sb.acquireCalls)
	}
	// Sandbox-mode unbound surfaces a sandbox-preparation hint — the
	// createAgent goroutine is racing to write runtime_id back, and a
	// user retry in ~10s should land on a healthy binding.
	if !contains(gotErr.Error, "sandbox is preparing") {
		t.Fatalf("expected sandbox-preparation hint, got %q", gotErr.Error)
	}
	// And nothing got bound.
	if _, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code"); err == nil {
		t.Fatalf("expected no binding after refusing auto-acquire; one was persisted")
	}
}

// TestStreamPrompt_SandboxModeProviderDisabled_StillRefusesAutoAcquire:
// no sandbox provider wired + sandbox mode requested → user still sees
// a sandbox-aware hint. Both routes converge on SandboxBindingReader's
// nil-reader fallback which only knows about bindings, not provider
// wiring.
func TestStreamPrompt_SandboxModeProviderDisabled_StillRefusesAutoAcquire(t *testing.T) {
	reg := gateway.NewRegistry()
	// No Sandbox in Config → New defaults to NoopSandboxProvider
	c := New(Config{Registry: reg, Binder: binding.NewInMemoryBinder()})

	in := basicInput()
	in.AgentConfig = map[string]any{"daemon_mode": "sandbox"}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if !contains(gotErr.Error, "sandbox is preparing") {
		t.Fatalf("expected sandbox-preparation hint, got %q", gotErr.Error)
	}
}

type fakeOwnerResolver struct {
	owner store.AgentDaemonDeviceOwnerRead
	ok    bool
}

func (f fakeOwnerResolver) GetAgentDaemonDeviceOwner(context.Context, string) (store.AgentDaemonDeviceOwnerRead, bool, error) {
	return f.owner, f.ok, nil
}

type fakeRemoteStreamer struct {
	called bool
	owner  store.AgentDaemonDeviceOwnerRead
	input  connector.PromptInput
}

func (f *fakeRemoteStreamer) StreamPromptRemote(_ context.Context, owner store.AgentDaemonDeviceOwnerRead, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	f.called = true
	f.owner = owner
	f.input = in
	ch := make(chan connector.PromptEvent, 1)
	ch <- connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{Content: "remote ok"}}
	close(ch)
	return ch, nil
}

func TestStreamPrompt_RoutesToRemoteOwnerPod(t *testing.T) {
	binder := binding.NewInMemoryBinder()
	if err := binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-1",
		AgentID:        "pa-1",
		DeviceID:       "11111111-1111-1111-1111-111111111111",
		AgentKind:      "claude_code",
	}); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	remote := &fakeRemoteStreamer{}
	owner := store.AgentDaemonDeviceOwnerRead{
		DeviceID:       "11111111-1111-1111-1111-111111111111",
		WorkspaceID:    "wks-1",
		OwnerPodID:     "pod-b",
		OwnerURL:       "http://pod-b",
		Generation:     3,
		Status:         store.AgentDaemonOwnerStatusConnected,
		LeaseExpiresAt: time.Now().Add(time.Minute),
	}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binder,
		OwnerResolver: fakeOwnerResolver{owner: owner, ok: true},
		OwnerPodID:    "pod-a",
		Remote:        remote,
	})

	ch, err := c.StreamPrompt(context.Background(), basicInput())
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	_, done := drainEvents(ch, t)
	if done == nil || done.Final == nil || done.Final.Content != "remote ok" {
		t.Fatalf("unexpected remote stream final: %+v", done)
	}
	if !remote.called {
		t.Fatal("remote streamer was not called")
	}
	if remote.owner.OwnerPodID != "pod-b" || remote.input.RunID != "run-1" {
		t.Fatalf("remote call mismatch owner=%+v input=%+v", remote.owner, remote.input)
	}
}

// ----------------------------------------------------------------------
// lazy bind from agent.config.device_id
// ----------------------------------------------------------------------

// TestStreamPrompt_LazyBindsFromAgentConfigDeviceID is the
// connector half of the device_id roundtrip. store.CreateAgent persists
// the picked device_id on agents.config; on first prompt the
// connector must materialize that into a connector_session_bindings
// row (via binder.Bind) so subsequent turns Resolve() fast.
//
// We assert the full pipeline still streams (deltas + done) AND that
// the binder now contains the bound conversation — streaming alone
// could mean lazy-bind ran but the binder mutation didn't stick;
// Resolve alone wouldn't prove end-to-end health.
func TestStreamPrompt_LazyBindsFromAgentConfigDeviceID(t *testing.T) {
	reg := gateway.NewRegistry()
	conn := newFakeConn()
	sess := gateway.NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("unexpected displaced session: %p", prev)
	}
	sess.Start()
	defer sess.Close("test done")

	binder := binding.NewInMemoryBinder()
	c := New(Config{Registry: reg, Binder: binder})

	in := basicInput()
	// Empty binder + device_id in config = the lazy bind path. Sandbox
	// is NOT set, so we strictly test that code.
	in.AgentConfig = map[string]any{"device_id": "dev-1"}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}

	waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second)

	deltaEnv, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "hi"})
	conn.Feed(deltaEnv)
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "hi"})
	conn.Feed(doneEnv)

	var sawDelta bool
	var final *connector.PromptOutput
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break loop
			}
			switch ev.Type {
			case connector.EventDelta:
				sawDelta = true
			case connector.EventDone:
				final = ev.Final
			case connector.EventError:
				t.Fatalf("unexpected EventError after lazy bind: %s", ev.Error)
			}
		case <-deadline:
			t.Fatal("stream never closed after lazy bind")
		}
	}
	if !sawDelta || final == nil || final.Content != "hi" {
		t.Fatalf("lazy-bound stream incomplete: sawDelta=%v final=%+v", sawDelta, final)
	}

	// The mutation the production fix is actually about: the binder
	// now resolves the conversation to the chosen device. If this
	// regresses, every subsequent turn would re-hit ErrNotBound and
	// re-bind on the slow path, which mostly works but is wrong by
	// design (see "lazy on first prompt" decision in the plan).
	bind, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code")
	if err != nil {
		t.Fatalf("expected binder.Resolve to succeed after lazy bind, got %v", err)
	}
	if bind.DeviceID != "dev-1" {
		t.Fatalf("lazy-bound DeviceID = %q, want %q", bind.DeviceID, "dev-1")
	}
	if bind.ConversationID != "conv-1" || bind.AgentID != "pa-1" {
		t.Fatalf("lazy-bound key mismatch: conv=%q agent=%q", bind.ConversationID, bind.AgentID)
	}
	// AgentKind must be populated so the daemon's prompt_request
	// envelope routes to the right runner. Default ("claude_code")
	// flows through resolveAgentKind when AgentConfig doesn't
	// override it.
	if bind.AgentKind != "claude_code" {
		t.Fatalf("lazy-bound AgentKind = %q, want %q", bind.AgentKind, "claude_code")
	}
}

// TestStreamPrompt_NoDeviceConfigStillReportsPickDevice is the
// regression pin for the legacy "pick a device" branch. Adding the
// new lazy-bind case is a behavioral change in the ErrNotBound
// fan-out; this test makes sure the historical fallback still fires
// when neither sandbox mode nor a config device_id is present (i.e.
// raw API callers / pre-fix agent rows still get the actionable
// error instead of a silent failure or a panic on missing key).
func TestStreamPrompt_NoDeviceConfigStillReportsPickDevice(t *testing.T) {
	reg := gateway.NewRegistry()
	binder := binding.NewInMemoryBinder()
	c := New(Config{Registry: reg, Binder: binder})

	in := basicInput()
	// Non-empty AgentConfig but explicitly no device_id key —
	// stronger than basicInput()'s empty map: it proves that
	// defaultDeviceIDFromConfig handles the "key absent" map.(string,bool)
	// branch the same as the empty-map case.
	in.AgentConfig = map[string]any{"some_other_key": "value"}

	ch, err := c.StreamPrompt(context.Background(), in)
	if err != nil {
		t.Fatalf("StreamPrompt: %v", err)
	}
	gotErr, gotDone := drainEvents(ch, t)
	if gotErr == nil || gotDone == nil {
		t.Fatalf("expected EventError + EventDone, got err=%v done=%v", gotErr, gotDone)
	}
	if !contains(gotErr.Error, "Runtime") {
		t.Fatalf("expected 'Runtime' hint, got %q", gotErr.Error)
	}

	// And: nothing should have been written to the binder. If the new
	// switch ever falls through to Bind with empty device_id, this
	// would catch it before it corrupts a fresh row.
	if _, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code"); !errors.Is(err, binding.ErrNotBound) {
		t.Fatalf("binder must remain unbound after 'pick a device' error, got %v", err)
	}

	// Whitespace-only device_id is the other not-actually-set case
	// (frontends sometimes send "   "); trimming must reduce it to
	// "" so we still take the pick-a-device branch instead of
	// trying to bind to a junk device id.
	in2 := basicInput()
	in2.RunID = "run-2"
	in2.ConversationID = "conv-2"
	in2.AgentConfig = map[string]any{"device_id": "   "}
	ch2, err := c.StreamPrompt(context.Background(), in2)
	if err != nil {
		t.Fatalf("StreamPrompt #2: %v", err)
	}
	gotErr2, gotDone2 := drainEvents(ch2, t)
	if gotErr2 == nil || gotDone2 == nil {
		t.Fatalf("expected EventError + EventDone for whitespace device_id, got err=%v done=%v", gotErr2, gotDone2)
	}
	if !contains(gotErr2.Error, "Runtime") {
		t.Fatalf("whitespace device_id must take Runtime-hint branch, got %q", gotErr2.Error)
	}
}

// ----------------------------------------------------------------------
// Submit owner routing (Permission + PromptForUserChoice)
// ----------------------------------------------------------------------

// fakeSubmitSlots maps request id → device id, mirroring the
// store-side reverse lookup the production wiring uses. Either map can
// be nil; a missing key returns ErrUnknownConversation (the slot is
// gone) so the connector can exercise the "slot already cleared"
// fallback path.
type fakeSubmitSlots struct {
	perms map[string]string
	asks  map[string]string
}

func (f fakeSubmitSlots) DeviceIDForPermissionRequest(_ context.Context, requestID string) (string, error) {
	if f.perms == nil {
		return "", store.ErrUnknownConversation
	}
	v, ok := f.perms[requestID]
	if !ok {
		return "", store.ErrUnknownConversation
	}
	return v, nil
}

func (f fakeSubmitSlots) DeviceIDForPromptForUserChoiceRequest(_ context.Context, requestID string) (string, error) {
	if f.asks == nil {
		return "", store.ErrUnknownConversation
	}
	v, ok := f.asks[requestID]
	if !ok {
		return "", store.ErrUnknownConversation
	}
	return v, nil
}

type fakeRemoteSubmitter struct {
	permCalled bool
	permOwner  store.AgentDaemonDeviceOwnerRead
	permDec    connector.PermissionDecision

	askCalled bool
	askOwner  store.AgentDaemonDeviceOwnerRead
	askDec    connector.PromptForUserChoiceDecision

	permErr error
	askErr  error
}

func (f *fakeRemoteSubmitter) SubmitPermissionRemote(_ context.Context, owner store.AgentDaemonDeviceOwnerRead, dec connector.PermissionDecision) error {
	f.permCalled = true
	f.permOwner = owner
	f.permDec = dec
	return f.permErr
}

func (f *fakeRemoteSubmitter) SubmitPromptForUserChoiceRemote(_ context.Context, owner store.AgentDaemonDeviceOwnerRead, dec connector.PromptForUserChoiceDecision) error {
	f.askCalled = true
	f.askOwner = owner
	f.askDec = dec
	return f.askErr
}

func remoteOwner(deviceID string) store.AgentDaemonDeviceOwnerRead {
	return store.AgentDaemonDeviceOwnerRead{
		DeviceID:       deviceID,
		WorkspaceID:    "wks-1",
		OwnerPodID:     "pod-b",
		OwnerURL:       "http://pod-b",
		Generation:     7,
		Status:         store.AgentDaemonOwnerStatusConnected,
		LeaseExpiresAt: time.Now().Add(time.Minute),
	}
}

// TestSubmitPermission_ForwardsToRemoteOwnerPod pins the multi-pod bug
// the Phase 2 fix exists for: webhook lands on pod-a, permission was
// registered on pod-b, SubmitPermission must POST the decision over.
func TestSubmitPermission_ForwardsToRemoteOwnerPod(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	owner := remoteOwner("dev-1")
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: owner, ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		SubmitSlots:   fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}},
	})

	if err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-x", Approved: true, Note: "lgtm",
	}); err != nil {
		t.Fatalf("SubmitPermission: %v", err)
	}
	if !remote.permCalled {
		t.Fatal("remote SubmitPermissionRemote was not called")
	}
	if remote.permOwner.OwnerPodID != "pod-b" {
		t.Fatalf("remote called with owner=%+v, want pod-b", remote.permOwner)
	}
	if remote.permDec.RequestID != "perm-x" || !remote.permDec.Approved || remote.permDec.Note != "lgtm" {
		t.Fatalf("decision payload mismatch: %+v", remote.permDec)
	}
}

func TestSubmitPermission_UsesCanonicalDeviceWithoutIMSlot(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	c := New(Config{
		Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-canonical"), ok: true},
		OwnerPodID:    "pod-a", RemoteSubmit: remote,
	})
	if err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-canonical", DeviceID: "dev-canonical", Approved: true,
	}); err != nil {
		t.Fatalf("SubmitPermission: %v", err)
	}
	if !remote.permCalled || remote.permOwner.DeviceID != "dev-canonical" || remote.permDec.DeviceID != "dev-canonical" {
		t.Fatalf("remote submission = called:%v owner:%+v decision:%+v", remote.permCalled, remote.permOwner, remote.permDec)
	}
}

// TestSubmitPermission_ThisPodHandlesLocally covers the "I am the
// owner" branch — owner check passes, so we must NOT POST out, we must
// fall into the registry lookup path. A missing perm registration
// surfaces as the regular not-pending error.
func TestSubmitPermission_ThisPodHandlesLocally(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	owner := remoteOwner("dev-1")
	owner.OwnerPodID = "pod-a" // SAME as ownerPodID below
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: owner, ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		SubmitSlots:   fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}},
	})

	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-x", Approved: true,
	})
	if err == nil {
		t.Fatal("expected NotRegistered error from local lookup, got nil")
	}
	if remote.permCalled {
		t.Fatal("remote must NOT be called when this pod is the owner")
	}
}

// TestSubmitPermission_NoOwnerResolverIsSinglePod is the back-compat
// case: tests / single-pod deployments leave OwnerResolver nil, the
// owner-check is skipped, and Local lookup runs verbatim.
func TestSubmitPermission_NoOwnerResolverIsSinglePod(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	c := New(Config{
		Registry:     gateway.NewRegistry(),
		Binder:       binding.NewInMemoryBinder(),
		RemoteSubmit: remote,
		SubmitSlots:  fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}},
	})

	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-x", Approved: true,
	})
	if err == nil {
		t.Fatal("expected local NotRegistered, got nil")
	}
	if remote.permCalled {
		t.Fatal("remote must NOT be called without OwnerResolver")
	}
}

// TestSubmitPromptForUserChoice_ForwardsToRemoteOwnerPod mirrors the
// permission test for the ask-user-question path.
func TestSubmitPromptForUserChoice_ForwardsToRemoteOwnerPod(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	owner := remoteOwner("dev-2")
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: owner, ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		SubmitSlots:   fakeSubmitSlots{asks: map[string]string{"ask-y": "dev-2"}},
	})

	if err := c.SubmitPromptForUserChoice(context.Background(), connector.PromptForUserChoiceDecision{
		RequestID: "ask-y", Answers: []string{"yes"},
	}); err != nil {
		t.Fatalf("SubmitPromptForUserChoice: %v", err)
	}
	if !remote.askCalled {
		t.Fatal("remote SubmitPromptForUserChoiceRemote was not called")
	}
	if remote.askOwner.OwnerPodID != "pod-b" {
		t.Fatalf("remote called with owner=%+v, want pod-b", remote.askOwner)
	}
	if remote.askDec.RequestID != "ask-y" || len(remote.askDec.Answers) != 1 || remote.askDec.Answers[0] != "yes" {
		t.Fatalf("decision payload mismatch: %+v", remote.askDec)
	}
}

func TestSubmitPromptForUserChoice_UsesCanonicalDeviceWithoutIMSlot(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	c := New(Config{
		Registry: gateway.NewRegistry(), Binder: binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-canonical"), ok: true},
		OwnerPodID:    "pod-a", RemoteSubmit: remote,
	})
	if err := c.SubmitPromptForUserChoice(context.Background(), connector.PromptForUserChoiceDecision{
		RequestID: "ask-canonical", DeviceID: "dev-canonical", Answers: []string{"yes"},
	}); err != nil {
		t.Fatalf("SubmitPromptForUserChoice: %v", err)
	}
	if !remote.askCalled || remote.askOwner.DeviceID != "dev-canonical" || remote.askDec.DeviceID != "dev-canonical" {
		t.Fatalf("remote submission = called:%v owner:%+v decision:%+v", remote.askCalled, remote.askOwner, remote.askDec)
	}
}

// TestSubmitPermission_SlotLookupErrorFallsBackToLocal covers the
// "slot already cleared" race: feishu webhook arrives but the
// inflight slot has just been swept. We must not error early — let
// the local lookup speak so the toast remains "already processed or expired" rather
// than "update failed".
func TestSubmitPermission_SlotLookupErrorFallsBackToLocal(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-1"), ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		// empty slots → DeviceIDForPermissionRequest returns
		// ErrUnknownConversation. The connector swallows the lookup
		// failure and tries Local, which itself returns NotRegistered.
		SubmitSlots: fakeSubmitSlots{},
	})

	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "ghost-x", Approved: true,
	})
	if err == nil {
		t.Fatal("expected local NotRegistered after slot miss, got nil")
	}
	if remote.permCalled {
		t.Fatal("remote must not be called on slot lookup failure")
	}
}

// TestSubmitPermission_EmptyDeviceIDFallsBackToLocal pins the legacy-row
// path: slot exists but its DeviceID is "" (written before the device
// id stamping change). We must not owner-route in that case — local
// lookup is the right answer for the single-pod world it came from.
func TestSubmitPermission_EmptyDeviceIDFallsBackToLocal(t *testing.T) {
	remote := &fakeRemoteSubmitter{}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-1"), ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		SubmitSlots:   fakeSubmitSlots{perms: map[string]string{"perm-legacy": ""}},
	})

	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-legacy", Approved: true,
	})
	if err == nil {
		t.Fatal("expected local NotRegistered, got nil")
	}
	if remote.permCalled {
		t.Fatal("remote must not be called for empty device id")
	}
}

// TestSubmitPermission_RemoteFailurePropagates ensures HTTP errors
// from the owner pod surface to the caller. The feishu handler shows
// "Update failed, please try again later" toast based on this error; swallowing it would
// leave the user thinking the verdict landed when it did not.
func TestSubmitPermission_RemoteFailurePropagates(t *testing.T) {
	remote := &fakeRemoteSubmitter{permErr: errors.New("network unreachable")}
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-1"), ok: true},
		OwnerPodID:    "pod-a",
		RemoteSubmit:  remote,
		SubmitSlots:   fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}},
	})

	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-x", Approved: true,
	})
	if err == nil || !contains(err.Error(), "network unreachable") {
		t.Fatalf("expected remote error to propagate, got %v", err)
	}
}

// TestSubmitPermission_RemoteOwnerWithoutSubmitterErrors guards the
// misconfiguration: someone wires OwnerResolver but not RemoteSubmit.
// Rather than silently fall back to a local-only lookup that always
// misses, we surface a clear error so the deployment notices.
func TestSubmitPermission_RemoteOwnerWithoutSubmitterErrors(t *testing.T) {
	c := New(Config{
		Registry:      gateway.NewRegistry(),
		Binder:        binding.NewInMemoryBinder(),
		OwnerResolver: fakeOwnerResolver{owner: remoteOwner("dev-1"), ok: true},
		OwnerPodID:    "pod-a",
		// RemoteSubmit deliberately nil
		SubmitSlots: fakeSubmitSlots{perms: map[string]string{"perm-x": "dev-1"}},
	})
	err := c.SubmitPermission(context.Background(), connector.PermissionDecision{
		RequestID: "perm-x", Approved: true,
	})
	if err == nil || !contains(err.Error(), "remote submit is not configured") {
		t.Fatalf("expected configuration error, got %v", err)
	}
}
