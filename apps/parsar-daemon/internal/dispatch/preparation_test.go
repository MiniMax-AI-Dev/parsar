package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type controlledPreparation struct {
	closed    chan struct{}
	once      sync.Once
	starts    atomic.Int32
	start     func(context.Context, string, string, chan<- proto.Envelope) (agent.Session, error)
	closeHook func()
}

func (p *controlledPreparation) Close() error {
	p.once.Do(func() {
		if p.closeHook != nil {
			p.closeHook()
		}
		close(p.closed)
	})
	return nil
}
func (p *controlledPreparation) Start(ctx context.Context, id, prompt string, out chan<- proto.Envelope) (agent.Session, error) {
	p.starts.Add(1)
	return p.start(ctx, id, prompt, out)
}

func preparationRequest() proto.ExecutionPreparePayload {
	return proto.ExecutionPreparePayload{Configuration: proto.PromptRequestPayload{AgentKind: "prepared", AgentStateKey: "execution-session", StrictResume: true, ReleaseOnCompletion: true, RemoteEnvironment: &proto.RemoteEnvironment{ID: "environment"}}}
}

func preparationRouter(t *testing.T, sender dispatch.Sender, timeout time.Duration, factory agent.PreparationFactory) *dispatch.Router {
	t.Helper()
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "prepared", Available: true, Capabilities: proto.AgentKindCapabilities{RemoteEnvironment: true}}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		return nil, errors.New("ordinary Factory must not be used for preparation")
	})
	reg.RegisterPreparation("prepared", true, factory)
	r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, PreparationTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return r
}

func waitPreparationStatus(t *testing.T, sender *recSender, request, state string, differentHandle string) proto.PreparationStatusPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range sender.snapshot() {
			var p proto.PreparationStatusPayload
			if env.Type == proto.TypePreparationStatus && env.ID == request && env.DecodePayload(&p) == nil && p.State == state && (differentHandle == "" || p.Handle != differentHandle) {
				return p
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("preparation %s did not reach %s", request, state)
	return proto.PreparationStatusPayload{}
}

func waitPreparationClosed(t *testing.T, p *controlledPreparation) {
	t.Helper()
	select {
	case <-p.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("native preparation leaked")
	}
}

const preparedCapacityFrameCount = 96

func preparedCapacityFrames(t *testing.T, runID string) []proto.Envelope {
	t.Helper()
	frames := make([]proto.Envelope, 0, preparedCapacityFrameCount)
	for sequence := uint64(1); sequence <= preparedCapacityFrameCount; sequence++ {
		frames = append(frames, mustEnv(t, proto.TypeDelta, runID, proto.DeltaPayload{
			Delta: "bounded", Sequence: sequence,
		}))
	}
	return frames
}

func sendPreparedCapacityFrames(out chan<- proto.Envelope, frames []proto.Envelope, abort <-chan struct{}) bool {
	for _, frame := range frames {
		select {
		case out <- frame:
		case <-abort:
			return false
		}
	}
	return true
}

func assertPreparedCapacityFrames(t *testing.T, sender *recSender, runID string, count int) {
	t.Helper()
	seen := 0
	for _, frame := range sender.snapshot() {
		if frame.ID != runID || frame.Type != proto.TypeDelta {
			continue
		}
		seen++
		var delta proto.DeltaPayload
		if frame.DecodePayload(&delta) != nil || delta.Sequence != uint64(seen) {
			t.Fatalf("prepared output sequence %d = %+v", seen, delta)
		}
	}
	if seen != count {
		t.Fatalf("prepared output count = %d, want %d", seen, count)
	}
}

func waitPreparedCapacity(t *testing.T, sent <-chan struct{}, abort chan<- struct{}, r *dispatch.Router) {
	t.Helper()
	select {
	case <-sent:
		return
	case <-time.After(2 * time.Second):
		close(abort)
		waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "blocked prepared Start cleanup")
		t.Fatal("prepared Start output blocked at the bounded channel capacity")
	}
}

type failPreparedOutputSender struct {
	*recSender
	failAt int32
	seen   atomic.Int32
}

