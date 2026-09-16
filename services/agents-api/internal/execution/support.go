package execution

import (
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// ValidateSessionConfiguration checks engine placement and configuration before persistence.
func ValidateSessionConfiguration(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return store.ErrInvalidInput
	}
	_, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return err
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" {
		if engine != "codex" || snapshot.Daemon != nil || strings.TrimSpace(snapshot.Agent.Model) == "" || !path.IsAbs(snapshot.Environment.WorkspaceDirectory) || strings.ContainsAny(snapshot.Environment.WorkspaceDirectory, "\x00\r\n\\") || len(snapshot.Environment.CapabilityDirectories) != 0 {
			return store.ErrInvalidInput
		}
		_, _, err := executionTools(snapshot.Agent.Tools)
		return err
	}
	if engine != "claude_sdk" {
		_, mcp, err := executionTools(snapshot.Agent.Tools)
		if len(mcp) != 0 && (engine != "codex" || snapshot.Environment == nil || snapshot.Environment.Type != "none" || snapshot.Daemon != nil) {
			return errors.New("HTTP MCP execution currently requires the Codex service-side environment:none profile")
		}
		return err
	}
	return validateClaudeConfiguration(snapshot)
}

func validateClaudeConfiguration(snapshot Snapshot) error {
	agent := snapshot.Agent
	if snapshot.Environment == nil || snapshot.Environment.Type != "none" || snapshot.Daemon != nil || strings.TrimSpace(agent.Model) == "" {
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
	if err := validateClaudeMCP(snapshot, mcp); err != nil {
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

func canAdmitInputs(engine string, configuration json.RawMessage) bool {
	var snapshot Snapshot
	return (engine == "codex" || engine == "claude_sdk") && json.Unmarshal(configuration, &snapshot) == nil && snapshot.Environment != nil && snapshot.Environment.Type == "none" && snapshot.Daemon == nil && (engine != "claude_sdk" || validateClaudeConfiguration(snapshot) == nil)
}

func validateEngineInputs(engine string, inputs []store.Input) error {
	if engine != "claude_sdk" {
		return nil
	}
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

// engineCapabilities is shared by device selection and the final preclaim check.
// Capability bits describe the adapter; supported values still depend on its profile.
func engineCapabilities(peer *gateway.Session, engine string, snapshot Snapshot) (device.KindCapabilities, error) {
	fail := func(message string) (device.KindCapabilities, error) {
		return device.KindCapabilities{}, errors.New(message)
	}
	switch engine {
	case "codex":
	case "claude_sdk":
		if err := validateClaudeConfiguration(snapshot); err != nil {
			return device.KindCapabilities{}, err
		}
	default:
		return fail("execution engine is not supported")
	}
	info, found, known := peer.AgentKindStatus(engine)
	caps := info.Capabilities
	if !known || !found || !info.Available || !caps.Streaming || !caps.Steering || !caps.DurableTurns || !caps.DurableInputReceipts {
		return fail("device must advertise streaming, steering and durable turns for this engine")
	}
	if !caps.ExecutionControls {
		return fail("device must advertise execution_controls")
	}
	if engine == "codex" && !caps.WebSearchControl {
		return fail("device must advertise web_search_control")
	}
	if engine == "codex" && !caps.TextVerbosity {
		return fail("device must advertise text_verbosity")
	}
	if !caps.ToolObservations {
		return fail("device must advertise tool_observations")
	}
	if !snapshot.Agent.MultiAgent.Enabled && !caps.SubagentControl {
		return fail("device must advertise subagent_control")
	}
	functions, mcp, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return fail("invalid execution tool configuration")
	}
	if len(functions) > 0 && !caps.FunctionTools {
		return fail("device must advertise function_tools")
	}
	if len(mcp) > 0 && (!caps.MCPHTTPTools || snapshot.Environment == nil || (snapshot.Environment.Type != "none" && snapshot.Environment.Type != "self_hosted") || snapshot.Daemon != nil) {
		return fail("device must support the service-side HTTP MCP profile")
	}
	selected, err := selectedMCPCredentials(snapshot)
	if err != nil {
		return device.KindCapabilities{}, err
	}
	for _, server := range mcp {
		if server.Required && !caps.MCPHTTPRequired {
			return fail("device must advertise mcp_http_required")
		}
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" && len(mcp) > 0 {
		if !caps.MCPHTTPRemoteEnvironment {
			return fail("device must support service-side HTTP MCP with a remote environment")
		}
		if len(selected) > 0 && !caps.MCPHTTPRemoteBearerAuth {
			return fail("device must advertise mcp_http_remote_bearer_auth")
		}
	}
	if len(selected) > 0 && !caps.MCPHTTPBearerAuth {
		return fail("device must advertise mcp_http_bearer_auth")
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" && (!caps.Preparation || !caps.RemoteEnvironment) {
		return fail("device must advertise preparation and remote_environment")
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "none" && !caps.EnvironmentNone {
		return fail("device must advertise environment_none")
	}
	return caps, nil
}
