package dispatch_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// ---------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------

// recSender records every Envelope. failNow makes the next Send fail
// (used to exercise the pump's error path).
type recSender struct {
	mu      sync.Mutex
	frames  []proto.Envelope
	failNow bool
}

func (s *recSender) Send(_ context.Context, env proto.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNow {
		s.failNow = false
		return errors.New("sender broken")
	}
	s.frames = append(s.frames, env)
	return nil
}

func (s *recSender) snapshot() []proto.Envelope {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proto.Envelope, len(s.frames))
	copy(out, s.frames)
	return out
}

func (s *recSender) typesFor(runID string) []string {
	out := []string{}
	for _, f := range s.snapshot() {
		if f.ID == runID {
			out = append(out, f.Type)
		}
	}
	return out
}

// fakeSession is a controllable session. The test owns its out
// channel and manipulates outbound traffic / cancel observability.
type fakeSession struct {
	cancelCalls         int
	submitCalls         []permCall
	askCalls            []askCall
	submitErr           error
	askErr              error
	cancelMu, submitMu  sync.Mutex
	askMu               sync.Mutex
	closeOutOnCancel    bool
	out                 chan<- proto.Envelope
	closeOutOnCancelMu  sync.Once
	postCancelEnvelopes []proto.Envelope // emitted to out after Cancel fires
	ctx                 context.Context
}

type permCall struct {
	id       string
	decision proto.PermissionDecisionPayload
}

type askCall struct {
	id       string
	decision proto.PromptForUserChoiceDecisionPayload
}

func (s *fakeSession) Cancel(context.Context) error {
	s.cancelMu.Lock()
	s.cancelCalls++
	s.cancelMu.Unlock()
	if s.closeOutOnCancel {
		s.closeOutOnCancelMu.Do(func() {
			for _, env := range s.postCancelEnvelopes {
				s.out <- env
			}
			close(s.out)
		})
	}
	return nil
}

func (s *fakeSession) SubmitPermission(_ context.Context, permID string, dec proto.PermissionDecisionPayload) error {
	s.submitMu.Lock()
	s.submitCalls = append(s.submitCalls, permCall{id: permID, decision: dec})
	s.submitMu.Unlock()
	return s.submitErr
}

func (s *fakeSession) SubmitPromptForUserChoice(_ context.Context, askID string, dec proto.PromptForUserChoiceDecisionPayload) error {
	s.askMu.Lock()
	s.askCalls = append(s.askCalls, askCall{id: askID, decision: dec})
	s.askMu.Unlock()
	return s.askErr
}

func (s *fakeSession) cancels() int {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	return s.cancelCalls
}

func (s *fakeSession) submissions() []permCall {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	out := make([]permCall, len(s.submitCalls))
	copy(out, s.submitCalls)
	return out
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// newHarness builds a Router whose registry exposes a single
// claude_code factory that records inputs and exposes the in-flight
// session.
type harness struct {
	router  *dispatch.Router
	sender  *recSender
	reg     *agent.Registry
	gotReq  chan proto.PromptRequestPayload
	gotSess chan *fakeSession
}

func newHarness(t *testing.T) *harness {
	return newHarnessWithIdleTimeout(t, time.Hour)
}

func newHarnessWithIdleTimeout(t *testing.T, idleTimeout time.Duration) *harness {
	t.Helper()
	h := &harness{
		sender:  &recSender{},
		reg:     agent.NewRegistry(),
		gotReq:  make(chan proto.PromptRequestPayload, 16),
		gotSess: make(chan *fakeSession, 16),
	}
	h.reg.Register("claude_code", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		sess := &fakeSession{out: out, ctx: ctx}
		h.gotReq <- req
		h.gotSess <- sess
		return sess, nil
	})
	r, err := dispatch.New(dispatch.Config{Registry: h.reg, Sender: h.sender, IdleTimeout: idleTimeout})
	if err != nil {
		t.Fatalf("dispatch.New: %v", err)
	}
	h.router = r
	return h
}

func TestCompletedSessionCancelsAfterIdleTimeout(t *testing.T) {
	h := newHarnessWithIdleTimeout(t, 40*time.Millisecond)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_idle", proto.PromptRequestPayload{
		AgentKind: "claude_code", ConversationID: "conv-idle", AgentStateKey: "conv-idle/agent/claude_code",
	})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("Handle prompt_request: %v", err)
	}
	sess := <-h.gotSess
	close(sess.out)
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "active run cleanup")

	select {
	case <-sess.ctx.Done():
		t.Fatal("completed session cancelled before idle timeout")
	case <-time.After(15 * time.Millisecond):
	}
	waitFor(t, func() bool { return sess.cancels() == 1 }, "idle session cancellation")
}

