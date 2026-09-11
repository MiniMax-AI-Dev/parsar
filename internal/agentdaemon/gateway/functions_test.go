package gateway

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"testing"
)

func TestFunctionCapabilitySurvivesHeartbeatMapping(t *testing.T) {
	for _, supported := range []bool{false, true} {
		kinds := deviceKindsFromHeartbeat(proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{FunctionTools: supported}}}})
		s := &Session{}
		s.setSupportedAgentKinds(kinds)
		info, found, known := s.AgentKindStatus("codex")
		if !found || !known || info.Capabilities.FunctionTools != supported {
			t.Fatal(info, found, known)
		}
	}
}
