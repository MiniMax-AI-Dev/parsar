package store

// Credential-form auto-retry stash, hosted as a slot inside
// conversations.metadata.gateway_inflight.pending_credential_form
// alongside the working / permission / terminal_delivered slots
// (see store_inflight.go).
//
// Why this lives on conversations.metadata rather than its own table:
// reuses the lookup-by-conversation paths the working / permission
// slots already use; jsonb `||` / `#-` patterns are battle-tested.
// Trade-off: a stalled sweep cron leaves raw_query strings inside the
// jsonb subtree rather than as obvious orphan rows.
// CountStalePendingCredentialFormSlots gives Prometheus a query to
// alert on; the claim path also gates on `expires_at > now()` so even
// a stale slot in storage cannot be used post-expiry.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// FeishuCredentialQkeyPrefix is the human-recognisable marker so a
// leaked qkey is obvious in logs / chat. Not security-significant —
// the entropy is in the hex body.
const FeishuCredentialQkeyPrefix = "qkey_"

// MintFeishuCredentialQkey returns a fresh qkey suitable for use as
// the lookup key of a pending_credential_form slot. 16 bytes of
// entropy → 32 hex chars; far above brute-force in the 1-hour stash
// window.
func MintFeishuCredentialQkey() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("mint feishu credential qkey: %w", err)
	}
	return FeishuCredentialQkeyPrefix + hex.EncodeToString(buf[:]), nil
}

// InflightSlotPendingCredentialForm is the slot name used under
// conversations.metadata.gateway_inflight. Lives in the same const
// group as InflightSlotWorking / InflightSlotPermission (declared in
// store_inflight.go) — defined separately here only to keep this
// slot's lifecycle code geographically together.
const InflightSlotPendingCredentialForm InflightSlotKind = "pending_credential_form"

// PendingCredentialFormSlot is the typed shape of
// conversations.metadata.gateway_inflight.pending_credential_form.
//
// AgentID is stamped at stash time so the submit re-enqueue can target
// the same agent the inbound originally hit (gateway_sessions.selected_agent_id
// only exists for shared-bot `/select` flows; direct @-mentions and
// per-agent DMs never write it, which used to fire a "会话路由丢失"
// toast for the majority of credential-form paths).
type PendingCredentialFormSlot struct {
	// Qkey is the lookup key flowing through the Feishu callback
	// `action_value.qkey`. The slot is indexed on this via the partial
	// expression index from migration 000008.
	Qkey string `json:"qkey"`

	// ExternalMsgID is the Feishu `om_…` of the credential-form card
	// the driver shipped to the chat. Empty until the driver stamps it
	// after the first SendMessage / ReplyMessage; pinned thereafter so
	// subsequent inflight ticks PatchMessage the same card instead of
	// shipping a new one (which would let the same conversation
	// accumulate multiple cards on every tick).
	ExternalMsgID string `json:"external_msg_id,omitempty"`

	// InitiatorOpenID is the raw Feishu open_id of the user who
	// triggered the inbound. The submit handler compares it against
	// callback.Operator.OpenID to enforce that only the original
	// sender can fill the form. open_id rather than union_id because
	// the callback envelope only carries open_id.
	InitiatorOpenID string `json:"initiator_open_id"`

	// InitiatorUserID is the Parsar user_id resolved at inbound
	// time. Stored (rather than reconstructed from open_id at submit
	// time) because auth_identities.subject stores Feishu union_id,
	// not open_id — translating open_id → union_id would require a
	// Feishu contact.GetUser API round-trip on every submit. Inbound
	// already has the user_id in hand, so the cost of storing is zero.
	InitiatorUserID string `json:"initiator_user_id"`

	// AgentID is the agent the original inbound targeted.
	// The re-fired inbound uses it as CreateInboundIMMessageInput.TargetAgentID
	// so the rerun lands on the same agent the user originally talked
	// to. Old slots written before this field landed have AgentID ==
	// "" — the submit handler treats that as a routing failure and
	// surfaces a toast asking the user to re-send.
	AgentID string `json:"agent_id,omitempty"`

	// RawQuery is the user's original prompt, kept verbatim so the
	// submit handler can re-enqueue the same turn after the user
	// fills the missing credentials. ExpiresAt below caps its
	// privacy lifetime.
	RawQuery string `json:"raw_query"`

	// ExpiresAt is when this slot should disappear. Sweep cron
	// (every 10 minutes) clears expired slots; the claim path also
	// gates on `expires_at > now()` so even a slot that the sweep
	// hasn't reached yet can't be used post-expiry.
	ExpiresAt time.Time `json:"expires_at"`
}

