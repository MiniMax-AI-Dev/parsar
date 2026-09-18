package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DeleteSession removes public access while retaining state needed to settle execution.
func (s *Store) DeleteSession(ctx context.Context, tenantID, sessionID string) error {
	return s.withPublicSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		if err := cancelSessionWork(ctx, q, session); err != nil {
			return err
		}
		if err := q.DeleteSessionArtifacts(ctx, session); err != nil {
			return err
		}
		return q.MarkSessionDeleted(ctx, session)
	})
}

func cancelSessionWork(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	turn, err := q.GetActiveTurn(ctx, session)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		if err := requestTurnCancel(ctx, q, session, turn); err != nil {
			return err
		}
	}
	if err := q.CancelSessionEnvironmentInput(ctx, session); err != nil {
		return err
	}
	return nil
}

func requestTurnCancel(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, turn sqlc.Turn) error {
	if err := q.RequestTurnCancel(ctx, sqlc.RequestTurnCancelParams{ID: turn.ID, SessionID: session}); err != nil {
		return err
	}
	if turn.Status == TurnQueued {
		cancelled, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		if err := recordTurnChange(ctx, q, cancelled, false); err != nil {
			return err
		}
	} else if turn.Status == TurnWaiting && !turn.CancelRequestedAt.Valid {
		cancelling, err := q.SessionEventTurn(ctx, sqlc.SessionEventTurnParams{SessionID: session, ID: turn.ID})
		if err != nil {
			return err
		}
		if err := recordSessionActivity(ctx, q, cancelling, nil); err != nil {
			return err
		}
	}
	return nil
}
