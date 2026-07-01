package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

const CapabilityCredentialOwnerNoticeKind = "capability_credential_owner_notice"
const RuntimeErrorMessageKind = "runtime_error"

// SandboxOfflineNoticeKind tags conversation-level system messages
// the agent_daemon connector inserts when a sandbox-mode prompt finds
// the cached device offline. The platform deliberately does NOT
// auto-replace sandboxes — they may carry user-installed config /
// Claude session state the user does not want silently discarded.
const SandboxOfflineNoticeKind = "sandbox_offline_notice"

// CreateSandboxOfflineNoticeInput is the narrow surface for inserting
// the per-conversation sandbox-offline system message.
type CreateSandboxOfflineNoticeInput struct {
	WorkspaceID    string
	AgentID        string
	RunID          string
	ConversationID string
	DeviceID       string
	Content        string
}

type CreateCapabilityCredentialOwnerNoticeInput struct {
	ConversationID string
	UserID         string
}

type CreateRuntimeErrorSystemMessageInput struct {
	WorkspaceID      string
	AgentID          string
	RunID            string
	ConversationID   string
	SubKind          string
	CapabilityID     string
	CapabilityName   string
	CredentialKind   string
	UserCredentialID string
}

func (s *Store) CreateCapabilityCredentialOwnerNoticeOnce(ctx context.Context, input CreateCapabilityCredentialOwnerNoticeInput) (bool, error) {
	conversation, err := s.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return false, err
	}
	userUUID, err := uuid(input.UserID)
	if err != nil {
		return false, err
	}
	displayName, err := sqlc.New(s.db).GetUserDisplayName(ctx, userUUID)
	if err != nil {
		return false, err
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = input.UserID
	}
	metadata, err := json.Marshal(map[string]any{
		"kind":                CapabilityCredentialOwnerNoticeKind,
		"role":                "system",
		"credential_owner_id": input.UserID,
		"credential_owner":    displayName,
	})
	if err != nil {
		return false, fmt.Errorf("capability credential notice: marshal metadata: %w", err)
	}
	now := time.Now().UTC()
	content := fmt.Sprintf("This Agent is currently using @%s's credentials for external calls. Other members' requests in the same conversation will use these credentials too.", displayName)
	rows, err := sqlc.New(s.db).CreateSystemMessageOnce(ctx, sqlc.CreateSystemMessageOnceParams{
		ID:             mustUUID(newID()),
		WorkspaceID:    mustUUID(conversation.WorkspaceID),
		ConversationID: mustUUID(conversation.ID),
		Visibility:     "workspace",
		Content:        content,
		Metadata:       metadata,
		Now:            timestamptz(now),
		Kind:           CapabilityCredentialOwnerNoticeKind,
	})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (s *Store) CreateRuntimeErrorSystemMessage(ctx context.Context, input CreateRuntimeErrorSystemMessageInput) (string, error) {
	conversation, err := s.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return "", err
	}
	messageID := newID()
	metadata, err := json.Marshal(map[string]any{
		"kind":                     RuntimeErrorMessageKind,
		"sub_kind":                 strings.TrimSpace(input.SubKind),
		"role":                     "system",
		"workspace_id":             nonEmpty(input.WorkspaceID, conversation.WorkspaceID),
		"agent_id":                 strings.TrimSpace(input.AgentID),
		"run_id":                   strings.TrimSpace(input.RunID),
		"conversation_id":          conversation.ID,
		"capability_id":      strings.TrimSpace(input.CapabilityID),
		"capability_name":    strings.TrimSpace(input.CapabilityName),
		"credential_kind":    strings.TrimSpace(input.CredentialKind),
		"user_credential_id": strings.TrimSpace(input.UserCredentialID),
	})
	if err != nil {
		return "", fmt.Errorf("runtime error system message: marshal metadata: %w", err)
	}
	content := strings.TrimSpace(input.SubKind)
	if content == "" {
		content = RuntimeErrorMessageKind
	}
	if err := sqlc.New(s.db).CreateRuntimeErrorSystemMessage(ctx, sqlc.CreateRuntimeErrorSystemMessageParams{
		ID:             mustUUID(messageID),
		WorkspaceID:    mustUUID(conversation.WorkspaceID),
		ConversationID: mustUUID(conversation.ID),
		Visibility:     "workspace",
		Content:        content,
		Metadata:       metadata,
		Now:            timestamptz(time.Now().UTC()),
	}); err != nil {
		return "", err
	}
	return messageID, nil
}