// ClaimedPendingCredentialForm is what ClaimPendingCredentialFormSlot
// hands back: the slot itself plus the conversation's external-facing
// identifiers (workspace_id / external_chat_id / external_thread_id /
// source_app_id) in one round-trip — so the submit handler doesn't
// need a second SELECT just to read the chat coords for patching the
// reject card / firing the rerun inbound.
type ClaimedPendingCredentialForm struct {
	Slot             PendingCredentialFormSlot
	ConversationID   string
	WorkspaceID      string
	ExternalChatID   string
	ExternalThreadID string
	SourceAppID      string
}

// ErrPendingCredentialFormNotFound is returned by
// ClaimPendingCredentialFormSlot when no slot matches the qkey, or
// when the matched slot's expires_at is in the past. Submit handlers
// branch on this to render "已过期" / "已处理" toasts instead of
// failing loudly.
//
// Also returned by UpdatePendingCredentialFormSlotMessageID when the
// targeted slot is gone (claimed / swept / replaced) — driver-side
// callers log and move on.
var ErrPendingCredentialFormNotFound = errors.New("store: pending credential form slot not found or expired")

// WritePendingCredentialFormSlot is insert-or-noop: if a pending slot
// already exists on this conversation, the existing one is returned
// unchanged so the driver can reuse its qkey + external_msg_id on
// subsequent inflight ticks (Patch the same Feishu card instead of
// shipping a new one each tick).
//
// ExpiresAt of zero on the input falls back to now() + 1h. The
// fallback is applied before marshal, so the returned slot's
// ExpiresAt reflects whatever is actually persisted — either our
// freshly-stamped value or the pre-existing slot's.
//
// Callers should compare slot.Qkey on the return against what they
// just attempted to write: equal → we won the insert; different → an
// existing slot was kept, and the caller should switch to the
// reuse / patch path keyed on the returned slot's data.
func (s *Store) WritePendingCredentialFormSlot(ctx context.Context, conversationID string, slot PendingCredentialFormSlot) (PendingCredentialFormSlot, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return PendingCredentialFormSlot{}, fmt.Errorf("conversation_id: %w", err)
	}
	if strings.TrimSpace(slot.Qkey) == "" {
		return PendingCredentialFormSlot{}, fmt.Errorf("qkey is required")
	}
	if strings.TrimSpace(slot.InitiatorOpenID) == "" {
		// Without initiator_open_id the submit handler cannot verify
		// the click came from the inbound's original sender. Reject at
		// write time so a regression upstream that drops the open_id
		// fails loud instead of degrading the authz check into a no-op.
		return PendingCredentialFormSlot{}, fmt.Errorf("initiator_open_id is required")
	}
	if strings.TrimSpace(slot.InitiatorUserID) == "" {
		return PendingCredentialFormSlot{}, fmt.Errorf("initiator_user_id is required")
	}
	if strings.TrimSpace(slot.RawQuery) == "" {
		return PendingCredentialFormSlot{}, fmt.Errorf("raw_query is required")
	}
	if slot.ExpiresAt.IsZero() {
		slot.ExpiresAt = time.Now().UTC().Add(time.Hour)
	}
	payload, err := json.Marshal(slot)
	if err != nil {
		return PendingCredentialFormSlot{}, fmt.Errorf("marshal pending_credential_form slot: %w", err)
	}
	row, err := sqlc.New(s.db).WritePendingCredentialFormSlot(ctx, sqlc.WritePendingCredentialFormSlotParams{
		Payload:        payload,
		Now:            timestamptz(time.Now().UTC()),
		ConversationID: convUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PendingCredentialFormSlot{}, fmt.Errorf("%w: %s", ErrUnknownConversation, conversationID)
		}
		return PendingCredentialFormSlot{}, fmt.Errorf("write pending_credential_form slot: %w", err)
	}
	return decodePendingCredentialFormSlot(row.Slot), nil
}

// UpdatePendingCredentialFormSlotMessageID stamps the Feishu `om_…`
// onto an already-stashed slot, gated on @qkey so a slot that's been
// claimed / replaced in the meantime is left alone (returns
// ErrPendingCredentialFormNotFound — the driver logs and moves on).
//
// Called by the inflight driver immediately after the first
// SendMessage / ReplyMessage for the credential-form card, so the
// next tick can PatchMessage the same `om_…` rather than ship a new
// card.
func (s *Store) UpdatePendingCredentialFormSlotMessageID(ctx context.Context, conversationID, qkey, externalMsgID string) error {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return fmt.Errorf("conversation_id: %w", err)
	}
	qkey = strings.TrimSpace(qkey)
	if qkey == "" {
		return fmt.Errorf("qkey is required")
	}
	if strings.TrimSpace(externalMsgID) == "" {
		return fmt.Errorf("external_msg_id is required")
	}
	rows, err := sqlc.New(s.db).UpdatePendingCredentialFormSlotMessageID(ctx, sqlc.UpdatePendingCredentialFormSlotMessageIDParams{
		ExternalMsgID:  externalMsgID,
		Now:            timestamptz(time.Now().UTC()),
		ConversationID: convUUID,
		Qkey:           qkey,
	})
	if err != nil {
		return fmt.Errorf("update pending_credential_form external_msg_id: %w", err)
	}
	if rows == 0 {
		return ErrPendingCredentialFormNotFound
	}
	return nil
}

