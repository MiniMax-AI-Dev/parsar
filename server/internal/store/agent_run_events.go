package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type AgentRunEventRead struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	AgentRunID  string         `json:"agent_run_id"`
	Sequence    int64          `json:"sequence"`
	EventKind   string         `json:"event_kind"`
	Payload     map[string]any `json:"payload"`
	OccurredAt  time.Time      `json:"occurred_at"`
	CreatedAt   time.Time      `json:"created_at"`
}

type RecordAgentRunEventInput struct {
	RunID      string
	EventKind  string
	Payload    map[string]any
	OccurredAt time.Time
}

func (s *Store) RecordAgentRunEvent(ctx context.Context, input RecordAgentRunEventInput) error {
	runID, err := uuid(input.RunID)
	if err != nil {
		return err
	}
	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	occurredAt := input.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	now := time.Now().UTC()

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockAgentRunEventSequence(ctx, tx, input.RunID); err != nil {
		return err
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `select coalesce(max(sequence), 0) + 1 from agent_run_events where agent_run_id = $1`, runID).Scan(&sequence); err != nil {
		return err
	}
	queries := sqlc.New(tx)
	_, err = queries.InsertAgentRunEvent(ctx, sqlc.InsertAgentRunEventParams{
		Sequence:   sequence,
		EventKind:  input.EventKind,
		Payload:    encoded,
		OccurredAt: timestamptz(occurredAt),
		CreatedAt:  timestamptz(now),
		AgentRunID: runID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrUnknownAgentRun, input.RunID)
		}
		return err
	}
	if kind, requestID, deviceID, ttl, ok := interactionFromRunEvent(input.EventKind, payload); ok {
		if err := queries.InsertAgentInteractionFromRun(ctx, sqlc.InsertAgentInteractionFromRunParams{
			RequestID:  requestID,
			Kind:       kind,
			Request:    encoded,
			DeviceID:   deviceID,
			CreatedAt:  timestamptz(occurredAt),
			ExpiresAt:  timestamptz(occurredAt.Add(ttl)),
			AgentRunID: runID,
		}); err != nil {
			return err
		}
	}
	if reason, ok := terminalInteractionReason(input.EventKind, payload); ok {
		if _, err := queries.CancelOpenAgentInteractionsByRunID(ctx, sqlc.CancelOpenAgentInteractionsByRunIDParams{
			Reason: reason, Now: timestamptz(occurredAt), AgentRunID: runID,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func interactionFromRunEvent(eventKind string, payload map[string]any) (kind, requestID, deviceID string, ttl time.Duration, ok bool) {
	switch eventKind {
	case "permission.asked":
		kind = AgentInteractionKindPermission
	case "prompt_for_user_choice.asked":
		kind = AgentInteractionKindUserChoice
	default:
		return "", "", "", 0, false
	}
	requestID, _ = payload["request_id"].(string)
	deviceID, _ = payload["device_id"].(string)
	requestID = strings.TrimSpace(requestID)
	ttl = AgentInteractionTTL
	if millis, valid := positiveUint64(payload["auto_resolution_ms"]); valid {
		const maxMillis = uint64(^uint64(0)>>1) / uint64(time.Millisecond)
		if millis <= maxMillis {
			ttl = time.Duration(millis) * time.Millisecond
		}
	}
	return kind, requestID, strings.TrimSpace(deviceID), ttl, requestID != ""
}

func positiveUint64(value any) (uint64, bool) {
	switch typed := value.(type) {
	case uint64:
		return typed, typed > 0
	case int:
		return uint64(typed), typed > 0
	case int64:
		return uint64(typed), typed > 0
	case float64:
		return uint64(typed), typed > 0 && typed == float64(uint64(typed))
	default:
		return 0, false
	}
}

func terminalInteractionReason(eventKind string, payload map[string]any) (string, bool) {
	switch eventKind {
	case "run.completed", "run.failed", "run.cancelled":
		reason, _ := payload["reason"].(string)
		if strings.TrimSpace(reason) == "" {
			reason = eventKind
		}
		return reason, true
	default:
		return "", false
	}
}

func (s *Store) ListAgentRunEvents(ctx context.Context, runID string, afterSequence int64) ([]AgentRunEventRead, error) {
	runUUID, err := uuid(runID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	if _, err := queries.GetAgentRunForRead(ctx, runUUID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		return nil, err
	}
	rows, err := queries.ListAgentRunEventsByRun(ctx, sqlc.ListAgentRunEventsByRunParams{
		AgentRunID:    runUUID,
		AfterSequence: afterSequence,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AgentRunEventRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentRunEventFromRow(row.ID, row.WorkspaceID, row.AgentRunID, row.Sequence, row.EventKind, row.Payload, row.OccurredAt, row.CreatedAt))
	}
	return out, nil
}

func lockAgentRunEventSequence(ctx context.Context, tx pgx.Tx, runID string) error {
	h := fnv.New64a()
	_, _ = h.Write([]byte(runID))
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1::bigint)`, int64(h.Sum64()))
	return err
}

func agentRunEventFromRow(id, workspaceID, runID string, sequence int64, kind string, payload []byte, occurredAt, createdAt pgtype.Timestamptz) AgentRunEventRead {
	return AgentRunEventRead{
		ID:          id,
		WorkspaceID: workspaceID,
		AgentRunID:  runID,
		Sequence:    sequence,
		EventKind:   kind,
		Payload:     decodeJSONMap(payload),
		OccurredAt:  pgTime(occurredAt),
		CreatedAt:   pgTime(createdAt),
	}
}
