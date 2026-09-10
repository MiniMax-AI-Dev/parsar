package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

type SendUserMessageToConversationInput struct {
	ConversationID    string
	UserID            string
	Content           string
	Source            string // Empty uses the existing web source; MCP callers use mcp.
	MentionedAgentIDs []string
}

type SendUserMessageToConversationResult struct {
	Message MessageRead
	RunIDs  []string
}

func (s *Store) SendUserMessageToConversation(ctx context.Context, input SendUserMessageToConversationInput) (SendUserMessageToConversationResult, error) {
	var result SendUserMessageToConversationResult
	now := time.Now().UTC()
	source := input.Source
	if source == "" {
		source = "web"
	}
	if source != "web" && source != "mcp" {
		return result, ErrInvalidInput
	}
	conversationID, err := uuid(input.ConversationID)
	if err != nil {
		return result, err
	}
	userID, err := uuid(input.UserID)
	if err != nil {
		return result, err
	}
	content := strings.TrimSpace(input.Content)
	if content == "" || utf8.RuneCountInString(content) > 32000 {
		return result, ErrInvalidInput
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	conversation, err := queries.GetConversation(ctx, conversationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, fmt.Errorf("%w: %s", ErrUnknownConversation, input.ConversationID)
		}
		return result, err
	}

	mentionNames := userMessageMentionNames(content)
	if len(input.MentionedAgentIDs) > 0 {
		mentionNames = nil
	}
	mentionedAgents := make([]mentionedAgent, 0, len(input.MentionedAgentIDs)+len(mentionNames)+1)
	seenAgents := map[string]struct{}{}
	for _, mention := range mentionNames {
		agent, err := queries.GetActiveMentionedAgent(ctx, sqlc.GetActiveMentionedAgentParams{WorkspaceID: mustUUID(conversation.WorkspaceID), MentionName: strings.TrimPrefix(mention, "@")})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return result, err
		}
		if _, ok := seenAgents[agent.AgentID]; ok {
			continue
		}
		seenAgents[agent.AgentID] = struct{}{}
		mentionedAgents = append(mentionedAgents, mentionedAgent{agentID: agent.AgentID, name: agent.Name, slug: agent.Slug, connectorType: agent.ConnectorType})
	}
	for _, mentionedID := range input.MentionedAgentIDs {
		trimmedID := strings.TrimSpace(mentionedID)
		agentUUID, err := uuid(trimmedID)
		if err != nil {
			return result, fmt.Errorf("%w: %s", ErrUnknownMention, mentionedID)
		}
		row := tx.QueryRow(ctx, `select a.id::text, a.name, a.slug, a.connector_type from agents a where a.id = $1 and a.workspace_id = $2 and a.status = 'active' and a.deleted_at is null`, agentUUID, mustUUID(conversation.WorkspaceID))
		var agent mentionedAgent
		if err := row.Scan(&agent.agentID, &agent.name, &agent.slug, &agent.connectorType); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result, fmt.Errorf("%w: %s", ErrUnknownMention, mentionedID)
			}
			return result, err
		}
		if _, ok := seenAgents[agent.agentID]; ok {
			continue
		}
		seenAgents[agent.agentID] = struct{}{}
		mentionedAgents = append(mentionedAgents, agent)
	}
	// Only an actual agent mention suppresses the 1v1 fallback. The mention parser
	// excludes @ fragments embedded in email addresses and SSH repository URLs.
	implicitPrimary := ""
	if len(mentionNames) == 0 && len(input.MentionedAgentIDs) == 0 {
		implicitPrimary = strings.TrimSpace(conversation.PrimaryAgentID)
	}
	// The implicit primary must be active; otherwise silently drop to "no run dispatched"
	// so the user message still lands and the UI shows the bound-agent-disabled state.
	if implicitPrimary != "" {
		agentUUID, err := uuid(implicitPrimary)
		if err == nil {
			row := tx.QueryRow(ctx, `select a.id::text, a.name, a.slug, a.connector_type from agents a where a.id = $1 and a.workspace_id = $2 and a.status = 'active' and a.deleted_at is null`, agentUUID, mustUUID(conversation.WorkspaceID))
			var agent mentionedAgent
			if scanErr := row.Scan(&agent.agentID, &agent.name, &agent.slug, &agent.connectorType); scanErr == nil {
				if _, ok := seenAgents[agent.agentID]; !ok {
					seenAgents[agent.agentID] = struct{}{}
					mentionedAgents = append(mentionedAgents, agent)
				}
			} else if !errors.Is(scanErr, pgx.ErrNoRows) {
				return result, scanErr
			}
		}
	}
	metadataMap := map[string]any{"source": source}
	metadata, err := json.Marshal(metadataMap)
	if err != nil {
		return result, err
	}
	messageID := newID()
	messageUUID := mustUUID(messageID)
	if err := queries.CreateMessage(ctx, sqlc.CreateMessageParams{ID: messageUUID, WorkspaceID: mustUUID(conversation.WorkspaceID), ConversationID: mustUUID(conversation.ID), SenderType: "user", SenderID: userID, Content: content, Metadata: metadata, Now: timestamptz(now)}); err != nil {
		return result, err
	}
	result.Message = MessageRead{ID: messageID, WorkspaceID: conversation.WorkspaceID, ConversationID: conversation.ID, SenderType: "user", SenderID: input.UserID, Kind: "message", ContentFormat: "text", Content: content, Metadata: metadataMap, CreatedAt: now}

	pendingAudit := []audit.Event{{OccurredAt: now, Source: audit.SourceRuntime, EventType: auditUserMessageSent, ActorType: audit.ActorTypeUser, ActorID: input.UserID, TargetType: "message", TargetID: messageID, WorkspaceID: conversation.WorkspaceID, Payload: map[string]any{"conversation_id": conversation.ID, "mentioned_count": len(mentionedAgents)}}}
	var pendingStreaming []StreamingDispatchInput
	for _, agent := range mentionedAgents {
		runID := newID()
		runMetadata, err := json.Marshal(map[string]any{"source": source, "mention": "@" + agent.name})
		if err != nil {
			return result, err
		}
		if err := queries.CreateAgentRun(ctx, sqlc.CreateAgentRunParams{ID: mustUUID(runID), WorkspaceID: mustUUID(conversation.WorkspaceID), ConversationID: mustUUID(conversation.ID), TriggerMessageID: messageUUID, TriggerChannel: source, RequestedByID: userID, AgentID: mustUUID(agent.agentID), ConnectorType: agent.connectorType, Metadata: runMetadata, Now: timestamptz(now)}); err != nil {
			return result, err
		}
		result.RunIDs = append(result.RunIDs, runID)
		pendingAudit = append(pendingAudit, audit.Event{OccurredAt: now, Source: audit.SourceRuntime, EventType: auditAgentRunCreated, ActorType: audit.ActorTypeUser, ActorID: input.UserID, TargetType: "agent_run", TargetID: runID, WorkspaceID: conversation.WorkspaceID, Payload: map[string]any{"source": source, "trigger_message_id": messageID, "agent_id": agent.agentID}})
		// agent_daemon needs StreamingDispatcher to flip queued → running and
		// push the prompt; otherwise the run sits at queued forever.
		switch {
		case connectorNeedsStreamingDispatch(agent.connectorType):
			pendingStreaming = append(pendingStreaming, StreamingDispatchInput{RunID: runID, ConversationID: conversation.ID, ConnectorType: agent.connectorType})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	s.dispatchPendingStreaming(ctx, pendingStreaming)
	for _, ev := range pendingAudit {
		s.emitAuditEvent(ev)
	}
	return result, nil
}