// ClaimPendingCredentialFormSlot is the atomic "I won this submit"
// primitive: a single statement returns the slot (and host
// conversation coords) to exactly one caller; siblings racing on the
// same qkey see ErrPendingCredentialFormNotFound and short-circuit
// to a "已处理" toast without writing credentials.
//
// Expired slots are filtered at the SQL layer (`expires_at > now()`
// in the CTE's pre-image SELECT) so a submit landing between a slot's
// expires_at and the sweep ticker's next pass returns
// ErrPendingCredentialFormNotFound — same outcome as if sweep had
// already run, giving the user a stable "已过期" toast instead of
// cron-timing-dependent behaviour.
func (s *Store) ClaimPendingCredentialFormSlot(ctx context.Context, qkey string) (ClaimedPendingCredentialForm, error) {
	row, err := sqlc.New(s.db).ClaimPendingCredentialFormSlotByQkey(ctx, sqlc.ClaimPendingCredentialFormSlotByQkeyParams{
		Qkey: strings.TrimSpace(qkey),
		Now:  timestamptz(time.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClaimedPendingCredentialForm{}, ErrPendingCredentialFormNotFound
		}
		return ClaimedPendingCredentialForm{}, fmt.Errorf("claim pending_credential_form slot: %w", err)
	}
	return ClaimedPendingCredentialForm{
		Slot:             decodePendingCredentialFormSlot(row.Slot),
		ConversationID:   row.ConversationID,
		WorkspaceID:      row.WorkspaceID,
		ExternalChatID:   row.ExternalChatID,
		ExternalThreadID: row.ExternalThreadID,
		SourceAppID:      row.SourceAppID,
	}, nil
}

// ClearPendingCredentialFormSlotByConversation drops any pending
// form slot on the given conversation. Idempotent — `#-` is a no-op
// when the slot is absent.
//
// Called by the inbound path when a fresh user message arrives in
// the same conversation without going through the form submit, so
// a future stray submit (e.g. a long-cached card someone scrolls
// back to and clicks) cannot auto-resume a stale draft. Also called
// by the outbound driver when PatchMessage permanently fails (e.g.
// past Feishu's 24h edit window) — clearing here lets the next tick
// stash a fresh slot and send a fresh card.
func (s *Store) ClearPendingCredentialFormSlotByConversation(ctx context.Context, conversationID string) error {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return fmt.Errorf("conversation_id: %w", err)
	}
	if err := sqlc.New(s.db).ClearPendingCredentialFormSlotByConversation(ctx, sqlc.ClearPendingCredentialFormSlotByConversationParams{
		Now:            timestamptz(time.Now().UTC()),
		ConversationID: convUUID,
	}); err != nil {
		return fmt.Errorf("clear pending_credential_form slot by conversation: %w", err)
	}
	return nil
}

// SweepExpiredPendingCredentialFormSlots clears every slot whose
// expires_at is before cutoff and returns the count cleared. The
// cron passes time.Now().UTC(); tests inject a deterministic cutoff.
func (s *Store) SweepExpiredPendingCredentialFormSlots(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := sqlc.New(s.db).SweepExpiredPendingCredentialFormSlots(ctx, timestamptz(cutoff))
	if err != nil {
		return 0, fmt.Errorf("sweep expired pending_credential_form slots: %w", err)
	}
	return n, nil
}

// CountStalePendingCredentialFormSlots returns how many slots are
// still hanging around with expires_at past the supplied cutoff.
// Intended for a Prometheus gauge: production calls with cutoff =
// now() - 1h so the 10-minute sweep has plenty of cushion to clear
// just-expired rows without triggering alerts. Sustained non-zero
// means the sweep cron has stalled.
func (s *Store) CountStalePendingCredentialFormSlots(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := sqlc.New(s.db).CountStalePendingCredentialFormSlots(ctx, timestamptz(cutoff))
	if err != nil {
		return 0, fmt.Errorf("count stale pending_credential_form slots: %w", err)
	}
	return n, nil
}

// decodePendingCredentialFormSlot mirrors decodeWorkingSlot /
// decodePermissionSlot in store_inflight.go: tolerant of nil / []byte
// / string / map[string]any (pgx may decode jsonb to any of these
// shapes depending on driver internals).
func decodePendingCredentialFormSlot(raw any) PendingCredentialFormSlot {
	var slot PendingCredentialFormSlot
	switch v := raw.(type) {
	case nil:
		return slot
	case []byte:
		_ = json.Unmarshal(v, &slot)
	case string:
		_ = json.Unmarshal([]byte(v), &slot)
	default:
		if data, err := json.Marshal(v); err == nil {
			_ = json.Unmarshal(data, &slot)
		}
	}
	return slot
}
