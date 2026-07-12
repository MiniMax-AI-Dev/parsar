package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// Wrappers around the inflight-card jsonb slots, keyed at
// conversations.metadata.gateway_inflight.{slot}. Gateway-IM-agnostic
// — feishuoutbound is the only caller today; other IMs reuse as-is.

// ErrConversationInflightConflict signals that an optimistic-lock
// guard rejected an upsert because another writer raced ahead.
var ErrConversationInflightConflict = errors.New("conversation inflight slot conflict")

// InflightSlotKind enumerates the keys the inflight jsonb tree
// supports.
type InflightSlotKind string

const (
	InflightSlotWorking             InflightSlotKind = "working"
	InflightSlotPermission          InflightSlotKind = "permission"
	InflightSlotPromptForUserChoice InflightSlotKind = "prompt_for_user_choice"
)

// WorkingInflightSlot is the typed shape of
// conversations.metadata.gateway_inflight.working.
//
// Retry fields ride on the same slot rather than a side-car table so
// a single optimistic-lock window covers both "card state" and "retry
// state" — when two pods race the same tick, the pod that wins
// Upsert also wins the retry-counter bump.
type WorkingInflightSlot struct {
	ExternalMsgID    string         `json:"external_msg_id"`
	AppID            string         `json:"app_id"`
	ExternalChatID   string         `json:"external_chat_id"`
	ExternalThreadID string         `json:"external_thread_id,omitempty"`
	AgentRunID       string         `json:"agent_run_id"`
	SeqEmitted       int64          `json:"seq_emitted"`
	Payload          map[string]any `json:"payload,omitempty"`
	UpdatedAt        time.Time      `json:"updated_at"`

	// Attempts: 0 == fresh (no failure yet). Bumped on each upstream
	// error; reset to 0 on success or ClearSlot.
	Attempts int `json:"attempts,omitempty"`

	// LastError holds the most recent upstream error for
	// /dev/gateway/outbound diagnostics. Caller truncates.
	LastError string `json:"last_error,omitempty"`

	// NextRetryAt: claim CTE picks this up again after a transient
	// failure. Zero == ready now; future timestamp parks the row.
	// omitzero keeps the jsonb tree clean.
	NextRetryAt time.Time `json:"next_retry_at,omitzero"`
}

