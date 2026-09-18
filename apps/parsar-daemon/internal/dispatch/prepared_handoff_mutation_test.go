package dispatch_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type preparedMutationSession struct {
	*fakeSession
	cancelEntered chan struct{}
	cancelOnce    sync.Once
	beforeCancel  func()
	functions     atomic.Int32
	steers        atomic.Int32
	reads         atomic.Int32
}

func (s *preparedMutationSession) Cancel(ctx context.Context) error {
	if s.beforeCancel != nil {
		s.beforeCancel()
	}
	s.cancelOnce.Do(func() { close(s.cancelEntered) })
	return s.fakeSession.Cancel(ctx)
}

func (s *preparedMutationSession) SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error {
	s.functions.Add(1)
	return nil
}

func (s *preparedMutationSession) Steer(context.Context, proto.PromptSteerPayload) error {
	s.steers.Add(1)
	return nil
}

func (s *preparedMutationSession) ReadWorkspaceFile(context.Context, string, int) (agent.WorkspaceReadResult, error) {
	s.reads.Add(1)
	return agent.WorkspaceReadResult{Data: []byte("x")}, nil
}

type blockingPreparedReceiptSender struct {
	*recSender
	deliveryID string
	inputID    string
	entered    chan struct{}
	release    chan struct{}
	exited     chan struct{}
	once       sync.Once
}

