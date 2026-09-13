package dispatch_test

import (
	"context"
	"errors"
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

func TestNoEnvironmentUsesAvailableCapability(t *testing.T) {
	for _, available := range []bool{false, true} {
		h := newHarness(t)
		defer h.router.Shutdown(context.Background())
		called := false
		h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "claude_sdk", Available: available, Capabilities: proto.AgentKindCapabilities{EnvironmentNone: true}}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			called = true
			return nil, errors.New("controlled factory stop")
		})
		_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "sdk", proto.PromptRequestPayload{AgentKind: "claude_sdk", DisableExecutionEnvironment: true}))
		if called != available {
			t.Fatalf("factory called=%t, available=%t", called, available)
		}
	}
}

func TestRemoteEnvironmentRequiresAvailableCapability(t *testing.T) {
	for _, mode := range []string{"unsupported", "unavailable", "none conflict", "supported"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			called := false
			h.reg.RegisterKind(proto.SupportedAgentKind{Kind: "codex", Available: mode != "unavailable",
				Capabilities: proto.AgentKindCapabilities{RemoteEnvironment: mode != "unsupported"}},
				func(_ context.Context, req proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
					called = true
					if req.RemoteEnvironment == nil || req.RemoteEnvironment.ID != "environment-test" {
						t.Error("remote descriptor lost before factory")
					}
					return nil, errors.New("controlled factory stop")
				})
			req := proto.PromptRequestPayload{AgentKind: "codex", RemoteEnvironment: &proto.RemoteEnvironment{ID: "environment-test"}, DisableExecutionEnvironment: mode == "none conflict"}
			_ = h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "remote", req))
			if called != (mode == "supported") {
				t.Fatalf("unexpected factory call for %s", mode)
			}
			frames := h.sender.snapshot()
			if len(frames) != 2 || frames[0].Type != proto.TypeError || frames[1].Type != proto.TypeDone {
				t.Fatal("missing terminal error frames")
			}
		})
	}
}
