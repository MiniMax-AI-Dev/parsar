package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SubmitFunctionResult stores a caller-validated result object; its wire schema belongs to the API.
func (s *Store) SubmitFunctionResult(ctx context.Context, tenantID, sessionID, turnID, callID string, result json.RawMessage) error {
	if len(result) == 0 || len(result) > 512*1024 {
		return ErrInvalidInput
	}
	result, err := canonicalJSONObject(result)
	if err != nil {
		return err
	}
	return s.withFunctionCall(ctx, tenantID, sessionID, turnID, callID, func(q *sqlc.Queries, turn sqlc.Turn, call sqlc.FunctionCall) error {
		match, err := q.MatchFunctionResult(ctx, sqlc.MatchFunctionResultParams{SessionID: turn.SessionID, TurnID: turn.ID, CallID: callID, Result: result})
		if err != nil {
			return err
		}
		if match.Submitted {
			if !match.Matches {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !acceptsFunctionResult(turn) {
			return ErrTurnConflict
		}
		return q.SubmitFunctionResult(ctx, sqlc.SubmitFunctionResultParams{SessionID: turn.SessionID, TurnID: turn.ID, CallID: call.CallID, Result: result})
	})
}

// ConfirmFunctionResult records native application, not external tool success.
func (s *Store) ConfirmFunctionResult(ctx context.Context, tenantID, sessionID, turnID, callID string) error {
	return s.withFunctionCall(ctx, tenantID, sessionID, turnID, callID, func(q *sqlc.Queries, turn sqlc.Turn, call sqlc.FunctionCall) error {
		if call.Applied {
			return nil
		}
		if len(call.Result) == 0 || !acceptsFunctionResult(turn) {
			return ErrTurnConflict
		}
		return q.ApplyFunctionResult(ctx, sqlc.ApplyFunctionResultParams{SessionID: turn.SessionID, TurnID: turn.ID, CallID: call.CallID})
	})
}

func (s *Store) withFunctionCall(ctx context.Context, tenantID, sessionID, turnID, callID string, fn func(*sqlc.Queries, sqlc.Turn, sqlc.FunctionCall) error) error {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if !validFunctionIdentity(callID) {
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
		call, err := q.GetFunctionCall(ctx, sqlc.GetFunctionCallParams{TenantID: p.TenantID, SessionID: session, TurnID: p.ID, CallID: callID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return fn(q, turn, call)
	})
}
