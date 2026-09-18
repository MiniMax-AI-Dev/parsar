package store

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

func settleTurnArtifacts(ctx context.Context, q *sqlc.Queries, turn sqlc.Turn) error {
	if turn.Status == TurnCompleted {
		return q.PublishTurnArtifacts(ctx, sqlc.PublishTurnArtifactsParams{SessionID: turn.SessionID, TurnID: turn.ID, CreatedAt: turn.CompletedAt})
	}
	return q.DeleteUnpublishedTurnArtifacts(ctx, sqlc.DeleteUnpublishedTurnArtifactsParams{SessionID: turn.SessionID, TurnID: turn.ID})
}