func (s *failPreparedOutputSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.ID == "run" && env.Type == proto.TypeDelta && s.seen.Add(1) == s.failAt {
		return errors.New("controlled prepared output failure")
	}
	return s.recSender.Send(ctx, env)
}

type blockingPreparedTerminalSender struct {
	*recSender
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type failStartedPreparationSender struct {
	*recSender
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type blockingStartedPreparationSender struct {
	*recSender
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingStartedPreparationSender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "started" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.recSender.Send(ctx, env)
}

func (s *failStartedPreparationSender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "started" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
			return errors.New("controlled started status failure")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.recSender.Send(ctx, env)
}

func (s *blockingPreparedTerminalSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.ID == "run" && env.Type == proto.TypeDone {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparationStartForwardsBeyondCapacityBeforeReturn(t *testing.T) {
	sender := &recSender{}
	frames := preparedCapacityFrames(t, "run")
	abort, sent, allowReturn := make(chan struct{}), make(chan struct{}), make(chan struct{})
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		if !sendPreparedCapacityFrames(out, frames, abort) {
			return nil, errors.New("controlled test abort")
		}
		close(sent)
		select {
		case <-allowReturn:
			return session, nil
		case <-abort:
			return nil, errors.New("controlled test abort")
		}
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitPreparedCapacity(t, sent, abort, r)
	waitFor(t, func() bool { return len(sender.typesFor("run")) == preparedCapacityFrameCount }, "prepared output forwarding before Start return")
	assertPreparedCapacityFrames(t, sender, "run", preparedCapacityFrameCount)
	if r.ActiveRuns() != 1 {
		t.Fatal("pending Start released run ownership")
	}
	close(allowReturn)
	waitPreparationStatus(t, sender, "request", "started", "")
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "completed prepared run cleanup")
	if session.cancels() != 1 {
		t.Fatal("completed prepared Session was not released once")
	}
	assertPreparedCapacityFrames(t, sender, "run", preparedCapacityFrameCount)
}

func TestPreparationStartDefersEarlyCompletionReleaseUntilSessionReturn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		releaseErr error
		want       []string
	}{
		{name: "released", want: []string{proto.TypeDone}},
		{name: "release_error", releaseErr: errors.New("controlled release failure"), want: []string{proto.TypeError, proto.TypeDone}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &recSender{}
			abort, emitted, allowReturn := make(chan struct{}), make(chan struct{}), make(chan struct{})
			session := &fakeSession{closeOutOnCancel: true, cancelErr: tc.releaseErr}
			done := mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
			p := &controlledPreparation{closed: make(chan struct{})}
			p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
				session.out = out
				select {
				case out <- done:
					out <- mustEnv(t, proto.TypeDelta, "run", proto.DeltaPayload{Delta: "after terminal", Sequence: 999})
					out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "duplicate"})
					close(emitted)
				case <-abort:
					return nil, errors.New("controlled test abort")
				}
				select {
				case <-allowReturn:
					return session, nil
				case <-abort:
					return nil, errors.New("controlled test abort")
				}
			}
			r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
			startCancellationPreparation(t, r, sender)
			select {
			case <-emitted:
			case <-time.After(2 * time.Second):
				close(abort)
				waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "blocked early completion cleanup")
				t.Fatal("early completion was not consumed while Start was pending")
			}
			waitFor(t, func() bool { return len(session.out) == 0 }, "early completion consumption")
			if got := sender.typesFor("run"); len(got) != 0 {
				t.Fatal("early completion was visible before native release", got)
			}
			if session.cancels() != 0 || r.ActiveRuns() != 1 {
				t.Fatal("early completion released a Session before Start returned")
			}
			close(allowReturn)
			waitPreparationStatus(t, sender, "request", "started", "")
			waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "early completion release")
			if session.cancels() != 1 {
				t.Fatal("early completion did not release the returned Session once")
			}
			if got := sender.typesFor("run"); !reflect.DeepEqual(got, tc.want) {
				t.Fatal("early completion terminal order differs", got)
			}
		})
	}
}

