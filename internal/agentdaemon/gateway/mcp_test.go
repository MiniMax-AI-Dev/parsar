package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMCPHTTPBearerCapabilitySurvivesHeartbeatMapping(t *testing.T) {
	for _, supported := range []bool{false, true} {
		heartbeat := proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true,
			Capabilities: proto.AgentKindCapabilities{MCPHTTPTools: true, MCPHTTPBearerAuth: supported}}}}
		raw, err := json.Marshal(heartbeat)
		if err != nil || strings.Contains(string(raw), `"mcp_http_bearer_auth":true`) != supported {
			t.Fatal("wire capability changed", err)
		}
		var decoded proto.HeartbeatPayload
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		s := &Session{}
		s.setSupportedAgentKinds(deviceKindsFromHeartbeat(decoded))
		info, found, known := s.AgentKindStatus("codex")
		if !found || !known || !info.Capabilities.MCPHTTPTools || info.Capabilities.MCPHTTPBearerAuth != supported {
			t.Fatal("bearer capability was lost or inferred from credential-free MCP")
		}
	}
}

func TestMCPRemoteCapabilityIsExplicit(t *testing.T) {
	for _, supported := range []bool{false, true} {
		heartbeat := proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{RemoteEnvironment: true, MCPHTTPTools: true, MCPHTTPRemoteEnvironment: supported}}}}
		raw, err := json.Marshal(heartbeat)
		if err != nil || strings.Contains(string(raw), `"mcp_http_remote_environment":true`) != supported {
			t.Fatal("wire capability differs", err)
		}
		var decoded proto.HeartbeatPayload
		if err = json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		s := &Session{}
		s.setSupportedAgentKinds(deviceKindsFromHeartbeat(decoded))
		info, found, known := s.AgentKindStatus("codex")
		if !found || !known || info.Capabilities.MCPHTTPRemoteEnvironment != supported {
			t.Fatal("combination capability lost or inferred")
		}
	}
}
