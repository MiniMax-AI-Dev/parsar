package dispatch_test

import (
	"context"
	"errors"
	"fmt"
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
	mu        sync.Mutex
	session   agent.Session
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
	session, err := p.start(ctx, id, prompt, out)
	if session != nil {
		p.mu.Lock()
		p.session = session
		p.mu.Unlock()
	}
	return session, err
}

func (p *controlledPreparation) Cancel(ctx context.Context) error {
	p.mu.Lock()
	session := p.session
	p.mu.Unlock()
	if session != nil {
		return session.Cancel(ctx)
	}
	return p.Close()
}

func (p *controlledPreparation) CancellationOutcome() proto.DonePayload {
	p.mu.Lock()
	session := p.session
	p.mu.Unlock()
	if provider, ok := session.(interface{ CancellationOutcome() proto.DonePayload }); ok {
		return provider.CancellationOutcome()
	}
	return proto.DonePayload{}
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
	entered, cancelEntered, allowReturn := make(chan struct{}), make(chan struct{}), make(chan struct{})
	lateSession := make(chan *fakeSession, 1)
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		close(entered)
		<-allowReturn
		s := &fakeSession{out: out, closeOutOnCancel: true}
		lateSession <- s
		return s, nil
	}
	p.cancel = func(ctx context.Context) error {
		close(cancelEntered)
		return (<-lateSession).Cancel(ctx)
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "request", "ready", "")
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "real-run", Prompt: "input"}))
	<-entered
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "real-run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelEntered:
	case <-time.After(time.Second):
		t.Fatal("fixed cancellation target was not called across Start")
	}
	close(allowReturn)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "late cancelled Session cleanup")
	p.mu.Lock()
	session := p.session.(*fakeSession)
	p.mu.Unlock()
	if session.cancels() != 1 || r.ActiveRuns() != 0 {
		t.Fatal("late session resurrected cancelled run")
	}
	select {
	case <-p.controlledPreparation.closed:
		t.Fatal("transferred preparation was released a second time")
	default:
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