func TestPreparationStartedStatusFailureAndCompletionReleaseSessionOnce(t *testing.T) {
	sender := &failStartedPreparationSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	session := &fakeSession{closeOutOnCancel: true}
	done := mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	session.postCancelEnvelopes = []proto.Envelope{done}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("started status delivery did not block")
	}
	session.out <- done
	waitFor(t, func() bool { return len(session.out) == 0 }, "completion consumption during started status delivery")
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed started status cleanup")
	if session.cancels() != 1 {
		t.Fatal("started status failure released Session more than once", session.cancels())
	}
	if got := sender.typesFor("run"); !reflect.DeepEqual(got, []string{proto.TypeDone}) {
		t.Fatal("started status failure changed terminal delivery", got)
	}
}

func TestPreparationPromptCancelJoinsCompletionDuringStartedStatus(t *testing.T) {
	sender := &blockingStartedPreparationSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	cancelEntered, cancelRelease := make(chan struct{}), make(chan struct{})
	session := &fakeSession{closeOutOnCancel: true, cancelEntered: cancelEntered, cancelRelease: cancelRelease}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("started status delivery did not block")
	}
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	select {
	case <-cancelEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not start shared release")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	if session.cancels() != 1 || len(sender.typesFor("run")) != 0 || len(cancellationAcks(sender.recSender)) != 0 {
		t.Fatal("cancel bypassed the shared release barrier")
	}
	close(cancelRelease)
	waitFor(t, func() bool { return session.cancels() == 1 }, "native release")
	if len(sender.typesFor("run")) != 0 {
		t.Fatal("terminal overtook started status publication")
	}
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 && len(cancellationAcks(sender.recSender)) == 1 }, "shared cancellation settlement")
	if session.cancels() != 1 {
		t.Fatal("completion and prompt cancellation released Session more than once", session.cancels())
	}
	if got := sender.typesFor("run"); !reflect.DeepEqual(got, []string{proto.TypeDone, proto.TypeInteractionDecisionAck}) {
		t.Fatal("terminal/receipt order differs", got)
	}
}

func TestPreparationShutdownDuringStartedStatusReleasesSessionOnce(t *testing.T) {
	sender := &failStartedPreparationSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("started status delivery did not block")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if session.cancels() != 1 {
		t.Fatal("shutdown released settling Session more than once", session.cancels())
	}
}

func TestPreparationDeviceShutdownJoinsCompletionAndSteeringReceipt(t *testing.T) {
	sender := &blockedReceiptSender{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	releaseErr := errors.New("controlled release failure")
	session := &fakeSession{closeOutOnCancel: true, cancelErr: releaseErr}
	preparedSession := &steeringSession{fakeSession: session, steer: func(context.Context, proto.PromptSteerPayload) error { return nil }}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return preparedSession, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, &sender.recSender)
	waitPreparationStatus(t, &sender.recSender, "request", "started", "")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "one", Text: "more"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("steering receipt did not block")
	}
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	waitFor(t, func() bool { return r.SteeringClosedForTest("run") }, "completion release admission")
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- r.Handle(t.Context(), mustEnv(t, proto.TypeDeviceShutdown, "device", proto.DeviceShutdownPayload{Reason: "test"}))
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("device shutdown bypassed shared release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if session.cancels() != 0 || len(sender.typesFor("run")) != 0 {
		t.Fatal("native release or terminal overtook steering receipt")
	}
	close(sender.release)
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "device shutdown settlement")
	if session.cancels() != 1 {
		t.Fatal("device shutdown and completion released Session more than once", session.cancels())
	}
	if got := sender.typesFor("run"); !reflect.DeepEqual(got, []string{proto.TypePromptSteerAck, proto.TypeError, proto.TypeDone}) {
		t.Fatal("steering/release/terminal order differs", got)
	}
}