// CreateSandboxOfflineNotice inserts a per-conversation system message
// warning the user their sandbox went offline. The platform does NOT
// auto-acquire a fresh sandbox — the existing one may carry user
// state the user does not want silently discarded; recovery is an
// explicit "delete and recreate the Agent" in the web UI.
func (s *Store) CreateSandboxOfflineNotice(ctx context.Context, input CreateSandboxOfflineNoticeInput) (string, error) {
	conversation, err := s.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return "", err
	}
	messageID := newID()
	metadata, err := json.Marshal(map[string]any{
		"kind":            SandboxOfflineNoticeKind,
		"role":            "system",
		"workspace_id":    nonEmpty(input.WorkspaceID, conversation.WorkspaceID),
		"agent_id":        strings.TrimSpace(input.AgentID),
		"run_id":          strings.TrimSpace(input.RunID),
		"conversation_id": conversation.ID,
		"device_id":       strings.TrimSpace(input.DeviceID),
	})
	if err != nil {
		return "", fmt.Errorf("sandbox offline notice: marshal metadata: %w", err)
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		content = "⚠️ Sandbox is unavailable — likely reclaimed after idle timeout or a network issue. " +
			"Wait a few minutes for it to recover, or delete and recreate the Agent in its settings to reset immediately."
	}
	// Dedicated sqlc query — does NOT reuse runtime_error's INSERT
	// because that one hard-codes kind='error' + folds
	// metadata.error.source='runtime' into the row, which would make
	// downstream renderers treat this as an error frame.
	if err := sqlc.New(s.db).CreateSandboxOfflineNotice(ctx, sqlc.CreateSandboxOfflineNoticeParams{
		ID:             mustUUID(messageID),
		WorkspaceID:    mustUUID(conversation.WorkspaceID),
		ConversationID: mustUUID(conversation.ID),
		Visibility:     "workspace",
		Content:        content,
		Metadata:       metadata,
		Now:            timestamptz(time.Now().UTC()),
	}); err != nil {
		return "", err
	}
	return messageID, nil
}

// CapabilityCredentialMissingNotice is one row from the
// capability_credential_missing system-message stream, scoped to a
// specific run. The Feishu outbound driver folds these into the
// credential-form card it renders in place of the regular DoneCard.
type CapabilityCredentialMissingNotice struct {
	MessageID      string
	CapabilityID   string
	CapabilityName string
	CredentialKind string
	CreatedAt      time.Time
}

// ListCapabilityCredentialMissingForRun returns every credential-missing
// notice emitted for the given run, in created order. The outbound
// driver de-duplicates by (kind, capability) at render time.
func (s *Store) ListCapabilityCredentialMissingForRun(ctx context.Context, conversationID, runID string) ([]CapabilityCredentialMissingNotice, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return nil, fmt.Errorf("conversation_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListCapabilityCredentialMissingForRun(ctx, sqlc.ListCapabilityCredentialMissingForRunParams{
		ConversationID: convUUID,
		RunID:          []byte(strings.TrimSpace(runID)),
	})
	if err != nil {
		return nil, fmt.Errorf("list capability_credential_missing: %w", err)
	}
	out := make([]CapabilityCredentialMissingNotice, 0, len(rows))
	for _, row := range rows {
		out = append(out, CapabilityCredentialMissingNotice{
			MessageID:      row.MessageID,
			CapabilityID:   asString(row.CapabilityID),
			CapabilityName: asString(row.CapabilityName),
			CredentialKind: asString(row.CredentialKind),
			CreatedAt:      pgTime(row.CreatedAt),
		})
	}
	return out, nil
}

// asString safely flattens a sqlc-typed jsonb extraction into a plain
// string. Anything non-string returns "".
func asString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return ""
	}
}

// InboundUserMessageForRun is the most recent inbound user query in the
// conversation that the given run is responding to. Used to stash the
// raw_query + chat-side identifiers so a credential-form submit
// handler can re-enqueue without re-fetching from the Feishu API.
//
// ReenqueuedFrom is the inbound metadata flag set by the
// credential-form submit handler. When non-empty the form-card path
// recognises "we already retried this turn" and falls through to the
// regular terminal card to avoid an infinite form ↔ submit loop on
// mistyped credentials.
//
// SenderOpenID is the raw Feishu open_id of the user who sent this
// message — the only sender identifier Feishu callbacks carry. Empty
// for legacy rows / non-Feishu inbounds; submit handlers treat empty
// as a hard authz failure.
type InboundUserMessageForRun struct {
	MessageID         string
	RawQuery          string
	TargetAgentID     string
	ExternalChatID    string
	ExternalThreadID  string
	ExternalMessageID string
	SenderOpenID      string
	ReenqueuedFrom    string
	SenderUserID      string
}

