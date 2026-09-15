package codex

import (
	"crypto/rand"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// This execution profile accepts RFC 6750 b64token bytes without normalization.
// Credential resource storage has a separate, opaque-string contract.
func validMCPHTTPBearerToken(token string) bool {
	value := strings.TrimRight(token, "=")
	if value == "" {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", rune(c))) {
			return false
		}
	}
	return true
}

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
