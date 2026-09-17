package dispatch

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const workspaceReadCapacity = 4

func (r *Router) handleWorkspaceRead(ctx context.Context, env proto.Envelope) error {
	var request proto.WorkspaceReadPayload
	if env.DecodePayload(&request) != nil || strings.TrimSpace(env.ID) == "" ||
		(request.Handle == "") == (request.RunID == "") || request.EnvironmentID == "" ||
		request.MaxBytes < 1 || request.MaxBytes > proto.WorkspaceReadMaxBytes {
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead("invalid_request"))
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if _, exists := r.workspaceReads[env.ID]; exists {
		r.mu.Unlock()
		return errors.New("dispatch: workspace read already pending")
	}
	if len(r.workspaceReads) >= workspaceReadCapacity {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead("read_capacity"))
	}
	reader, code := r.workspaceReaderLocked(request)
	if code != "" {
		r.mu.Unlock()
		return r.sendWorkspaceRead(ctx, env, rejectedWorkspaceRead(code))
	}
	if r.workspaceReads == nil {
		r.workspaceReads = make(map[string]struct{})
	}
	r.workspaceReads[env.ID] = struct{}{}
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		defer func() { r.mu.Lock(); delete(r.workspaceReads, env.ID); r.mu.Unlock() }()
		// Observer loss does not discard an admitted native wait or replay it.
		operation, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		defer cancel()
		read, err := reader.ReadWorkspaceFile(operation, request.Path, request.MaxBytes)
		result := workspaceReadResult(read, err, request.MaxBytes)
		_ = r.sendWorkspaceRead(context.WithoutCancel(ctx), env, result)
	}()
	return nil
}

func (r *Router) workspaceReaderLocked(request proto.WorkspaceReadPayload) (agent.WorkspaceReader, string) {
	var resource any
	if request.Handle != "" {
		p := r.preparations[request.Handle]
		if p == nil || p.environmentID != request.EnvironmentID || p.status.State != "ready" ||
			!p.owns || p.busy || p.ctx.Err() != nil || !time.Now().Before(p.deadline) {
			return nil, "resource_unavailable"
		}
		resource = p.prepared
	} else {
		s := r.sessions[request.RunID]
		if s == nil || s.environmentID != request.EnvironmentID || s.session == nil ||
			s.preparationStart != nil || s.steeringClosed || s.ctx.Err() != nil {
			return nil, "resource_unavailable"
		}
		resource = s.session
	}
	reader, ok := resource.(agent.WorkspaceReader)
	if !ok {
		return nil, "read_unsupported"
	}
	return reader, ""
}

func rejectedWorkspaceRead(code string) proto.WorkspaceReadResultPayload {
	return proto.WorkspaceReadResultPayload{Outcome: "rejected", ErrorCode: code}
}

func workspaceReadResult(read agent.WorkspaceReadResult, err error, limit int) proto.WorkspaceReadResultPayload {
	if err == nil && len(read.Data) <= limit && (!read.Truncated || len(read.Data) == limit) {
		return proto.WorkspaceReadResultPayload{Outcome: "completed", Data: read.Data, Truncated: read.Truncated, CloseAcknowledged: true}
	}
	for _, failure := range []struct {
		err  error
		code string
	}{
		{agent.ErrWorkspaceReadUnsupported, "read_unsupported"},
		{agent.ErrWorkspaceReadUnavailable, "resource_unavailable"},
		{agent.ErrWorkspaceReadBusy, "read_capacity"},
		{agent.ErrWorkspaceReadInvalid, "invalid_request"},
		{fs.ErrNotExist, "not_found"},
		{fs.ErrPermission, "permission_denied"},
	} {
		if errors.Is(err, failure.err) {
			return rejectedWorkspaceRead(failure.code)
		}
	}
	return proto.WorkspaceReadResultPayload{Outcome: "unknown", ErrorCode: "read_unconfirmed"}
}

func (r *Router) sendWorkspaceRead(ctx context.Context, request proto.Envelope, result proto.WorkspaceReadResultPayload) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env, err := proto.NewEnvelopeWithTrace(proto.TypeWorkspaceReadResult, request.ID, result, request.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, env)
}