// PermissionInflightSlot is the typed shape of
// conversations.metadata.gateway_inflight.permission. Retry fields
// mirror WorkingInflightSlot.
type PermissionInflightSlot struct {
	ExternalMsgID    string `json:"external_msg_id"`
	AppID            string `json:"app_id"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id,omitempty"`
	AgentRunID       string `json:"agent_run_id"`
	// DeviceID is the agent_daemon device uuid that owns this run. The
	// card-callback path uses it to look up which pod currently holds
	// the device's WebSocket session, so SubmitPermission lands on the
	// right pod's in-memory byPerm map. Empty for legacy slots written
	// before this field existed — callers must tolerate "" (fall back
	// to a same-pod lookup and accept potential miss).
	DeviceID            string         `json:"device_id,omitempty"`
	PermissionRequestID string         `json:"permission_request_id"`
	Payload             map[string]any `json:"payload,omitempty"`
	UpdatedAt           time.Time      `json:"updated_at"`

	Attempts    int       `json:"attempts,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	NextRetryAt time.Time `json:"next_retry_at,omitzero"`
}

// PromptForUserChoiceOption is one selectable answer in the slot's
// snapshot of the AskUserQuestion call.
type PromptForUserChoiceOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PromptForUserChoiceQuestion mirrors proto.PromptForUserChoiceQuestion
// — kept here so server callers don't have to import the daemon proto
// package just to render a card.
type PromptForUserChoiceQuestion struct {
	Header      string                      `json:"header,omitempty"`
	Question    string                      `json:"question"`
	MultiSelect bool                        `json:"multi_select,omitempty"`
	Options     []PromptForUserChoiceOption `json:"options"`
}

// PromptForUserChoiceInflightSlot is the typed shape of
// conversations.metadata.gateway_inflight.prompt_for_user_choice.
// Mirrors PermissionInflightSlot: stores enough to render the card +
// to wire the user's pick back to the daemon (RequestID is the daemon-
// minted "ask_<8hex>").
//
// Questions is the canonical multi-question payload. The legacy single-
// question fields (Question / Header / MultiSelect / Options) remain
// for back-compat: decodePromptForUserChoiceSlot reads either shape,
// and EffectiveQuestions returns a unified view.
type PromptForUserChoiceInflightSlot struct {
	ExternalMsgID    string `json:"external_msg_id"`
	AppID            string `json:"app_id"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id,omitempty"`
	AgentRunID       string `json:"agent_run_id"`
	// DeviceID lets the card-callback path resolve the owning pod —
	// see PermissionInflightSlot.DeviceID for the rationale.
	DeviceID  string                        `json:"device_id,omitempty"`
	RequestID string                        `json:"request_id"`
	Questions []PromptForUserChoiceQuestion `json:"questions,omitempty"`

	// Legacy single-question fields, populated only when reading an old
	// slot. New writers leave them empty.
	Question    string                      `json:"question,omitempty"`
	Header      string                      `json:"header,omitempty"`
	MultiSelect bool                        `json:"multi_select,omitempty"`
	Options     []PromptForUserChoiceOption `json:"options,omitempty"`

	UpdatedAt time.Time `json:"updated_at"`

	Attempts    int       `json:"attempts,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	NextRetryAt time.Time `json:"next_retry_at,omitzero"`
}

// EffectiveQuestions returns the question list to render. Prefers the
// new Questions slice; falls back to the legacy single-question fields
// so existing inflight rows stay readable after a deploy.
func (s PromptForUserChoiceInflightSlot) EffectiveQuestions() []PromptForUserChoiceQuestion {
	if len(s.Questions) > 0 {
		return s.Questions
	}
	if s.Question == "" && len(s.Options) == 0 {
		return nil
	}
	return []PromptForUserChoiceQuestion{{
		Header:      s.Header,
		Question:    s.Question,
		MultiSelect: s.MultiSelect,
		Options:     s.Options,
	}}
}

// FeishuInflightConversation is one row from
// ListActiveFeishuInflightConversations. ConversationMetadata carries
// the full conversations.metadata jsonb; consume via the typed
// WorkingInflightSlot / PermissionInflightSlot helpers.
type FeishuInflightConversation struct {
	ConversationID       string
	WorkspaceID          string
	ExternalChatID       string
	ExternalThreadID     string
	SourceAppID          string
	// Platform is the IM platform the conversation belongs to
	// (conversations.platform: "feishu", "slack", ...). The outbound
	// driver dispatches by this field — Feishu rows take the legacy
	// terminal path; other platforms take the neutral Channel path.
	Platform             string
	ConversationMetadata map[string]any
	AgentRunID           string
	RunStatus            string
	RunStartedAt         time.Time
	RunFinishedAt        time.Time
	OutputMessageID      string
	MaxEventSequence     int64
	// AgentName is the display name via agents. Empty
	// when the binding is missing/soft-deleted; callers fall back to
	// FeishuCardTitle.
	AgentName string
	// SenderOpenID is the raw Feishu open_id of the user who triggered
	// the run. The inflight driver builds an `<at user_id="...">` ping
	// after each terminal/permission card. Empty for legacy /
	// system-initiated runs; helper degrades to plain text.
	SenderOpenID string
	// TenantKey is the platform workspace id (Slack team_id, Feishu
	// tenant_key) captured from the inbound trigger message metadata. The
	// neutral outbound path threads it into channel.ReplyTarget so a
	// multi-workspace Slack channel resolves the per-team bot token at
	// send time. Empty on the list (debug) path and for legacy rows; the
	// resolver falls back to the static/env token. Feishu ignores it.
	TenantKey string
}

// AgentRunEvent is one row from ListAgentRunEventsAfterSeq.
type AgentRunEvent struct {
	Sequence   int64
	EventKind  string
	Payload    map[string]any
	OccurredAt time.Time
}

// ListActiveFeishuInflightConversations returns conversations the
// inflight driver should tick. cutoff bounds completed runs (older
// finished runs aren't worth revisiting).
func (s *Store) ListActiveFeishuInflightConversations(ctx context.Context, cutoff time.Time, limit int32) ([]FeishuInflightConversation, error) {
	if limit <= 0 {
		limit = 32
	}
	rows, err := sqlc.New(s.db).ListActiveFeishuInflightConversations(ctx, sqlc.ListActiveFeishuInflightConversationsParams{
		FinishedCutoff: timestamptz(cutoff),
		ItemLimit:      limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]FeishuInflightConversation, 0, len(rows))
	for _, row := range rows {
		out = append(out, FeishuInflightConversation{
			ConversationID:       row.ConversationID,
			WorkspaceID:          row.WorkspaceID,
			ExternalChatID:       row.ExternalChatID,
			ExternalThreadID:     row.ExternalThreadID,
			SourceAppID:          row.SourceAppID,
			// List query is not platform-parameterized (feishu-only debug
			// path), so the row carries no platform column — literal here.
			Platform:             "feishu",
			ConversationMetadata: decodeJSONMap(row.ConversationMetadata),
			AgentRunID:           row.AgentRunID,
			RunStatus:            row.RunStatus,
			RunStartedAt:         pgTime(row.RunStartedAt),
			RunFinishedAt:        pgTime(row.RunFinishedAt),
			OutputMessageID:      row.OutputMessageID,
			MaxEventSequence:     row.MaxEventSequence,
			AgentName:            row.AgentName,
			SenderOpenID:         row.SenderOpenID,
		})
	}
	return out, nil
}

// ClaimActiveFeishuInflightConversationsInput drives the multi-pod
// safe claim. claimedBy lets a pod re-acquire its own claim across
// ticks (avoids flapping when staleBefore drifts past claim_at).
// staleBefore = now - 30s, past which a claim is treated as recoverable.
type ClaimActiveFeishuInflightConversationsInput struct {
	// Platforms restricts the claim to these conversation platforms. The
	// worker passes only platforms whose neutral Channel it can deliver
	// to. Empty defaults to {"feishu"} so legacy callers are unchanged.
	Platforms      []string
	FinishedCutoff time.Time
	StaleBefore    time.Time
	ClaimedBy      string
	Limit          int32
}

// ClaimActiveFeishuInflightConversations is the multi-pod-safe sibling
// of ListActiveFeishuInflightConversations. Locks the conversation row
// with FOR UPDATE OF c SKIP LOCKED and stamps gateway_inflight_claim
// so sibling pods see disjoint batches. A pod re-acquires its own
// claim on every tick (via @claimed_by branch); stalled pods —
// claim_at older than staleBefore — release the row.
func (s *Store) ClaimActiveFeishuInflightConversations(ctx context.Context, input ClaimActiveFeishuInflightConversationsInput) ([]FeishuInflightConversation, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 32
	}
	platforms := input.Platforms
	if len(platforms) == 0 {
		// Backward-compatible default: Feishu-only. Keeps every existing
		// caller's behavior identical until a platform set is supplied.
		platforms = []string{"feishu"}
	}
	now := time.Now().UTC()
	rows, err := sqlc.New(s.db).ClaimActiveFeishuInflightConversations(ctx, sqlc.ClaimActiveFeishuInflightConversationsParams{
		Platforms:      platforms,
		FinishedCutoff: timestamptz(input.FinishedCutoff),
		StaleBefore:    timestamptz(input.StaleBefore),
		ClaimedBy:      input.ClaimedBy,
		ItemLimit:      limit,
		Now:            timestamptz(now),
	})
	if err != nil {
		return nil, err
	}
	out := make([]FeishuInflightConversation, 0, len(rows))
	for _, row := range rows {
		out = append(out, FeishuInflightConversation{
			ConversationID:       row.ConversationID,
			WorkspaceID:          row.WorkspaceID,
			ExternalChatID:       row.ExternalChatID,
			ExternalThreadID:     row.ExternalThreadID,
			SourceAppID:          row.SourceAppID,
			Platform:             row.Platform,
			ConversationMetadata: decodeJSONMap(row.ConversationMetadata),
			AgentRunID:           row.AgentRunID,
			RunStatus:            row.RunStatus,
			RunStartedAt:         pgTime(row.RunStartedAt),
			RunFinishedAt:        pgTime(row.RunFinishedAt),
			OutputMessageID:      row.OutputMessageID,
			MaxEventSequence:     row.MaxEventSequence,
			AgentName:            row.AgentName,
			SenderOpenID:         row.SenderOpenID,
			TenantKey:            row.TenantKey,
		})
	}
	return out, nil
}

// ListAgentRunEventsAfterSeq returns events past afterSeq for the
// given run. limit caps a single tick so a runaway run can't starve
// other conversations.
func (s *Store) ListAgentRunEventsAfterSeq(ctx context.Context, runID string, afterSeq int64, limit int32) ([]AgentRunEvent, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("ListAgentRunEventsAfterSeq: run_id required")
	}
	if limit <= 0 {
		limit = 200
	}
	rid, err := uuid(runID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListAgentRunEventsAfterSeq(ctx, sqlc.ListAgentRunEventsAfterSeqParams{
		AgentRunID: rid,
		AfterSeq:   afterSeq,
		ItemLimit:  limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AgentRunEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, AgentRunEvent{
			Sequence:   row.Seq,
			EventKind:  row.EventKind,
			Payload:    decodeJSONMap(row.Payload),
			OccurredAt: pgTime(row.OccurredAt),
		})
	}
	return out, nil
}

// UpsertConversationInflightWorkingCardInput drives the optimistic-
// lock upsert. ExpectedOldRunID="" is the first-send path (slot must
// be empty or carry no agent_run_id); non-empty asserts the slot
// still belongs to that run.
type UpsertConversationInflightWorkingCardInput struct {
	ConversationID   string
	Slot             WorkingInflightSlot
	ExpectedOldRunID string
}

// UpsertConversationInflightWorkingCard writes the working-card slot
// with an optimistic-lock guard. Returns ErrConversationInflightConflict
// on guard failure. Caller stamps Slot.UpdatedAt.
func (s *Store) UpsertConversationInflightWorkingCard(ctx context.Context, input UpsertConversationInflightWorkingCardInput) (WorkingInflightSlot, error) {
	convID, err := uuid(input.ConversationID)
	if err != nil {
		return WorkingInflightSlot{}, err
	}
	if input.Slot.UpdatedAt.IsZero() {
		input.Slot.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(input.Slot)
	if err != nil {
		return WorkingInflightSlot{}, err
	}
	row, err := sqlc.New(s.db).UpsertConversationInflightWorkingCard(ctx, sqlc.UpsertConversationInflightWorkingCardParams{
		Payload:          payload,
		Now:              timestamptz(time.Now().UTC()),
		ConversationID:   convID,
		ExpectedOldRunID: input.ExpectedOldRunID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkingInflightSlot{}, ErrConversationInflightConflict
		}
		return WorkingInflightSlot{}, err
	}
	return decodeWorkingSlot(row.WorkingSlot), nil
}

// UpsertConversationInflightPermissionCardInput drives the permission
// upsert. ExpectedOldRequestID="" means "no permission slot exists";
// non-empty asserts the slot still points at the expected request.
type UpsertConversationInflightPermissionCardInput struct {
	ConversationID       string
	Slot                 PermissionInflightSlot
	ExpectedOldRequestID string
}

// UpsertConversationInflightPermissionCard writes the permission
// slot. Same conflict semantics as the working variant.
func (s *Store) UpsertConversationInflightPermissionCard(ctx context.Context, input UpsertConversationInflightPermissionCardInput) (PermissionInflightSlot, error) {
	convID, err := uuid(input.ConversationID)
	if err != nil {
		return PermissionInflightSlot{}, err
	}
	if input.Slot.UpdatedAt.IsZero() {
		input.Slot.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(input.Slot)
	if err != nil {
		return PermissionInflightSlot{}, err
	}
	row, err := sqlc.New(s.db).UpsertConversationInflightPermissionCard(ctx, sqlc.UpsertConversationInflightPermissionCardParams{
		Payload:              payload,
		Now:                  timestamptz(time.Now().UTC()),
		ConversationID:       convID,
		ExpectedOldRequestID: input.ExpectedOldRequestID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PermissionInflightSlot{}, ErrConversationInflightConflict
		}
		return PermissionInflightSlot{}, err
	}
	return decodePermissionSlot(row.PermissionSlot), nil
}

// ClearConversationInflightSlot removes a single slot (working or
// permission). Idempotent. Empty expectedAgentRunID skips the run
// guard; non-empty deletes only when the slot's agent_run_id matches.
func (s *Store) ClearConversationInflightSlot(ctx context.Context, conversationID string, slot InflightSlotKind, expectedAgentRunID string) error {
	convID, err := uuid(conversationID)
	if err != nil {
		return err
	}
	return sqlc.New(s.db).ClearConversationInflightSlot(ctx, sqlc.ClearConversationInflightSlotParams{
		Slot:               string(slot),
		Now:                timestamptz(time.Now().UTC()),
		ConversationID:     convID,
		ExpectedAgentRunID: strings.TrimSpace(expectedAgentRunID),
	})
}

// UpsertConversationInflightPromptForUserChoiceCardInput drives the
// prompt_for_user_choice upsert. ExpectedOldRequestID="" means "no
// slot exists"; non-empty asserts the slot still points at the
// expected request_id — guards two pods racing the same daemon ask
// frame.
type UpsertConversationInflightPromptForUserChoiceCardInput struct {
	ConversationID       string
	Slot                 PromptForUserChoiceInflightSlot
	ExpectedOldRequestID string
}

// UpsertConversationInflightPromptForUserChoiceCard writes the slot.
// Same conflict semantics as the permission variant.
func (s *Store) UpsertConversationInflightPromptForUserChoiceCard(ctx context.Context, input UpsertConversationInflightPromptForUserChoiceCardInput) (PromptForUserChoiceInflightSlot, error) {
	convID, err := uuid(input.ConversationID)
	if err != nil {
		return PromptForUserChoiceInflightSlot{}, err
	}
	if input.Slot.UpdatedAt.IsZero() {
		input.Slot.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(input.Slot)
	if err != nil {
		return PromptForUserChoiceInflightSlot{}, err
	}
	row, err := sqlc.New(s.db).UpsertConversationInflightPromptForUserChoiceCard(ctx, sqlc.UpsertConversationInflightPromptForUserChoiceCardParams{
		Payload:              payload,
		Now:                  timestamptz(time.Now().UTC()),
		ConversationID:       convID,
		ExpectedOldRequestID: input.ExpectedOldRequestID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PromptForUserChoiceInflightSlot{}, ErrConversationInflightConflict
		}
		return PromptForUserChoiceInflightSlot{}, err
	}
	return decodePromptForUserChoiceSlot(row.PromptForUserChoiceSlot), nil
}

// FindConversationByPromptForUserChoiceRequestID is the card-callback
// reverse lookup: given the request_id baked into the button value,
// find the conversation that's waiting on a decision.
func (s *Store) FindConversationByPromptForUserChoiceRequestID(ctx context.Context, requestID string) (ConversationInflightCards, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ConversationInflightCards{}, fmt.Errorf("FindConversationByPromptForUserChoiceRequestID: id required")
	}
	row, err := sqlc.New(s.db).FindConversationByPromptForUserChoiceRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationInflightCards{}, ErrUnknownConversation
		}
		return ConversationInflightCards{}, err
	}
	out := ConversationInflightCards{
		ConversationID: row.ID,
		WorkspaceID:    row.WorkspaceID,
		ExternalChatID: row.ExternalChatID,
		SourceAppID:    row.SourceAppID,
	}
	if row.PromptForUserChoiceSlot != nil {
		out.PromptForUserChoice = decodePromptForUserChoiceSlot(row.PromptForUserChoiceSlot)
		out.HasPromptForUserChoice = strings.TrimSpace(out.PromptForUserChoice.RequestID) != ""
	}
	return out, nil
}

// ListStaleFeishuPromptForUserChoiceInflightCards is the server-side
// belt for the daemon's 10-min watchdog. Anything older than cutoff
// gets the card patched to "timed out" and the slot cleared by the
// outbound driver.
func (s *Store) ListStaleFeishuPromptForUserChoiceInflightCards(ctx context.Context, cutoff time.Time, limit int32) ([]ConversationInflightCards, error) {
	if limit <= 0 {
		limit = 16
	}
	rows, err := sqlc.New(s.db).ListStaleFeishuPromptForUserChoiceInflightCards(ctx, sqlc.ListStaleFeishuPromptForUserChoiceInflightCardsParams{
		StaleCutoff: timestamptz(cutoff),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConversationInflightCards, 0, len(rows))
	for _, row := range rows {
		entry := ConversationInflightCards{
			ConversationID: row.ConversationID,
			WorkspaceID:    row.WorkspaceID,
			ExternalChatID: row.ExternalChatID,
			SourceAppID:    row.SourceAppID,
		}
		if row.PromptForUserChoiceSlot != nil {
			entry.PromptForUserChoice = decodePromptForUserChoiceSlot(row.PromptForUserChoiceSlot)
			entry.HasPromptForUserChoice = strings.TrimSpace(entry.PromptForUserChoice.RequestID) != ""
		}
		out = append(out, entry)
	}
	return out, nil
}

// MarkConversationInflightTerminalDelivered stamps a per-run
// fingerprint at conversations.metadata.gateway_inflight.terminal_delivered
// so the claim filter can skip a finished run on subsequent ticks.
//
// Why: a run that failed before producing an output message (the
// common FailAgentRun path) has no message row to stamp via
// messages.metadata.gateway_delivered_at. MarkGatewayOutboundDelivered
// no-ops on missing OutputMessageID, so the claim SQL would otherwise
// re-pick the row every tick and re-send a red "run failed" card.
//
// Idempotent: re-stamping the same run_id is harmless; a subsequent
// run overwrites.
func (s *Store) MarkConversationInflightTerminalDelivered(ctx context.Context, conversationID, runID string) error {
	convID, err := uuid(conversationID)
	if err != nil {
		return err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return fmt.Errorf("MarkConversationInflightTerminalDelivered: run_id required")
	}
	return sqlc.New(s.db).UpsertConversationInflightTerminalDelivered(ctx, sqlc.UpsertConversationInflightTerminalDeliveredParams{
		RunID:          runID,
		Now:            timestamptz(time.Now().UTC()),
		ConversationID: convID,
	})
}

// ConversationInflightCards is the read result for a single
// conversation's inflight tree. Either slot may be zero-valued when
// the underlying jsonb key is missing.
type ConversationInflightCards struct {
	ConversationID         string
	WorkspaceID            string
	ExternalChatID         string
	ExternalThreadID       string
	SourceAppID            string
	Working                WorkingInflightSlot
	Permission             PermissionInflightSlot
	PromptForUserChoice    PromptForUserChoiceInflightSlot
	HasWorking             bool
	HasPermission          bool
	HasPromptForUserChoice bool
}

// GetConversationInflightCards reads both inflight slots in one
// hop. Returns zero-value (HasWorking=false, HasPermission=false)
// when the conversation has no inflight tree at all.
func (s *Store) GetConversationInflightCards(ctx context.Context, conversationID string) (ConversationInflightCards, error) {
	convID, err := uuid(conversationID)
	if err != nil {
		return ConversationInflightCards{}, err
	}
	row, err := sqlc.New(s.db).GetConversationInflightCards(ctx, convID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationInflightCards{}, ErrUnknownConversation
		}
		return ConversationInflightCards{}, err
	}
	out := ConversationInflightCards{
		ConversationID:   row.ID,
		WorkspaceID:      row.WorkspaceID,
		ExternalChatID:   row.ExternalChatID,
		ExternalThreadID: row.ExternalThreadID,
		SourceAppID:      row.SourceAppID,
	}
	if row.WorkingSlot != nil {
		out.Working = decodeWorkingSlot(row.WorkingSlot)
		out.HasWorking = strings.TrimSpace(out.Working.ExternalMsgID) != ""
	}
	if row.PermissionSlot != nil {
		out.Permission = decodePermissionSlot(row.PermissionSlot)
		out.HasPermission = strings.TrimSpace(out.Permission.PermissionRequestID) != ""
	}
	return out, nil
}

// FindConversationByPermissionRequestID is the callback lookup: given
// the permission_request_id from the card button's `value`, find the
// waiting conversation. Returns ErrUnknownConversation when no
// inflight permission slot matches.
func (s *Store) FindConversationByPermissionRequestID(ctx context.Context, permissionRequestID string) (ConversationInflightCards, error) {
	permissionRequestID = strings.TrimSpace(permissionRequestID)
	if permissionRequestID == "" {
		return ConversationInflightCards{}, fmt.Errorf("FindConversationByPermissionRequestID: id required")
	}
	row, err := sqlc.New(s.db).FindConversationByPermissionRequestID(ctx, permissionRequestID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationInflightCards{}, ErrUnknownConversation
		}
		return ConversationInflightCards{}, err
	}
	out := ConversationInflightCards{
		ConversationID: row.ID,
		WorkspaceID:    row.WorkspaceID,
		ExternalChatID: row.ExternalChatID,
		SourceAppID:    row.SourceAppID,
	}
	if row.PermissionSlot != nil {
		out.Permission = decodePermissionSlot(row.PermissionSlot)
		out.HasPermission = strings.TrimSpace(out.Permission.PermissionRequestID) != ""
	}
	return out, nil
}

// DeviceIDForPermissionRequest returns the agent_daemon device id
// stamped onto the inflight permission slot for the given request id.
// Returns the empty string with nil error when the slot has no device
// id (legacy rows written before the slot carried it) — the connector
// treats that as "single-pod / no owner routing". Returns
// ErrUnknownConversation if no matching slot exists.
func (s *Store) DeviceIDForPermissionRequest(ctx context.Context, requestID string) (string, error) {
	conv, err := s.FindConversationByPermissionRequestID(ctx, requestID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(conv.Permission.DeviceID), nil
}

// DeviceIDForPromptForUserChoiceRequest mirrors DeviceIDForPermissionRequest
// for AskUserQuestion slots.
func (s *Store) DeviceIDForPromptForUserChoiceRequest(ctx context.Context, requestID string) (string, error) {
	conv, err := s.FindConversationByPromptForUserChoiceRequestID(ctx, requestID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(conv.PromptForUserChoice.DeviceID), nil
}

// ListStaleFeishuPermissionInflightCards returns conversations whose
// permission inflight slot is older than cutoff. The driver
// auto-expires these so the agent run resumes when the user never
// clicked.
func (s *Store) ListStaleFeishuPermissionInflightCards(ctx context.Context, cutoff time.Time, limit int32) ([]ConversationInflightCards, error) {
	if limit <= 0 {
		limit = 16
	}
	rows, err := sqlc.New(s.db).ListStaleFeishuPermissionInflightCards(ctx, sqlc.ListStaleFeishuPermissionInflightCardsParams{
		StaleCutoff: timestamptz(cutoff),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ConversationInflightCards, 0, len(rows))
	for _, row := range rows {
		entry := ConversationInflightCards{
			ConversationID: row.ConversationID,
			WorkspaceID:    row.WorkspaceID,
			ExternalChatID: row.ExternalChatID,
			SourceAppID:    row.SourceAppID,
		}
		if row.PermissionSlot != nil {
			entry.Permission = decodePermissionSlot(row.PermissionSlot)
			entry.HasPermission = strings.TrimSpace(entry.Permission.PermissionRequestID) != ""
		}
		out = append(out, entry)
	}
	return out, nil
}

// PendingQueuedFeishuRun describes one queued agent_run that hasn't
// yet had a "Queued" placeholder card sent. The queue-card driver
// iterates these per tick — one notice card per run, stamped via
// StampQueueCardSent to avoid double-fires.
type PendingQueuedFeishuRun struct {
	RunID            string
	WorkspaceID      string
	ConversationID   string
	ExternalChatID   string
	ExternalThreadID string
	SourceAppID      string
	// AgentName via agents. Empty when binding is
	// missing/soft-deleted; callers fall back to FeishuCardTitle.
	AgentName string
}

// ClaimPendingQueuedFeishuRunsInput drives the multi-pod-safe queue
// card claim. staleBefore = now - 30s; claimedBy lets a pod re-acquire
// its own claim across ticks without flapping.
type ClaimPendingQueuedFeishuRunsInput struct {
	Cutoff      time.Time
	StaleBefore time.Time
	ClaimedBy   string
	Limit       int32
}

// ClaimPendingQueuedFeishuRuns claims queued runs whose
// metadata->>'queue_card_sent_at' is unset and were created after
// cutoff, stamping queue_card_claim so sibling pods see a disjoint
// batch. Replaces a previous list variant that let every pod see the
// same row → N duplicate "Queued" cards.
func (s *Store) ClaimPendingQueuedFeishuRuns(ctx context.Context, input ClaimPendingQueuedFeishuRunsInput) ([]PendingQueuedFeishuRun, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 16
	}
	now := time.Now().UTC()
	rows, err := sqlc.New(s.db).ClaimPendingQueuedFeishuRuns(ctx, sqlc.ClaimPendingQueuedFeishuRunsParams{
		Cutoff:      timestamptz(input.Cutoff),
		StaleBefore: timestamptz(input.StaleBefore),
		ClaimedBy:   strings.TrimSpace(input.ClaimedBy),
		Now:         timestamptz(now),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]PendingQueuedFeishuRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingQueuedFeishuRun{
			RunID:            row.RunID,
			WorkspaceID:      row.WorkspaceID,
			ConversationID:   row.ConversationID,
			ExternalChatID:   row.ExternalChatID,
			ExternalThreadID: row.ExternalThreadID,
			SourceAppID:      row.SourceAppID,
			AgentName:        row.AgentName,
		})
	}
	return out, nil
}

// StampQueueCardSent marks the queued run as having had its
// placeholder card delivered, so subsequent claim ticks skip it.
// Idempotent.
func (s *Store) StampQueueCardSent(ctx context.Context, runID string, now time.Time) error {
	runUUID, err := uuid(runID)
	if err != nil {
		return err
	}
	return sqlc.New(s.db).StampQueueCardSent(ctx, sqlc.StampQueueCardSentParams{
		RunID: runUUID,
		Now:   timestamptz(now),
	})
}

// ----- private helpers -----

func decodeWorkingSlot(raw any) WorkingInflightSlot {
	return decodeJSONBValue[WorkingInflightSlot](raw)
}

func decodePermissionSlot(raw any) PermissionInflightSlot {
	return decodeJSONBValue[PermissionInflightSlot](raw)
}

func decodePromptForUserChoiceSlot(raw any) PromptForUserChoiceInflightSlot {
	return decodeJSONBValue[PromptForUserChoiceInflightSlot](raw)
}

// Keep the pgtype import live.
var _ pgtype.UUID
