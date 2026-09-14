// Package execution delivers durable Turns through the existing daemon protocol.
package execution

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Snapshot is resolved internally; Daemon is not a public environment wire type.
type Snapshot struct {
	Agent       v1.Agent        `json:"agent"`
	Daemon      *DaemonConfig   `json:"daemon"`
	Environment *v1.Environment `json:"environment"`
}

type DaemonConfig struct {
	WorkDir string `json:"work_dir"`
}

type Dispatcher struct {
	Store    *store.Store
	Registry *gateway.Registry
	// Options resolves transient engine credentials; they are never stored here.
	Options               func(context.Context, store.Session) (map[string]any, error)
	EnvironmentConnection func(context.Context, store.Session, store.Environment) (EnvironmentConnection, error)
	// CloseEnvironmentConnections drains transport observations before releasing execution ownership.
	CloseEnvironmentConnections func()
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
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return store.Turn{}, store.ErrInvalidInput
	}
	caps, err := engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return store.Turn{}, err
	}
	workDir, noEnvironment, err := resolveExecutionEnvironment(snapshot)
	if err != nil {
		return store.Turn{}, err
	}
	text, through, err := d.initialInput(ctx, tenantID, sessionID, turnID)
	if err != nil {
		return store.Turn{}, err
	}
	req, err := d.executionRequest(ctx, session, snapshot, caps, bound.NativeSessionID)
	if err != nil {
		return store.Turn{}, err
	}
	if _, err := d.Store.TransitionTurn(ctx, tenantID, sessionID, turnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		return store.Turn{}, err
	}
	req.ConversationID, req.RunID, req.Prompt = sessionID, turnID, text
	req.WorkDir, req.DisableExecutionEnvironment = workDir, noEnvironment
	result, status := d.deliver(ctx, tenantID, sessionID, peer, req, through, nil)
	return d.finishRun(tenantID, sessionID, turnID, snapshot.Agent.Model, result, status)
}

func (d *Dispatcher) finishRun(tenantID, sessionID, turnID, model string, result Result, status string) (store.Turn, error) {
	if result.Done.Usage.Model == "" {
		result.Done.Usage.Model = model
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
