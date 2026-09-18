package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const preparedBurstFrames = 96

func TestPreparedHandoffDrainsBurstBeforeStartReturns(t *testing.T) {
	sender := &recSender{}
	sent, allowReturn := make(chan struct{}), make(chan struct{})
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		for sequence := uint64(1); sequence <= preparedBurstFrames; sequence++ {
			select {
			case out <- mustEnv(t, proto.TypeDelta, "run", proto.DeltaPayload{Delta: "burst", Sequence: sequence}):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		close(sent)
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
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("prepared output blocked at the 64-frame channel capacity")
	}
	waitFor(t, func() bool { return preparedDeltaCount(sender, "run") == preparedBurstFrames }, "pre-Start burst forwarding")
	assertPreparedDeltaOrder(t, sender, "run", preparedBurstFrames)
	close(allowReturn)
	waitPreparationStatus(t, sender, "request", "started", "")
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "prepared burst completion")
	if session.cancels() != 1 {
		t.Fatalf("Session release calls = %d, want 1", session.cancels())
	}
	assertPreparedDeltaOrder(t, sender, "run", preparedBurstFrames)
}

func preparedDeltaCount(sender *recSender, runID string) int {
	count := 0
	for _, frame := range sender.snapshot() {
		if frame.ID == runID && frame.Type == proto.TypeDelta {
			count++
		}
	}
	return count
}

func assertPreparedDeltaOrder(t *testing.T, sender *recSender, runID string, want int) {
	t.Helper()
	seen := 0
	for _, frame := range sender.snapshot() {
		if frame.ID != runID || frame.Type != proto.TypeDelta {
			continue
		}
		seen++
		var delta proto.DeltaPayload
		if err := frame.DecodePayload(&delta); err != nil || delta.Sequence != uint64(seen) {
			t.Fatalf("delta %d = %+v, err=%v", seen, delta, err)
		}
	}
	if seen != want {
		t.Fatalf("delta count = %d, want %d", seen, want)
	}
}

type blockingStartedSender struct {
	*recSender
	entered  chan struct{}
	release  chan struct{}
	attempts atomic.Int32
}

type blockingStartingSender struct {
	*recSender
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingStartingSender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "starting" {
		s.once.Do(func() { close(s.entered) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparedHandoffAbortBeforeStartAdmissionSkipsNativeStart(t *testing.T) {
	sender := &blockingStartingSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(context.Context, string, string, chan<- proto.Envelope) (agent.Session, error) {
		t.Fatal("abort that won admission called native Start")
		return nil, errors.New("unexpected Start")
	}
	p.cancel = func(context.Context) error { return p.Close() }
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	ready := startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(time.Second):
		t.Fatal("starting publication did not block")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.calls.Load() == 1 }, "fixed cancellation target")
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 && len(cancellationAcks(sender.recSender)) == 1 }, "pre-Start abort settlement")
	if p.starts.Load() != 0 || ready.Handle == "" {
		t.Fatalf("native Start calls = %d", p.starts.Load())
	}
	ack := cancellationAcks(sender.recSender)[0]
	if !ack.Applied || ack.ErrorCode != "" || ack.Outcome == nil {
		t.Fatalf("pre-Start cancellation receipt = %+v", ack)
	}
}

func (s *blockingStartedSender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "started" {
		if s.attempts.Add(1) == 1 {
			close(s.entered)
			select {
			case <-s.release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparedHandoffDuplicateStartDoesNotReexecuteDuringPublication(t *testing.T) {
	sender := &blockingStartedSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	session := &preparedMutationSession{fakeSession: &fakeSession{closeOutOnCancel: true}, cancelEntered: make(chan struct{})}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		out <- mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "publication-permission"})
		out <- mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "publication-choice"})
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	ready := startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("started status did not reach publication boundary")
	}
	waitFor(t, func() bool {
		return hasFrame(sender.recSender, proto.TypePermissionRequest, "run") && hasFrame(sender.recSender, proto.TypePromptForUserChoice, "run")
	}, "pre-publication interaction routes")
	for _, mutation := range []proto.Envelope{
		mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: "publication-function"}),
		mustEnv(t, proto.TypePermissionDecision, "publication-permission", proto.PermissionDecisionPayload{DeliveryID: "publication-permission", Approved: true}),
		mustEnv(t, proto.TypePromptForUserChoiceDecision, "publication-choice", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "publication-choice", Answers: []string{"yes"}}),
	} {
		if err := r.Handle(t.Context(), mutation); err != nil {
			t.Fatal(err)
		}
	}
	assertDecisionAck(t, sender.recSender, "publication-function", false, "not_ready")
	assertDecisionAck(t, sender.recSender, "publication-permission", false, "not_ready")
	assertDecisionAck(t, sender.recSender, "publication-choice", false, "not_ready")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "publication-steering", Text: "continue"})); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, sender.recSender, "run", "publication-steering"); ack.ErrorCode != "not_ready" {
		t.Fatalf("pre-publication steering = %+v", ack)
	}
	read := proto.WorkspaceReadPayload{RunID: "run", EnvironmentID: "environment", Path: "file", MaxBytes: 1}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "publication-read", read)); err != nil {
		t.Fatal(err)
	}
	var readResult proto.WorkspaceReadResultPayload
	frames := sender.snapshot()
	if len(frames) == 0 || frames[len(frames)-1].Type != proto.TypeWorkspaceReadResult || frames[len(frames)-1].DecodePayload(&readResult) != nil || readResult.ErrorCode != "resource_unavailable" {
		t.Fatalf("pre-publication workspace read = %+v", frames)
	}
	session.askMu.Lock()
	askCalls := len(session.askCalls)
	session.askMu.Unlock()
	if session.functions.Load() != 0 || session.steers.Load() != 0 || session.reads.Load() != 0 || len(session.submissions()) != 0 || askCalls != 0 {
		t.Fatal("private Session accepted work before started publication")
	}
	duplicate := mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "input"})
	if err := r.Handle(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	if p.starts.Load() != 1 || sender.attempts.Load() != 1 {
		t.Fatalf("duplicate Start re-executed work: starts=%d publications=%d", p.starts.Load(), sender.attempts.Load())
	}
	close(sender.release)
	waitPreparationStatus(t, sender.recSender, "request", "started", "")
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "duplicate Start cleanup")
}