func TestPreparationStartActivatesEarlyInteractionRoutesAfterSessionReturn(t *testing.T) {
	sender := &recSender{}
	emitted, allowReturn := make(chan struct{}), make(chan struct{})
	session := &functionSession{fakeSession: &fakeSession{closeOutOnCancel: true}}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		out <- mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{
			RequestID: "permission", Tool: "Bash", Title: "approve",
		})
		out <- mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{
			AskID: "ask", Question: "continue?", Options: []proto.PromptForUserChoiceOption{{Label: "yes"}},
		})
		out <- mustEnv(t, proto.TypeFunctionCall, "run", proto.FunctionCallPayload{CallID: "call", Name: "lookup"})
		close(emitted)
		select {
		case <-allowReturn:
			return session, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	select {
	case <-emitted:
	case <-time.After(2 * time.Second):
		t.Fatal("early interactions were not emitted")
	}
	waitFor(t, func() bool { return len(sender.typesFor("run")) == 3 }, "early interaction forwarding")

	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: "permission-before", Approved: true})); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "ask-before", Answers: []string{"yes"}})); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("early"), DeliveryID: "function-before"})); err != nil {
		t.Fatal(err)
	}
	assertDecisionAck(t, sender, "permission-before", false, "not_ready")
	assertDecisionAck(t, sender, "ask-before", false, "not_ready")
	assertDecisionAck(t, sender, "function-before", false, "not_ready")
	if len(session.submissions()) != 0 {
		t.Fatal("permission reached the Session before Start returned")
	}
	session.askMu.Lock()
	earlyAskCalls := len(session.askCalls)
	session.askMu.Unlock()
	if earlyAskCalls != 0 {
		t.Fatal("user choice reached the Session before Start returned")
	}
	session.mu.Lock()
	earlyFunctionCalls := session.calls
	session.mu.Unlock()
	if earlyFunctionCalls != 0 {
		t.Fatal("function result reached the Session before Start returned")
	}

	close(allowReturn)
	waitPreparationStatus(t, sender, "request", "started", "")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: "permission-after", Approved: true})); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "ask-after", Answers: []string{"yes"}})); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: "function-after"})); err != nil {
		t.Fatal(err)
	}
	assertDecisionAck(t, sender, "permission-after", true, "")
	assertDecisionAck(t, sender, "ask-after", true, "")
	assertDecisionAck(t, sender, "function-after", true, "")
	if calls := session.submissions(); len(calls) != 1 || calls[0].id != "permission" {
		t.Fatalf("permission calls = %+v", calls)
	}
	session.askMu.Lock()
	askCalls := append([]askCall(nil), session.askCalls...)
	session.askMu.Unlock()
	if len(askCalls) != 1 || askCalls[0].id != "ask" {
		t.Fatalf("user-choice calls = %+v", askCalls)
	}
	session.mu.Lock()
	functionCalls := session.calls
	session.mu.Unlock()
	if functionCalls != 1 {
		t.Fatalf("function-result calls = %d", functionCalls)
	}

	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "interaction run cleanup")
}

func TestPreparationFailedStartDoesNotDuplicateEarlyCompletion(t *testing.T) {
	sender := &recSender{}
	frames := preparedCapacityFrames(t, "run")
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		for _, frame := range frames {
			out <- frame
		}
		out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "observed"})
		return nil, errors.New("controlled Start failure after completion")
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed completed Start cleanup")
	waitPreparationClosed(t, p)
	assertPreparedCapacityFrames(t, sender, "run", preparedCapacityFrameCount)
	got := sender.typesFor("run")
	if len(got) != preparedCapacityFrameCount+2 || got[len(got)-2] != proto.TypeError || got[len(got)-1] != proto.TypeDone {
		t.Fatalf("failed completed Start terminal order = %v", got)
	}
}

func TestPreparationFailedStartForwardsAcceptedOutput(t *testing.T) {
	sender := &blockingPreparedTerminalSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-sender.release:
		default:
			close(sender.release)
		}
	}()
	frames := preparedCapacityFrames(t, "run")
	abort, sent := make(chan struct{}), make(chan struct{})
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		if !sendPreparedCapacityFrames(out, frames, abort) {
			return nil, errors.New("controlled test abort")
		}
		close(sent)
		return nil, errors.New("controlled Start failure")
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	waitPreparedCapacity(t, sent, abort, r)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("failed prepared Start did not reach its terminal frame")
	}
	if r.ActiveRuns() != 1 {
		t.Fatal("failed prepared Start released run capacity before terminal delivery")
	}
	assertPreparedCapacityFrames(t, sender.recSender, "run", preparedCapacityFrameCount)
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed prepared Start cleanup")
	waitPreparationClosed(t, p)
	assertPreparedCapacityFrames(t, sender.recSender, "run", preparedCapacityFrameCount)
	got := sender.typesFor("run")
	if len(got) != preparedCapacityFrameCount+2 || got[len(got)-2] != proto.TypeError || got[len(got)-1] != proto.TypeDone {
		t.Fatalf("failed Start terminal order = %v", got)
	}
}

