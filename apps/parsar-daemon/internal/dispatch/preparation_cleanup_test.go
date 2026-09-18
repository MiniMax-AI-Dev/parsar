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
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type retryablePreparation struct {
	controlledPreparation
	calls atomic.Int32
	close func(int32) error
}

func (p *retryablePreparation) Close() error { return p.close(p.calls.Add(1)) }

type cancellingRetryablePreparation struct{ *retryablePreparation }

func (p *cancellingRetryablePreparation) Cancel(context.Context) error { return p.Close() }
func (p *cancellingRetryablePreparation) CancellationOutcome() proto.DonePayload {
	return proto.DonePayload{}
}

func TestPreparedCancellationDoesNotAcknowledgeFailedCleanup(t *testing.T) {
	entered, cancelled := make(chan struct{}), make(chan struct{})
	p := &cancellingRetryablePreparation{&retryablePreparation{close: func(call int32) error {
		if call == 1 {
			return errors.New("cleanup incomplete")
		}
		close(cancelled)
		return nil
	}}}
	p.start = func(_ context.Context, _, _ string, _ chan<- proto.Envelope) (agent.Session, error) {
		close(entered)
		<-cancelled
		return nil, context.Canceled
	}
	sender := &recSender{}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	ready := startCancellationPreparation(t, r, sender)
	<-entered
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"}))
	waitFor(t, func() bool { return len(cancellationAcks(sender)) == 1 }, "failed cleanup receipt")
	ack := cancellationAcks(sender)[0]
	if ack.Applied || ack.ErrorCode != "cancel_failed" || ack.Outcome != nil {
		t.Fatalf("cleanup failure reported as applied: %+v", ack)
	}
	if owned, _ := r.PreparationOwnershipForTest(ready.Handle); !owned {
		t.Fatal("cancel receipt discarded unsettled preparation")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: ready.Handle}))
	waitFor(t, func() bool { owned, _ := r.PreparationOwnershipForTest(ready.Handle); return !owned }, "retained cancellation cleanup")
}

func TestShutdownRetriesFailedPreparedCancellationOnSameTarget(t *testing.T) {
	want := errors.New("prepared cleanup incomplete")
	sender := &recSender{}
	var session *fakeSession
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true}
		return session, nil
	}
	p.cancel = func(ctx context.Context) error {
		if p.calls.Load() == 1 {
			return want
		}
		return session.Cancel(ctx)
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitPreparationStatus(t, sender, "request", "started", "")
	if err := r.Shutdown(t.Context()); !errors.Is(err, want) {
		t.Fatalf("first shutdown lost native failure: %v", err)
	}
	if r.ActiveRuns() != 1 || p.calls.Load() != 1 || session.cancels() != 0 {
		t.Fatal("failed shutdown released ownership or changed release target")
	}
	select {
	case <-p.closed:
		t.Fatal("failed prepared cancellation fell back to Close")
	default:
	}
	if err := r.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.ActiveRuns() != 0 || p.calls.Load() != 2 || session.cancels() != 1 {
		t.Fatalf("shutdown did not retry the same target once: active=%d target=%d session=%d", r.ActiveRuns(), p.calls.Load(), session.cancels())
	}
}

func TestPreparationCloseFailureRetainsCapacityAndRetries(t *testing.T) {
	sender := &recSender{}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	p := &retryablePreparation{close: func(call int32) error {
		if call == 1 {
			return errors.New("cleanup incomplete")
		}
		if call == 2 {
			close(entered)
			<-release
		}
		return nil
	}}
	var factories atomic.Int32
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		if factories.Add(1) == 1 {
			return p, nil
		}
		return &controlledPreparation{closed: make(chan struct{})}, nil
	})
	t.Cleanup(unblock)
	var handle string
	for i := range 4 {
		id := fmt.Sprint(i)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, id, preparationRequest()))
		ready := waitPreparationStatus(t, sender, id, "ready", "")
		if i == 0 {
			handle = ready.Handle
		}
	}
	request := mustEnv(t, proto.TypeExecutionRelease, "0", proto.ExecutionReleasePayload{Handle: handle})
	_ = r.Handle(t.Context(), request)
	waitFor(t, func() bool { _, busy := r.PreparationOwnershipForTest(handle); return p.calls.Load() == 1 && !busy }, "failed cleanup return")
	if owned, _ := r.PreparationOwnershipForTest(handle); !owned {
		t.Fatal("failed cleanup discarded ownership")
	}
	replacement := mustEnv(t, proto.TypeExecutionPrepare, "replacement", preparationRequest())
	if err := r.Handle(t.Context(), replacement); err == nil {
		t.Fatal("failed cleanup released capacity")
	}
	start := proto.ExecutionStartPayload{Handle: handle, RunID: "run", Prompt: "do not execute"}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "0", start)); err == nil {
		t.Fatal("failed cleanup allowed Start")
	}
	_ = r.Handle(t.Context(), request)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("release did not retry retained resource")
	}
	_ = r.Handle(t.Context(), request)
	if p.calls.Load() != 2 {
		t.Fatal("release started overlapping cleanup")
	}
	unblock()
	waitFor(t, func() bool { owned, _ := r.PreparationOwnershipForTest(handle); return !owned }, "successful cleanup")
	if err := r.Handle(t.Context(), replacement); err != nil {
		t.Fatalf("settled cleanup retained capacity: %v", err)
	}
	waitPreparationStatus(t, sender, "replacement", "ready", "")
	if p.starts.Load() != 0 {
		t.Fatal("cleanup restarted native execution")
	}
}

