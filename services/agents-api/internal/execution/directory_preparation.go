package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) readPreparedDirectory(ctx context.Context, peer *gateway.Session, session store.Session, environment store.Environment, bound store.ExecutionDevice, read proto.WorkspaceReadPayload) directoryReadResult {
	owner, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var result directoryReadResult
	err := d.withPreparedWorkspace(owner, peer, session, environment, bound, func(ctx context.Context, handle string) error {
		read.Handle = handle
		result = readEnvironmentDirectory(ctx, peer, read)
		return result.err
	})
	if err != nil {
		result.err = err
	}
	return result
}

func (d *Dispatcher) withPreparedWorkspace(owner context.Context, peer *gateway.Session, session store.Session, environment store.Environment, bound store.ExecutionDevice, consume func(context.Context, string) error) error {
	req := proto.PromptRequestPayload{AgentKind: session.Engine, AgentStateKey: "agents-api-" + session.ID, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true}
	release, err := d.configurePreparedEnvironment(owner, session, environment, bound, &req)
	if release != nil {
		defer release()
	}
	if err != nil {
		return ErrExecutionUnavailable
	}
	prepared, err := newPreparedStart(peer)
	if err != nil {
		return ErrExecutionUnavailable
	}
	releaseAttempted := false
	defer func() {
		if !releaseAttempted {
			prepared.close()
		} else {
			peer.UnsubscribePreparation(prepared.requestID)
		}
	}()
	prepare, stop := context.WithTimeout(owner, 10*time.Second)
	err = send(prepare, peer, proto.TypeExecutionPrepare, prepared.requestID, proto.ExecutionPreparePayload{Configuration: req})
	if err == nil {
		err = prepared.awaitDirectoryReady(prepare)
	}
	stop()
	if err != nil {
		return ErrExecutionUnavailable
	}
	err = consume(owner, prepared.handle)
	releaseAttempted = true
	if prepared.releaseDirectory() != nil {
		return ErrExecutionUnavailable
	}
	return err
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
