package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// FunctionResultInput identifies a persisted call; Result is validated by the API.
// It is an internal command, not an upstream input event.
type FunctionResultInput struct {
	TurnID string          `json:"turn_id"`
	CallID string          `json:"call_id"`
	Result json.RawMessage `json:"result"`
}

func functionInput(raw json.RawMessage) (FunctionResultInput, error) {
	var input FunctionResultInput
	if json.Unmarshal(raw, &input) != nil || !validFunctionIdentity(input.CallID) {
		return input, ErrInvalidInput
	}
	if _, err := uuid.Parse(input.TurnID); err != nil {
		return input, ErrInvalidInput
	}
	result, err := canonicalJSONObject(input.Result)
	if err != nil {
		return input, err
	}
	input.Result = result
	return input, nil
}

func admitFunctionResult(ctx context.Context, q *sqlc.Queries, tenantID string, session pgtype.UUID, key string, position int32, input Input) (InputReceipt, error) {
	result, err := functionInput(input.Payload)
	if err != nil {
		return InputReceipt{}, err
	}
	lookup, err := turnLookup(tenantID, uuid.UUID(session.Bytes).String(), result.TurnID)
	if err != nil {
		return InputReceipt{}, err
	}
	turn, err := q.GetTurn(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return InputReceipt{}, ErrNotFound
	}
	if err != nil {
		return InputReceipt{}, err
	}
	if err := storeFunctionResult(ctx, q, turn, result.CallID, result.Result); err != nil {
		return InputReceipt{}, err
	}
	sequence, err := q.CreateTurnInput(ctx, sqlc.CreateTurnInputParams{
		SessionID: session, TurnID: turn.ID, IdempotencyKey: key, Kind: input.Kind, Payload: input.Payload, BatchPosition: position,
	})
	if err != nil {
		return InputReceipt{}, err
	}
	return inputReceipt(sequence, turn.ID, false), nil
}
