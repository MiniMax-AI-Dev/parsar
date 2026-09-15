package dispatch

import (
	"errors"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Reject authenticated declarations before factory selection, including peers
// that support credential-free MCP but cannot safely inject credentials.
func validateMCPHTTPBearer(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if req.MCPHTTPServers == nil {
		return nil
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
