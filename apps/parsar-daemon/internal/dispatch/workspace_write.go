package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Router.mu protects this single bounded transfer for the dedicated Environment.
type workspaceUpload struct {
	envelope  proto.Envelope
	request   proto.WorkspaceWritePayload
	data      []byte
	ready     chan struct{}
	finished  bool
	apply     bool
	uncertain bool
}

func (r *Router) handleWorkspaceWrite(ctx context.Context, env proto.Envelope) error {
	id, err := uuid.Parse(env.ID)
	if err != nil || id == uuid.Nil || id.String() != env.ID {
		return errors.New("dispatch: invalid workspace write identity")
	}
	var request proto.WorkspaceWritePayload
	if len(env.Payload) > proto.WorkspaceWriteMaxFrameBytes || env.DecodePayload(&request) != nil || !proto.ValidWorkspaceWriteRequest(request) {
		// A malformed frame on an already admitted operation cannot claim that
		// its earlier commit did not execute.
		r.mu.Lock()
		pending := r.workspaceWrite != nil && r.workspaceWrite.envelope.ID == env.ID
		r.mu.Unlock()
		if pending {
			return errors.New("dispatch: malformed pending write frame")
		}
		return r.sendWorkspaceWrite(ctx, env.ID, rejectedWorkspaceWrite("invalid_request"))
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	if request.Step == "begin" {
		if r.workspaceWrite != nil {
			duplicate := r.workspaceWrite.envelope.ID == env.ID
			r.mu.Unlock()
			if duplicate {
				return errors.New("dispatch: workspace write already admitted")
			}
			return r.sendWorkspaceWrite(ctx, env.ID, rejectedWorkspaceWrite("write_capacity"))
		}
		if !r.localWorkspace.AcceptsFileWrite(request.EnvironmentID, request.SessionID) || len(r.sessions) != 0 || len(r.idle) != 0 {
			r.mu.Unlock()
			return r.sendWorkspaceWrite(ctx, env.ID, rejectedWorkspaceWrite("resource_unavailable"))
		}
		for _, p := range r.preparations {
			if p.owns {
				r.mu.Unlock()
				return r.sendWorkspaceWrite(ctx, env.ID, rejectedWorkspaceWrite("resource_unavailable"))
			}
		}
		u := &workspaceUpload{envelope: env, request: request, data: make([]byte, 0, request.SizeBytes), ready: make(chan struct{})}
		r.workspaceWrite = u
		r.shutdownWG.Add(1)
		r.mu.Unlock()
		go r.runWorkspaceUpload(context.WithoutCancel(ctx), u)
		return r.sendWorkspaceWrite(ctx, env.ID, proto.WorkspaceWriteResultPayload{Outcome: "ready"})
	}
	u := r.workspaceWrite
	if u == nil || u.envelope.ID != env.ID {
		r.mu.Unlock()
		return r.sendWorkspaceWrite(ctx, env.ID, rejectedWorkspaceWrite("resource_unavailable"))
	}
	if u.finished {
		r.mu.Unlock()
		return errors.New("dispatch: workspace write body already closed")
	}
	if request.Step == "chunk" && request.Offset == len(u.data) && len(request.Data) <= u.request.SizeBytes-len(u.data) {
		u.data = append(u.data, request.Data...)
		offset := len(u.data)
		r.mu.Unlock()
		return r.sendWorkspaceWrite(ctx, env.ID, proto.WorkspaceWriteResultPayload{Outcome: "received", Offset: offset})
	}
	if request.Step == "commit" && len(u.data) == u.request.SizeBytes {
		digest := sha256.Sum256(u.data)
		u.apply = hex.EncodeToString(digest[:]) == u.request.SHA256
	}
	u.finished = true
	close(u.ready)
	r.mu.Unlock()
	return nil
}

func (r *Router) runWorkspaceUpload(ctx context.Context, u *workspaceUpload) {
	defer r.shutdownWG.Done()
	timer := time.NewTimer(120 * time.Second)
	defer timer.Stop()
	select {
	case <-u.ready:
	case <-r.shutdownCh:
	case <-timer.C:
	}
	r.mu.Lock()
	apply := u.apply && !r.closed
	u.finished = true
	data := u.data
	u.data = nil
	r.mu.Unlock()
	result := rejectedWorkspaceWrite("invalid_request")
	if apply {
		write, err := r.localWorkspace.WriteWorkspaceFile(ctx, u.request.Path, data)
		result = workspaceWriteResult(write, err, u.request.SizeBytes)
	}
	r.mu.Lock()
	u.uncertain = result.Outcome == "unknown"
	if !u.uncertain {
		r.workspaceWrite = nil
	}
	r.mu.Unlock()
	_ = r.sendWorkspaceWrite(ctx, u.envelope.ID, result)
}

func rejectedWorkspaceWrite(code string) proto.WorkspaceWriteResultPayload {
	return proto.WorkspaceWriteResultPayload{Outcome: "rejected", ErrorCode: code}
}

func workspaceWriteResult(write agent.WorkspaceWriteResult, err error, size int) proto.WorkspaceWriteResultPayload {
	if err == nil && write.SizeBytes == int64(size) {
		return proto.WorkspaceWriteResultPayload{Outcome: "completed", SizeBytes: size}
	}
	for _, failure := range []struct {
		err  error
		code string
	}{
		{agent.ErrWorkspaceWriteUnsupported, "write_unsupported"},
		{agent.ErrWorkspaceWriteUnavailable, "resource_unavailable"},
		{agent.ErrWorkspaceWriteBusy, "write_capacity"},
		{agent.ErrWorkspaceWriteInvalid, "invalid_request"},
		{agent.ErrWorkspaceWriteRejected, "write_rejected"},
	} {
		if errors.Is(err, failure.err) {
			return rejectedWorkspaceWrite(failure.code)
		}
	}
	return proto.WorkspaceWriteResultPayload{Outcome: "unknown", ErrorCode: "write_unconfirmed"}
}

func (r *Router) sendWorkspaceWrite(ctx context.Context, id string, result proto.WorkspaceWriteResultPayload) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env, err := proto.NewEnvelope(proto.TypeWorkspaceWriteResult, id, result)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, env)
}
