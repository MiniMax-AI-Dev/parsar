package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// FunctionCall retains public identity and its opaque execution-adapter reference.
type FunctionCall struct {
	CallID, ExecutorCallID, Name string
	Arguments                    json.RawMessage
	Result                       json.RawMessage
	Applied                      bool
}

// RecordFunctionCall makes an execution callback durable without changing public state.
func (s *Store) RecordFunctionCall(ctx context.Context, tenantID, sessionID, turnID string, call FunctionCall) error {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if !validFunctionIdentity(call.CallID) || !validFunctionIdentity(call.ExecutorCallID) || !validFunctionIdentity(call.Name) || len(call.Arguments) > 512*1024 || !json.Valid(call.Arguments) || call.Result != nil || call.Applied {
		return ErrInvalidInput
	}
	return s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		turn, err := q.GetTurn(ctx, p)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		matches, err := q.MatchFunctionCall(ctx, sqlc.MatchFunctionCallParams{SessionID: session, TurnID: p.ID, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
		if err == nil {
			if !matches {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if !acceptsFunctionResult(turn) {
			return ErrTurnConflict
		}
		count, err := q.CreateFunctionCall(ctx, sqlc.CreateFunctionCallParams{SessionID: session, TurnID: p.ID, CallID: call.CallID, ExecutorCallID: call.ExecutorCallID, Name: call.Name, Arguments: call.Arguments})
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrIdempotencyConflict
		}
		return nil
	})
}

func (s *Store) GetFunctionCall(ctx context.Context, tenantID, sessionID, turnID, callID string) (FunctionCall, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return FunctionCall{}, err
	}
	if !validFunctionIdentity(callID) {
		return FunctionCall{}, ErrInvalidInput
	}
	row, err := s.queries.GetFunctionCall(ctx, sqlc.GetFunctionCallParams{TenantID: p.TenantID, SessionID: p.SessionID, TurnID: p.ID, CallID: callID})
	if errors.Is(err, pgx.ErrNoRows) {
		return FunctionCall{}, ErrNotFound
	}
	if err != nil {
		return FunctionCall{}, err
	}
	return functionCallFromRow(row), nil
}

// PendingFunctionCalls excludes applied results and cancelling or terminal Turns.
func (s *Store) PendingFunctionCalls(ctx context.Context, tenantID, sessionID, turnID string) ([]FunctionCall, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return nil, err
	}
	result := make([]FunctionCall, 0)
	err = s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := q.GetTurn(ctx, p); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := q.ListPendingFunctionCalls(ctx, sqlc.ListPendingFunctionCallsParams{SessionID: session, TurnID: p.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			result = append(result, functionCallFromRow(row))
		}
		return nil
	})
	return result, err
}

func functionCallFromRow(row sqlc.FunctionCall) FunctionCall {
	return FunctionCall{CallID: row.CallID, ExecutorCallID: row.ExecutorCallID, Name: row.Name, Arguments: row.Arguments, Result: row.Result, Applied: row.Applied}
}

func validFunctionIdentity(id string) bool { return strings.TrimSpace(id) != "" && len(id) <= 512 }

func acceptsFunctionResult(turn sqlc.Turn) bool {
	return (turn.Status == TurnInProgress || turn.Status == TurnWaiting) && !turn.CancelRequestedAt.Valid
}
