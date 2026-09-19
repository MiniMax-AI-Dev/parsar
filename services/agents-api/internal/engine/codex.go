package engine

import (
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func codexProfile() Profile {
	return Profile{
		Placements:       []string{"none", "self_hosted", "openai_hosted"},
		WebSearchControl: true, TextVerbosity: true, MCPBearer: true,
		ValidateTools: func(environment *v1.Environment, hasDaemon bool, _ []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
			if len(mcp) != 0 && (environment == nil || (environment.Type != "none" && environment.Type != "self_hosted") || hasDaemon) {
				return errors.New("HTTP MCP execution currently requires the Codex service-side environment:none profile")
			}
			return nil
		},
	}
}
