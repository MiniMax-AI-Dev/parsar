// Package execution delivers durable Turns through the existing daemon protocol.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Snapshot is resolved internally; Daemon is not a public environment wire type.
type Snapshot struct {
	Agent  v1.Agent      `json:"agent"`
	Daemon *DaemonConfig `json:"daemon"`
}

type DaemonConfig struct {
	WorkDir string `json:"work_dir"`
}

type Dispatcher struct {
	Store    *store.Store
	Registry *gateway.Registry
	// Options resolves transient engine credentials; they are never stored here.
	Options func(context.Context, store.Session) (map[string]any, error)
}

type Result struct {
	Done           proto.DonePayload `json:"done"`
	ErrorCode      string            `json:"error_code,omitempty"`
	Error          string            `json:"error,omitempty"`
	AppliedThrough int64             `json:"applied_through"`
}

// Run claims once before subscribing or sending. Uncertain deliveries are not replayed.
func (d *Dispatcher) Run(ctx context.Context, tenantID, sessionID, turnID string) (store.Turn, error) {
	session, err := d.Store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return store.Turn{}, err
	}
	bound, err := d.Store.GetSessionDevice(ctx, tenantID, sessionID)
	if err != nil {
		return store.Turn{}, err
	}
	peer, err := d.Registry.LookupDevice(bound.ID)
	if err != nil {
		return store.Turn{}, err
	}
	info, found, known := peer.AgentKindStatus(session.Engine)
	if !known || !found || !info.Available || !info.Capabilities.Streaming || !info.Capabilities.Steering {
		return store.Turn{}, errors.New("device must advertise streaming and steering for this engine")
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || snapshot.Daemon == nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return store.Turn{}, store.ErrInvalidInput
	}
	workDir := snapshot.Daemon.WorkDir
	if workDir != "" && !filepath.IsAbs(workDir) && !strings.HasPrefix(workDir, "~/") {
		return store.Turn{}, store.ErrInvalidInput
	}
	inputs, err := d.Store.ListTurnInputs(ctx, tenantID, sessionID, turnID, 0, 1)
	if err != nil {
		return store.Turn{}, err
	}
	if len(inputs) != 1 || inputs[0].Kind != "message" {
		return store.Turn{}, store.ErrInvalidInput
	}
	text, err := messageText(inputs[0].Payload)
	if err != nil {
		return store.Turn{}, err
	}
	options := map[string]any{}
	if d.Options != nil {
		options, err = d.Options(ctx, session)
		if err != nil {
			return store.Turn{}, err
		}
		options = maps.Clone(options)
		if options == nil {
			options = map[string]any{}
		}
	}
	options["model"], options["system_prompt"] = snapshot.Agent.Model, snapshot.Agent.Instructions
	delete(options, "override_system_prompt")
	if _, err := d.Store.TransitionTurn(ctx, tenantID, sessionID, turnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		return store.Turn{}, err
	}
	req := proto.PromptRequestPayload{AgentKind: session.Engine, ConversationID: sessionID, RunID: turnID, Prompt: text, WorkDir: workDir, AgentOptions: options, AgentStateKey: "agents-api-" + sessionID, AgentSessionID: bound.NativeSessionID, ReleaseOnCompletion: true}
	result, status := d.deliver(ctx, tenantID, sessionID, peer, req, inputs[0].Sequence)
	if result.Done.Usage.Model == "" {
		result.Done.Usage.Model = snapshot.Agent.Model
	}
	nativeID, _ := result.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	finishCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 512*1024 || len(nativeID) > 512 {
		result = Result{ErrorCode: "invalid_executor_result", AppliedThrough: result.AppliedThrough}
		encoded, _ = json.Marshal(result)
		status, nativeID = store.TurnFailed, ""
	}
	turn, err := d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, status, encoded, nativeID, result.AppliedThrough)
	if errors.Is(err, store.ErrUnappliedInputs) {
		result.ErrorCode = "input_not_applied"
		encoded, _ = json.Marshal(result)
		return d.Store.CompleteExecution(finishCtx, tenantID, sessionID, turnID, store.TurnFailed, encoded, nativeID, result.AppliedThrough)
	}
	return turn, err
}

func messageText(raw json.RawMessage) (string, error) {
	var input struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &input) != nil || strings.TrimSpace(input.Text) == "" {
		return "", store.ErrInvalidInput
	}
	return input.Text, nil
}
