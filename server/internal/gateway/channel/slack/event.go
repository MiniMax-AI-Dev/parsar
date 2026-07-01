// Package slack — inbound event normalization (PR #4c).
//
// Normalize turns a verified Slack Events API payload (an event_callback
// envelope) into the neutral gateway.InboundEvent the manager routes. It maps
// the two message-bearing inner events this gateway acts on:
//
//   - app_mention — the bot was @-mentioned in a channel/thread (the primary
//     trigger), and
//   - message — a DM or channel message (channel_type distinguishes them).
//
// slackevents.ParseEvent does the envelope→typed-inner decode; we read the
// neutral fields off the typed event. Slack identifies a message by
// (channel, ts), so ExternalMessageID carries the message ts and ExternalChatID
// the channel id — the same pair Edit pins. team_id becomes TenantKey (the
// Slack workspace), mirroring how Feishu carries tenant_key. Like Feishu's
// Normalize this is a pure decode: bot-loop / subtype filtering is the
// manager's policy, not the adapter's.
package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack/slackevents"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// Normalize converts a verified Slack event_callback payload into a neutral
// gateway.InboundEvent. It returns an error for envelopes it cannot parse or
// inner events it does not handle, so the caller can skip them.
func (c *Channel) Normalize(verified []byte) (gateway.InboundEvent, error) {
	ev, err := slackevents.ParseEvent(json.RawMessage(verified), slackevents.OptionNoVerifyToken())
	if err != nil {
		return gateway.InboundEvent{}, fmt.Errorf("slack channel: parse event: %w", err)
	}

	switch inner := ev.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		return c.inboundEvent(verified, ev.APIAppID, ev.TeamID, inboundFields{
			user:     inner.User,
			text:     inner.Text,
			channel:  inner.Channel,
			ts:       inner.TimeStamp,
			threadTS: inner.ThreadTimeStamp,
			botID:    inner.BotID,
		}), nil
	case *slackevents.MessageEvent:
		return c.inboundEvent(verified, ev.APIAppID, ev.TeamID, inboundFields{
			user:        inner.User,
			text:        inner.Text,
			channel:     inner.Channel,
			ts:          inner.TimeStamp,
			threadTS:    inner.ThreadTimeStamp,
			botID:       inner.BotID,
			subType:     inner.SubType,
			channelType: inner.ChannelType,
		}), nil
	default:
		return gateway.InboundEvent{}, fmt.Errorf("slack channel: unsupported event type %q", ev.InnerEvent.Type)
	}
}

// inboundFields is the small projection shared by the app_mention and message
// branches so both build the neutral event identically. botID/subType are the
// bot-loop signal (app_mention only carries botID; message also carries the
// bot_message subtype); channelType is the message event's chat kind
// (app_mention has none, so chat type falls back to the channel id prefix).
type inboundFields struct {
	user        string
	text        string
	channel     string
	ts          string
	threadTS    string
	botID       string
	subType     string
	channelType string
}

// inboundEvent assembles the neutral InboundEvent from the platform-neutral
// fields. botID is the Slack app id; tenant is the workspace team_id. The
// thread ts, when present, is both the thread anchor and the reply parent.
//
// It also fills the precomputed platform facts the shared inbound gating reads
// (mirroring Feishu's Normalize), so the same neutral policy helpers work for
// Slack: ChatType (dm/channel/group), SenderIsBot (a bot_id or bot_message
// subtype), MentionedUserIDs (the <@U…> ids parsed out of the text), and
// Sender.LocalUserID (the per-workspace user id — Slack has a single id space,
// so it equals PlatformUserID). ExternalRootID carries the thread parent ts so
// ThreadKey() groups a thread on its root the way Feishu's RootID-or-MessageID
// does. subtype/channel_type, which have no neutral slot, ride in Metadata.
func (c *Channel) inboundEvent(raw []byte, botID, tenant string, f inboundFields) gateway.InboundEvent {
	threadID := strings.TrimSpace(f.threadTS)
	user := strings.TrimSpace(f.user)
	ev := gateway.InboundEvent{
		Platform:          string(c.Platform()),
		BotID:             strings.TrimSpace(botID),
		ExternalMessageID: strings.TrimSpace(f.ts),
		ExternalChatID:    strings.TrimSpace(f.channel),
		ExternalThreadID:  threadID,
		ExternalRootID:    threadID,
		ChatType:          slackChatType(f.channelType, f.channel),
		SenderIsBot:       slackSenderIsBot(f.botID, f.subType),
		MentionedUserIDs:  parseMentionedUserIDs(f.text),
		Sender: gateway.ExternalIdentity{
			PlatformUserID: user,
			LocalUserID:    user,
			TenantKey:      strings.TrimSpace(tenant),
		},
		Text:    stripLeadingSlackMentions(f.text),
		Raw:     json.RawMessage(raw),
		ReplyTo: threadID,
	}
	if st := strings.TrimSpace(f.subType); st != "" {
		ev.Metadata = map[string]any{"subtype": st}
	}
	if ct := strings.TrimSpace(f.channelType); ct != "" {
		if ev.Metadata == nil {
			ev.Metadata = map[string]any{}
		}
		ev.Metadata["channel_type"] = ct
	}
	return ev
}

// slackChatType maps a Slack message to the neutral chat type. It prefers the
// message event's channel_type ("im"/"channel"/"group"/"mpim"); app_mention
// carries none, so it falls back to the channel id prefix (D=im, G=private
// group, C=public channel). Anything unrecognised yields "channel".
func slackChatType(channelType, channelID string) string {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case "im":
		return "dm"
	case "group", "mpim":
		return "group"
	case "channel":
		return "channel"
	}
	switch {
	case strings.HasPrefix(channelID, "D"):
		return "dm"
	case strings.HasPrefix(channelID, "G"):
		return "group"
	default:
		return "channel"
	}
}

// slackSenderIsBot reports whether the message originated from a bot: Slack
// fills bot_id when a bot posts, and tags re-posted bot output with the
// bot_message subtype. Either is sufficient.
func slackSenderIsBot(botID, subType string) bool {
	return strings.TrimSpace(botID) != "" ||
		strings.EqualFold(strings.TrimSpace(subType), "bot_message")
}

// stripLeadingSlackMentions removes any leading Slack mention tokens (<@U…> or
// <@U…|label>) and the whitespace around them from the message text. In a
// channel the user must @-mention the bot to trigger it, so the raw
// app_mention/message text begins with "<@BOT> …"; the router's command parser
// (which requires a leading "/") and the agent prompt both want that prefix
// gone — mirroring Feishu's stripFeishuMentionKeys. Mid-text mentions that are
// part of the prompt are left intact. MentionedUserIDs still parses the raw
// text, so the bot-mention gate is unaffected.
func stripLeadingSlackMentions(text string) string {
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

// parseMentionedUserIDs extracts the user ids from Slack mention tokens in the
// message text. Slack encodes a mention as <@U123> or <@U123|display>; we read
// the id up to an optional "|" label. Channel/special mentions (<!here>,
// <#C…>) are skipped. Returns nil when there are no user mentions.
func parseMentionedUserIDs(text string) []string {
	var ids []string
	rest := text
	for {
		open := strings.Index(rest, "<@")
		if open < 0 {
			break
		}
		rest = rest[open+2:]
		close := strings.IndexByte(rest, '>')
		if close < 0 {
			break
		}
		token := rest[:close]
		rest = rest[close+1:]
		if bar := strings.IndexByte(token, '|'); bar >= 0 {
			token = token[:bar]
		}
		if token = strings.TrimSpace(token); token != "" {
			ids = append(ids, token)
		}
	}
	return ids
}
