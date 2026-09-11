package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrEventLimit = errors.New("execution event storage limit exceeded")

type TurnEvent struct {
	Ordinal   int32
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type ExecutionEvent struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// AppendTurnEvents records an ordered batch atomically, not public SSE replay events.
func (s *Store) AppendTurnEvents(ctx context.Context, tenantID, sessionID, turnID string, first int32, events []ExecutionEvent) error {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if first < 1 || len(events) == 0 || len(events) > 64 {
		return ErrInvalidInput
	}
	normalized := make([]ExecutionEvent, len(events))
	payloadBytes := 0
	for i, event := range events {
		if len(event.Payload) > 512*1024 || !enginePattern.MatchString(event.Kind) {
			return ErrInvalidInput
		}
		payload, err := canonicalJSONObject(event.Payload)
		if err != nil {
			return err
		}
		normalized[i] = ExecutionEvent{Kind: event.Kind, Payload: payload}
		payloadBytes += len(payload)
	}
	if payloadBytes > 1024*1024 {
		return ErrEventLimit
	}
	batch, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, _ pgtype.UUID) error {
		turn, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if first <= turn.EventCount {
			matches, err := q.MatchTurnEventBatch(ctx, sqlc.MatchTurnEventBatchParams{SessionID: p.SessionID, TurnID: p.ID, FirstOrdinal: first, EventCount: int32(len(events)), Batch: batch})
			if err != nil {
				return err
			}
			if !matches {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if turn.Status != TurnInProgress || first != turn.EventCount+1 {
			return ErrTurnConflict
		}
		if turn.EventCount+int32(len(events)) > 65536 || turn.EventBytes+int64(payloadBytes) > 32*1024*1024 {
			return ErrEventLimit
		}
		if err = q.InsertTurnEventBatch(ctx, sqlc.InsertTurnEventBatchParams{SessionID: p.SessionID, TurnID: p.ID, FirstOrdinal: first, Batch: batch}); err != nil {
			return err
		}
		if err := indexEvents(ctx, q, p.SessionID, p.ID, first); err != nil {
			return err
		}
		return q.CountTurnEvent(ctx, sqlc.CountTurnEventParams{SessionID: p.SessionID, ID: p.ID, EventCount: int32(len(events)), PayloadBytes: int64(payloadBytes)})
	})
}

func insertTurnEvent(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn, kind string, payload json.RawMessage) error {
	err := q.InsertTurnEvent(ctx, sqlc.InsertTurnEventParams{SessionID: turn.SessionID, TurnID: turn.ID, Ordinal: turn.EventCount + 1, Kind: kind, Payload: payload})
	if err != nil {
		return err
	}
	if err = q.CountTurnEvent(ctx, sqlc.CountTurnEventParams{SessionID: turn.SessionID, ID: turn.ID, EventCount: 1, PayloadBytes: int64(len(payload))}); err != nil {
		return err
	}
	return indexEvents(ctx, q, turn.SessionID, turn.ID, turn.EventCount+1)
}

func (s *Store) ListTurnEvents(ctx context.Context, tenantID, sessionID, turnID string, after int32, limit int) ([]TurnEvent, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 100 {
		return nil, ErrInvalidInput
	}
	if _, err = s.GetTurn(ctx, tenantID, sessionID, turnID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTurnEvents(ctx, sqlc.ListTurnEventsParams{TenantID: p.TenantID, SessionID: p.SessionID, TurnID: p.ID, Ordinal: after, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	events := make([]TurnEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, TurnEvent{Ordinal: row.Ordinal, Kind: row.Kind, Payload: row.Payload, CreatedAt: row.CreatedAt.Time})
	}
	return events, nil
}
