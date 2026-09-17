package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// ReadWorkspaceFile observes one private operation; cancellation never retries or cancels native work.
func (s *Session) ReadWorkspaceFile(ctx context.Context, request proto.WorkspaceReadPayload) (proto.WorkspaceReadResultPayload, error) {
	var result proto.WorkspaceReadResultPayload
	if (request.Handle == "") == (request.RunID == "") || request.EnvironmentID == "" ||
		request.MaxBytes < 1 || request.MaxBytes > proto.WorkspaceReadMaxBytes {
		return result, errors.New("agentdaemon gateway: invalid workspace read")
	}
	id := uuid.NewString()
	s.workspaceReadMu.Lock()
	if s.IsClosed() {
		s.workspaceReadMu.Unlock()
		return result, ErrSessionClosed
	}
	if len(s.workspaceReads) >= 4 {
		s.workspaceReadMu.Unlock()
		return result, errors.New("agentdaemon gateway: workspace read capacity")
	}
	if s.workspaceReads == nil {
		s.workspaceReads = make(map[string]chan proto.Envelope)
	}
	replies := make(chan proto.Envelope, 1)
	s.workspaceReads[id] = replies
	s.workspaceReadMu.Unlock()
	defer func() { s.workspaceReadMu.Lock(); delete(s.workspaceReads, id); s.workspaceReadMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 17*time.Second)
	defer cancel()
	env, err := proto.NewEnvelope(proto.TypeWorkspaceRead, id, request)
	if err != nil {
		return result, err
	}
	if err = s.Send(ctx, env); err != nil {
		return result, err
	}
	select {
	case reply, ok := <-replies:
		if !ok {
			return result, ErrSessionClosed
		}
		if reply.DecodePayload(&result) != nil || !validWorkspaceReadResult(result, request.MaxBytes) {
			return proto.WorkspaceReadResultPayload{}, errors.New("agentdaemon gateway: invalid workspace read response")
		}
		return result, nil
	case <-ctx.Done():
		return result, ctx.Err()
	case <-s.closed:
		return result, ErrSessionClosed
	}
}

func validWorkspaceReadResult(result proto.WorkspaceReadResultPayload, limit int) bool {
	if result.Outcome == "completed" {
		return result.CloseAcknowledged && result.ErrorCode == "" && len(result.Data) <= limit &&
			(!result.Truncated || len(result.Data) == limit)
	}
	if len(result.Data) != 0 || result.Truncated || result.CloseAcknowledged {
		return false
	}
	if result.Outcome == "unknown" {
		return result.ErrorCode == "read_unconfirmed"
	}
	if result.Outcome != "rejected" {
		return false
	}
	switch result.ErrorCode {
	case "invalid_request", "resource_unavailable", "read_capacity", "read_unsupported", "not_found", "permission_denied":
		return true
	default:
		return false
	}
}

func (s *Session) dispatchWorkspaceRead(env proto.Envelope) {
	s.workspaceReadMu.Lock()
	defer s.workspaceReadMu.Unlock()
	if replies := s.workspaceReads[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
		}
	}
}

func (s *Session) closeWorkspaceReads() {
	s.workspaceReadMu.Lock()
	defer s.workspaceReadMu.Unlock()
	for id, replies := range s.workspaceReads {
		close(replies)
		delete(s.workspaceReads, id)
	}
}
