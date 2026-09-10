package cli

import (
	"context"
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func withSkillUploadServer(factory agent.Factory, serverURL string) agent.Factory {
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		env, _ := req.AgentOptions["env"].(map[string]any)
		if token, _ := env["PARSAR_CAPABILITY_UPLOAD_TOKEN"].(string); token != "" {
			env = maps.Clone(env)
			env["PARSAR_SERVER_URL"] = serverURL
			req.AgentOptions = maps.Clone(req.AgentOptions)
			req.AgentOptions["env"] = env
		}
		return factory(ctx, req, out)
	}
}
