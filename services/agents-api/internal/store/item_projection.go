package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func projectItemSource(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, kind string, sequence int64, raw json.RawMessage, created pgtype.Timestamptz) error {
	updates, err := items.Project(uuid.UUID(turn.Bytes).String(), kind, sequence, raw)
	if err != nil {
		return fmt.Errorf("project execution item: %w", err)
	}
	for _, update := range updates {
		id, _ := parseID(update.Item.ID)
		if update.LegacyFinal {
			native, err := q.HasNativeMessageItem(ctx, sqlc.HasNativeMessageItemParams{TurnID: turn, ID: id})
			if err != nil {
				return err
			}
			if native {
				continue
			}
		}
		old, err := q.GetSessionItem(ctx, sqlc.GetSessionItemParams{SessionID: session, ID: id})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var previous v1.Item
		if err == nil {
			if err = json.Unmarshal(old.Payload, &previous); err != nil {
				return err
			}
		}
		item := items.Merge(update, previous)
		payload, err := json.Marshal(item)
		if err != nil {
			return err
		}
		stored, err := q.PutSessionItem(ctx, sqlc.PutSessionItemParams{ID: id, SessionID: session, TurnID: turn, CreatedAt: created, Payload: payload, IsOutput: kind != "message" && item.Type != "function_call_output"})
		if err != nil {
			return err
		}
		var delta *string
		if kind == "delta" {
			delta = update.Item.Content[0].Text
		}
		if err := recordItemChange(ctx, q, session, stored.OutputIndex, previous, item, delta); err != nil {
			return err
		}
	}
	return nil
}

func indexInput(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, sequence int64) error {
	row, err := q.ItemInputSource(ctx, sqlc.ItemInputSourceParams{SessionID: session, Sequence: sequence})
	if err != nil {
		return err
	}
	if row.Kind != "message" {
		return nil
	}
	return projectSource(ctx, q, session, row.TurnID, row.Kind, row.Sequence, row.Payload, row.CreatedAt)
}

func indexEvents(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, first int32) error {
	rows, err := q.ItemEventSources(ctx, sqlc.ItemEventSourcesParams{SessionID: session, TurnID: turn, Ordinal: first})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err = projectSource(ctx, q, session, turn, row.Kind, int64(row.Ordinal), row.Payload, row.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// Only pre-migration Turns need replay; new writes maintain the projection atomically.
func ensureSessionItems(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	ctx = context.WithValue(ctx, suppressSessionEvents{}, true)
	for {
		turn, err := q.UnindexedItemTurn(ctx, session)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p := sqlc.ItemSourcesParams{SessionID: session, TurnID: turn.ID}
		for {
			rows, err := q.ItemSources(ctx, p)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				if err = projectSource(ctx, q, session, turn.ID, row.Kind, row.SourceOrder, row.Payload, row.CreatedAt); err != nil {
					return err
				}
				p.AfterCreated = row.CreatedAt
				p.AfterType = row.SourceType
				p.AfterOrder = row.SourceOrder
			}
		}
		created := turn.CompletedAt
		if !created.Valid {
			created = turn.CreatedAt
		}
		if err = projectSource(ctx, q, session, turn.ID, "execution_"+turn.Status, 0, turn.Outcome, created); err != nil {
			return err
		}
		if err = q.MarkItemsIndexed(ctx, sqlc.MarkItemsIndexedParams{SessionID: session, ID: turn.ID}); err != nil {
			return err
		}
	}
}
