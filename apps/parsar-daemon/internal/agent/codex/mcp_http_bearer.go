package codex

import (
	"crypto/rand"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Only the daemon-generated reference enters native configuration. The returned
// secrets are added to the app-server environment after other launch probes.
func prepareMCPHTTPBearer(servers map[string]mcpServerConfig, declarations *[]proto.MCPHTTPServer) []string {
	if declarations == nil {
		return nil
	}
	var env []string
	for _, declaration := range *declarations {
		if declaration.BearerToken == nil {
			continue
		}
		server := servers[declaration.ServerLabel]
		server.BearerTokenEnvVar = "PARSAR_MCP_BEARER_" + rand.Text()
		servers[declaration.ServerLabel] = server
		env = append(env, server.BearerTokenEnvVar+"="+*declaration.BearerToken)
	}
	return env
}
