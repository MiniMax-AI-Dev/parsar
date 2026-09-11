package cli

import (
	"context"
	"maps"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func withAuthoringBridge(factory agent.Factory, bridge *authoring.Bridge) agent.Factory {
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		if !req.WorkspaceAuthoring {
			return factory(ctx, req, out)
		}
		path, release, err := bridge.Listen(ctx, req.RunID)
		if err != nil {
			return nil, err
		}
		req.AgentOptions = maps.Clone(req.AgentOptions)
		if req.AgentOptions == nil {
			req.AgentOptions = make(map[string]any)
		}
		env, _ := req.AgentOptions["env"].(map[string]any)
		env = maps.Clone(env)
		if env == nil {
			env = make(map[string]any)
		}
		env[proto.AuthoringSocketEnv] = path
		if executable, err := os.Executable(); err == nil {
			addCompanionCLIPath(env, filepath.Dir(executable))
		}
		req.AgentOptions["env"] = env
		upstream := make(chan proto.Envelope, 64)
		session, err := factory(ctx, req, upstream)
		if err != nil {
			release()
			return nil, err
		}
		go func() {
			defer close(out)
			defer release()
			for event := range upstream {
				if event.Type == proto.TypeDone {
					release()
				}
				out <- event
			}
		}()
		return session, nil
	}
}

func authoringRegistry(registry *agent.Registry, bridge *authoring.Bridge) *agent.Registry {
	wrapped := agent.NewRegistry()
	for _, info := range registry.SupportedAgentKinds() {
		factory, _ := registry.Resolve(info.Kind)
		info.Capabilities.WorkspaceAuthoring = true
		wrapped.RegisterKind(info, withAuthoringBridge(factory, bridge))
	}
	return wrapped
}
