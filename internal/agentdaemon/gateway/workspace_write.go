package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// WriteWorkspaceFile sends an already durably owned mutation once. A transport
// error is not a rejection and must retain the caller's unknown-outcome gate.
func (s *Session) WriteWorkspaceFile(ctx context.Context, id string, request proto.WorkspaceWritePayload, data []byte) (proto.WorkspaceWriteResultPayload, error) {
	var empty proto.WorkspaceWriteResultPayload
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id || len(data) > proto.WorkspaceWriteMaxBytes {
		return empty, errors.New("agentdaemon gateway: invalid workspace write")
	}
	digest := sha256.Sum256(data)
	request.Step, request.SizeBytes, request.SHA256 = "begin", len(data), hex.EncodeToString(digest[:])
	if !proto.ValidWorkspaceWriteRequest(request) {
		return empty, errors.New("agentdaemon gateway: invalid workspace write")
	}
	s.workspaceWriteMu.Lock()
	if s.IsClosed() {
		s.workspaceWriteMu.Unlock()
		return empty, ErrSessionClosed
	}
	if len(s.workspaceWrites) != 0 {
		s.workspaceWriteMu.Unlock()
		return empty, errors.New("agentdaemon gateway: workspace write capacity")
	}
	replies := make(chan proto.Envelope, 1)
	s.workspaceWrites = map[string]chan proto.Envelope{id: replies}
	s.workspaceWriteMu.Unlock()
	defer func() { s.workspaceWriteMu.Lock(); delete(s.workspaceWrites, id); s.workspaceWriteMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 195*time.Second)
	defer cancel()
	exchange := func(payload proto.WorkspaceWritePayload, outcome string, offset int) (proto.WorkspaceWriteResultPayload, error) {
		env, err := proto.NewEnvelope(proto.TypeWorkspaceWrite, id, payload)
		if err != nil || len(env.Payload) > proto.WorkspaceWriteMaxFrameBytes {
			return empty, errors.New("agentdaemon gateway: invalid write frame")
		}
		if err := s.Send(ctx, env); err != nil {
			return empty, err
		}
		select {
		case env, ok := <-replies:
			if !ok {
				return empty, ErrSessionClosed
			}
			var result proto.WorkspaceWriteResultPayload
			if env.DecodePayload(&result) != nil || !validWorkspaceWriteResult(result, outcome, offset, len(data)) {
				return empty, errors.New("agentdaemon gateway: invalid write receipt")
			}
			return result, nil
		case <-ctx.Done():
			return empty, ctx.Err()
		case <-s.closed:
			return empty, ErrSessionClosed
		}
	}
	result, err := exchange(request, "ready", 0)
	if err != nil || result.Outcome != "ready" {
		return result, err
	}
	for offset := 0; offset < len(data); {
		end := min(offset+proto.WorkspaceWriteChunkBytes, len(data))
		result, err = exchange(proto.WorkspaceWritePayload{Step: "chunk", Offset: offset, Data: data[offset:end]}, "received", end)
		if err != nil || result.Outcome != "received" {
			return result, err
		}
		offset = end
	}
	return exchange(proto.WorkspaceWritePayload{Step: "commit"}, "completed", 0)
}

func validWorkspaceWriteResult(r proto.WorkspaceWriteResultPayload, expected string, offset, size int) bool {
	if r.Outcome == "rejected" {
		if r.Offset != 0 || r.SizeBytes != 0 {
			return false
		}
		switch r.ErrorCode {
		case "invalid_request", "resource_unavailable", "write_capacity", "write_unsupported", "write_rejected":
			return true
		default:
			return false
		}
	}
	if r.Outcome == "unknown" {
		return r.Offset == 0 && r.SizeBytes == 0 && r.ErrorCode == "write_unconfirmed"
	}
	if r.Outcome != expected || r.Offset != offset || r.ErrorCode != "" {
		return false
	}
	if expected == "completed" {
		return r.SizeBytes == size
	}
	return r.SizeBytes == 0
}

func (s *Session) dispatchWorkspaceWrite(env proto.Envelope) {
	s.workspaceWriteMu.Lock()
	defer s.workspaceWriteMu.Unlock()
	if replies := s.workspaceWrites[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
		}
	}
}

func (s *Session) closeWorkspaceWrites() {
	s.workspaceWriteMu.Lock()
	defer s.workspaceWriteMu.Unlock()
	for id, replies := range s.workspaceWrites {
		close(replies)
		delete(s.workspaceWrites, id)
	}
}
