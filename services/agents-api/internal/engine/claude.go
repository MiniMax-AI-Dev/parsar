package engine

import (
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func claudeProfile() Profile {
	return Profile{
		Placements: []string{"none", "openai_hosted"}, MCPBearer: true,
		ValidateConfiguration: validateClaudeConfiguration,
		ValidateTools:         validateClaudeTools,
		ValidateFunctionResult: func(content []proto.FunctionResultContent) error {
			for _, part := range content {
				if part.Type != "input_text" {
					return ErrInvalidInput
				}
			}
			return nil
		},
	}
}

func validateClaudeConfiguration(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error {
	if environment == nil || (environment.Type != "none" && environment.Type != "openai_hosted") || hasDaemon || strings.TrimSpace(agent.Model) == "" {
		return ErrInvalidInput
	}
	if agent.Text.Verbosity != "" && agent.Text.Verbosity != "medium" {
		return errors.New("The configured engine currently supports medium text verbosity only.")
	}
	if agent.MultiAgent.Enabled || agent.MultiAgent.MaxConcurrentSubagents != nil || agent.Reasoning.Effort != nil || agent.Reasoning.Summary != nil || (agent.ServiceTier != "" && agent.ServiceTier != "auto") || (agent.Text.Format.Type != "" && agent.Text.Format.Type != "text") {
		return ErrInvalidInput
	}
	return nil
}

func validateClaudeTools(environment *v1.Environment, _ bool, tools []proto.FunctionTool, mcp []proto.MCPHTTPServer) error {
	if environment.Type == "openai_hosted" && len(mcp) != 0 {
		return errors.New("The configured workspace profile does not support HTTP MCP tools.")
	}
	if err := validateClaudeMCP(mcp); err != nil {
		return err
	}
	for _, tool := range tools {
		var schema struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema.Type != "object" {
			return errors.New("The configured engine currently requires function schemas with root type object.")
		}
	}
	return nil
}