func TestPreparedHandoffUnsupportedFunctionReleasesOperationBarrier(t *testing.T) {
	sender := &recSender{}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitPreparationStatus(t, sender, "request", "started", "")
	result := mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "unsupported", Success: true, Content: functionResultContent("answer"), DeliveryID: "unsupported"})
	if err := r.Handle(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	assertDecisionAck(t, sender, "unsupported", false, "not_pending")
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{})
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "release after unsupported function")
	if session.cancels() != 1 {
		t.Fatalf("native release calls = %d, want 1", session.cancels())
	}
}

func TestPreparedHandoffEarlyDoneStillAllowsExplicitAbort(t *testing.T) {
	sender := &recSender{}
	cancelled := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(cancelled) }) }
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{})
		<-cancelled
		return nil, context.Canceled
	}
	p.cancel = func(context.Context) error { unblock(); return nil }
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	defer unblock()
	startCancellationPreparation(t, r, sender)
	waitFor(t, func() bool { return r.SteeringClosedForTest("run") }, "early Done release claim")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "abort"})); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for p.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.calls.Load() == 0 {
		t.Error("explicit abort never reaches native Cancel after early Done: natural release is still waiting for Start")
	}
}

func TestPreparedHandoffExpiresDuringStartedPublication(t *testing.T) {
	sender := &blockingStartedSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, 100*time.Millisecond, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	<-sender.entered
	time.Sleep(250 * time.Millisecond)
	if session.cancels() == 0 {
		t.Error("preparation deadline did not cancel an unpublished Session")
	}
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "expired handoff cleanup")
	if attempts := sender.attempts.Load(); attempts != 1 {
		t.Fatalf("expired preparation republished provisional started: attempts=%d", attempts)
	}
	for _, frame := range sender.snapshot() {
		var status proto.PreparationStatusPayload
		if frame.Type == proto.TypePreparationStatus && frame.DecodePayload(&status) == nil && status.State == "started" {
			t.Fatal("expired preparation published started")
		}
	}
}

func TestPreparedHandoffEarlyDoneDetachesPublishedPreparation(t *testing.T) {
	sender := &recSender{}
	allowReturn := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allowReturn) }) }
	defer unblock()
	session := &fakeSession{closeOutOnCancel: true}
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{})
		<-allowReturn
		return session, nil
	}
	p.cancel = func(ctx context.Context) error {
		if p.calls.Load() == 1 {
			return errors.New("cleanup incomplete")
		}
		return session.Cancel(ctx)
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	ready := startCancellationPreparation(t, r, sender)
	waitFor(t, func() bool { return r.SteeringClosedForTest("run") }, "early Done claim")
	unblock()
	waitPreparationStatus(t, sender, "request", "started", "")
	waitFor(t, func() bool {
		_, busy := r.PreparationOwnershipForTest(ready.Handle)
		return p.calls.Load() == 1 && !busy
	}, "failed natural cleanup")
	if owned, _ := r.PreparationOwnershipForTest(ready.Handle); owned {
		t.Fatal("published Run retained preparation capacity after early Done")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		count := 0
		for _, frame := range sender.snapshot() {
			var status proto.PreparationStatusPayload
			if frame.Type == proto.TypePreparationStatus && frame.DecodePayload(&status) == nil && status.State == "started" {
				count++
			}
		}
		return count == 2
	}, "published preparation release response")
	if p.calls.Load() != 1 {
		t.Fatal("old preparation handle retried native cancellation")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "retry"})); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(cancellationAcks(sender)) == 1 && r.ActiveRuns() == 0 }, "Run-owned retry settlement")
	if p.calls.Load() != 2 || session.cancels() != 1 || !cancellationAcks(sender)[0].Applied {
		t.Fatal("explicit Run cancellation did not retry the retained native target")
	}
}