// GetInboundUserMessageForRun fetches the inbound message lineage for
// the run. Keyed on agent_runs.trigger_message_id rather than
// "most recent user message <= run_started_at": a fresh user message
// typed between the credential-form submit and the daemon's run-status
// flip would satisfy the older predicate and bypass the anti-rerun
// loop guard.
func (s *Store) GetInboundUserMessageForRun(ctx context.Context, conversationID, runID string) (InboundUserMessageForRun, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return InboundUserMessageForRun{}, fmt.Errorf("conversation_id: %w", err)
	}
	runUUID, err := uuid(runID)
	if err != nil {
		return InboundUserMessageForRun{}, fmt.Errorf("run_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetInboundUserMessageForRun(ctx, sqlc.GetInboundUserMessageForRunParams{
		ConversationID: convUUID,
		RunID:          runUUID,
	})
	if err != nil {
		return InboundUserMessageForRun{}, fmt.Errorf("get inbound user message: %w", err)
	}
	return InboundUserMessageForRun{
		MessageID:         row.MessageID,
		RawQuery:          row.RawQuery,
		TargetAgentID:     asString(row.TargetAgentID),
		ExternalChatID:    asString(row.ExternalChatID),
		ExternalThreadID:  asString(row.ExternalThreadID),
		ExternalMessageID: asString(row.ExternalMessageID),
		SenderOpenID:      asString(row.SenderOpenID),
		ReenqueuedFrom:    asString(row.ReenqueuedFrom),
		SenderUserID:      asString(row.SenderID),
	}, nil
}

// GetGuestReplyHintForRun returns the "go register" hint stamped on
// the inbound that triggered the run, or "" when the trigger inbound
// has none. Guests (visibility=public + unregistered sender) land as
// sender_type='external' and would be filtered out by
// GetInboundUserMessageForRun's 'user'-only predicate, so the Feishu
// terminal-card path uses this lighter read to surface the hint
// without disturbing the credential-form lineage logic.
func (s *Store) GetGuestReplyHintForRun(ctx context.Context, conversationID, runID string) (string, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return "", fmt.Errorf("conversation_id: %w", err)
	}
	runUUID, err := uuid(runID)
	if err != nil {
		return "", fmt.Errorf("run_id: %w", err)
	}
	hint, err := sqlc.New(s.db).GetGuestReplyHintForRun(ctx, sqlc.GetGuestReplyHintForRunParams{
		ConversationID: convUUID,
		RunID:          runUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get guest reply hint: %w", err)
	}
	return hint, nil
}

func nonEmpty(value string, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}

// SendSystemNoticeMessageInput drives the dead-letter / informational
// system message the inflight driver writes into a conversation when
// upstream delivery is permanently dropped (after retry budget
// exhausted).
type SendSystemNoticeMessageInput struct {
	ConversationID string
	WorkspaceID    string // tolerated empty — resolved from conversation
	Kind           string // dedup key under metadata.kind, e.g. "feishu_outbound_dead_letter"
	Content        string // user-visible text
	// SourceRunID is the agent_run_id that produced this notice. When
	// set it lets ops query `metadata->>'run_id'` to find all notices
	// tied to a single failed run.
	SourceRunID string
}

// SendSystemNoticeMessageResult reports what got written. Created
// distinguishes the "fresh insert" vs. "already there, deduped on
// metadata.kind" cases.
type SendSystemNoticeMessageResult struct {
	MessageID string
	Created   bool
}

// SendSystemNoticeMessage writes a sender_type='system', kind='system_event'
// row into messages. Idempotent on (conversation_id, metadata.kind):
// a second call with the same Kind is a no-op so a runaway driver
// loop can't spam the user. Callers that need a fresh notice per
// attempt should suffix Kind with a unique discriminator (e.g.
// include the run_id or attempt number).
func (s *Store) SendSystemNoticeMessage(ctx context.Context, input SendSystemNoticeMessageInput) (SendSystemNoticeMessageResult, error) {
	conv, err := s.GetConversation(ctx, input.ConversationID)
	if err != nil {
		return SendSystemNoticeMessageResult{}, err
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		return SendSystemNoticeMessageResult{}, fmt.Errorf("SendSystemNoticeMessage: Kind required")
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		content = kind
	}
	workspaceID := nonEmpty(input.WorkspaceID, conv.WorkspaceID)
	metadata, err := json.Marshal(map[string]any{
		"kind":         kind,
		"role":         "system",
		"workspace_id": workspaceID,
		"run_id":       strings.TrimSpace(input.SourceRunID),
	})
	if err != nil {
		return SendSystemNoticeMessageResult{}, fmt.Errorf("system notice: marshal metadata: %w", err)
	}
	messageID := newID()
	rows, err := sqlc.New(s.db).CreateSystemMessageOnce(ctx, sqlc.CreateSystemMessageOnceParams{
		ID:             mustUUID(messageID),
		WorkspaceID:    mustUUID(workspaceID),
		ConversationID: mustUUID(conv.ID),
		Visibility:     "workspace",
		Content:        content,
		Metadata:       metadata,
		Now:            timestamptz(time.Now().UTC()),
		Kind:           kind,
	})
	if err != nil {
		return SendSystemNoticeMessageResult{}, err
	}
	if rows == 0 {
		// Dedup hit — surface as soft success so callers don't
		// have to special-case it.
		return SendSystemNoticeMessageResult{Created: false}, nil
	}
	return SendSystemNoticeMessageResult{MessageID: messageID, Created: true}, nil
}
