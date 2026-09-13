package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type blockedReceiptSender struct {
	recSender
	once    sync.Once
	entered chan struct{}
	release chan struct{}
	exited  chan struct{}
	fail    bool
}

func (s *blockedReceiptSender) Send(ctx context.Context, env proto.Envelope) error {
	blocked := false
	if env.Type == proto.TypePromptSteerAck {
		s.once.Do(func() { blocked = true })
	}
	if blocked {
		close(s.entered)
		defer close(s.exited)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if s.fail {
			return errors.New("receipt transport failed")
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestDurableCompletionWaitsForSteeringReceiptSend(t *testing.T) {
	for _, mode := range []string{"consumed", "unknown", "send_failure"} {
		t.Run(mode, func(t *testing.T) {
			sender := &blockedReceiptSender{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}), fail: mode == "send_failure"}
			registry := agent.NewRegistry()
			var session *fakeSession
			var calls atomic.Int32
			registry.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				session = &fakeSession{out: out, closeOutOnCancel: true}
				return &steeringSession{fakeSession: session, steer: func(context.Context, proto.PromptSteerPayload) error {
					calls.Add(1)
					if mode == "unknown" {
						return errors.New("native outcome unknown")
					}
					return nil
				}}, nil
			})
			router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender})
			if err != nil {
				t.Fatal(err)
			}
			defer router.Shutdown(context.Background())
			var release sync.Once
			defer release.Do(func() { close(sender.release) })
			handle := func(kind string, payload any) {
				t.Helper()
				if err := router.Handle(context.Background(), mustEnv(t, kind, "ordered", payload)); err != nil {
					t.Fatal(err)
				}
			}
			handle(proto.TypePromptRequest, proto.PromptRequestPayload{AgentKind: "codex", AgentStateKey: "stable", ReleaseOnCompletion: true})
			input := proto.PromptSteerPayload{InputID: "input-1", Text: "original"}
			handle(proto.TypePromptSteer, input)
			<-sender.entered
			session.out <- mustEnv(t, proto.TypeDone, "ordered", proto.DonePayload{Content: "finished"})
			waitFor(t, func() bool { return router.SteeringClosedForTest("ordered") }, "closed steering admission")
			if session.cancels() != 0 || len(sender.snapshot()) != 0 {
				t.Fatal("native release or Done overtook receipt")
			}
			if mode != "send_failure" {
				handle(proto.TypePromptSteer, input)
				frames := sender.snapshot()
				var ack proto.PromptSteerAckPayload
				if len(frames) != 1 || frames[0].DecodePayload(&ack) != nil || ack.Accepted != (mode == "consumed") {
					t.Fatalf("cached receipt lost: %+v", ack)
				}
				if mode == "unknown" && ack.ErrorCode != "outcome_unknown" {
					t.Fatal("uncertainty lost")
				}
				input.Text = "changed"
				handle(proto.TypePromptSteer, input)
				input.InputID = "new"
				handle(proto.TypePromptSteer, input)
				frames = sender.snapshot()
				for i, want := range []string{"input_conflict", "run_inactive"} {
					if frames[i+1].DecodePayload(&ack) != nil || ack.ErrorCode != want {
						t.Fatalf("receipt %d: %+v", i, ack)
					}
				}
			}
			release.Do(func() { close(sender.release) })
			waitFor(t, func() bool { return router.ActiveRuns() == 0 }, "durable completion")
			frames := sender.snapshot()
			if len(frames) == 0 || frames[len(frames)-1].Type != proto.TypeDone || calls.Load() != 1 || session.cancels() != 1 {
				t.Fatalf("invalid terminal order/calls: %+v, calls=%d cancels=%d", frames, calls.Load(), session.cancels())
			}
			if mode == "send_failure" && len(frames) != 1 {
				t.Fatal("failed send fabricated an applied receipt")
			}
		})
	}
}

func TestShutdownReleasesSteeringWorkerAndCompletionBarrier(t *testing.T) {
	for _, phase := range []string{"native", "receipt_send"} {
		t.Run(phase, func(t *testing.T) {
			sender := &blockedReceiptSender{entered: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
			registry := agent.NewRegistry()
			entered, exited := make(chan struct{}), make(chan struct{})
			var session *fakeSession
			registry.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				session = &fakeSession{out: out, closeOutOnCancel: true}
				return &steeringSession{fakeSession: session, steer: func(ctx context.Context, _ proto.PromptSteerPayload) error {
					close(entered)
					defer close(exited)
					if phase == "native" {
						<-ctx.Done()
						return ctx.Err()
					}
					return nil
				}}, nil
			})
			router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender})
			if err != nil {
				t.Fatal(err)
			}
			defer router.Shutdown(context.Background())
			if err = router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "shutdown", proto.PromptRequestPayload{AgentKind: "codex", ReleaseOnCompletion: true})); err != nil {
				t.Fatal(err)
			}
			if err = router.Handle(context.Background(), mustEnv(t, proto.TypePromptSteer, "shutdown", proto.PromptSteerPayload{InputID: "one", Text: "text"})); err != nil {
				t.Fatal(err)
			}
			<-entered
			if phase == "receipt_send" {
				<-sender.entered
				session.out <- mustEnv(t, proto.TypeDone, "shutdown", proto.DonePayload{})
				waitFor(t, func() bool { return router.SteeringClosedForTest("shutdown") }, "completion barrier")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err = router.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-exited:
			default:
				t.Fatal("native receipt worker leaked")
			}
			select {
			case <-sender.exited:
			default:
				t.Fatal("receipt send worker leaked")
			}
			if router.ActiveRuns() != 0 {
				t.Fatal("run remained after shutdown")
			}
		})
	}
}
