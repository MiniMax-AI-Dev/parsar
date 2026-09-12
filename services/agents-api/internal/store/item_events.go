package store

import (
	"context"
	"reflect"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func recordItemChange(ctx context.Context, q *sqlc.Queries, session pgtype.UUID, index pgtype.Int4, previous, item v1.Item, delta *string) error {
	if reflect.DeepEqual(previous, item) || ctx.Value(suppressSessionEvents{}) != nil {
		return nil
	}
	// Function results are Session inputs, not AgentOutputItem variants.
	if item.Type == "function_call_output" {
		index.Valid = false
	}
	base := v1.SessionEvent{TurnID: item.TurnID}
	if index.Valid {
		base.OutputIndex = &index.Int32
	}
	emit := func(kind string, event v1.SessionEvent) error {
		event.Type = "agent.session.turn." + kind
		return recordSessionChange(ctx, q, session, SessionChange{Event: event})
	}
	textMessage := item.Type == "message" && item.Role == "assistant" && len(item.Content) == 1 && item.Content[0].Text != nil
	if previous.ID == "" {
		initial := item
		if textMessage && delta != nil {
			empty := ""
			initial.Content = []v1.ItemContent{{Type: "output_text", Text: &empty}}
		}
		event := base
		event.Item = &initial
		if err := emit("item.added", event); err != nil {
			return err
		}
		if textMessage {
			zero := 0
			event = base
			event.ItemID, event.ContentIndex, event.Part = item.ID, &zero, &initial.Content[0]
			if err := emit("content_part.added", event); err != nil {
				return err
			}
		}
	}
	if !index.Valid {
		return nil
	}
	if textMessage {
		zero := 0
		event := base
		event.ItemID, event.ContentIndex = item.ID, &zero
		if delta != nil && *delta != "" {
			event.Delta = delta
			if err := emit("output_text.delta", event); err != nil {
				return err
			}
			event.Delta = nil
		}
		if item.Status != "in_progress" {
			event.Text = item.Content[0].Text
			if err := emit("output_text.done", event); err != nil {
				return err
			}
			event.Text, event.Part = nil, &item.Content[0]
			if err := emit("content_part.done", event); err != nil {
				return err
			}
		}
	}
	if item.Status != "in_progress" {
		event := base
		event.Item = &item
		return emit("item.done", event)
	}
	return nil
}