func TestNewPromptResetsCompletedSessionIdleTimeout(t *testing.T) {
	h := newHarnessWithIdleTimeout(t, 80*time.Millisecond)
	defer h.router.Shutdown(context.Background())

	stateKey := "conv-renew/agent/claude_code"
	first := mustEnv(t, proto.TypePromptRequest, "run_first", proto.PromptRequestPayload{
		AgentKind: "claude_code", ConversationID: "conv-renew", AgentStateKey: stateKey,
	})
	if err := h.router.Handle(context.Background(), first); err != nil {
		t.Fatalf("Handle first prompt: %v", err)
	}
	firstSession := <-h.gotSess
	close(firstSession.out)
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "first run cleanup")
	time.Sleep(50 * time.Millisecond)

	second := mustEnv(t, proto.TypePromptRequest, "run_second", proto.PromptRequestPayload{
		AgentKind: "claude_code", ConversationID: "conv-renew", AgentStateKey: stateKey,
	})
	if err := h.router.Handle(context.Background(), second); err != nil {
		t.Fatalf("Handle second prompt: %v", err)
	}
	secondSession := <-h.gotSess

	time.Sleep(45 * time.Millisecond)
	if firstSession.cancels() != 0 {
		t.Fatal("new prompt did not renew the completed session idle timeout")
	}
	close(secondSession.out)
	waitFor(t, func() bool { return firstSession.cancels() == 1 }, "renewed idle session cancellation")
}

func mustEnv(t *testing.T, typ, id string, payload any) proto.Envelope {
	t.Helper()
	env, err := proto.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatalf("NewEnvelope %s: %v", typ, err)
	}
	return env
}

// ---------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------

func TestHandlePromptRequestInvokesFactoryAndForwardsOutput(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_1", proto.PromptRequestPayload{
		AgentKind: "claude_code", Prompt: "hi", ConversationID: "c1",
	})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("Handle prompt_request: %v", err)
	}

	req := <-h.gotReq
	if req.RunID != "run_1" || req.AgentKind != "claude_code" || req.Prompt != "hi" {
		t.Errorf("factory got %+v, want run_1/claude_code/hi", req)
	}
	sess := <-h.gotSess

	// Session emits a delta + done; both should reach the sender.
	sess.out <- mustEnv(t, proto.TypeDelta, "run_1", proto.DeltaPayload{Delta: "hello", Sequence: 1})
	sess.out <- mustEnv(t, proto.TypeDone, "run_1", proto.DonePayload{Content: "hello"})
	close(sess.out)

	waitForTypes(t, h.sender, "run_1", []string{proto.TypeDelta, proto.TypeDone})

	// Pump should have removed the session.
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "active runs to drop to 0")
}

func TestHandlePromptRequestRejectsDuplicateRunID(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_dup", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	<-h.gotReq // drain first
	sess := <-h.gotSess

	// Second prompt_request with same RunID should NOT spin up a second factory call.
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("duplicate Handle: %v", err)
	}

	select {
	case extra := <-h.gotReq:
		t.Fatalf("factory invoked twice for duplicate run, second req=%+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
	close(sess.out)
}

func TestHandlePromptRequestUnsupportedKindEmitsErrorDone(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_x", proto.PromptRequestPayload{AgentKind: "opencode"})
	err := h.router.Handle(context.Background(), env)
	if !errors.Is(err, agent.ErrUnsupportedKind) {
		t.Errorf("Handle unsupported = %v, want ErrUnsupportedKind", err)
	}
	got := h.sender.typesFor("run_x")
	want := []string{proto.TypeError, proto.TypeDone}
	if !slices.Equal(got, want) {
		t.Errorf("sender frames for run_x = %v, want %v", got, want)
	}
}

func TestHandlePromptRequestMissingRunIDIsError(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err == nil {
		t.Fatal("expected error on missing run id")
	}
}

func TestHandlePromptCancelInvokesSessionCancel(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_2", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("prompt_request: %v", err)
	}
	<-h.gotReq
	sess := <-h.gotSess
	sess.closeOutOnCancel = true

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "run_2", nil)); err != nil {
		t.Fatalf("prompt_cancel: %v", err)
	}

	waitFor(t, func() bool { return sess.cancels() == 1 }, "session.Cancel to fire once")
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "session to be cleaned up")
}

func TestHandlePromptCancelUnknownRunIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "ghost", nil)); err != nil {
		t.Errorf("cancel for unknown run = %v, want nil", err)
	}
}

