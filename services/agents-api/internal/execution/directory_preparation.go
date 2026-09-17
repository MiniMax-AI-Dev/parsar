package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) readPreparedDirectory(ctx context.Context, peer *gateway.Session, session store.Session, environment store.Environment, workspace string, read proto.WorkspaceReadPayload) directoryReadResult {
	unavailable := directoryReadResult{err: ErrExecutionUnavailable}
	if d.EnvironmentConnection == nil {
		return unavailable
	}
	owner, cancel := context.WithCancel(ctx)
	defer cancel()
	connection, err := d.EnvironmentConnection(owner, session, environment)
	if connection.Release != nil {
		defer connection.Release()
	}
	if err != nil || connection.URL == "" || connection.Token == "" || connection.Release == nil {
		return unavailable
	}
	prepared, err := newPreparedStart(peer)
	if err != nil {
		return unavailable
	}
	releaseAttempted := false
	defer func() {
		if !releaseAttempted {
			prepared.close()
		} else {
			peer.UnsubscribePreparation(prepared.requestID)
		}
	}()
	req := proto.PromptRequestPayload{AgentKind: session.Engine, AgentStateKey: "agents-api-" + session.ID, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true,
		RemoteEnvironment: &proto.RemoteEnvironment{ID: environment.ID, WorkspaceDirectory: workspace, ConnectionURL: connection.URL, ConnectionToken: connection.Token}}
	prepare, stop := context.WithTimeout(owner, 10*time.Second)
	err = send(prepare, peer, proto.TypeExecutionPrepare, prepared.requestID, proto.ExecutionPreparePayload{Configuration: req})
	if err == nil {
		err = prepared.awaitDirectoryReady(prepare)
	}
	stop()
	if err != nil {
		return unavailable
	}
	read.Handle = prepared.handle
	result := readEnvironmentDirectory(owner, peer, read)
	releaseAttempted = true
	if prepared.releaseDirectory() != nil {
		return unavailable
	}
	return result
}

func (p *preparedStart) awaitDirectoryReady(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ErrExecutionUnavailable
		case env, ok := <-p.sub.Events:
			if !ok {
				return ErrExecutionUnavailable
			}
			status, err := p.observation(env)
			if err != nil || status.RunID != "" {
				return ErrExecutionUnavailable
			}
			if status.State == "ready" {
				return nil
			}
			if status.State != "preparing" {
				return ErrExecutionUnavailable
			}
		}
	}
}

func (p *preparedStart) releaseDirectory() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if send(ctx, p.peer, proto.TypeExecutionRelease, p.requestID, proto.ExecutionReleasePayload{Handle: p.handle}) != nil {
		return ErrExecutionUnavailable
	}
	for {
		select {
		case <-ctx.Done():
			return ErrExecutionUnavailable
		case env, ok := <-p.sub.Events:
			if !ok {
				return ErrExecutionUnavailable
			}
			status, err := p.controlStatus(env)
			if err != nil || status.RunID != "" {
				return ErrExecutionUnavailable
			}
			if status.State == "released" && status.ErrorCode == "" {
				return nil
			}
			if status.State != "preparing" && status.State != "ready" {
				return ErrExecutionUnavailable
			}
		}
	}
}
