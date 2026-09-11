package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type TurnPage struct {
	Turns      []Turn
	NextCursor string
}

func (s *Store) ListTurns(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (TurnPage, error) {
	if limit < 1 || limit > 100 {
		return TurnPage{}, fmt.Errorf("%w: page size must be 1..100", ErrInvalidInput)
	}
	if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
		return TurnPage{}, err
	}
	tenant, _ := parseID(tenantID)
	session, _ := parseID(sessionID)
	params := sqlc.ListTurnsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		after, err := s.GetTurn(ctx, tenantID, sessionID, cursor)
		if err != nil {
			return TurnPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListTurns(ctx, params)
	if err != nil {
		return TurnPage{}, fmt.Errorf("list turns: %w", err)
	}
	page := TurnPage{Turns: make([]Turn, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Turns = append(page.Turns, turnFromRow(row))
	}
	return page, nil
}
