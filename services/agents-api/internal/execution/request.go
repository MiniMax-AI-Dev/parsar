package execution

import (
	"context"
	"errors"
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) executionRequest(ctx context.Context, session store.Session, snapshot Snapshot, caps device.KindCapabilities, nativeID string) (proto.PromptRequestPayload, error) {
	functions, mcp, err := executionTools(snapshot.Agent.Tools)
	if err != nil {
		return proto.PromptRequestPayload{}, err
	}
	options := map[string]any{}
	if d.Options != nil {
		options, err = d.Options(ctx, session)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
		options = maps.Clone(options)
		if options == nil {
			options = map[string]any{}
		}
	}
	options["model"], options["system_prompt"] = snapshot.Agent.Model, snapshot.Agent.Instructions
	delete(options, "override_system_prompt")
	verbosity := snapshot.Agent.Text.Verbosity
	if verbosity == "" {
		verbosity = "medium"
	}
	controls := &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: verbosity}
	request := proto.PromptRequestPayload{AgentKind: session.Engine, FunctionTools: functions,
		AgentOptions: options, ExecutionControls: controls, AgentStateKey: "agents-api-" + session.ID,
		AgentSessionID: nativeID, ReleaseOnCompletion: true, StrictResume: true,
		ObserveMessages: caps.MessageItems, ObserveToolObservations: true,
		ObserveSubagentIdentities: snapshot.Agent.MultiAgent.Enabled,
		DisableSubagents:          !snapshot.Agent.MultiAgent.Enabled}
	if len(mcp) != 0 {
		selected, err := selectedMCPCredentials(snapshot)
		if err != nil {
			return proto.PromptRequestPayload{}, err
		}
		if len(selected) > 0 {
			profileSupported := snapshot.Environment != nil && (snapshot.Environment.Type == "none" && caps.EnvironmentNone || snapshot.Environment.Type == "self_hosted" && caps.RemoteEnvironment && caps.Preparation && caps.MCPHTTPRemoteEnvironment && caps.MCPHTTPRemoteBearerAuth)
			if d.Store == nil || !caps.MCPHTTPBearerAuth || !caps.MCPHTTPTools || session.Engine != "codex" || snapshot.Daemon != nil || !profileSupported {
				return proto.PromptRequestPayload{}, errors.New("authenticated MCP execution is unavailable")
			}
		}
		for i := range mcp {
			if binding, ok := selected[mcp[i].ServerLabel]; ok {
				token, err := d.Store.MCPBearerToken(ctx, session.TenantID, snapshot.VaultIDs, binding)
				if err != nil {
					return proto.PromptRequestPayload{}, err
				}
				mcp[i].BearerToken = &token
			}
		}
		request.MCPHTTPServers = &mcp
	}
	return request, nil
}
