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
	// Never echo an unbounded correlation ID onto the shared connection.
	if len(env.ID) > proto.WorkspaceReadMaxIDBytes {
		return errors.New("dispatch: invalid workspace read ID")
	}
	var request proto.WorkspaceReadPayload
	if len(env.Payload) > proto.WorkspaceReadMaxRequestBytes || env.DecodePayload(&request) != nil || strings.TrimSpace(env.ID) == "" ||
		!proto.ValidWorkspaceReadRequest(request) {
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
	resource, code := r.workspaceResourceLocked(request)
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
		result := executeWorkspaceRead(operation, resource, request)
		_ = r.sendWorkspaceRead(context.WithoutCancel(ctx), env, result)
	}()
	return nil
}

func (r *Router) workspaceResourceLocked(request proto.WorkspaceReadPayload) (any, string) {
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
	return resource, ""
}

func executeWorkspaceRead(ctx context.Context, resource any, request proto.WorkspaceReadPayload) proto.WorkspaceReadResultPayload {
	if request.Operation == "directory" {
		reader, ok := resource.(agent.WorkspaceDirectoryLister)
		if !ok {
			return rejectedWorkspaceRead("read_unsupported")
		}
		read, err := reader.ListWorkspaceDirectory(ctx, request.Path, request.MaxEntries)
		if err != nil {
			return workspaceReadResult(agent.WorkspaceReadResult{}, err, 0)
		}
		if read.Entries == nil || len(read.Entries) > request.MaxEntries {
			return workspaceReadResult(agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain, 0)
		}
		directory := &proto.WorkspaceDirectoryResult{Entries: make([]proto.WorkspaceDirectoryEntry, 0, len(read.Entries)), Truncated: read.Truncated}
		for _, entry := range read.Entries {
			directory.Entries = append(directory.Entries, proto.WorkspaceDirectoryEntry{Name: entry.Name, Kind: entry.Kind, SizeBytes: entry.SizeBytes})
		}
		if !proto.ValidWorkspaceDirectory(directory, request.MaxEntries) {
			return workspaceReadResult(agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain, 0)
		}
		return proto.WorkspaceReadResultPayload{Outcome: "completed", Directory: directory, CloseAcknowledged: true}
	}
	reader, ok := resource.(agent.WorkspaceReader)
	if !ok {
		return rejectedWorkspaceRead("read_unsupported")
	}
	read, err := reader.ReadWorkspaceFile(ctx, request.Path, request.MaxBytes)
	return workspaceReadResult(read, err, request.MaxBytes)
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
	trace := request.Trace
	if len(trace) > 256 {
		trace = ""
	}
	env, err := proto.NewEnvelopeWithTrace(proto.TypeWorkspaceReadResult, request.ID, result, trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, env)
}