func TestShutdownRetriesFailedPreparationCleanup(t *testing.T) {
	want := errors.New("native cleanup incomplete")
	p := &retryablePreparation{close: func(call int32) error {
		if call == 1 {
			return want
		}
		return nil
	}}
	sender := &recSender{}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest()))
	handle := waitPreparationStatus(t, sender, "request", "ready", "").Handle
	if err := r.Shutdown(t.Context()); !errors.Is(err, want) {
		t.Fatalf("shutdown lost cleanup error: %v", err)
	}
	if owned, _ := r.PreparationOwnershipForTest(handle); !owned {
		t.Fatal("shutdown discarded unsettled resource")
	}
	if err := r.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owned, _ := r.PreparationOwnershipForTest(handle); owned || p.calls.Load() != 2 {
		t.Fatalf("retry did not settle original resource: owned=%t calls=%d", owned, p.calls.Load())
	}
}

func TestShutdownTimeoutAndConcurrentRetryWaitForCleanup(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	p := &retryablePreparation{close: func(int32) error { close(entered); <-release; return nil }}
	sender := &recSender{}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	t.Cleanup(unblock)
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest()))
	waitPreparationStatus(t, sender, "request", "ready", "")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := r.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while cleanup blocked: %v", err)
	}
	<-entered
	results := make(chan error, 4)
	for range cap(results) {
		go func() { results <- r.Shutdown(t.Context()) }()
	}
	select {
	case err := <-results:
		t.Fatalf("retry returned before cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	for range cap(results) {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("shutdown wait did not finish")
		}
	}
	if p.calls.Load() != 1 {
		t.Fatal("shutdown repeated in-flight cleanup")
	}
}

func TestPublishedPreparedRunRetainsRetryAfterHandleRetirement(t *testing.T) {
	sender := &recSender{}
	session := &fakeSession{closeOutOnCancel: true}
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	p.cancel = func(ctx context.Context) error {
		if p.calls.Load() == 1 {
			return errors.New("cleanup incomplete")
		}
		return session.Cancel(ctx)
	}
	var factories atomic.Int32
	r := preparationRouter(t, sender, 200*time.Millisecond, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		if factories.Add(1) == 1 {
			return p, nil
		}
		return &controlledPreparation{closed: make(chan struct{})}, nil
	})
	ready := startCancellationPreparation(t, r, sender)
	waitPreparationStatus(t, sender, "request", "started", "")
	waitFor(t, func() bool { owned, _ := r.PreparationOwnershipForTest(ready.Handle); return !owned }, "publication transfer")
	time.Sleep(time.Until(time.UnixMilli(ready.ExpiresAt)) + 20*time.Millisecond)
	if p.calls.Load() != 0 {
		t.Fatal("preparation expiry cancelled a published Run")
	}
	// A later admission retires the expired preparation record. The Run still
	// owns the exact cancellation target independently of that old handle.
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "replacement", preparationRequest())); err != nil {
		t.Fatal(err)
	}
	waitPreparationStatus(t, sender, "replacement", "ready", "")
	for i := range 2 {
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: fmt.Sprint("cancel-", i)})); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return len(cancellationAcks(sender)) == i+1 }, "Run cancellation receipt")
		if i == 0 {
			if ack := cancellationAcks(sender)[0]; ack.Applied || ack.ErrorCode != "cancel_failed" || r.ActiveRuns() != 1 {
				t.Fatalf("failed cleanup lost retained Run: %+v", ack)
			}
			if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: ready.Handle})); err == nil {
				t.Fatal("retired preparation handle was restored")
			}
		}
	}
	if p.calls.Load() != 2 || session.cancels() != 1 || r.ActiveRuns() != 0 {
		t.Fatal("Run cancellation did not retry and settle the same target")
	}
	select {
	case <-p.closed:
		t.Fatal("Run cancellation fell back to preparation Close")
	default:
	}
}
