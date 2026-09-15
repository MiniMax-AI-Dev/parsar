package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type cancellationPreparation struct {
	*controlledPreparation
	cancel  func(context.Context) error
	outcome proto.DonePayload
	calls   atomic.Int32
}

func (p *cancellationPreparation) Cancel(ctx context.Context) error {
	p.calls.Add(1)
	return p.cancel(ctx)
}

func (p *cancellationPreparation) CancellationOutcome() proto.DonePayload { return p.outcome }

func startCancellationPreparation(t *testing.T, r *dispatch.Router, sender *recSender) proto.PreparationStatusPayload {
	t.Helper()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "request", preparationRequest())); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "request", "ready", "")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "request", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "input"})); err != nil {
		t.Fatal(err)
	}
	return ready
}

func cancellationAcks(sender *recSender) []proto.InteractionDecisionAckPayload {
	var acks []proto.InteractionDecisionAckPayload
	for _, frame := range sender.snapshot() {
		if frame.Type == proto.TypeInteractionDecisionAck && frame.ID == "run" {
			var ack proto.InteractionDecisionAckPayload
			_ = frame.DecodePayload(&ack)
			acks = append(acks, ack)
		}
	}
	return acks
}

type cancellationOutputSender struct {
	*recSender
	entered, release chan struct{}
	fail             bool
}

func (s cancellationOutputSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.Type == proto.TypeUsage {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if s.fail {
			return errors.New("controlled output delivery failure")
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestPreparedCancellationWaitsForOutputAndCleanup(t *testing.T) {
	sender := cancellationOutputSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{})}
	startEntered, startReturn := make(chan struct{}), make(chan struct{})
	cancelEntered := make(chan struct{})
	cleanupEntered, cleanupReturn := make(chan struct{}), make(chan struct{})
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, outcome: proto.DonePayload{
		Content: "observed", Usage: proto.Usage{Tokens: &proto.TokenUsage{InputTokens: 7, OutputTokens: 3, TotalTokens: 10}}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "observed-native"},
	}}
	var session *fakeSession
	p.start = func(_ context.Context, id, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true,
			postCancelEnvelopes: []proto.Envelope{mustEnv(t, proto.TypeDone, id, p.outcome)}}
		out <- mustEnv(t, proto.TypeDelta, id, proto.DeltaPayload{Delta: "observed"})
		out <- mustEnv(t, proto.TypePermissionRequest, id, proto.PermissionRequestPayload{RequestID: "permission"})
		out <- mustEnv(t, proto.TypePromptForUserChoice, id, proto.PromptForUserChoicePayload{AskID: "ask"})
		out <- mustEnv(t, proto.TypeUsage, id, proto.UsagePayload{Usage: p.outcome.Usage})
		close(startEntered)
		<-startReturn
		return session, nil
	}
	p.cancel = func(ctx context.Context) error { close(cancelEntered); return session.Cancel(ctx) }
	p.closeHook = func() { close(cleanupEntered); <-cleanupReturn }
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	startCancellationPreparation(t, r, sender.recSender)
	<-startEntered
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"})); err != nil {
		t.Fatal(err)
	}
	<-cancelEntered
	if len(cancellationAcks(sender.recSender)) != 0 {
		t.Fatal("receipt preceded Start handoff")
	}
	close(startReturn)
	<-sender.entered
	if len(cancellationAcks(sender.recSender)) != 0 {
		t.Fatal("receipt preceded output delivery")
	}
	// Another receipt may wait, but it must not repeat native cancellation.
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "second-delivery"}))
	close(sender.release)
	<-cleanupEntered
	if len(cancellationAcks(sender.recSender)) != 0 || r.ActiveRuns() != 1 {
		t.Fatal("cleanup released ownership early")
	}
	for _, decision := range []proto.Envelope{
		mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: "permission-reply", Approved: true}),
		mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "ask-reply", Answers: []string{"yes"}}),
	} {
		if err := r.Handle(t.Context(), decision); err != nil {
			t.Fatal(err)
		}
	}
	assertDecisionAck(t, sender.recSender, "permission-reply", false, "not_pending")
	assertDecisionAck(t, sender.recSender, "ask-reply", false, "not_pending")
	close(cleanupReturn)
	waitFor(t, func() bool { return len(cancellationAcks(sender.recSender)) == 2 && r.ActiveRuns() == 0 }, "prepared cancellation receipts")
	if p.calls.Load() != 1 || session.cancels() != 1 {
		t.Fatal("native cancellation was repeated")
	}
	for _, ack := range cancellationAcks(sender.recSender) {
		if !ack.Applied || ack.ErrorCode != "" || ack.Outcome == nil || !reflect.DeepEqual(*ack.Outcome, p.outcome) {
			t.Fatalf("observed outcome lost: %+v", ack)
		}
	}
	got := sender.typesFor("run")
	want := []string{proto.TypeDelta, proto.TypePermissionRequest, proto.TypePromptForUserChoice, proto.TypeUsage, proto.TypeDone, proto.TypeInteractionDecisionAck, proto.TypeInteractionDecisionAck}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output/receipt order = %v", got)
	}
}