func (s *blockingPreparedReceiptSender) Send(ctx context.Context, env proto.Envelope) error {
	if s.matches(env) {
		s.once.Do(func() { close(s.entered) })
		defer close(s.exited)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.recSender.Send(ctx, env)
}

func (s *blockingPreparedReceiptSender) matches(env proto.Envelope) bool {
	switch env.Type {
	case proto.TypeInteractionDecisionAck:
		var ack proto.InteractionDecisionAckPayload
		return env.DecodePayload(&ack) == nil && ack.DeliveryID == s.deliveryID
	case proto.TypePromptSteerAck:
		var ack proto.PromptSteerAckPayload
		return env.DecodePayload(&ack) == nil && ack.InputID == s.inputID
	default:
		return false
	}
}

func TestPreparedHandoffReleaseWaitsForMutationReceipt(t *testing.T) {
	for _, operation := range []string{"function", "permission", "choice", "steering"} {
		t.Run(operation, func(t *testing.T) {
			sender := &blockingPreparedReceiptSender{
				recSender: &recSender{}, deliveryID: operation + "-delivery", inputID: operation + "-input",
				entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}),
			}
			session := &preparedMutationSession{fakeSession: &fakeSession{closeOutOnCancel: true}, cancelEntered: make(chan struct{})}
			p := &controlledPreparation{closed: make(chan struct{})}
			p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
				session.out = out
				return session, nil
			}
			r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
			startCancellationPreparation(t, r, sender.recSender)
			waitPreparationStatus(t, sender.recSender, "request", "started", "")

			var mutation proto.Envelope
			switch operation {
			case "function":
				mutation = mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: sender.deliveryID})
			case "permission":
				session.out <- mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "permission"})
				waitFor(t, func() bool { return hasFrame(sender.recSender, proto.TypePermissionRequest, "run") }, "permission request")
				mutation = mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: sender.deliveryID, Approved: true})
			case "choice":
				session.out <- mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "ask"})
				waitFor(t, func() bool { return hasFrame(sender.recSender, proto.TypePromptForUserChoice, "run") }, "choice request")
				mutation = mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: sender.deliveryID, Answers: []string{"yes"}})
			case "steering":
				mutation = mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: sender.inputID, Text: "continue"})
			}

			returned := make(chan error, 1)
			go func() { returned <- r.Handle(t.Context(), mutation) }()
			select {
			case <-sender.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("mutation receipt did not block")
			}
			session.out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
			waitFor(t, func() bool { return r.SteeringClosedForTest("run") }, "release admission closure")
			switch operation {
			case "function":
				late := mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "late", Success: true, Content: functionResultContent("late"), DeliveryID: "late-function"})
				if err := r.Handle(t.Context(), late); err != nil {
					t.Fatal(err)
				}
				assertDecisionAck(t, sender.recSender, "late-function", false, "not_ready")
			case "permission":
				late := mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: "late-permission", Approved: true})
				if err := r.Handle(t.Context(), late); err != nil {
					t.Fatal(err)
				}
				assertDecisionAck(t, sender.recSender, "late-permission", true, "")
				newRequest := mustEnv(t, proto.TypePermissionDecision, "new-permission", proto.PermissionDecisionPayload{DeliveryID: "new-permission", Approved: true})
				if err := r.Handle(t.Context(), newRequest); err != nil {
					t.Fatal(err)
				}
				assertDecisionAck(t, sender.recSender, "new-permission", false, "not_pending")
			case "choice":
				late := mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "late-choice", Answers: []string{"yes"}})
				if err := r.Handle(t.Context(), late); err != nil {
					t.Fatal(err)
				}
				assertDecisionAck(t, sender.recSender, "late-choice", true, "")
				newRequest := mustEnv(t, proto.TypePromptForUserChoiceDecision, "new-choice", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "new-choice", Answers: []string{"yes"}})
				if err := r.Handle(t.Context(), newRequest); err != nil {
					t.Fatal(err)
				}
				assertDecisionAck(t, sender.recSender, "new-choice", false, "not_pending")
			case "steering":
				late := mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "late-steering", Text: "late"})
				if err := r.Handle(t.Context(), late); err != nil {
					t.Fatal(err)
				}
				if ack := lastSteeringAck(t, sender.recSender, "run", "late-steering"); ack.ErrorCode != "run_inactive" {
					t.Fatalf("late steering = %+v", ack)
				}
			}
			select {
			case <-session.cancelEntered:
				t.Fatal("native release overtook an admitted receipt")
			case <-time.After(50 * time.Millisecond):
			}
			close(sender.release)
			select {
			case err := <-returned:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("mutation handler did not finish")
			}
			select {
			case <-session.cancelEntered:
			case <-time.After(2 * time.Second):
				t.Fatal("release did not follow the completed receipt")
			}
			waitFor(t, func() bool { return r.ActiveRuns() == 0 }, "mutation release cleanup")
			if session.cancels() != 1 {
				t.Fatalf("Session release calls = %d, want 1", session.cancels())
			}
			switch operation {
			case "function":
				if session.functions.Load() != 1 {
					t.Fatalf("function native calls = %d, want 1", session.functions.Load())
				}
			case "permission":
				if len(session.submissions()) != 1 {
					t.Fatalf("permission native calls = %d, want 1", len(session.submissions()))
				}
			case "choice":
				session.askMu.Lock()
				calls := len(session.askCalls)
				session.askMu.Unlock()
				if calls != 1 {
					t.Fatalf("choice native calls = %d, want 1", calls)
				}
			case "steering":
				if session.steers.Load() != 1 {
					t.Fatalf("steering native calls = %d, want 1", session.steers.Load())
				}
			}
			assertReceiptBeforeDone(t, sender.recSender, operation)
		})
	}
}

func hasFrame(sender *recSender, kind, id string) bool {
	for _, frame := range sender.snapshot() {
		if frame.Type == kind && frame.ID == id {
			return true
		}
	}
	return false
}

func assertReceiptBeforeDone(t *testing.T, sender *recSender, operation string) {
	t.Helper()
	receipt, done := -1, -1
	for i, frame := range sender.snapshot() {
		if frame.Type == proto.TypeDone && frame.ID == "run" {
			done = i
		}
		if operation == "steering" && frame.Type == proto.TypePromptSteerAck || operation != "steering" && frame.Type == proto.TypeInteractionDecisionAck {
			receipt = i
		}
	}
	if receipt < 0 || done < 0 || receipt >= done {
		t.Fatalf("receipt/Done order invalid: receipt=%d done=%d frames=%v", receipt, done, sender.typesFor("run"))
	}
}