func TestPermissionRequestIsIndexedAndDecisionRoutes(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_p", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("prompt_request: %v", err)
	}
	<-h.gotReq
	sess := <-h.gotSess

	// Session emits a permission_request; pump should index it.
	permEnv := mustEnv(t, proto.TypePermissionRequest, "perm_abcd1234", proto.PermissionRequestPayload{
		Tool: "Bash", Title: "rm -rf /",
	})
	sess.out <- permEnv
	// Wait until sender records — indexing happens before send.
	waitFor(t, func() bool { return len(h.sender.snapshot()) >= 1 }, "permission_request to be forwarded")

	dec := mustEnv(t, proto.TypePermissionDecision, "perm_abcd1234", proto.PermissionDecisionPayload{Approved: true})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("permission_decision: %v", err)
	}
	calls := sess.submissions()
	if len(calls) != 1 || calls[0].id != "perm_abcd1234" || !calls[0].decision.Approved {
		t.Errorf("submissions = %+v, want one approved perm_abcd1234", calls)
	}

	close(sess.out)
}

func TestPermissionCancelDeindexes(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_p2", proto.PromptRequestPayload{AgentKind: "claude_code"})
	_ = h.router.Handle(context.Background(), env)
	<-h.gotReq
	sess := <-h.gotSess

	sess.out <- mustEnv(t, proto.TypePermissionRequest, "perm_xx", proto.PermissionRequestPayload{Tool: "Bash"})
	waitFor(t, func() bool { return len(h.sender.snapshot()) >= 1 }, "perm forwarded")
	sess.out <- mustEnv(t, proto.TypePermissionCancel, "perm_xx", nil)
	waitFor(t, func() bool { return len(h.sender.snapshot()) >= 2 }, "perm_cancel forwarded")

	// A decision for the cancelled perm should be a no-op (session never sees it).
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePermissionDecision, "perm_xx", proto.PermissionDecisionPayload{})); err != nil {
		t.Fatalf("decision: %v", err)
	}
	if calls := sess.submissions(); len(calls) != 0 {
		t.Errorf("expected zero submissions after cancel, got %+v", calls)
	}

	close(sess.out)
}

func TestPermissionDecisionUnknownPermIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePermissionDecision, "perm_unknown", proto.PermissionDecisionPayload{})); err != nil {
		t.Errorf("decision for unknown perm = %v, want nil", err)
	}
}

func TestPromptForUserChoiceDecisionRoutesToSession(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_ask", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("prompt_request: %v", err)
	}
	<-h.gotReq
	sess := <-h.gotSess

	// Envelope.ID is the run id (server-side dispatch fans on it); the
	// ask id rides on the payload. Daemon's indexPermissionFrame reads
	// payload.AskID to seed askIndex.
	askEnv := mustEnv(t, proto.TypePromptForUserChoice, "run_ask", proto.PromptForUserChoicePayload{
		AskID:     "ask_abcd1234",
		Question:  "?",
		Options:   []proto.PromptForUserChoiceOption{{Label: "yes"}, {Label: "no"}},
		ToolUseID: "toolu_42",
	})
	sess.out <- askEnv
	waitFor(t, func() bool { return len(h.sender.snapshot()) >= 1 }, "prompt_for_user_choice forwarded")

	dec := mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask_abcd1234", proto.PromptForUserChoiceDecisionPayload{
		Answers: []string{"yes"},
	})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("prompt_for_user_choice_decision: %v", err)
	}

	sess.askMu.Lock()
	calls := append([]askCall(nil), sess.askCalls...)
	sess.askMu.Unlock()
	if len(calls) != 1 || calls[0].id != "ask_abcd1234" {
		t.Fatalf("askCalls = %+v, want one ask_abcd1234", calls)
	}
	if len(calls[0].decision.Answers) != 1 || calls[0].decision.Answers[0] != "yes" {
		t.Errorf("answer payload mismatch: %+v", calls[0].decision)
	}

	// Cleanup contract: a successful decision drops the ask from both
	// the router-level index and the session's pendingAsks set, so a
	// stale retry short-circuits as "run gone".
	if got := h.router.AskIndexLenForTest(); got != 0 {
		t.Errorf("askIndex len = %d, want 0 after decision", got)
	}
	if got := h.router.PendingAsksLenForTest("run_ask"); got != 0 {
		t.Errorf("pendingAsks len = %d, want 0 after decision", got)
	}

	close(sess.out)
}