func TestPreparedCancellationBeforeTransferPreservesUnknownOutcome(t *testing.T) {
	for _, boundary := range []string{"start_failure", "release", "expiry", "shutdown"} {
		t.Run(boundary, func(t *testing.T) {
			sender := &recSender{}
			entered, allowReturn := make(chan struct{}), make(chan struct{})
			p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, cancel: func(context.Context) error { return nil }}
			p.start = func(ctx context.Context, _, _ string, _ chan<- proto.Envelope) (agent.Session, error) {
				close(entered)
				<-ctx.Done()
				<-allowReturn
				return nil, ctx.Err()
			}
			timeout := time.Minute
			if boundary == "expiry" {
				timeout = 100 * time.Millisecond
			}
			r := preparationRouter(t, sender, timeout, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
			ready := startCancellationPreparation(t, r, sender)
			<-entered
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"}))
			var shutdown chan error
			switch boundary {
			case "release":
				_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "request", proto.ExecutionReleasePayload{Handle: ready.Handle}))
				waitPreparationStatus(t, sender, "request", "released", "")
			case "expiry":
				waitPreparationStatus(t, sender, "request", "expired", "")
			case "shutdown":
				shutdown = make(chan error, 1)
				go func() { shutdown <- r.Shutdown(t.Context()) }()
			}
			close(allowReturn)
			waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "failed Start cleanup")
			if shutdown != nil {
				if err := <-shutdown; err != nil {
					t.Fatal(err)
				}
			} else {
				waitFor(t, func() bool { return len(cancellationAcks(sender)) == 1 }, "unstarted cancellation receipt")
				ack := cancellationAcks(sender)[0]
				if !ack.Applied || ack.Outcome == nil || !reflect.DeepEqual(*ack.Outcome, proto.DonePayload{}) {
					t.Fatalf("unknown outcome was invented or unavailable: %+v", ack)
				}
			}
			waitPreparationClosed(t, p.controlledPreparation)
			if p.calls.Load() != 1 || p.starts.Load() != 1 {
				t.Fatal("cancellation replayed work")
			}
		})
	}
}

func TestPreparedCancellationFailuresRemainConservative(t *testing.T) {
	for _, failure := range []string{"missing_capability", "cancel_failed", "cancel_output_unavailable"} {
		t.Run(failure, func(t *testing.T) {
			sender := cancellationOutputSender{recSender: &recSender{}, entered: make(chan struct{}), release: make(chan struct{}), fail: failure == "cancel_output_unavailable"}
			close(sender.release)
			entered, allowReturn := make(chan struct{}), make(chan struct{})
			var session *fakeSession
			p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}}
			p.start = func(_ context.Context, id, _ string, out chan<- proto.Envelope) (agent.Session, error) {
				session = &fakeSession{out: out, closeOutOnCancel: true}
				out <- mustEnv(t, proto.TypeUsage, id, proto.UsagePayload{Usage: proto.Usage{InputTokens: 3}})
				close(entered)
				<-allowReturn
				return session, errors.New("controlled late Start failure")
			}
			p.cancel = func(ctx context.Context) error {
				if failure == "cancel_failed" {
					return errors.New("controlled native failure")
				}
				return session.Cancel(ctx)
			}
			r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
				if failure == "missing_capability" {
					return p.controlledPreparation, nil
				}
				return p, nil
			})
			startCancellationPreparation(t, r, sender.recSender)
			<-entered
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"}))
			close(allowReturn)
			waitFor(t, func() bool { return len(cancellationAcks(sender.recSender)) == 1 && r.ActiveRuns() == 0 }, "failed cancellation")
			ack := cancellationAcks(sender.recSender)[0]
			want := failure
			if failure == "missing_capability" {
				want = "cancel_outcome_unavailable"
			}
			if ack.Applied || ack.Outcome != nil || ack.ErrorCode != want || session.cancels() != 1 {
				t.Fatalf("uncertain cancellation accepted: %+v", ack)
			}
		})
	}
}

func TestPreparedCancellationTimeoutKeepsCapacityUntilStartReturns(t *testing.T) {
	sender := &recSender{}
	entered, allowReturn := make(chan struct{}), make(chan struct{})
	p := &cancellationPreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, cancel: func(context.Context) error { return nil }}
	p.start = func(ctx context.Context, _, _ string, _ chan<- proto.Envelope) (agent.Session, error) {
		close(entered)
		<-allowReturn
		return nil, ctx.Err()
	}
	var count atomic.Int32
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		if count.Add(1) == 1 {
			return p, nil
		}
		return &controlledPreparation{closed: make(chan struct{})}, nil
	})
	startCancellationPreparation(t, r, sender)
	<-entered
	for i := 0; i < 3; i++ {
		id := fmt.Sprint(i)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, id, preparationRequest()))
		waitPreparationStatus(t, sender, id, "ready", "")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel"}))
	deadline := time.Now().Add(12 * time.Second)
	for len(cancellationAcks(sender)) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	acks := cancellationAcks(sender)
	if len(acks) != 1 || acks[0].Applied || acks[0].Outcome != nil || acks[0].ErrorCode != "cancel_timeout" || r.ActiveRuns() != 1 {
		t.Fatal("timeout claimed settlement or lost ownership", acks)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "overflow", preparationRequest())); err == nil {
		t.Fatal("timed-out cancellation returned capacity early")
	}
	close(allowReturn)
	waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "late cancelled Start cleanup")
	if len(cancellationAcks(sender)) != 1 {
		t.Fatal("late settlement emitted a second receipt")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "replacement", preparationRequest())); err != nil {
		t.Fatal("completed cleanup retained capacity", err)
	}
	waitPreparationStatus(t, sender, "replacement", "ready", "")
}
