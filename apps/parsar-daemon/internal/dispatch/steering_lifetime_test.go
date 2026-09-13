package dispatch_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type durableSteeringSession struct {
	*steeringSession
	phased func(context.Context, proto.PromptSteerPayload, func()) error
}

func (s *durableSteeringSession) SteerWithReceipt(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
	return s.phased(ctx, input, written)
}

func TestDurableSteeringWaitsBeyondTransportDeadline(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	var session *fakeSession
	var calls atomic.Int32
	release := make(chan struct{})
	h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true}
		return &durableSteeringSession{steeringSession: &steeringSession{fakeSession: session}, phased: func(ctx context.Context, input proto.PromptSteerPayload, written func()) error {
			calls.Add(1)
			written()
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}}, nil
	})
	ctx := context.Background()
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "durable", proto.PromptRequestPayload{AgentKind: "codex", ReleaseOnCompletion: true})); err != nil {
		t.Fatal(err)
	}
	input := proto.PromptSteerPayload{InputID: "extra", Text: "additional", DurableReceipt: true}
	env := mustEnv(t, proto.TypePromptSteer, "durable", input)
	if err := handleSteeringAndWait(t, h, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Written || ack.Accepted || ack.ErrorCode != "" {
		t.Fatalf("write phase: %+v", ack)
	}
	time.Sleep(11 * time.Second)
	if len(h.sender.snapshot()) != 1 {
		t.Fatal("native wait ended at transport deadline")
	}
	if err := handleSteeringAndWait(t, h, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Written || ack.Accepted {
		t.Fatalf("cached phase: %+v", ack)
	}
	close(release)
	waitFor(t, func() bool { return len(h.sender.snapshot()) == 3 }, "native acceptance")
	if ack := lastSteeringAck(t, h.sender, "durable", "extra"); !ack.Accepted || ack.Written {
		t.Fatalf("final phase: %+v", ack)
	}
	session.out <- mustEnv(t, proto.TypeDone, "durable", proto.DonePayload{})
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "completion")
	frames := h.sender.snapshot()
	if calls.Load() != 1 || frames[len(frames)-1].Type != proto.TypeDone {
		t.Fatal("replayed input or incorrect completion order")
	}
}

func TestDurableSteeringTransportTimeoutAndShutdown(t *testing.T) {
	for _, phase := range []string{"blocked-write", "written"} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			exited := make(chan struct{})
			h.reg.Register("codex", func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				return &durableSteeringSession{steeringSession: &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}}, phased: func(ctx context.Context, _ proto.PromptSteerPayload, written func()) error {
					defer close(exited)
					if phase == "written" {
						written()
					}
					<-ctx.Done()
					return ctx.Err()
				}}, nil
			})
			ctx := context.Background()
			if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "codex", ReleaseOnCompletion: true})); err != nil {
				t.Fatal(err)
			}
			if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "one", Text: "text", DurableReceipt: true})); err != nil {
				t.Fatal(err)
			}
			if phase == "blocked-write" {
				select {
				case <-exited:
				case <-time.After(12 * time.Second):
					t.Fatal("blocked write was not bounded")
				}
				waitFor(t, func() bool { return len(h.sender.snapshot()) == 1 }, "unknown receipt")
				if ack := lastSteeringAck(t, h.sender, "run", "one"); ack.Written || ack.Accepted || ack.ErrorCode != "outcome_unknown" {
					t.Fatalf("transport uncertainty: %+v", ack)
				}
			} else {
				waitFor(t, func() bool { return len(h.sender.snapshot()) == 1 }, "written phase")
				stopCtx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				if err := h.router.Shutdown(stopCtx); err != nil {
					t.Fatal(err)
				}
				select {
				case <-exited:
				default:
					t.Fatal("native waiter leaked")
				}
			}
		})
	}
}

func TestDurableSteeringRequiresOptInAndAdapter(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-adapter", true: "retained-run"}[supported], func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			h.reg.Register("codex", func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				s := &steeringSession{fakeSession: &fakeSession{out: out, closeOutOnCancel: true}, steer: func(context.Context, proto.PromptSteerPayload) error { t.Error("unexpected legacy call"); return nil }}
				if !supported {
					return s, nil
				}
				return &durableSteeringSession{steeringSession: s, phased: func(context.Context, proto.PromptSteerPayload, func()) error {
					t.Error("unexpected phased call")
					return nil
				}}, nil
			})
			ctx := context.Background()
			if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "codex", ReleaseOnCompletion: !supported})); err != nil {
				t.Fatal(err)
			}
			if err := handleSteeringAndWait(t, h, mustEnv(t, proto.TypePromptSteer, "run", proto.PromptSteerPayload{InputID: "one", Text: "text", DurableReceipt: true})); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "run", "one"); ack.ErrorCode != "unsupported" {
				t.Fatalf("capability gate: %+v", ack)
			}
		})
	}
}
