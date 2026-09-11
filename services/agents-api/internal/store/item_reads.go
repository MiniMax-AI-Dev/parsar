package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ItemPage struct {
	Items   []v1.Item
	HasMore bool
}

func (s *Store) ListItems(ctx context.Context, tenantID, sessionID, cursor string, limit int, ascending bool) (ItemPage, error) {
	if limit < 1 || limit > 100 {
		return ItemPage{}, ErrInvalidInput
	}
	page := ItemPage{Items: make([]v1.Item, 0, limit)}
	err := s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		p := sqlc.ListSessionItemsParams{SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
		if cursor != "" {
			id, err := parseID(cursor)
			if err != nil {
				return err
			}
			row, err := q.GetSessionItem(ctx, sqlc.GetSessionItemParams{SessionID: session, ID: id})
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return err
			}
			p.AfterCreated = row.CreatedAt
			p.AfterID = id
			p.AfterPosition = row.Position
		}
		rows, err := q.ListSessionItems(ctx, p)
		if err != nil {
			return err
		}
		page.HasMore = len(rows) > limit
		if page.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			var item v1.Item
			if err = json.Unmarshal(row.Payload, &item); err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return nil
	})
	return page, err
}