func TestPreparedHandoffRouterShutdownWaitsForReceiptAttempt(t *testing.T) {
	for _, operation := range []string{"function", "permission", "choice", "steering"} {
		t.Run(operation, func(t *testing.T) {
			sender := &blockingPreparedReceiptSender{recSender: &recSender{}, deliveryID: operation + "-delivery", inputID: operation + "-input", entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
			defer close(sender.release)
			var cancelBeforeReceipt atomic.Bool
			session := &preparedMutationSession{fakeSession: &fakeSession{closeOutOnCancel: true}, cancelEntered: make(chan struct{})}
			p := &controlledPreparation{closed: make(chan struct{})}
			p.start = func(_ context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
				session.out = out
				return session, nil
			}
			r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
			startCancellationPreparation(t, r, sender.recSender)
			waitPreparationStatus(t, sender.recSender, "request", "started", "")

			session.beforeCancel = func() {
				select {
				case <-sender.exited:
				default:
					cancelBeforeReceipt.Store(true)
				}
			}
			var mutation proto.Envelope
			switch operation {
			case "function":
				mutation = mustEnv(t, proto.TypeFunctionResult, "run", proto.FunctionResultPayload{CallID: "call", Success: true, Content: functionResultContent("answer"), DeliveryID: sender.deliveryID})
			case "permission":
				session.out <- mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "permission"})
				waitFor(t, func() bool { return hasFrame(sender.recSender, proto.TypePermissionRequest, "run") }, "permission request")
				mutation = mustEnv(t, proto.TypePermissionDecision, "permission", proto.PermissionDecisionPayload{DeliveryID: sender.deliveryID, Approved: true})
			case "choice":
				session.out <- mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "ask"})
				waitFor(t, func() bool { return hasFrame(sender.recSender, proto.TypePromptForUserChoice, "run") }, "choice request")
				mutation = mustEnv(t, proto.TypePromptForUserChoiceDecision, "ask", proto.PromptForUserChoiceDecisionPayload{DeliveryID: sender.deliveryID, Answers: []string{"yes"}})
			case "steering":
				mutation = mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: sender.inputID, Text: "continue"})
			}
			go func() { _ = r.Handle(t.Context(), mutation) }()
			select {
			case <-sender.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("mutation receipt did not block")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			if cancelBeforeReceipt.Load() || session.cancels() != 1 || r.ActiveRuns() != 0 {
				t.Fatalf("shutdown crossed receipt barrier: early=%t cancels=%d active=%d", cancelBeforeReceipt.Load(), session.cancels(), r.ActiveRuns())
			}
		})
	}
}

func TestPreparedHandoffEarlyDonePublishesAfterStarted(t *testing.T) {
	sender := &recSender{}
	emitted, allowReturn := make(chan struct{}), make(chan struct{})
	session := &fakeSession{closeOutOnCancel: true}
	p := &controlledPreparation{closed: make(chan struct{})}
	p.start = func(ctx context.Context, _, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		session.out = out
		out <- mustEnv(t, proto.TypeDone, "run", proto.DonePayload{Content: "complete"})
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
	<-emitted
	if hasFrame(sender, proto.TypeDone, "run") || session.cancels() != 0 {
		t.Fatal("terminal escaped before Start returned")
	}
	close(allowReturn)
	waitFor(t, func() bool { return hasFrame(sender, proto.TypeDone, "run") && r.ActiveRuns() == 0 }, "early terminal settlement")
	frames := sender.snapshot()
	started, done := -1, -1
	for i, frame := range frames {
		var status proto.PreparationStatusPayload
		if frame.Type == proto.TypePreparationStatus && frame.DecodePayload(&status) == nil && status.State == "started" {
			started = i
		}
		if frame.Type == proto.TypeDone && frame.ID == "run" {
			done = i
		}
	}
	if started < 0 || done < 0 || started >= done || session.cancels() != 1 {
		t.Fatalf("started/Done order invalid: started=%d done=%d cancels=%d", started, done, session.cancels())
	}
}

var _ agent.FunctionResultSubmitter = (*preparedMutationSession)(nil)
var _ agent.Steerer = (*preparedMutationSession)(nil)
