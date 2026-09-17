package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type fileWriteResult struct {
	size int64
	err  error
}
type fileWriteRequest struct {
	ctx         context.Context
	environment store.Environment
	path        string
	data        []byte
	result      chan fileWriteResult
}

// WriteEnvironmentFile observes a Worker-owned mutation. Caller detachment never
// clears the durable write intent or starts a replacement operation.
func (w *Worker) WriteEnvironmentFile(ctx context.Context, environment store.Environment, path string, data []byte) (int64, error) {
	if len(data) > proto.WorkspaceWriteMaxBytes {
		return 0, store.ErrInvalidInput
	}
	request := fileWriteRequest{ctx: ctx, environment: environment, path: path, data: data, result: make(chan fileWriteResult, 1)}
	select {
	case w.fileWrites <- request:
	case <-ctx.Done():
		return 0, ErrExecutionUnavailable
	case <-w.stopped:
		return 0, ErrExecutionUnavailable
	}
	select {
	case result := <-request.result:
		return result.size, result.err
	case <-ctx.Done():
		return 0, ErrExecutionUnavailable
	case <-w.stopped:
		return 0, ErrExecutionUnavailable
	}
}

func (w *Worker) runFileWrite(owner context.Context, request fileWriteRequest) fileWriteResult {
	unavailable := fileWriteResult{err: ErrExecutionUnavailable}
	if owner.Err() != nil || request.ctx.Err() != nil {
		return unavailable
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(owner), 205*time.Second)
	defer cancel()
	if w.CheckOwnership(ctx) != nil {
		return unavailable
	}
	environment, err := w.dispatcher.Store.GetEnvironment(ctx, request.environment.TenantID, request.environment.ID)
	if err != nil {
		return fileWriteResult{err: err}
	}
	if environment.SessionID != request.environment.SessionID {
		return fileWriteResult{err: store.ErrNotFound}
	}
	placement, err := parseEnvironmentPlacement(environment.Configuration)
	if err != nil || placement.Type != "openai_hosted" {
		return unavailable
	}
	session, err := w.dispatcher.Store.GetSession(ctx, environment.TenantID, environment.SessionID)
	if err != nil {
		return fileWriteResult{err: err}
	}
	bound, err := w.dispatcher.Store.GetSessionDevice(ctx, session.TenantID, session.ID)
	if err != nil || !environmentDeviceMatches(session, environment, bound, placement) || w.dispatcher.Registry == nil {
		return unavailable
	}
	peer, err := w.dispatcher.Registry.LookupDevice(bound.ID)
	if err != nil {
		return unavailable
	}
	info, found, known := peer.AgentKindStatus(session.Engine)
	if !known || !found || !info.Available || !info.Capabilities.LocalEnvironment {
		return unavailable
	}
	dataDigest := sha256.Sum256(request.data)
	body, _ := json.Marshal([]any{request.path, len(request.data), hex.EncodeToString(dataDigest[:])})
	digest := sha256.Sum256(body)
	wire := proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment.ID, SessionID: session.ID, Path: request.path, SizeBytes: len(request.data), SHA256: hex.EncodeToString(dataDigest[:])}
	if !proto.ValidWorkspaceWriteRequest(wire) {
		return fileWriteResult{err: store.ErrInvalidInput}
	}
	key := store.FileWriteIdentity{ID: uuid.NewString(), DeviceID: bound.ID, RequestSHA256: hex.EncodeToString(digest[:])}
	intent, err := w.dispatcher.Store.ReserveEnvironmentFileWrite(ctx, environment.TenantID, environment.ID, key)
	if err != nil {
		return fileWriteResult{err: err}
	}
	if intent.Replayed {
		return unavailable
	}
	result, err := peer.WriteWorkspaceFile(ctx, key.ID, proto.WorkspaceWritePayload{EnvironmentID: environment.ID, SessionID: session.ID, Path: request.path}, request.data)
	if err != nil || (result.Outcome != "completed" && result.Outcome != "rejected") {
		return unavailable
	}
	state := "rejected"
	if result.Outcome == "completed" {
		state = "committed"
	}
	settle, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if _, err := w.dispatcher.Store.SettleEnvironmentFileWrite(settle, environment.TenantID, environment.ID, key, state); err != nil {
		return unavailable
	}
	if state == "rejected" {
		if result.ErrorCode == "invalid_request" || result.ErrorCode == "write_rejected" {
			return fileWriteResult{err: store.ErrInvalidInput}
		}
		return unavailable
	}
	return fileWriteResult{size: int64(result.SizeBytes)}
}