func TestPreparationStartDrainsAfterOutputDeliveryFailure(t *testing.T) {
	sender := &failPreparedOutputSender{recSender: &recSender{}, failAt: 17}
	frames := preparedCapacityFrames(t, "run")
	abort, sent := make(chan struct{}), make(chan struct{})
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		if !sendPreparedCapacityFrames(out, frames, abort) {
			return nil, errors.New("controlled test abort")
		}
		close(sent)
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	waitPreparedCapacity(t, sent, abort, r)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed output cleanup")
	waitPreparationClosed(t, p)
	if sender.seen.Load() != sender.failAt || session.cancels() != 1 {
		t.Fatal("output failure did not drain producer and cancel late Session", sender.seen.Load(), session.cancels())
	}
}

func TestPreparationActiveOutputFailureReleasesSessionOnce(t *testing.T) {
	sender := &failPreparedOutputSender{recSender: &recSender{}, failAt: 1}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	waitPreparationStatus(t, sender.recSender, "request", "started", "")
	session.out <- mustEnv(t, proto.TypeDelta, "run", proto.DeltaPayload{Delta: "unavailable", Sequence: 1})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "active output failure cleanup")
	if session.cancels() != 1 || sender.seen.Load() != 1 {
		t.Fatal("active output failure did not release Session exactly once", session.cancels(), sender.seen.Load())
	}
}

func TestPreparationStartSessionWithErrorTransfersOutputOwnership(t *testing.T) {
	sender := &recSender{}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, errors.New("controlled error after transfer")
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "transferred error cleanup")
	waitPreparationClosed(t, p)
	if session.cancels() != 1 {
		t.Fatal("non-nil failed Session was not released exactly once", session.cancels())
	}
	if got := sender.typesFor("run"); !reflect.DeepEqual(got, []string{proto.TypeError, proto.TypeDone}) {
		t.Fatal("transferred error terminal order differs", got)
	}
}

func TestPreparationStartShutdownDrainsBeyondCapacity(t *testing.T) {
	sender := &recSender{}
	frames := preparedCapacityFrames(t, "run")
	abort, sent := make(chan struct{}), make(chan struct{})
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		if !sendPreparedCapacityFrames(out, frames, abort) {
			return nil, errors.New("controlled test abort")
		}
		close(sent)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-abort:
			return nil, errors.New("controlled test abort")
		}
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitPreparedCapacity(t, sent, abort, r)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitPreparationClosed(t, p)
	if r.ActiveRuns() != 0 {
		t.Fatal("shutdown retained prepared run ownership")
	}
}

func TestPreparationReleaseDuringBlockedFactory(t *testing.T) {
	sender := &recSender{}
	p := &controlledPreparation{closed: make(chan struct{})}
	entered, allowReturn := make(chan context.Context, 1), make(chan struct{})
	r := preparationRouter(t, sender, time.Minute, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
		if req.RunID != "" || req.Prompt != "" {
			t.Error("run input reached preparation")
		}
		entered <- ctx
		<-allowReturn
		return p, nil
	})
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest())); err != nil {
		t.Fatal(err)
	}
	accepted := waitPreparationStatus(t, sender, "request", "preparing", "")
	owner := <-entered
	if r.ActiveRuns() != 0 {
		t.Fatal("preparation created a run")
	}
	// Receive-loop operations remain available while native initialization blocks.
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "other-run", proto.PromptCancelPayload{})); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: accepted.Handle})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.Done():
	case <-time.After(time.Second):
		t.Fatal("release did not cancel owner")
	}
	close(allowReturn)
	waitPreparationClosed(t, p)
	if p.starts.Load() != 0 {
		t.Fatal("released preparation started work")
	}
	for _, env := range sender.snapshot() {
		if env.Type == proto.TypeError || env.Type == proto.TypeDone {
			t.Fatal("preparation emitted run terminal frames")
		}
	}
}

