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
func (p Policy) ValidateSessionConfiguration(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil {
		return store.ErrInvalidInput
	}
	profile, ok := p.Engines.Lookup(engine)
	if !ok || (snapshot.Environment != nil && !profile.Accepts(snapshot.Environment.Type)) {
		return store.ErrInvalidInput
	}
	_, err := p.mcpCredentialBindings(engine, snapshot)
	if err != nil {
		return err
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" {
		if snapshot.Daemon != nil || strings.TrimSpace(snapshot.Agent.Model) == "" || !path.IsAbs(snapshot.Environment.WorkspaceDirectory) || strings.ContainsAny(snapshot.Environment.WorkspaceDirectory, "\x00\r\n\\") || len(snapshot.Environment.CapabilityDirectories) != 0 {
			return store.ErrInvalidInput
		}
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "openai_hosted" {
		// Only this placement/engine combination has current native qualification.
		// Runtime capability checks still apply before any execution claim.
		if snapshot.Daemon != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
			return store.ErrInvalidInput
		}
		configuration, err := json.Marshal(snapshot.Environment)
		if err != nil || !LocalWorkspaceConfiguration(configuration) {
			return store.ErrInvalidInput
		}
	}
	return validateProfileConfiguration(profile, snapshot)
}

func (p Policy) canAdmitInputs(engine string, configuration json.RawMessage) bool {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || snapshot.Environment == nil || snapshot.Environment.Type != "none" || snapshot.Daemon != nil {
		return false
	}
	return p.ValidateSessionConfiguration(engine, configuration) == nil
}

func (p Policy) validateEngineInputs(engine string, inputs []store.Input) error {
	profile, ok := p.Engines.Lookup(engine)
	if !ok {
		return store.ErrInvalidInput
	}
	return validateProfileInputs(profile, inputs)
}

// engineCapabilities is shared by device selection and the final preclaim check.
// Capability bits describe the adapter; supported values still depend on its profile.
func (p Policy) engineCapabilities(peer *gateway.Session, engine string, snapshot Snapshot) (device.KindCapabilities, error) {
	fail := func(message string) (device.KindCapabilities, error) {
		return device.KindCapabilities{}, errors.New(message)
	}
	profile, ok := p.Engines.Lookup(engine)
	if !ok || (snapshot.Environment != nil && !profile.Accepts(snapshot.Environment.Type)) {
		return fail("execution engine placement is not supported")
	}
	if profile.ValidateConfiguration != nil {
		if err := validateProfileConfiguration(profile, snapshot); err != nil {
			return device.KindCapabilities{}, err
		}
	}
	info, found, known := peer.AgentKindStatus(engine)
	caps := info.Capabilities
	if !known || !found || !info.Available || !caps.Streaming || !caps.Steering || !caps.DurableTurns || !caps.DurableInputReceipts {
		return fail("device must advertise streaming, steering and durable turns for this engine")
	}
	if !caps.ExecutionControls {
		return fail("device must advertise execution_controls")
	}
	if profile.WebSearchControl && !caps.WebSearchControl {
		return fail("device must advertise web_search_control")
	}
	if profile.TextVerbosity && !caps.TextVerbosity {
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
	if _, err := p.mcpExecutionCredentials(engine, snapshot, mcp, caps); err != nil {
		return device.KindCapabilities{}, err
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted" && (!caps.Preparation || !caps.RemoteEnvironment) {
		return fail("device must advertise preparation and remote_environment")
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "openai_hosted" {
		if !caps.Preparation || !caps.LocalEnvironment || !caps.WorkspaceReadPreparation || !caps.WorkspaceOutputExport {
			return fail("device must advertise local preparation, workspace reads and output export")
		}
		if (snapshot.Environment.Network == nil || snapshot.Environment.Network.Access != "disabled") && !caps.LocalEnvironmentNetworkPolicy {
			return fail("device must advertise local_environment_network_policy")
		}
	}
	if snapshot.Environment != nil && snapshot.Environment.Type == "none" && !caps.EnvironmentNone {
		return fail("device must advertise environment_none")
	}
	return caps, nil
}
