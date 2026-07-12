// Package teams — inbound event normalization.
//
// Normalize turns a verified Bot Framework "message" Activity into the neutral
// gateway.InboundEvent the shared router consumes. It is a pure decode; the
// self-echo and mention gates are the runner's policy (mirroring Slack/Discord).
//
// The two facts the mention gate (gateway.ShouldSkipGroupWithoutMention) needs
// are filled here so the "must @-mention every time" pitfall is handled centrally rather than
// re-implemented per platform:
//
//   - ChatType: a Teams "personal" conversation maps to "dm" (no @mention
//     required); "channel"/"groupChat" map to "channel"/"group" (an @mention —
//     or an already-activated thread — is required).
//   - MentionedUserIDs: the "28:<appId>" ids of the bot-mention entities, which
//     the gate matches against the bot's own BotLocalID.
//
// ExternalChatID carries conversation.id verbatim because that is BOTH the id
// the outbound Connector posts to AND (for a channel reply) the thread-scoped id
// that already encodes ";messageid=<root>" — so ExternalRootID mirrors it and
// ThreadKey groups every message in one thread into one conversation, letting
// history-backed continuation admit un-@-mentioned follow-ups.
package teams

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// activity is the subset of the Bot Framework Activity schema the adapter reads.
// Unmodelled fields are ignored; Raw preserves the full payload downstream.
type activity struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Text       string          `json:"text"`
	Locale     string          `json:"locale"`
	ServiceURL string          `json:"serviceUrl"`
	ChannelID  string          `json:"channelId"`
	ReplyToID  string          `json:"replyToId"`
	Value      json.RawMessage `json:"value"`
	From       channelAccount  `json:"from"`
	Recipient  channelAccount  `json:"recipient"`
	Conv       conversation    `json:"conversation"`
	Entities   []entity        `json:"entities"`
	ChannelDat channelData     `json:"channelData"`
}

type channelAccount struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AADObjectID string `json:"aadObjectId"`
	Role        string `json:"role"`
}

type conversation struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ConversationType string `json:"conversationType"`
	TenantID         string `json:"tenantId"`
	IsGroup          bool   `json:"isGroup"`
}

type entity struct {
	Type      string         `json:"type"`
	Text      string         `json:"text"`
	Mentioned channelAccount `json:"mentioned"`
}

type channelData struct {
	Tenant struct {
		ID string `json:"id"`
	} `json:"tenant"`
	// Channel.ID is the Microsoft Graph channel id, used as the path segment of
	// /teams/{team-id}/channels/{channel-id}/messages for history fetches.
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
	// Team.ID is the Microsoft Graph team id, the parent scope of a channel
	// message. Personal/groupChat messages leave it empty.
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
}

// atTagRe matches a Teams <at ...>label</at> mention token, whose bare form is
// left embedded in activity.text; stripping it keeps the router's "/"-prefixed
// command parser and the agent prompt clean (mirroring stripLeadingSlackMentions).
var atTagRe = regexp.MustCompile(`(?s)<at[^>]*>.*?</at>`)

