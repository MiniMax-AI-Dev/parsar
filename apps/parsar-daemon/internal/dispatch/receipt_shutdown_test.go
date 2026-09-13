package dispatch_test

import (
	"context"
	"errors"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"testing"
	"time"
)

type shutdownAllSendsBlockSender struct {
	recSender
	entered  chan struct{}
	terminal chan context.Context
	rescue   chan struct{}
}

func (s *shutdownAllSendsBlockSender) Send(ctx context.Context, env proto.Envelope) error {
	if env.Type == proto.TypePromptSteerAck {
		close(s.entered)
		select {
		case <-ctx.Done():
			<-time.After(50 * time.Millisecond)
			return ctx.Err()
		case <-s.rescue:
			return errors.New("test cleanup")
		}
	}
	if env.Type == proto.TypeError {
		s.terminal <- ctx
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.rescue:
			return errors.New("test cleanup")
		}
	}
	return s.recSender.Send(ctx, env)
}

func TestShutdownCancelsCompletionErrorSend(t *testing.T) {
	sender := &shutdownAllSendsBlockSender{entered: make(chan struct{}), terminal: make(chan context.Context, 1), rescue: make(chan struct{})}
	registry := agent.NewRegistry()
	var session *fakeSession
	registry.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		session = &fakeSession{out: out, closeOutOnCancel: true}
		return &steeringSession{fakeSession: session, steer: func(context.Context, proto.PromptSteerPayload) error { return nil }}, nil
	})
	router, err := dispatch.New(dispatch.Config{Registry: registry, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	if err = router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "shutdown-terminal", proto.PromptRequestPayload{AgentKind: "codex", ReleaseOnCompletion: true})); err != nil {
		t.Fatal(err)
	}
	if err = router.Handle(context.Background(), mustEnv(t, proto.TypePromptSteer, "shutdown-terminal", proto.PromptSteerPayload{InputID: "one", Text: "text"})); err != nil {
		t.Fatal(err)
	}
	<-sender.entered
	session.out <- mustEnv(t, proto.TypeDone, "shutdown-terminal", proto.DonePayload{})
	waitFor(t, func() bool { return router.SteeringClosedForTest("shutdown-terminal") }, "completion barrier")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdownErr := router.Shutdown(ctx)
	active := router.ActiveRuns()
	select {
	case terminalCtx := <-sender.terminal:
		t.Logf("error send context: Done is nil=%t, Err=%v", terminalCtx.Done() == nil, terminalCtx.Err())
	default:
	}
	close(sender.rescue)
	waitFor(t, func() bool { return router.ActiveRuns() == 0 }, "test cleanup")
	if shutdownErr != nil || active != 0 {
		t.Fatalf("cooperating receipt sender canceled, but shutdown failed: err=%v active_runs=%d", shutdownErr, active)
	}
}
