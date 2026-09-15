package dispatch

import (
	"errors"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Validate combined placement and authentication before factory selection.
func validateMCPHTTP(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if req.MCPHTTPServers == nil {
		return nil
	}
	if req.RemoteEnvironment != nil && (req.AgentKind != "codex" || !caps.MCPHTTPTools || !caps.MCPHTTPRemoteEnvironment) {
		return errors.New("engine does not support service-side HTTP MCP with a remote environment")
	}
	for _, server := range *req.MCPHTTPServers {
		if server.BearerToken == nil {
			continue
		}
		if !caps.MCPHTTPTools || !caps.MCPHTTPBearerAuth || !caps.EnvironmentNone {
			return errors.New("engine does not support authenticated HTTP MCP")
		}
		if req.AgentKind != "codex" || !req.DisableExecutionEnvironment || req.RemoteEnvironment != nil {
			return errors.New("authenticated HTTP MCP requires service-side Codex environment:none")
		}
		endpoint, err := url.Parse(server.ServerURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" {
			return errors.New("authenticated HTTP MCP requires HTTPS")
		}
	}
	return nil
}
