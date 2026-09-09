package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

var ErrAgentRunNotRetryable = errors.New("run must be failed, cancelled, or interrupted with an active agent, conversation, and trigger message")

type RetryAgentRunInput struct {
	RunID  string
	UserID string
	Reason string
}

type RetryAgentRunResult struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
}

func (s *Store) RetryAgentRun(ctx context.Context, input RetryAgentRunInput) (RetryAgentRunResult, error) {
	var result RetryAgentRunResult
	runID, err := uuid(input.RunID)
	if err != nil {
		return result, err
	}
	userID, err := uuid(input.UserID)
	if err != nil {
		return result, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New(tx)
	source, err := q.GetAgentRunRetrySource(ctx, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, ErrAgentRunNotRetryable
		}
		return result, err
	}
	result.ConversationID = source.ConversationID
	if source.RetryRunID != "" {
		result.RunID = source.RetryRunID
		return result, nil
	}
	now := time.Now().UTC()
	result.RunID = newID()
	metadata, _ := json.Marshal(map[string]any{"source": "manual_retry", "retry_of_run_id": input.RunID, "retry_reason": input.Reason})
	err = q.CreateAgentRunRetry(ctx, sqlc.CreateAgentRunRetryParams{
		ID: mustUUID(result.RunID), WorkspaceID: mustUUID(source.WorkspaceID),
		ConversationID: mustUUID(source.ConversationID), AgentID: mustUUID(source.AgentID),
		ConnectorType: source.ConnectorType, TriggerMessageID: source.TriggerMessageID,
		RetryOfRunID: runID, RequestedByID: userID, Visibility: source.Visibility,
		Metadata: metadata, Now: timestamptz(now),
	})
	if err != nil {
		return RetryAgentRunResult{}, err
	}
	patch, _ := json.Marshal(map[string]any{"retry_run_id": result.RunID})
	if err := q.AppendAgentRunMetadata(ctx, sqlc.AppendAgentRunMetadataParams{ID: runID, Patch: patch, Now: timestamptz(now)}); err != nil {
		return RetryAgentRunResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RetryAgentRunResult{}, err
	}
	s.emitAuditEvent(audit.Event{OccurredAt: now, Source: audit.SourceRuntime, EventType: auditAgentRunCreated,
		ActorType: audit.ActorTypeUser, ActorID: input.UserID, TargetType: "agent_run", TargetID: result.RunID,
		WorkspaceID: source.WorkspaceID, Payload: map[string]any{"source": "manual_retry", "retry_of_run_id": input.RunID, "agent_id": source.AgentID}})
	if connectorNeedsStreamingDispatch(source.ConnectorType) {
		// A committed retry must still dispatch if the HTTP caller disconnects.
		dispatchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		s.dispatchPendingStreaming(dispatchCtx, []StreamingDispatchInput{{RunID: result.RunID, ConversationID: source.ConversationID, ConnectorType: source.ConnectorType}})
	}
	return result, nil
}