// TestPromptForUserChoiceDecisionClearsIndexOnAgentUnknown locks in the
// other cleanup branch: when the session returns ErrUnknownAsk (timer
// already consumed the entry), the router still drops the index so a
// retry doesn't loop into Submit again.
func TestPromptForUserChoiceDecisionClearsIndexOnAgentUnknown(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	env := mustEnv(t, proto.TypePromptRequest, "run_ask_u", proto.PromptRequestPayload{AgentKind: "claude_code"})
	if err := h.router.Handle(context.Background(), env); err != nil {
		t.Fatalf("prompt_request: %v", err)
	}
	<-h.gotReq
	sess := <-h.gotSess
	sess.askErr = agent.ErrUnknownAsk

	askEnv := mustEnv(t, proto.TypePromptForUserChoice, "run_ask_u", proto.PromptForUserChoicePayload{
		AskID:     "ask_xxxxxxxx",
		Question:  "?",
		Options:   []proto.PromptForUserChoiceOption{{Label: "yes"}},
		ToolUseID: "toolu_y",
	})
	sess.out <- askEnv
	waitFor(t, func() bool { return len(h.sender.snapshot()) >= 1 }, "prompt_for_user_choice forwarded")

	dec := mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask_xxxxxxxx", proto.PromptForUserChoiceDecisionPayload{
		Answers: []string{"yes"},
	})
	if err := h.router.Handle(context.Background(), dec); err != nil {
		t.Fatalf("prompt_for_user_choice_decision: %v", err)
	}

	if got := h.router.AskIndexLenForTest(); got != 0 {
		t.Errorf("askIndex len = %d, want 0 after ErrUnknownAsk", got)
	}
	if got := h.router.PendingAsksLenForTest("run_ask_u"); got != 0 {
		t.Errorf("pendingAsks len = %d, want 0 after ErrUnknownAsk", got)
	}

	close(sess.out)
}

func TestPromptForUserChoiceDecisionUnknownAskIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask_unknown", proto.PromptForUserChoiceDecisionPayload{})); err != nil {
		t.Errorf("decision for unknown ask = %v, want nil", err)
	}
}

func TestHandleDeviceShutdownCancelsAllSessions(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	for _, rid := range []string{"r1", "r2", "r3"} {
		if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, rid, proto.PromptRequestPayload{AgentKind: "claude_code"})); err != nil {
			t.Fatalf("start %s: %v", rid, err)
		}
		<-h.gotReq
	}
	sessions := make([]*fakeSession, 3)
	for i := range sessions {
		sessions[i] = <-h.gotSess
		sessions[i].closeOutOnCancel = true
	}

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypeDeviceShutdown, "", proto.DeviceShutdownPayload{Reason: "reap"})); err != nil {
		t.Fatalf("device_shutdown: %v", err)
	}
	for _, s := range sessions {
		waitFor(t, func() bool { return s.cancels() == 1 }, "each session.Cancel to fire")
	}
}

func TestHandleUnknownTypeIsNoop(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())

	if err := h.router.Handle(context.Background(), proto.Envelope{Type: "fancy_new_event"}); err != nil {
		t.Errorf("unknown type Handle = %v, want nil", err)
	}
}

func TestHandleAfterShutdownReturnsErrRouterClosed(t *testing.T) {
	h := newHarness(t)
	if err := h.router.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "r", proto.PromptRequestPayload{AgentKind: "claude_code"}))
	if !errors.Is(err, dispatch.ErrRouterClosed) {
		t.Errorf("post-shutdown Handle = %v, want ErrRouterClosed", err)
	}
}

func TestShutdownWaitsForPumpDrain(t *testing.T) {
	h := newHarness(t)

	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "rs", proto.PromptRequestPayload{AgentKind: "claude_code"})); err != nil {
		t.Fatalf("prompt_request: %v", err)
	}
	<-h.gotReq
	sess := <-h.gotSess

	// Background: emit one frame then close out shortly after
	// shutdown is asked for.
	go func() {
		sess.out <- mustEnv(t, proto.TypeDelta, "rs", proto.DeltaPayload{Delta: "x"})
		time.Sleep(20 * time.Millisecond)
		close(sess.out)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.router.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned %v before pump drained", err)
	}
	if h.router.ActiveRuns() != 0 {
		t.Errorf("ActiveRuns after Shutdown = %d, want 0", h.router.ActiveRuns())
	}
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

func waitForTypes(t *testing.T, s *recSender, runID string, want []string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := s.typesFor(runID)
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never observed types %v for run %s; got %v", want, runID, s.typesFor(runID))
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}
