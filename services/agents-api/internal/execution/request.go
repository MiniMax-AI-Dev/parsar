package execution

import (
	"context"
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) executionRequest(ctx context.Context, session store.Session, snapshot Snapshot, caps device.KindCapabilities, nativeID string) (proto.PromptRequestPayload, error) {
	functions, err := functionTools(snapshot.Agent.Tools)
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
	return proto.PromptRequestPayload{AgentKind: session.Engine, FunctionTools: functions,
		AgentOptions: options, ExecutionControls: controls, AgentStateKey: "agents-api-" + session.ID,
		AgentSessionID: nativeID, ReleaseOnCompletion: true, StrictResume: true,
		ObserveMessages: caps.MessageItems, ObserveToolObservations: true,
		DisableSubagents: !snapshot.Agent.MultiAgent.Enabled}, nil
}
