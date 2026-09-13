package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPreparationRegistrationBypassesProductWrappers(t *testing.T) {
	for _, supported := range []bool{false, true} {
		reg := agent.NewRegistry()
		registerAgentKinds(reg, agentCLIDiscovery{Codex: proto.SupportedAgentKind{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{RemoteEnvironment: supported}}, ClaudeCode: proto.SupportedAgentKind{Kind: "claude_code"}, OpenCode: proto.SupportedAgentKind{Kind: "opencode"}, Pi: proto.SupportedAgentKind{Kind: "pi"}, MCode: proto.SupportedAgentKind{Kind: "mcode"}}, "http://unreachable.invalid")
		_, err := reg.ResolvePreparation("codex")
		if (err == nil) != supported {
			t.Fatal("unverified native version advertised preparation")
		}
		if !supported {
			continue
		}
		stop := errors.New("controlled preparation stop")
		reg.RegisterPreparation("codex", func(_ context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
			if req.RunID != "" || req.WorkspaceAuthoring || len(req.AgentOptions) != 0 {
				t.Error("product wrapper injected preparation context")
			}
			return nil, stop
		})
		wrapped := authoringRegistry(reg, authoring.New(nil))
		prepare, err := wrapped.ResolvePreparation("codex")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := prepare(t.Context(), proto.PromptRequestPayload{AgentKind: "codex"}); !errors.Is(err, stop) {
			t.Fatal("raw preparation was lost or wrapped", err)
		}
		for _, info := range wrapped.SupportedAgentKinds() {
			if info.Kind == "codex" && !info.Capabilities.Preparation {
				t.Fatal("real heartbeat registry lost preparation")
			}
		}
		wrapped.Register("codex", func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, stop
		})
		if _, err := wrapped.ResolvePreparation("codex"); err == nil {
			t.Fatal("factory replacement retained stale preparation")
		}
	}
}