func TestPreparationSingleTransferAndReleaseDoesNotCancelRun(t *testing.T) {
	sender := &recSender{}
	gotSession := make(chan *fakeSession, 1)
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, id, prompt string, out chan<- proto.Envelope) (agent.Session, error) {
		if id != "real-run" || prompt != "actual input" {
			t.Error("start identity or prompt changed")
		}
		s := &fakeSession{ctx: ctx, out: out, closeOutOnCancel: true}
		gotSession <- s
		return s, nil
	}
	r := preparationRouter(t, sender, 80*time.Millisecond, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	prepare := mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest())
	if err := r.Handle(t.Context(), prepare); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "request", "ready", "")
	if err := r.Handle(t.Context(), prepare); err != nil {
		t.Fatal(err)
	}
	start := mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "real-run", Prompt: "actual input"})
	if err := r.Handle(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	waitPreparationStatus(t, sender, "request", "started", "")
	session := <-gotSession
	if err := r.Handle(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
		t.Fatal(err)
	}
	// The old preparation deadline must not govern the transferred Session.
	time.Sleep(100 * time.Millisecond)
	if session.ctx.Err() != nil || session.cancels() != 0 || p.starts.Load() != 1 || r.ActiveRuns() != 1 {
		t.Fatal("transfer was duplicated or cancelled")
	}
	select {
	case <-p.closed:
		t.Fatal("transferred preparation closed")
	default:
	}
	session.out <- mustEnv(t, proto.TypeDone, "real-run", proto.DonePayload{Content: "complete"})
	deadline := time.Now().Add(time.Second)
	for r.ActiveRuns() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.ActiveRuns() != 0 || session.cancels() != 1 {
		t.Fatal("normal completion release was bypassed")
	}
}

func TestPreparationCancelDuringStartClosesLateSession(t *testing.T) {
	sender := &recSender{}
	entered, allowReturn := make(chan context.Context, 1), make(chan struct{})
	lateSession := make(chan *fakeSession, 1)
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		entered <- ctx
		<-allowReturn
		s := &fakeSession{out: out, closeOutOnCancel: true}
		lateSession <- s
		return s, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "request", "ready", "")
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "real-run", Prompt: "input"}))
	owner := <-entered
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "real-run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.Done():
	case <-time.After(time.Second):
		t.Fatal("start blocked cancellation")
	}
	close(allowReturn)
	waitPreparationClosed(t, p)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "late cancelled Session cleanup")
	if (<-lateSession).cancels() != 1 || r.ActiveRuns() != 0 {
		t.Fatal("late session resurrected cancelled run")
	}
	for _, frame := range sender.snapshot() {
		var status proto.PreparationStatusPayload
		if frame.Type == proto.TypePreparationStatus && frame.DecodePayload(&status) == nil && status.State == "started" {
			t.Fatal("cancelled start published active Session")
		}
	}
}

func TestPreparationCapacityAndConnectionOwnership(t *testing.T) {
	sender := &recSender{}
	var count atomic.Int32
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		count.Add(1)
		return &controlledPreparation{closed: make(chan struct{})}, nil
	})
	var handles []string
	for i := 0; i < 4; i++ {
		id := fmt.Sprint(i)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, id, preparationRequest()))
		handles = append(handles, waitPreparationStatus(t, sender, id, "ready", "").Handle)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "overflow", preparationRequest())); err == nil {
		t.Fatal("capacity unbounded")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "0", preparationRequest())); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 4 {
		t.Fatal("retry recreated native resource")
	}
	other := preparationRouter(t, &recSender{}, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		t.Error("unexpected new native resource")
		return nil, errors.New("unexpected")
	})
	if err := other.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "0", proto.ExecutionStartPayload{Handle: handles[0], RunID: "run", Prompt: "input"})); err == nil {
		t.Fatal("another connection consumed handle")
	}
}

