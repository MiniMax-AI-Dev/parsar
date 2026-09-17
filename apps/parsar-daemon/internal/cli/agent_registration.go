package cli

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func registerAgentKinds(registry *agent.Registry, agentCLIs agentCLIDiscovery, serverURL string) {
	registerProductAgentKind(registry, agentCLIs.ClaudeCode, withSkillUploadServer(withCapabilityDownloads(claudecode.Factory, serverURL), serverURL))
	registerProductAgentKind(registry, agentCLIs.OpenCode, withSkillUploadServer(withCapabilityDownloads(opencodeagent.Factory, serverURL), serverURL))
	registerProductAgentKind(registry, agentCLIs.Codex, withSkillUploadServer(withCapabilityDownloads(codex.Factory, serverURL), serverURL))
	if agentCLIs.Codex.Available && (agentCLIs.Codex.Capabilities.RemoteEnvironment || agentCLIs.Codex.Capabilities.LocalEnvironment) {
		registry.RegisterPreparation("codex", codex.SupportsWorkspaceReadPreparation() || agentCLIs.Codex.Capabilities.LocalEnvironment, func(ctx context.Context, req proto.PromptRequestPayload) (agent.Prepared, error) {
			prepared, err := codex.Prepare(ctx, req)
			if prepared == nil {
				return nil, err
			}
			return prepared, err
		})
	}
	registerProductAgentKind(registry, agentCLIs.Pi, withSkillUploadServer(withCapabilityDownloads(pi.Factory, serverURL), serverURL))
	registerProductAgentKind(registry, agentCLIs.MCode, withSkillUploadServer(withCapabilityDownloads(mcode.Factory, serverURL), serverURL))
	registerClaudeSDK(registry, agentCLIs.ClaudeSDK)
}

func registerProductAgentKind(registry *agent.Registry, info proto.SupportedAgentKind, factory agent.Factory) {
	info.Capabilities.WorkspaceAuthoring = true
	registry.RegisterKind(info, factory)
}
