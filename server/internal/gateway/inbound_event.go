package gateway

import (
	"encoding/json"
	"strings"
)

// InboundEvent is the platform-neutral, normalized form of a raw IM event.
// Each channel adapter's Normalize() turns its platform-specific webhook
// payload into one of these. It lives in package gateway (not channel) so
// the leaf channel package can import it without an import cycle; it reuses
// the contract.go primitives (MessageRef etc.).
//
// Platform is a plain string (e.g. "feishu") rather than channel.Platform
// for the same reason — keeping gateway dependency-free of channel.
type InboundEvent struct {
	Platform          string          `json:"platform"`
	BotID             string          `json:"bot_id"`
	ExternalMessageID string          `json:"external_message_id"`
	ExternalChatID    string          `json:"external_chat_id"`
	ExternalThreadID  string          `json:"external_thread_id,omitempty"`
	// ExternalRootID is the platform's reply-chain root id (Feishu root_id).
	// It is kept SEPARATE from ExternalThreadID because the conversation
	// grouping key (ThreadKey) derives from root_id, not thread_id — see
	// ThreadKey for why. Empty on a top-level message.
	ExternalRootID string           `json:"external_root_id,omitempty"`
	Sender         ExternalIdentity `json:"sender"`
	Text           string           `json:"text"`
	// ChatType is the neutral conversation kind: "dm" | "group" | "channel".
	// Adapters map their native value (Feishu p2p→dm, group→group).
	ChatType string `json:"chat_type,omitempty"`
	// SenderIsBot is the adapter's precomputed "this came from an app/bot,
	// not a human" fact (Feishu sender_type != "user"). Mention gating uses
	// it to treat bot-authored cards as already-targeted.
	SenderIsBot bool `json:"sender_is_bot,omitempty"`
	// MentionedUserIDs are the platform-local ids (Feishu open_id) the
	// message @-mentions. Mention gating checks the bot's own local id
	// against this set. Empty when the message mentions nobody.
	MentionedUserIDs []string        `json:"mentioned_user_ids,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
	ReplyTo          string          `json:"reply_to,omitempty"`
	// Metadata is the platform-specific extras bag the adapter assembles
	// for downstream storage (e.g. Feishu message_type / raw_content /
	// sender_type / mention_keys / parsed image refs). The shared router
	// folds these into the stored message metadata jsonb. Neutral fields
	// (chat_type, root_id, …) are NOT duplicated here — they have their
	// own typed slots above; Metadata carries only what has no neutral
	// home yet.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// ThreadKey returns the identifier that groups every inbound from the same
// reply chain / thread into one Parsar conversation. It mirrors
// FeishuInboundEvent.ThreadKey byte-for-byte: ExternalRootID then
// ExternalMessageID — deliberately NOT ExternalThreadID.
//
// Why not ExternalThreadID: Feishu's 话题 panel populates thread_id with a
// separate identifier (omt_…) that has no overlap with the root's
// message_id; using it would split root and replies into two
// conversations. root_id is consistent: replies stamp the root's
// message_id into root_id, and the root itself has empty root_id and falls
// back to its own message_id — both end up with the same key.
func (e InboundEvent) ThreadKey() string {
	if id := strings.TrimSpace(e.ExternalRootID); id != "" {
		return id
	}
	return strings.TrimSpace(e.ExternalMessageID)
}

// ExternalIdentity is the platform-side identity of a message sender.
// TenantKey carries the platform's tenant scoping value: Feishu tenant_key,
// Slack team_id, Discord guild_id, WeCom/DingTalk corp_id.
type ExternalIdentity struct {
	PlatformUserID string `json:"platform_user_id"`
	// LocalUserID is the per-app/per-tenant sender id (Feishu open_id;
	// Slack user id, same as PlatformUserID there). Distinct from
	// PlatformUserID (the cross-tenant stable subject, Feishu union_id):
	// self-message filtering and the credential-form pin key on the LOCAL
	// id, while the visibility gate keys on the stable subject.
	LocalUserID string `json:"local_user_id,omitempty"`
	TenantKey   string `json:"tenant_key,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// Attachment is a platform-neutral reference to a non-text payload. PR #1
// does not download binaries; it only carries the key/name for later fetch.
type Attachment struct {
	Kind string `json:"kind"` // "image" | "file" | "video" | "audio"
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}