// Normalize converts a verified Teams Activity into a neutral InboundEvent. It
// errors on non-message activities so the runner can skip them (an invoke /
// conversationUpdate is not a routable message).
func (c *Channel) Normalize(verified []byte) (gateway.InboundEvent, error) {
	var act activity
	if err := json.Unmarshal(verified, &act); err != nil {
		return gateway.InboundEvent{}, fmt.Errorf("teams channel: decode activity: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(act.Type), "message") {
		return gateway.InboundEvent{}, fmt.Errorf("teams channel: unsupported activity type %q", act.Type)
	}

	convID := strings.TrimSpace(act.Conv.ID)
	tenant := firstNonEmpty(act.Conv.TenantID, act.ChannelDat.Tenant.ID)
	platformUser := firstNonEmpty(act.From.AADObjectID, act.From.ID)
	// channelData.channel.id is the Microsoft Graph channel id (the path
	// segment the Graph API history endpoint needs). It wins over activity.channelId
	// (the channel registration name "msteams") when both are present.
	graphChannelID := strings.TrimSpace(act.ChannelDat.Channel.ID)
	if graphChannelID == "" {
		graphChannelID = strings.TrimSpace(act.ChannelID)
	}
	teamID := strings.TrimSpace(act.ChannelDat.Team.ID)

	ev := gateway.InboundEvent{
		Platform:          string(c.Platform()),
		BotID:             strings.TrimSpace(act.Recipient.ID),
		ExternalMessageID: strings.TrimSpace(act.ID),
		ExternalChatID:    convID,
		ExternalRootID:    convID,
		ChatType:          teamsChatType(act.Conv),
		SenderIsBot:       teamsSenderIsBot(act.From),
		MentionedUserIDs:  mentionedIDs(act.Entities),
		Sender: gateway.ExternalIdentity{
			PlatformUserID: strings.TrimSpace(platformUser),
			LocalUserID:    strings.TrimSpace(act.From.ID),
			TenantKey:      strings.TrimSpace(tenant),
			DisplayName:    strings.TrimSpace(act.From.Name),
		},
		Text:    stripMentions(act.Text),
		Raw:     append(json.RawMessage(nil), verified...),
		ReplyTo: strings.TrimSpace(act.ReplyToID),
		Metadata: map[string]any{
			"service_url":       strings.TrimSpace(act.ServiceURL),
			"tenant_id":         strings.TrimSpace(tenant),
			"recipient_id":      strings.TrimSpace(act.Recipient.ID),
			"conversation_type": strings.TrimSpace(act.Conv.ConversationType),
			"channel_id":        graphChannelID,
			"team_id":           teamID,
		},
	}
	if loc := strings.TrimSpace(act.Locale); loc != "" {
		ev.Metadata["locale"] = loc
	}
	return ev, nil
}

// conversationRefFrom extracts the outbound routing context (serviceUrl/tenant/
// recipient bot) the runner primes into the ConversationStore. It reads the same
// raw payload Normalize did; kept separate so the runner primes the cache even
// for activities Normalize rejects (a card submit rides a message activity, but
// belt-and-suspenders keeps the ref fresh). Returns the conversation id and its
// ref; an empty conversation id means "nothing to cache".
func conversationRefFrom(verified []byte) (string, ConversationRef, bool) {
	var act activity
	if err := json.Unmarshal(verified, &act); err != nil {
		return "", ConversationRef{}, false
	}
	convID := strings.TrimSpace(act.Conv.ID)
	if convID == "" {
		return "", ConversationRef{}, false
	}
	return convID, ConversationRef{
		ServiceURL:     strings.TrimSpace(act.ServiceURL),
		TenantID:       firstNonEmpty(act.Conv.TenantID, act.ChannelDat.Tenant.ID),
		BotAppID:       strings.TrimSpace(act.Recipient.ID),
		TeamID:         strings.TrimSpace(act.ChannelDat.Team.ID),
		GraphChannelID: firstNonEmpty(act.ChannelDat.Channel.ID, act.ChannelID),
	}, true
}

// teamsChatType maps a Teams conversationType to the neutral chat kind. Teams
// uses "personal" (1:1), "channel" (a team channel) and "groupChat" (an ad-hoc
// group). isGroup is the fallback when conversationType is absent.
func teamsChatType(conv conversation) string {
	switch strings.ToLower(strings.TrimSpace(conv.ConversationType)) {
	case "personal":
		return "dm"
	case "channel":
		return "channel"
	case "groupchat":
		return "group"
	}
	if conv.IsGroup {
		return "group"
	}
	return "dm"
}

// teamsSenderIsBot reports whether the message came from a bot/app rather than a
// human: Teams tags an app account with role "bot" and prefixes its id with
// "28:". Either is sufficient — the mention gate treats a bot-authored message
// as already targeted.
func teamsSenderIsBot(from channelAccount) bool {
	return strings.EqualFold(strings.TrimSpace(from.Role), "bot") ||
		strings.HasPrefix(strings.TrimSpace(from.ID), "28:")
}

// mentionedIDs pulls the mentioned account ids out of the activity's mention
// entities. A bot @mention carries mentioned.id == "28:<appId>", which the gate
// matches against the bot's BotLocalID. Returns nil when nothing is mentioned.
func mentionedIDs(entities []entity) []string {
	var ids []string
	for _, e := range entities {
		if !strings.EqualFold(strings.TrimSpace(e.Type), "mention") {
			continue
		}
		if id := strings.TrimSpace(e.Mentioned.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// stripMentions removes Teams <at>…</at> mention tokens and collapses the
// surrounding whitespace, so the prompt/command text is the user's words alone.
func stripMentions(text string) string {
	return strings.TrimSpace(atTagRe.ReplaceAllString(text, ""))
}

// firstNonEmpty returns the first trimmed non-empty argument, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// GraphChannelLocation is the (team, channel) id pair the Microsoft Graph
// history endpoint needs. Both come from channelData on the inbound activity;
// channel.id is preferred over the activity.channelId (which is just the
// channel registration name "msteams"). Empty fields mean "not routable" — the
// fetcher returns a 502 rather than calling Graph with placeholder ids.
type GraphChannelLocation struct {
	TeamID    string
	ChannelID string
}
