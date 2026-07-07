// Package inflight: user-ping helper.
//
// Feishu interactive cards land silently — desktop / mobile clients
// don't raise a push for them. We follow the card with a short text
// message embedding `<at user_id="...">`, which drives the OS-level
// push.
//
// Best-effort: any failure is logged and swallowed. The card has
// already been delivered through the driver's idempotent loop; the
// ping is a UX nudge, not load-bearing. Returning an error would
// force the tick to retry and re-send the card.
//
// Idempotency: piggybacks on caller fingerprints
// (terminal_delivered.run_id, the per-request permission slot) — each
// call site fires once per transition, no separate dedup needed.

package inflight

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Per-scenario ping messages. Exported so tests can reference them.
const (
	UserPingPermission          = "There's an action for you to approve ↑"
	UserPingCredentialForm      = "Some info is needed from you ↑"
	UserPingRunFailed           = "Task failed. Tap the card for details."
	UserPingPromptForUserChoice = "There's a question for you to confirm ↑"
)

// terminalPingMessage matches DoneCard footer wording so the user
// sees the same "took 17s" both inside and outside the card.
func terminalPingMessage(elapsed time.Duration) string {
	return fmt.Sprintf("Task complete ✓ took %s", gateway.FormatElapsed(elapsed))
}

// buildPingText assembles the at-mention text body. Empty openID
// degrades to message verbatim. "user" is the readable fallback when
// the bot can't resolve a display name (cross-tenant chats).
func buildPingText(openID, message string) string {
	openID = strings.TrimSpace(openID)
	message = strings.TrimSpace(message)
	if openID == "" {
		return message
	}
	return fmt.Sprintf(`<at user_id="%s">user</at> %s`, openID, message)
}

// sendUserPingText fires the at-mention follow-up after a card was
// delivered. Best-effort: any failure logged + swallowed.
//
// Thread anchoring mirrors the card-send sites: reply-in-thread when
// the conversation has an external_thread_id, else send to the chat.
func (w *Worker) sendUserPingText(
	ctx context.Context,
	c store.FeishuInflightConversation,
	creds gateway.OutboundCredentials,
	client *gateway.FeishuTenantClient,
	message string,
) {
	if strings.TrimSpace(message) == "" {
		return
	}
	text := buildPingText(c.SenderOpenID, message)
	if text == "" {
		return
	}
	contentBytes, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		// Marshalling a 2-field map shouldn't fail in practice.
		w.logger.Warn("feishu inflight: marshal user-ping content failed",
			"conversation_id", c.ConversationID,
			"run_id", c.AgentRunID,
			"err", err.Error(),
		)
		return
	}
	content := string(contentBytes)

	anchor := strings.TrimSpace(c.ExternalThreadID)
	if anchor != "" {
		if _, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       "text",
			Content:       content,
			ReplyInThread: true,
		}); err != nil {
			w.logger.Warn("feishu inflight: send user-ping (reply) failed",
				"conversation_id", c.ConversationID,
				"run_id", c.AgentRunID,
				"open_id", c.SenderOpenID,
				"err", err.Error(),
			)
		}
		return
	}
	if strings.TrimSpace(c.ExternalChatID) == "" {
		// No anchor and no chat — nothing to send to.
		return
	}
	if _, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     c.ExternalChatID,
		MsgType:       "text",
		Content:       content,
	}); err != nil {
		w.logger.Warn("feishu inflight: send user-ping failed",
			"conversation_id", c.ConversationID,
			"run_id", c.AgentRunID,
			"open_id", c.SenderOpenID,
			"err", err.Error(),
		)
	}
}
