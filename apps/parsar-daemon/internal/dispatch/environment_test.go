package dispatch_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestNoEnvironmentRejectsOtherEngineBeforeFactory(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	called := false
	h.reg.Register("claude_code", func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		called = true
		return nil, nil
	})
	err := h.router.Handle(context.Background(), mustEnv(t, proto.TypePromptRequest, "none", proto.PromptRequestPayload{AgentKind: "claude_code", DisableExecutionEnvironment: true}))
	if err == nil || called {
		t.Fatal("unsupported engine was started", err)
	}
	frames := h.sender.snapshot()
	if len(frames) != 2 || frames[0].Type != proto.TypeError || frames[1].Type != proto.TypeDone {
		t.Fatal(frames)
	}
}
