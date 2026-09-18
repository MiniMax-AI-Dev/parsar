package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// BeginTurnArtifactCapture separates native completion from bounded output publication.
// Later messages use the existing reservation path instead of the finished executor.
func (s *Store) BeginTurnArtifactCapture(ctx context.Context, tenantID, sessionID, turnID string, appliedThrough int64) error {
	lookup, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	if appliedThrough < 0 {
		return ErrInvalidInput
	}
	return s.withSession(ctx, tenantID, sessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		turn, err := q.GetTurn(ctx, lookup)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if turn.Status != TurnInProgress || turn.CancelRequestedAt.Valid {
			return ErrTurnConflict
		}
		pending, err := q.HasUnappliedMessages(ctx, sqlc.HasUnappliedMessagesParams{SessionID: session, TurnID: lookup.ID, Sequence: appliedThrough})
		if err != nil {
			return err
		}
		if pending {
			return ErrUnappliedInputs
		}
		count, err := q.BeginTurnArtifactCapture(ctx, sqlc.BeginTurnArtifactCaptureParams{SessionID: session, ID: lookup.ID})
		if err == nil && count != 1 {
			return ErrTurnConflict
		}
		return err
	})
}

func settleTurnArtifacts(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) error {
	if turn.Status == TurnCompleted {
		return q.PublishTurnArtifacts(ctx, sqlc.PublishTurnArtifactsParams{SessionID: turn.SessionID, TurnID: turn.ID, CreatedAt: turn.CompletedAt})
	}
	return q.DeleteUnpublishedTurnArtifacts(ctx, sqlc.DeleteUnpublishedTurnArtifactsParams{SessionID: turn.SessionID, TurnID: turn.ID})
}
