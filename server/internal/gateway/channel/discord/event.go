// Package discord — inbound event normalization (PR #5c).
//
// Normalize turns a Discord MESSAGE_CREATE gateway payload into the neutral
// gateway.InboundEvent the manager routes. The runner marshals discordgo's
// typed *MessageCreate back to its Discord wire JSON and hands the bytes here,
// so this stays a pure decoder testable with a captured payload — the same
// contract slack/event.go honours.
//
// Discord identifies a message by (channel_id, id); ExternalChatID carries the
// channel id and ExternalMessageID the message id — the pair Edit pins. guild_id
// becomes Sender.TenantKey (the Discord guild), mirroring how Slack carries
// team_id and Feishu tenant_key; the per-guild credential resolver threads it as
// the bot id. Like Feishu/Slack this is a pure decode: bot-loop and mention
// gating are the shared manager's policy, not the adapter's.
//
// Thread handling is deferred (Capabilities.Threads is false in 5a): a Discord
// thread is itself a channel, so a message posted in a thread already carries the
// thread as its channel_id and routes correctly without a separate
// ExternalThreadID. Filling the thread/root slots (so ThreadKey groups a thread
// on its parent) lands when native thread creation is enabled.
package discord

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// deUser is the subset of a Discord user object the decoder reads.
type deUser struct {
	ID  string `json:"id"`
	Bot bool   `json:"bot"`
}

// deInboundMessage is the subset of a Discord MESSAGE_CREATE payload Normalize
// reads. discordgo marshals its *Message to exactly this wire shape, so the
// runner can round-trip a typed event through json.Marshal into these bytes.
type deInboundMessage struct {
	ID        string   `json:"id"`
	ChannelID string   `json:"channel_id"`
	GuildID   string   `json:"guild_id"`
	Content   string   `json:"content"`
	Author    *deUser  `json:"author"`
	Mentions  []deUser `json:"mentions"`
	Type      int      `json:"type"`
}

// Normalize converts a Discord MESSAGE_CREATE payload into a neutral
// gateway.InboundEvent. It errors on a payload it cannot parse or one missing an
// author (a system message with no user), so the caller can skip it.
func (c *Channel) Normalize(verified []byte) (gateway.InboundEvent, error) {
	var m deInboundMessage
	if err := json.Unmarshal(verified, &m); err != nil {
		return gateway.InboundEvent{}, fmt.Errorf("discord channel: decode message: %w", err)
	}
	if m.Author == nil {
		return gateway.InboundEvent{}, fmt.Errorf("discord channel: message has no author")
	}

	user := strings.TrimSpace(m.Author.ID)
	guild := strings.TrimSpace(m.GuildID)
	ev := gateway.InboundEvent{
		Platform:          string(c.Platform()),
		BotID:             strings.TrimSpace(c.appID),
		ExternalMessageID: strings.TrimSpace(m.ID),
		ExternalChatID:    strings.TrimSpace(m.ChannelID),
		ChatType:          discordChatType(guild),
		SenderIsBot:       m.Author.Bot,
		MentionedUserIDs:  mentionedUserIDs(m.Mentions),
		Sender: gateway.ExternalIdentity{
			PlatformUserID: user,
			LocalUserID:    user,
			TenantKey:      guild,
		},
		Text: stripLeadingDiscordMentions(m.Content),
		Raw:  append(json.RawMessage(nil), verified...),
	}
	return ev, nil
}

// discordChatType maps a Discord message to the neutral chat type. A guild
// message is a "channel"; a message with no guild_id is a direct message.
// Discord native threads are deferred (a thread message already carries the
// thread as its channel_id), so there is no separate "thread"/"group" mapping
// here yet.
func discordChatType(guildID string) string {
	if strings.TrimSpace(guildID) == "" {
		return "dm"
	}
	return "channel"
}

// mentionedUserIDs projects the Discord message mentions array into the neutral
// platform-local id list the mention gate matches the bot's own id against.
// Discord delivers resolved mention user objects, so no text parsing is needed.
// Returns nil when the message mentions nobody.
func mentionedUserIDs(mentions []deUser) []string {
	if len(mentions) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mentions))
	for _, u := range mentions {
		if id := strings.TrimSpace(u.ID); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// stripLeadingDiscordMentions removes any leading bot/user mention tokens and the
// surrounding whitespace from the message text. In a guild channel the user
// @-mentions the bot to trigger it, so the raw content begins with "<@BOT> …"
// (or the nickname form "<@!BOT> …"); the router's command parser and the agent
// prompt both want that prefix gone — mirroring Feishu's stripFeishuMentionKeys
// and Slack's stripLeadingSlackMentions. Mid-text mentions that are part of the
// prompt are left intact; MentionedUserIDs still reflects the full mention set,
// so the bot-mention gate is unaffected.
func stripLeadingDiscordMentions(text string) string {
	s := strings.TrimSpace(text)
	for strings.HasPrefix(s, "<@") {
		gt := strings.IndexByte(s, '>')
		if gt < 0 {
			break
		}
		s = strings.TrimSpace(s[gt+1:])
	}
	return s
}
