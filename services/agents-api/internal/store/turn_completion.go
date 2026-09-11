package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrUnappliedInputs = errors.New("turn has messages without an executor receipt")

// CompleteExecution commits the outcome and native continuity under the admission lock.
func (s *Store) CompleteExecution(ctx context.Context, tenantID, sessionID, turnID, status string, outcome json.RawMessage, nativeID string, appliedThrough int64) (Turn, error) {
	p, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return Turn{}, err
	}
	if !terminalStatus(status) || len(outcome) > 512*1024 || len(nativeID) > 512 || appliedThrough < 0 {
		return Turn{}, ErrInvalidInput
	}
	outcome, err = canonicalJSONObject(outcome)
	if err != nil {
		return Turn{}, err
	}
	var row sqlc.Turn
	err = s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := q.GetTurn(ctx, p); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if status == TurnCompleted {
			pending, err := q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: session, TurnID: p.ID, Sequence: appliedThrough})
			if err != nil {
				return err
			}
			if pending {
				return ErrUnappliedInputs
			}
		}
		var err error
		row, err = q.TransitionTurn(ctx, sqlc.TransitionTurnParams{ID: p.ID, SessionID: session, ExpectedStatus: TurnInProgress, NewStatus: status, Outcome: outcome})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTurnConflict
		}
		if err != nil {
			return err
		}
		if err = insertTurnEvent(ctx, q, row, "execution_"+status, outcome); err != nil {
			return err
		}
		if nativeID != "" {
			n, err := q.RememberNativeSession(ctx, sqlc.RememberNativeSessionParams{SessionID: session, NativeSessionID: nativeID})
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrNotFound
			}
		}
		row, err = q.GetTurn(ctx, p)
		if err != nil {
			return err
		}
		return recordTurnChange(ctx, q, row, false)
	})
	if err != nil {
		return Turn{}, err
	}
	return turnFromRow(row), nil
}