func TestPreparationExpiryAndOldHandleCannotStartReplacement(t *testing.T) {
	sender := &recSender{}
	created := make(chan *controlledPreparation, 2)
	r := preparationRouter(t, sender, 60*time.Millisecond, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		p := &controlledPreparation{closed: make(chan struct{})}
		created <- p
		return p, nil
	})
	env := mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest())
	_ = r.Handle(t.Context(), env)
	old := waitPreparationStatus(t, sender, "request", "ready", "")
	waitPreparationStatus(t, sender, "request", "expired", "")
	waitPreparationClosed(t, <-created)
	_ = r.Handle(t.Context(), env)
	next := waitPreparationStatus(t, sender, "request", "preparing", old.Handle)
	if next.Handle == old.Handle || next.ExpiresAt <= old.ExpiresAt {
		t.Fatal("replacement reused expired identity")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: old.Handle, RunID: "late", Prompt: "late"})); err == nil {
		t.Fatal("old handle started replacement")
	}
}

type failReadySender struct{ *recSender }

func (s failReadySender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "ready" {
		return errors.New("controlled send failure")
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparationFailedReadyDeliveryClosesResource(t *testing.T) {
	p := &controlledPreparation{closed: make(chan struct{})}
	r := preparationRouter(t, failReadySender{&recSender{}}, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest()))
	waitPreparationClosed(t, p)
	if r.ActiveRuns() != 0 {
		t.Fatal("failed preparation became a run")
	}
}

func TestPreparationCapacityIncludesClosingResources(t *testing.T) {
	sender := &recSender{}
	entered, unblock := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(unblock) }) }
	defer release()
	var count atomic.Int32
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		p := &controlledPreparation{closed: make(chan struct{})}
		if count.Add(1) == 1 {
			p.closeHook = func() { close(entered); <-unblock }
		}
		return p, nil
	})
	var first proto.PreparationStatusPayload
	for i := 0; i < 4; i++ {
		id := fmt.Sprint(i)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, id, preparationRequest()))
		ready := waitPreparationStatus(t, sender, id, "ready", "")
		if i == 0 {
			first = ready
		}
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "0", proto.ExecutionReleasePayload{Handle: first.Handle}))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	request := mustEnv(t, proto.TypeExecutionPrepare, "replacement", preparationRequest())
	if err := r.Handle(t.Context(), request); err == nil {
		t.Fatal("closing resource returned capacity early")
	}
	release()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := r.Handle(t.Context(), request); err == nil {
			waitPreparationStatus(t, sender, "replacement", "ready", "")
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("completed cleanup did not release capacity")
}

func TestPreparationRejectsInputAndProductConfiguration(t *testing.T) {
	for name, change := range map[string]func(*proto.PromptRequestPayload){
		"run":            func(p *proto.PromptRequestPayload) { p.RunID = "run" },
		"input":          func(p *proto.PromptRequestPayload) { p.Prompt = "input" },
		"conversation":   func(p *proto.PromptRequestPayload) { p.ConversationID = "product" },
		"authoring":      func(p *proto.PromptRequestPayload) { p.WorkspaceAuthoring = true },
		"attachment":     func(p *proto.PromptRequestPayload) { p.Attachments = []proto.PromptAttachment{{Kind: "image"}} },
		"local fallback": func(p *proto.PromptRequestPayload) { p.RemoteEnvironment = nil },
		"resume":         func(p *proto.PromptRequestPayload) { p.StrictResume = false },
		"release":        func(p *proto.PromptRequestPayload) { p.ReleaseOnCompletion = false },
	} {
		t.Run(name, func(t *testing.T) {
			r := preparationRouter(t, &recSender{}, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
				t.Error("invalid preparation reached native factory")
				return nil, errors.New("invalid")
			})
			req := preparationRequest()
			change(&req.Configuration)
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", req)); err == nil {
				t.Fatal("invalid preparation accepted")
			}
			if r.ActiveRuns() != 0 {
				t.Fatal("invalid configuration became a Run")
			}
		})
	}
}
