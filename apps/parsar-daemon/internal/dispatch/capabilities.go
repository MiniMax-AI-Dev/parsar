package dispatch

import "github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"

func (r *Router) availableCapabilities(kind string) proto.AgentKindCapabilities {
	for _, info := range r.registry.SupportedAgentKinds() {
		if info.Kind == kind && info.Available {
			return info.Capabilities
		}
	}
	return proto.AgentKindCapabilities{}
}
