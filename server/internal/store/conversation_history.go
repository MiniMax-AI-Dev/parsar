package store

import (
	"context"
	"slices"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// ConversationHistoryMessage is one human/agent chat turn, trimmed to the
// fields a prompt-side transcript needs.
type ConversationHistoryMessage struct {
	ID         string    `json:"id"`
	SenderType string    `json:"sender_type"`
	SenderID   string    `json:"sender_id"`
	SenderName string    `json:"sender_name"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListRecentConversationHistory returns the newest `limit` chat turns of a
// conversation in oldest-first order. The query selects newest-first so a
// long conversation only reads the tail; the slice is reversed here because
// every consumer renders the transcript in reading order.
func (s *Store) ListRecentConversationHistory(ctx context.Context, conversationID string, limit int32) ([]ConversationHistoryMessage, error) {
	if limit <= 0 {
		return nil, nil
	}
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListRecentConversationMessages(ctx, sqlc.ListRecentConversationMessagesParams{
		ConversationID: conversationUUID,
		ItemLimit:      limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConversationHistoryMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, ConversationHistoryMessage{
			ID:         row.MID,
			SenderType: row.SenderType,
			SenderID:   row.MSenderID,
			SenderName: row.SenderName,
			Content:    row.Content,
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	slices.Reverse(out)
	return out, nil
}
