package gateway

import (
	"context"
	"strings"
)

// ThreadHistoryLookup is the narrow read ShouldSkipGroupWithoutMention needs
// to let an un-@-mentioned thread continuation through. Satisfied by the
// store (whose HasFeishuThreadInboundHistory is adapted to this neutral
// method name at the call site) and by test doubles.
type ThreadHistoryLookup interface {
	HasThreadInboundHistory(ctx context.Context, externalChatID, threadKey string) (bool, error)
}

// IsSelfSender reports whether an inbound was authored by the bot itself,
// so the caller can drop it before any routing/storage work (echo guard).
//
// Neutral port of inbound.isSelfMessage: that decoded the bot's open_id
// from the Feishu connector config; here the bot's platform-local id is
// injected by the caller (which already resolved the per-agent config).
// Keys on the LOCAL id (Feishu open_id), matching production.
func IsSelfSender(ev InboundEvent, botLocalID string) bool {
	sender := strings.TrimSpace(ev.Sender.LocalUserID)
	bot := strings.TrimSpace(botLocalID)
	return sender != "" && bot != "" && sender == bot
}

// ShouldSkipGroupWithoutMention decides whether a group-chat inbound should
// be silently dropped before any routing/storage work, because the bot was
// neither @-mentioned nor is this a continuation of a thread it already
// participates in.
//
// Neutral port of inbound.isGroupMessageWithoutBotMention. Differences:
//   - chat kind is the neutral ev.ChatType ("dm"/"group"/...) — "dm" or ""
//     bail out (was "p2p"/"" in the Feishu form).
//   - the bot's own platform-local id (Feishu open_id) is injected rather
//     than decoded from connector config.
//   - bot-authored messages (ev.SenderIsBot) are treated as already
//     targeted, since their "@bot" text lives in a card body, not mentions.
//
// hist may be nil; when set, a thread with prior inbound history is let
// through without a mention. Returns true = drop.
func ShouldSkipGroupWithoutMention(ctx context.Context, hist ThreadHistoryLookup, ev InboundEvent, botLocalID string) bool {
	chatType := strings.ToLower(strings.TrimSpace(ev.ChatType))
	if chatType == "dm" || chatType == "" {
		return false
	}
	if ev.SenderIsBot {
		return false
	}
	botLocalID = strings.TrimSpace(botLocalID)
	if len(ev.MentionedUserIDs) > 0 {
		if botLocalID == "" {
			return true
		}
		for _, mentioned := range ev.MentionedUserIDs {
			if strings.TrimSpace(mentioned) == botLocalID {
				return false
			}
		}
		return true
	}
	// Thread continuation: when this inbound is inside a thread (or reply
	// chain — ThreadKey falls back to ExternalRootID/ExternalMessageID) and
	// we already have history for (chat_id, thread_key), let it through
	// without an @mention. For a brand-new top-level message ThreadKey
	// returns the message id, which never has prior history, so this branch
	// is a no-op there.
	threadKey := strings.TrimSpace(ev.ThreadKey())
	if threadKey != "" && hist != nil {
		hasHistory, err := hist.HasThreadInboundHistory(ctx, strings.TrimSpace(ev.ExternalChatID), threadKey)
		if err == nil && hasHistory {
			return false
		}
	}
	return true
}
