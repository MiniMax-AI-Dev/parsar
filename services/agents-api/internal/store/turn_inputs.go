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

// Input is a validated execution command, not an upstream wire type.
// The API validates event fields before constructing this storage input.
type Input struct {
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// SubmitMessage and RequestCancel use the same request-level admission as batches.
func (s *Store) SubmitMessage(ctx context.Context, tenantID, sessionID, key string, payload json.RawMessage) (InputReceipt, error) {
	return s.submitOne(ctx, tenantID, sessionID, key, Input{Kind: "message", Payload: payload})
}

func (s *Store) RequestCancel(ctx context.Context, tenantID, sessionID, key string) (InputReceipt, error) {
	return s.submitOne(ctx, tenantID, sessionID, key, Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
}

func (s *Store) submitOne(ctx context.Context, tenantID, sessionID, key string, input Input) (InputReceipt, error) {
	receipts, err := s.SubmitInputs(ctx, tenantID, sessionID, key, []Input{input})
	if err != nil {
		return InputReceipt{}, err
	}
	return receipts[0], nil
}

// SubmitInputs commits a request in order under one Session lock. The entire
// batch is the retry identity; replay never re-evaluates a cancellation target.
// Internal receipts are not the response body of the public events endpoint.
func (s *Store) SubmitInputs(ctx context.Context, tenantID, sessionID, key string, inputs []Input) ([]InputReceipt, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return nil, fmt.Errorf("%w: idempotency key is required and limited to 128 bytes", ErrInvalidInput)
	}
	batch, encoded, err := validateInputs(inputs)
	if err != nil {
		return nil, err
	}
	receipts := make([]InputReceipt, 0, len(batch))
	err = s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.FindInputBatch(ctx, sqlc.FindInputBatchParams{SessionID: session, IdempotencyKey: key, Batch: encoded})
		if err != nil {
			return err
		}
		if len(previous) > 0 {
			if !previous[0].Matches {
				return ErrIdempotencyConflict
			}
			for _, row := range previous {
				receipts = append(receipts, inputReceipt(row.Sequence, row.TurnID, true))
			}
			return nil
		}
		for position, input := range batch {
			receipt, err := admitInput(ctx, q, session, key, int32(position), input)
			if err != nil {
				return err
			}
			receipts = append(receipts, receipt)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("submit turn inputs: %w", err)
	}
	return receipts, nil
}

func validateInputs(inputs []Input) ([]Input, json.RawMessage, error) {
	if len(inputs) == 0 || len(inputs) > 64 {
		return nil, nil, fmt.Errorf("%w: input batch must contain 1..64 events", ErrInvalidInput)
	}
	batch := make([]Input, len(inputs))
	size := 0
	for i, input := range inputs {
		size += len(input.Payload)
		if size > 512*1024 || len(input.Payload) == 0 || (input.Kind != "message" && input.Kind != "cancel") {
			return nil, nil, fmt.Errorf("%w: message/cancel payloads must be nonempty and total at most 512 KiB", ErrInvalidInput)
		}
		payload, err := canonicalJSONObject(input.Payload)
		if err != nil {
			return nil, nil, err
		}
		if input.Kind == "cancel" && string(payload) != "{}" {
			return nil, nil, fmt.Errorf("%w: cancel payload must be empty", ErrInvalidInput)
		}
		batch[i] = Input{Kind: input.Kind, Payload: payload}
	}
	encoded, err := json.Marshal(batch)
	return batch, encoded, err
}

func admitInput(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, key string, position int32, input Input) (InputReceipt, error) {
	turn, err := q.GetActiveTurn(ctx, session)
	if errors.Is(err, pgx.ErrNoRows) {
		if input.Kind == "message" {
			turn, err = q.CreateTurn(ctx, sqlc.CreateTurnParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session})
			if err == nil {
				err = recordTurnChange(ctx, q, turn, true)
			}
		} else {
			err = nil // Retain even an idle cancellation's retry identity.
		}
	}
	if err != nil {
		return InputReceipt{}, err
	}
	sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	if err != nil {
		return InputReceipt{}, err
	}
	if input.Kind == "cancel" && turn.ID.Valid {
		if err := q.RequestTurnCancel(ctx, sqlc.RequestTurnCancelParams{ID: turn.ID, SessionID: session}); err != nil {
			return InputReceipt{}, err
		}
		if turn.Status == TurnQueued {
			cancelled, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
			if err != nil {
				return InputReceipt{}, err
			}
			if err := recordTurnChange(ctx, q, cancelled, false); err != nil {
				return InputReceipt{}, err
			}
		}
	}
	if err := indexInput(ctx, q, session, sequence); err != nil {
		return InputReceipt{}, err
	}
	return inputReceipt(sequence, turn.ID, false), nil
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
