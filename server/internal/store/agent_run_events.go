package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
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
	_, err = sqlc.New(tx).InsertAgentRunEvent(ctx, sqlc.InsertAgentRunEventParams{
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
	return tx.Commit(ctx)
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
