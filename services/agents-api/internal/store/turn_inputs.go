package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

type InputReceipt struct {
	Sequence int64
	TurnID   string // Empty for a cancellation accepted while the Session was idle.
	Replayed bool
}

type TurnInput struct {
	Sequence  int64
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// SubmitMessage persists one validated input. An idle Session gets a new Turn;
// active Sessions receive steering input on the current Turn. API-level event
// validation and batch submission are separate from this internal primitive.
func (s *Store) SubmitMessage(ctx context.Context, tenantID, sessionID, key string, payload json.RawMessage) (InputReceipt, error) {
	if len(payload) == 0 || len(payload) > 512*1024 {
		return InputReceipt{}, fmt.Errorf("%w: message payload must be nonempty and at most 512 KiB", ErrInvalidInput)
	}
	canonical, err := canonicalJSONObject(payload)
	if err != nil {
		return InputReceipt{}, err
	}
	return s.submitInput(ctx, tenantID, sessionID, key, "message", canonical)
}

// RequestCancel fixes the cancellation target at first acceptance. A retry must
// not cancel a later Turn. Queued work stops immediately; running work remains
// active until the executor acknowledges cancellation or reports another outcome.
func (s *Store) RequestCancel(ctx context.Context, tenantID, sessionID, key string) (InputReceipt, error) {
	return s.submitInput(ctx, tenantID, sessionID, key, "cancel", json.RawMessage(`{}`))
}

func (s *Store) submitInput(ctx context.Context, tenantID, sessionID, key, kind string, payload json.RawMessage) (InputReceipt, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return InputReceipt{}, fmt.Errorf("%w: idempotency key is required and limited to 128 bytes", ErrInvalidInput)
	}
	var receipt InputReceipt
	err := s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.FindTurnInput(ctx, sqlc.FindTurnInputParams{SessionID: session, IdempotencyKey: key, Kind: kind, Payload: payload})
		if err == nil {
			if !previous.Matches {
				return ErrIdempotencyConflict
			}
			receipt = inputReceipt(previous.Sequence, previous.TurnID, true)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		turn, err := q.GetActiveTurn(ctx, session)
		if errors.Is(err, pgx.ErrNoRows) {
			if kind == "message" {
				turn, err = q.CreateTurn(ctx, sqlc.CreateTurnParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session})
			} else {
				err = nil // Retain even an idle cancellation's retry identity.
			}
		}
		if err != nil {
			return err
		}
		sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
			SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: kind, Payload: payload,
		})
		if err != nil {
			return err
		}
		if kind == "cancel" && turn.ID.Valid {
			if err := q.RequestTurnCancel(ctx, sqlc.RequestTurnCancelParams{ID: turn.ID, SessionID: session}); err != nil {
				return err
			}
		}
		receipt = inputReceipt(sequence, turn.ID, false)
		return nil
	})
	if err != nil {
		return InputReceipt{}, fmt.Errorf("submit turn input: %w", err)
	}
	return receipt, nil
}

// ListTurnInputs is an internal ordered recovery query, not the public SSE stream.
func (s *Store) ListTurnInputs(ctx context.Context, tenantID, sessionID, turnID string, after int64, limit int) ([]TurnInput, error) {
	params, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: nonnegative cursor and page size 1..100 required", ErrInvalidInput)
	}
	if _, err := s.GetTurn(ctx, tenantID, sessionID, turnID); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListTurnInputs(ctx, sqlc.ListTurnInputsParams{
		TenantID: params.TenantID, SessionID: params.SessionID, TurnID: params.ID, Sequence: after, Limit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list turn inputs: %w", err)
	}
	inputs := make([]TurnInput, 0, len(rows))
	for _, row := range rows {
		inputs = append(inputs, TurnInput{Sequence: row.Sequence, Kind: row.Kind, Payload: row.Payload, CreatedAt: row.CreatedAt.Time})
	}
	return inputs, nil
}

func inputReceipt(sequence int64, turn pgtype.UUID, replayed bool) InputReceipt {
	receipt := InputReceipt{Sequence: sequence, Replayed: replayed}
	if turn.Valid {
		receipt.TurnID = uuid.UUID(turn.Bytes).String()
	}
	return receipt
}
