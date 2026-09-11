package dispatch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type cancelReceiptSession struct {
	*fakeSession
	entered chan struct{}
	release chan struct{}
	err     error
}

func TestCompletionWaitsForNativeWriterRelease(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	sess := &cancelReceiptSession{entered: make(chan struct{}), release: make(chan struct{})}
	h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		sess.fakeSession = &fakeSession{out: out, closeOutOnCancel: true}
		return sess, nil
	})
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "release", proto.PromptRequestPayload{AgentKind: "codex", AgentStateKey: "stable", ReleaseOnCompletion: true})); err != nil {
		t.Fatal(err)
	}
	sess.out <- mustEnv(t, proto.TypeDone, "release", proto.DonePayload{Content: "Finished"})
	<-sess.entered
	if len(h.sender.snapshot()) != 0 {
		t.Fatal("completion acknowledged before native writer was released")
	}
	close(sess.release)
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "release completion")
	frames := h.sender.snapshot()
	if len(frames) != 1 || frames[0].Type != proto.TypeDone || sess.cancels() != 1 {
		t.Fatal("completion or native release missing")
	}
}

func (s *cancelReceiptSession) Cancel(ctx context.Context) error {
	if s.entered != nil {
		close(s.entered)
		<-s.release
		s.entered = nil
	}
	_ = s.fakeSession.Cancel(ctx)
	return s.err
}

func TestCancellationReceiptFollowsAdapterOutcome(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "applied", true: "rejected"}[fails], func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			sess := &cancelReceiptSession{entered: make(chan struct{}), release: make(chan struct{})}
			if fails {
				sess.err = errors.New("adapter could not cancel")
			}
			h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				sess.fakeSession = &fakeSession{out: out, closeOutOnCancel: true}
				return sess, nil
			})
			if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "codex"})); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{DeliveryID: "cancel-1"}))
			}()
			<-sess.entered
			for _, env := range h.sender.snapshot() {
				if env.Type == proto.TypeInteractionDecisionAck {
					t.Fatal("cancellation acknowledged before adapter returned")
				}
			}
			close(sess.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			found := false
			for _, env := range h.sender.snapshot() {
				if env.Type == proto.TypeInteractionDecisionAck {
					found = true
					var ack proto.InteractionDecisionAckPayload
					_ = env.DecodePayload(&ack)
					if ack.Applied == fails || ack.DeliveryID != "cancel-1" {
						t.Fatalf("wrong receipt: %+v", ack)
					}
				}
			}
			if !found {
				t.Fatal("missing cancellation receipt")
			}
		})
	}
}

func TestLegacyCancellationDoesNotEmitNewFrames(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "legacy", proto.PromptRequestPayload{AgentKind: "claude_code"})); err != nil {
		t.Fatal(err)
	}
	sess := <-h.gotSess
	sess.closeOutOnCancel = true
	if err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptCancel, "legacy", proto.PromptCancelPayload{})); err != nil {
		t.Fatal(err)
	}
	for _, env := range h.sender.snapshot() {
		if env.Type == proto.TypeInteractionDecisionAck {
			t.Fatal("legacy cancellation emitted new receipt")
		}
	}
}
