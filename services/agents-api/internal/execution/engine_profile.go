package execution

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Accepted profiles are explicit public qualifications, not inferred from a
// Runtime advertisement. Native configuration and execution remain in adapters.
type engineProfile struct {
	placements                      []string
	validateConfiguration           func(Snapshot) error
	validateInputs                  func([]store.Input) error
	webSearchControl, textVerbosity bool
}

func (p engineProfile) accepts(placement string) bool {
	return slices.Contains(p.placements, placement)
}
func acceptedEngine(engine string) (engineProfile, bool) {
	switch engine {
	case "codex":
		return engineProfile{placements: []string{"none", "self_hosted", "openai_hosted"}, webSearchControl: true, textVerbosity: true}, true
	case "claude_sdk":
		return engineProfile{placements: []string{"none", "openai_hosted"}, validateConfiguration: validateClaudeConfiguration, validateInputs: validateClaudeInputs}, true
	default:
		return engineProfile{}, false
	}
}

func validateClaudeConfiguration(snapshot Snapshot) error {
	agent := snapshot.Agent
	if snapshot.Environment == nil || (snapshot.Environment.Type != "none" && snapshot.Environment.Type != "openai_hosted") || snapshot.Daemon != nil || strings.TrimSpace(agent.Model) == "" {
		return store.ErrInvalidInput
	}
	if agent.Text.Verbosity != "" && agent.Text.Verbosity != "medium" {
		return errors.New("The configured engine currently supports medium text verbosity only.")
	}
	if agent.MultiAgent.Enabled || agent.MultiAgent.MaxConcurrentSubagents != nil || agent.Reasoning.Effort != nil || agent.Reasoning.Summary != nil || (agent.ServiceTier != "" && agent.ServiceTier != "auto") || (agent.Text.Format.Type != "" && agent.Text.Format.Type != "text") {
		return store.ErrInvalidInput
	}
	tools, mcp, err := executionTools(agent.Tools)
	if err != nil {
		return err
	}
	if snapshot.Environment.Type == "openai_hosted" && (len(tools) != 0 || len(mcp) != 0) {
		return errors.New("The configured workspace profile does not support function or MCP tools.")
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

func validateClaudeInputs(inputs []store.Input) error {
	for _, input := range inputs {
		if input.Kind != "tool_result" {
			continue
		}
		var value store.FunctionResultInput
		if json.Unmarshal(input.Payload, &value) != nil {
			return store.ErrInvalidInput
		}
		result, err := functionResult(store.FunctionCall{CallID: value.CallID, Result: value.Result})
		if err != nil {
			return store.ErrInvalidInput
		}
		for _, part := range result.Content {
			if part.Type != "input_text" {
				return store.ErrInvalidInput
			}
		}
	}
	return nil
}
