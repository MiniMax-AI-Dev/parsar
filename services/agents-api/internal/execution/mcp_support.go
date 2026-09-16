package execution

import (
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Public acceptance is separate from private adapter capability. Enabling a
// profile requires official-client and real execution validation for that path.
var publicMCPBearerProfiles = map[string]bool{
	"codex":      true,
	"claude_sdk": true,
}

func mcpCredentialBindings(engine string, snapshot Snapshot) (map[string]store.MCPCredentialBinding, error) {
	selected, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return nil, err
	}
	if len(selected) > 0 && !publicMCPBearerProfiles[engine] {
		return nil, errors.New("The configured engine currently supports anonymous HTTP MCP only.")
	}
	return selected, nil
}

// Selection, final preclaim and request construction use the same combination
// checks. This function never reads plaintext credentials or native configuration.
func mcpExecutionCredentials(engine string, snapshot Snapshot, servers []proto.MCPHTTPServer, caps device.KindCapabilities) (map[string]store.MCPCredentialBinding, error) {
	fail := func(message string) (map[string]store.MCPCredentialBinding, error) {
		return nil, errors.New(message)
	}
	if len(servers) > 0 && (!caps.MCPHTTPTools || snapshot.Environment == nil || (snapshot.Environment.Type != "none" && snapshot.Environment.Type != "self_hosted") || snapshot.Daemon != nil) {
		return fail("device must support the service-side HTTP MCP profile")
	}
	selected, err := mcpCredentialBindings(engine, snapshot)
	if err != nil {
		return nil, err
	}
	for _, server := range servers {
		if server.Required && !caps.MCPHTTPRequired {
			return fail("device must advertise mcp_http_required")
		}
	}
	if len(servers) > 0 && snapshot.Environment.Type == "self_hosted" {
		if !caps.MCPHTTPRemoteEnvironment {
			return fail("device must support service-side HTTP MCP with a remote environment")
		}
		if len(selected) > 0 && !caps.MCPHTTPRemoteBearerAuth {
			return fail("device must advertise mcp_http_remote_bearer_auth")
		}
		if !caps.Preparation || !caps.RemoteEnvironment {
			return fail("device must advertise preparation and remote_environment")
		}
	}
	if len(selected) > 0 && !caps.MCPHTTPBearerAuth {
		return fail("device must advertise mcp_http_bearer_auth")
	}
	if len(servers) > 0 && snapshot.Environment.Type == "none" && !caps.EnvironmentNone {
		return fail("device must advertise environment_none")
	}
	return selected, nil
}
