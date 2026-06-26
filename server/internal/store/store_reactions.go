package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// Reaction state is persisted on the inbound user message's metadata
// under the gateway_reaction.{reaction_id,app_id,emoji_type,added_at}
// subtree.
//
// Every step is best-effort UX. Any failure surfaces as a warn log on
// the caller; we never block the inbound ack or outbound delivered
// state on the reaction round-trip.

// RecordFeishuInboundReactionInput drives RecordFeishuInboundReaction.
// MessageID is the local inbound message UUID, not the Feishu
// external_message_id.
type RecordFeishuInboundReactionInput struct {
	MessageID  string
	ReactionID string
	AppID      string
	EmojiType  string
}

// RecordFeishuInboundReaction stamps the (reaction_id, app_id,
// emoji_type, added_at) tuple onto the inbound user message's
// metadata.gateway_reaction subtree. Idempotent.
//
// Returns nil when the row doesn't exist — the missing reaction is a
// UX miss not a data integrity issue.
func (s *Store) RecordFeishuInboundReaction(ctx context.Context, input RecordFeishuInboundReactionInput) error {
	messageID, err := uuid(input.MessageID)
	if err != nil {
		return err
	}
	reactionID := strings.TrimSpace(input.ReactionID)
	if reactionID == "" {
		return errors.New("reaction_id required")
	}
	return sqlc.New(s.db).RecordFeishuInboundReaction(ctx, sqlc.RecordFeishuInboundReactionParams{
		ReactionID: reactionID,
		AppID:      strings.TrimSpace(input.AppID),
		EmojiType:  strings.TrimSpace(input.EmojiType),
		Now:        timestamptz(time.Now().UTC()),
		MessageID:  messageID,
	})
}

// FeishuInboundReactionRow is what FindFeishuInboundReactionByExternalID
// resolves. ReactionID is empty when no reaction was ever recorded;
// caller treats that as "nothing to undo, skip the DELETE".
type FeishuInboundReactionRow struct {
	MessageID         string
	WorkspaceID       string
	ExternalMessageID string
	ReactionID        string
	AppID             string
}

// FindLatestFeishuInboundReactionByConversation resolves the most
// recent reaction-bearing inbound user message in a Feishu conversation.
//
// Returns ErrUnknownMessage when no reaction-bearing inbound exists in
// the conversation — caller treats as "nothing to undo".
func (s *Store) FindLatestFeishuInboundReactionByConversation(ctx context.Context, conversationID string) (FeishuInboundReactionRow, error) {
	conv, err := uuid(conversationID)
	if err != nil {
		return FeishuInboundReactionRow{}, err
	}
	row, err := sqlc.New(s.db).FindLatestFeishuInboundReactionByConversation(ctx, conv)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeishuInboundReactionRow{}, ErrUnknownMessage
		}
		return FeishuInboundReactionRow{}, err
	}
	return FeishuInboundReactionRow{
		MessageID:         row.MessageID,
		WorkspaceID:       row.WorkspaceID,
		ExternalMessageID: row.ExternalMessageID,
		ReactionID:        row.ReactionID,
		AppID:             row.AppID,
	}, nil
}

// FindFeishuInboundReactionByExternalID resolves an inbound Feishu user
// message by external_message_id and pulls out the reaction bookkeeping.
//
// Returns ErrUnknownMessage when no message matches. Empty ReactionID
// is returned as a successful row; caller short-circuits the DELETE.
func (s *Store) FindFeishuInboundReactionByExternalID(ctx context.Context, externalMessageID string) (FeishuInboundReactionRow, error) {
	externalMessageID = strings.TrimSpace(externalMessageID)
	if externalMessageID == "" {
		return FeishuInboundReactionRow{}, errors.New("external_message_id required")
	}
	row, err := sqlc.New(s.db).FindFeishuInboundReactionByExternalID(ctx, externalMessageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeishuInboundReactionRow{}, ErrUnknownMessage
		}
		return FeishuInboundReactionRow{}, err
	}
	return FeishuInboundReactionRow{
		MessageID:   row.MessageID,
		WorkspaceID: row.WorkspaceID,
		ReactionID:  row.ReactionID,
		AppID:       row.AppID,
	}, nil
}

// FindFeishuInboundReactionByAgentRun resolves the reaction-bearing
// inbound user message that triggered a specific agent_run, by joining
// agent_runs.trigger_message_id → messages.
//
// Why this exists alongside FindLatestFeishuInboundReactionByConversation:
// the "latest" lookup races when the user fires off another inbound
// while the previous run is still finishing — the terminal would undo
// the new inbound's typing reaction and leave the old one stuck.
//
// Returns ErrUnknownMessage when there's no precise undo target; caller
// falls back to the conversation-level lookup.
func (s *Store) FindFeishuInboundReactionByAgentRun(ctx context.Context, agentRunID string) (FeishuInboundReactionRow, error) {
	runUUID, err := uuid(agentRunID)
	if err != nil {
		return FeishuInboundReactionRow{}, err
	}
	var row FeishuInboundReactionRow
	queryErr := s.db.QueryRow(ctx, `
select
  m.id::text                                                          as message_id,
  m.workspace_id::text                                                as workspace_id,
  coalesce(m.metadata->>'external_message_id', '')::text              as external_message_id,
  coalesce(m.metadata->'gateway_reaction'->>'reaction_id', '')::text  as reaction_id,
  coalesce(m.metadata->'gateway_reaction'->>'app_id', '')::text       as app_id
from agent_runs r
join messages m on m.id = r.trigger_message_id
where r.id = $1::uuid
  and m.metadata->>'gateway' = 'feishu'
  and m.sender_type in ('user', 'external')
  and m.metadata ? 'gateway_reaction'
  and m.deleted_at is null
limit 1`, runUUID).Scan(&row.MessageID, &row.WorkspaceID, &row.ExternalMessageID, &row.ReactionID, &row.AppID)
	if queryErr != nil {
		if errors.Is(queryErr, pgx.ErrNoRows) {
			return FeishuInboundReactionRow{}, ErrUnknownMessage
		}
		return FeishuInboundReactionRow{}, queryErr
	}
	return row, nil
}

// ClearFeishuInboundReaction drops the gateway_reaction subtree on an
// inbound message after the DELETE Feishu call has been attempted.
// Idempotent: jsonb #- on a missing key is a no-op.
func (s *Store) ClearFeishuInboundReaction(ctx context.Context, messageID string) error {
	id, err := uuid(messageID)
	if err != nil {
		return err
	}
	return sqlc.New(s.db).ClearFeishuInboundReaction(ctx, sqlc.ClearFeishuInboundReactionParams{
		Now:       timestamptz(time.Now().UTC()),
		MessageID: id,
	})
}
