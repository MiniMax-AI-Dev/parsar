package execution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type directoryReadResult struct {
	directory proto.WorkspaceDirectoryResult
	err       error
}

type directoryReadRequest struct {
	ctx         context.Context
	environment store.Environment
	path        string
	result      chan directoryReadResult
}

func (r directoryReadRequest) reply(result directoryReadResult) { r.result <- result }

// ReadEnvironmentDirectory observes a Worker-owned read without admitting model input.
func (w *Worker) ReadEnvironmentDirectory(ctx context.Context, environment store.Environment, path string) (proto.WorkspaceDirectoryResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	current, err := w.admission.GetEnvironment(ctx, environment.TenantID, environment.ID)
	if err != nil {
		return proto.WorkspaceDirectoryResult{}, err
	}
	if current.SessionID != environment.SessionID {
		return proto.WorkspaceDirectoryResult{}, store.ErrNotFound
	}
	request := directoryReadRequest{ctx: ctx, environment: current, path: path, result: make(chan directoryReadResult, 1)}
	select {
	case w.directoryReads <- request:
	case <-ctx.Done():
		return proto.WorkspaceDirectoryResult{}, ErrExecutionUnavailable
	case <-w.stopped:
		return proto.WorkspaceDirectoryResult{}, ErrExecutionUnavailable
	}
	select {
	case result := <-request.result:
		return result.directory, result.err
	case <-ctx.Done():
		return proto.WorkspaceDirectoryResult{}, ErrExecutionUnavailable
	case <-w.stopped:
		return proto.WorkspaceDirectoryResult{}, ErrExecutionUnavailable
	}
}

func (w *Worker) runDirectoryRead(owner context.Context, request directoryReadRequest, reserved bool) (result directoryReadResult) {
	result.err = ErrExecutionUnavailable
	check, cancel := context.WithTimeout(owner, 5*time.Second)
	defer cancel()
	if w.CheckOwnership(check) != nil {
		return
	}
	environment, err := w.dispatcher.Store.GetEnvironment(check, request.environment.TenantID, request.environment.ID)
	if err != nil {
		result.err = err
		return
	}
	if environment.SessionID != request.environment.SessionID {
		result.err = store.ErrNotFound
		return
	}
	var configuration struct {
		Type               string `json:"type"`
		WorkspaceDirectory string `json:"workspace_directory"`
	}
	if json.Unmarshal(environment.Configuration, &configuration) != nil || configuration.Type != "self_hosted" || configuration.WorkspaceDirectory == "" {
		return
	}
	session, err := w.dispatcher.Store.GetSession(check, environment.TenantID, environment.SessionID)
	if err != nil {
		result.err = err
		return
	}
	run := ""
	if session.LastTurn != nil && (session.LastTurn.Status == store.TurnInProgress || session.LastTurn.Status == store.TurnWaiting) {
		run = session.LastTurn.ID
	}
	if (run == "") != reserved {
		return
	}
	if reserved {
		ready, err := w.bindSessionDevice(check, session, func(id string) bool { return w.directoryDeviceReady(id, session.Engine, true) })
		if err != nil || !ready {
			return
		}
	}
	bound, err := w.dispatcher.Store.GetSessionDevice(check, session.TenantID, session.ID)
	if err != nil || !w.directoryDeviceReady(bound.ID, session.Engine, reserved) {
		return
	}
	peer, err := w.dispatcher.Registry.LookupDevice(bound.ID)
	if err != nil {
		return
	}
	read := proto.WorkspaceReadPayload{EnvironmentID: environment.ID, RunID: run, Path: request.path, MaxEntries: proto.WorkspaceDirectoryMaxEntries}
	if !reserved {
		result = readEnvironmentDirectory(owner, peer, read)
		return
	}
	result = w.dispatcher.readPreparedDirectory(owner, peer, session, environment, configuration.WorkspaceDirectory, read)
	return
}

func (w *Worker) directoryDeviceReady(id, engine string, prepare bool) bool {
	if w.dispatcher.Registry == nil {
		return false
	}
	peer, err := w.dispatcher.Registry.LookupDevice(id)
	if err != nil {
		return false
	}
	info, found, known := peer.AgentKindStatus(engine)
	return known && found && info.Available && info.Capabilities.RemoteEnvironment && (!prepare || (info.Capabilities.Preparation && info.Capabilities.WorkspaceReadPreparation))
}

func readEnvironmentDirectory(ctx context.Context, peer *gateway.Session, request proto.WorkspaceReadPayload) directoryReadResult {
	result, err := peer.ListWorkspaceDirectory(ctx, request)
	if err == nil && result.Outcome == "completed" && result.Directory != nil && !result.Directory.Truncated && proto.ValidWorkspaceDirectory(result.Directory, request.MaxEntries) {
		return directoryReadResult{directory: *result.Directory}
	}
	if err == nil && result.Outcome == "rejected" && result.ErrorCode == "not_found" {
		return directoryReadResult{err: store.ErrNotFound}
	}
	return directoryReadResult{err: ErrExecutionUnavailable}
}
