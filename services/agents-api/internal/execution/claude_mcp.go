package execution

import (
	"errors"
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var claudeMCPLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var claudeMCPTool = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

// These are execution limits of the packaged adapter, not saved-Agent schema rules.
// Shared resolution still owns URL, transport and credential-binding validation.
func validateClaudeMCP(snapshot Snapshot, servers []proto.MCPHTTPServer) error {
	selected, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return err
	}
	if len(selected) != 0 {
		return errors.New("The configured engine currently supports anonymous HTTP MCP only.")
	}
	for _, server := range servers {
		if !claudeMCPLabel.MatchString(server.ServerLabel) || server.ServerLabel == "functions" || server.Required || strings.ContainsAny(server.ServerURL, "?#") {
			return errors.New("The configured engine does not support this HTTP MCP declaration.")
		}
		if server.AllowedTools != nil {
			for _, name := range *server.AllowedTools {
				if !claudeMCPTool.MatchString(name) {
					return errors.New("The configured engine does not support this MCP tool name.")
				}
			}
		}
	}
	return nil
}
