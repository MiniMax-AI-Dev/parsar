package engine

import (
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func mcodeProfile() Profile {
	return Profile{Placements: []string{"none"}, ValidateConfiguration: func(a v1.Agent, e *v1.Environment, daemon bool) error {
		if e == nil || e.Type != "none" || daemon || strings.TrimSpace(a.Model) == "" || a.MultiAgent.Enabled || a.MultiAgent.MaxConcurrentSubagents != nil || a.Reasoning.Effort != nil || a.Reasoning.Summary != nil || (a.ServiceTier != "" && a.ServiceTier != "auto") || (a.Text.Format.Type != "" && a.Text.Format.Type != "text") || (a.Text.Verbosity != "" && a.Text.Verbosity != "medium") {
			return ErrInvalidInput
		}
		return nil
	}, ValidateTools: func(_ *v1.Environment, _ bool, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
		if len(functions) > 0 || len(mcp) > 0 {
			return errors.New("The configured engine supports text execution without public tools.")
		}
		return nil
	}}
}
