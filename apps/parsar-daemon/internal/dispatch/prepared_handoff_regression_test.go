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

type failFirstStartedSender struct {
	*recSender
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	attempts atomic.Int32
}

func (s *failFirstStartedSender) Send(ctx context.Context, env proto.Envelope) error {
	var status proto.PreparationStatusPayload
	if env.Type == proto.TypePreparationStatus && env.DecodePayload(&status) == nil && status.State == "started" {
		if s.attempts.Add(1) == 1 {
			s.once.Do(func() { close(s.entered) })
			select {
			case <-s.release:
				return errors.New("controlled first started status failure")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparationDuplicateStartCannotPublishUncommittedStartedStatus(t *testing.T) {
	sender := &failFirstStartedSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	ready := startCancellationPreparation(t, r, sender.recSender)
	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("started status delivery did not block")
	}
	duplicate := mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "input"})
	if err := r.Handle(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	close(sender.release)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed started status cleanup")
	if sender.attempts.Load() != 1 {
		t.Fatalf("duplicate published uncommitted started status %d times", sender.attempts.Load())
	}
	for _, frame := range sender.snapshot() {
		var status proto.PreparationStatusPayload
		if frame.Type == proto.TypePreparationStatus && frame.DecodePayload(&status) == nil && status.State == "started" {
			t.Fatal("failed authoritative publication exposed started status")
		}
	}
}

func TestPreparationDoneClosesFunctionResultAdmission(t *testing.T) {
	sender := &recSender{}
	cancelEntered, cancelRelease := make(chan struct{}), make(chan struct{})
	session := &functionSession{fakeSession: &fakeSession{closeOutOnCancel: true, cancelEntered: cancelEntered, cancelRelease: cancelRelease}}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		return session, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender)
	waitPreparationStatus(t, sender, "request", "started", "")
	session.out <- mustEnv(t, proto.TypeFunctionCall, "run", proto.FunctionCallPayload{CallID: "call", Name: "lookup"})
	session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
	select {
	case <-cancelEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not start release")
	}
	result := mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("late"), DeliveryID: "late-result"})
	if err := r.Handle(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	assertDecisionAck(t, sender, "late-result", false, "not_ready")
	session.mu.Lock()
	calls := session.calls
	session.mu.Unlock()
	if calls != 0 {
		t.Fatal("function result reached Session after terminal admission closed")
	}
	close(cancelRelease)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "completion cleanup")
}

func TestPreparedCancellationRequiresObservedOutcome(t *testing.T) {
	t.Run("session_without_outcome", func(t *testing.T) {
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
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { return len(cancellationAcks(sender)) == 1 }, "cancellation receipt")
		ack := cancellationAcks(sender)[0]
		if ack.Applied || ack.Outcome != nil || ack.ErrorCode != "cancel_outcome_unavailable" {
			t.Fatalf("unobserved session outcome was accepted: %+v", ack)
		}
	})

	t.Run("close_target", func(t *testing.T) {
		sender := &recSender{}
		closeEntered, closeRelease := make(chan struct{}), make(chan struct{})
		p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, cancel: func(context.Context) error { return nil }}
		p.closeHook = func() { close(closeEntered); <-closeRelease }
		p.start = func(context.Context, string, string, chan<- proto.Envelope) (agent.Session, error) {
			return nil, errors.New("controlled start failure")
		}
		r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
		startCancellationPreparation(t, r, sender)
		select {
		case <-closeEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("close target did not start")
		}
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
			t.Fatal(err)
		}
		close(closeRelease)
		waitFor(t, func() bool { return len(cancellationAcks(sender)) == 1 }, "close-target cancellation receipt")
		ack := cancellationAcks(sender)[0]
		if ack.Applied || ack.Outcome != nil || ack.ErrorCode != "cancel_outcome_unavailable" {
			t.Fatalf("close target invented cancellation outcome: %+v", ack)
		}
		if p.calls.Load() != 0 {
			t.Fatal("close target retried through Prepared.Cancel")
		}
	})
}
