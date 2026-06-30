// Package discord — card-action callback decode + ack rendering (PR #5c).
//
// HandleAction decodes a raw Discord INTERACTION_CREATE payload (a message
// component click or a modal submit) into the neutral channel.CardAction, routes
// it through the injected channel.ActionRouter, and renders the router's neutral
// ActionAck into the Discord interaction-response card the runner sends back. The
// business routing (permission verdicts, credential-form submits, user-choice
// answers) stays platform-agnostic behind ActionRouter, mirroring slack/action.go
// and feishu/action.go. Until a router is wired HandleAction decodes and echoes a
// neutral "received" reply so a click never hangs.
//
// Discord carries the neutral action id AND its round-trip value packed into the
// component's ≤100-char custom_id as "<action>:<value>" (embed.go customID), so
// decode splits on the first ':' to recover both — where Slack reads the action
// from action_id and the value from a separate field, and Feishu from the button
// value's "action" key. The leading half maps to the same neutral CardActionKind
// the router dispatches on, so downstream routing never sees a platform detail.
//
// User-choice picks need special handling: Discord fires a separate interaction
// for every string-select change and does NOT echo the other selects' state with
// the Submit click (unlike Slack's state.values). So each pick is recorded into
// the injected ComponentPickStore and the picks are drained into FormValues when
// the Submit button arrives.
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// Discord interaction type discriminators (Interaction.type on the wire).
const (
	interactionMessageComponent = 3
	interactionModalSubmit      = 5
)

// discordActionKinds maps a decoded custom_id action prefix to the neutral
// CardActionKind. Keys are identical to slack/action.go's slackActionKinds and
// feishu/action.go's feishuActionKinds so all platforms converge on the same
// router dispatch. Unmapped prefixes classify as Unknown.
var discordActionKinds = map[string]channel.CardActionKind{
	"permission_allow":             channel.CardActionPermissionAllow,
	"permission_deny":              channel.CardActionPermissionDeny,
	"credential_form_submit":       channel.CardActionCredentialSubmit,
	"credential_form_acknowledged": channel.CardActionCredentialAck,
	"ask_user_choice_submit":       channel.CardActionUserChoiceSubmit,
	"ask_user_choice_pick":         channel.CardActionUserChoicePick,
}

// ActionKindFor maps a custom_id action prefix to its neutral CardActionKind,
// returning CardActionUnknown for unmapped prefixes.
func ActionKindFor(action string) channel.CardActionKind {
	if mapped, ok := discordActionKinds[strings.TrimSpace(action)]; ok {
		return mapped
	}
	return channel.CardActionUnknown
}

// deUserRef is the user object on an interaction (member.user in a guild, user in
// a DM).
type deUserRef struct {
	ID string `json:"id"`
}

// deModalComponent is one text input inside a modal submit (custom_id + value).
type deModalComponent struct {
	CustomID string `json:"custom_id"`
	Value    string `json:"value"`
}

// deModalRow is an action row wrapping modal components.
type deModalRow struct {
	Components []deModalComponent `json:"components"`
}

// deInteractionData is the data object of an INTERACTION_CREATE. For a message
// component it carries custom_id / component_type / values (values is the picked
// option set on a string select); for a modal submit it carries custom_id and
// the nested component rows.
type deInteractionData struct {
	CustomID      string       `json:"custom_id"`
	ComponentType int          `json:"component_type"`
	Values        []string     `json:"values"`
	Components    []deModalRow `json:"components"`
}

// deInteraction is the subset of a Discord INTERACTION_CREATE the decoder reads.
// discordgo marshals its *Interaction to this wire shape, so the runner can
// round-trip a typed event through json.Marshal into these bytes.
type deInteraction struct {
	Type      int               `json:"type"`
	GuildID   string            `json:"guild_id"`
	ChannelID string            `json:"channel_id"`
	Data      deInteractionData `json:"data"`
	Message   *struct {
		ID string `json:"id"`
	} `json:"message"`
	Member *struct {
		User *deUserRef `json:"user"`
	} `json:"member"`
	User *deUserRef `json:"user"`
}

// HandleAction decodes the raw Discord interaction payload, routes it through the
// injected ActionRouter, and renders the resulting ack. With no router wired it
// decodes and echoes a neutral "received" reply with Handled=false.
func (c *Channel) HandleAction(ctx context.Context, payload []byte) (channel.ActionResult, error) {
	action, pick, err := c.decodeAction(payload)
	if err != nil {
		return channel.ActionResult{}, err
	}
	// A bare select pick (ask_user_choice_pick) is a Discord UI state change, not
	// a routable business action: Discord fires an interaction for every select
	// change, but the picked values are only consumed at submit time (drained from
	// the pick store). Record it and silently ack so it neither reaches the router
	// — where it would classify as unrouted and log an error — nor mutates the
	// card. Feishu/Slack fold picks from the submit's state, so this is a
	// Discord-transport-only short-circuit.
	if action.Kind == channel.CardActionUserChoicePick {
		if c.picks != nil && pick.messageID != "" {
			c.picks.Record(pick.messageID, pick.questionIdx, pick.values)
		}
		return channel.ActionResult{Handled: false}, nil
	}
	if c.actions == nil {
		ack, err := renderDiscordAck(channel.ActionAck{ToastKind: "info", ToastContent: ackReceived})
		if err != nil {
			return channel.ActionResult{}, err
		}
		return channel.ActionResult{Ack: ack, Handled: false}, nil
	}
	ack, err := c.actions.RouteAction(ctx, action)
	if err != nil {
		return channel.ActionResult{}, err
	}
	rendered, err := renderDiscordAck(ack)
	if err != nil {
		return channel.ActionResult{}, err
	}
	return channel.ActionResult{Ack: rendered, Handled: true}, nil
}

// pickInfo carries the select-pick fields HandleAction records into the pick
// store: the source message id, the question index (the custom_id value), and the
// chosen option values.
type pickInfo struct {
	messageID   string
	questionIdx string
	values      []string
}

// decodeAction turns a raw Discord interaction payload into the neutral
// CardAction. Message-component and modal-submit interactions are accepted; the
// custom_id supplies the kind (its "<action>" prefix) and its value (the
// "<value>" suffix), string-coerced into Values. For the user-choice submit the
// drained picks are folded into FormValues keyed "q<idx>"; for a modal submit the
// typed values are folded keyed "credential_<field>". A select pick is returned
// as the action plus a populated pickInfo so HandleAction can record it.
func (c *Channel) decodeAction(payload []byte) (channel.CardAction, pickInfo, error) {
	var in deInteraction
	if err := json.Unmarshal(payload, &in); err != nil {
		return channel.CardAction{}, pickInfo{}, fmt.Errorf("discord channel: decode interaction: %w", err)
	}
	if in.Type != interactionMessageComponent && in.Type != interactionModalSubmit {
		return channel.CardAction{}, pickInfo{}, fmt.Errorf("discord channel: unsupported interaction type %d", in.Type)
	}

	action, value := splitCustomID(in.Data.CustomID)
	kind := ActionKindFor(action)
	messageID := ""
	if in.Message != nil {
		messageID = strings.TrimSpace(in.Message.ID)
	}

	values := map[string]string{"action": action}
	if value != "" {
		values["value"] = value
	}

	var formValues map[string]any
	var pick pickInfo
	switch kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		if value != "" {
			values["permission_request_id"] = value
		}
	case channel.CardActionUserChoicePick:
		// The picked option(s) ride on this very interaction (Discord delivers
		// data.values with each select change). Surface them so HandleAction can
		// record them against the source message until the Submit click drains
		// them. value is the question index (custom_id "ask_user_choice_pick:<idx>").
		pick = pickInfo{messageID: messageID, questionIdx: value, values: in.Data.Values}
	case channel.CardActionUserChoiceSubmit:
		if value != "" {
			values["request_id"] = value
		}
		// Discord does not echo the other selects' state with the Submit click, so
		// the picks recorded from the earlier select interactions are drained here
		// into the "q<idx>" FormValues shape routeUserChoiceSubmit reads.
		if c.picks != nil && messageID != "" {
			formValues = c.picks.Drain(messageID)
		}
	case channel.CardActionCredentialSubmit:
		if value != "" {
			values["qkey"] = value
		}
		// A modal submit carries the typed credential field(s); fold them into the
		// "credential_<field>" FormValues shape routeCredentialFormSubmit reads. A
		// plain button click (modal-open is deferred) carries no components, so
		// formValues stays nil and the router reports the empty-form case.
		formValues = foldModalValues(in.Data.Components)
	}

	operator := ""
	if in.Member != nil && in.Member.User != nil {
		operator = strings.TrimSpace(in.Member.User.ID)
	} else if in.User != nil {
		operator = strings.TrimSpace(in.User.ID)
	}

	// BotID carries the Discord guild_id so the outbound resolver can mint the
	// per-guild bot token (kind='discord_bot', metadata->>'guild_id'); it falls
	// back to the configured app id for a DM interaction (no guild) so a
	// single-bot install still resolves the static/env token.
	botID := strings.TrimSpace(in.GuildID)
	if botID == "" {
		botID = strings.TrimSpace(c.appID)
	}

	return channel.CardAction{
		Kind:              kind,
		Platform:          channel.PlatformDiscord,
		BotID:             botID,
		ExternalMessageID: messageID,
		ExternalChatID:    strings.TrimSpace(in.ChannelID),
		OperatorID:        operator,
		Values:            values,
		FormValues:        formValues,
		Raw:               append(json.RawMessage(nil), payload...),
	}, pick, nil
}

// splitCustomID recovers the neutral action id and its round-trip value from a
// component custom_id packed as "<action>:<value>" (embed.go customID). A
// custom_id with no ':' is all action and an empty value.
func splitCustomID(customID string) (action, value string) {
	customID = strings.TrimSpace(customID)
	if i := strings.IndexByte(customID, ':'); i >= 0 {
		return strings.TrimSpace(customID[:i]), strings.TrimSpace(customID[i+1:])
	}
	return customID, ""
}

// foldModalValues projects a modal submit's text inputs into the neutral
// FormValues map keyed "credential_<field>" — the shape routeCredentialFormSubmit
// reads, where the field is the input's custom_id (the capability name). Returns
// nil when no field carries text so the router reports the empty-form case.
func foldModalValues(rows []deModalRow) map[string]any {
	out := map[string]any{}
	for _, row := range rows {
		for _, comp := range row.Components {
			field := strings.TrimSpace(comp.CustomID)
			val := strings.TrimSpace(comp.Value)
			if field == "" || val == "" {
				continue
			}
			out["credential_"+field] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// renderDiscordAck marshals a neutral ActionAck into the Discord interaction
// response card the runner sends as a message update (deMessage JSON, which the
// runner maps to discordgo embeds/components via cardContent). A neutral Result
// (the channel renders it) takes priority; a pre-rendered ReplaceCard passes
// through as-is; otherwise a non-empty toast becomes a content-only message — the
// closest Discord analog to Feishu's toast. Returns nil bytes when there is
// nothing to render back.
func renderDiscordAck(ack channel.ActionAck) ([]byte, error) {
	switch {
	case ack.Result != nil:
		msg := renderActionResultCard(ack.Result)
		b, err := json.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("discord channel: encode result card: %w", err)
		}
		return b, nil
	case len(ack.ReplaceCard) > 0:
		return append(json.RawMessage(nil), ack.ReplaceCard...), nil
	case strings.TrimSpace(ack.ToastContent) != "":
		b, err := json.Marshal(deMessage{Content: ack.ToastContent})
		if err != nil {
			return nil, fmt.Errorf("discord channel: encode toast: %w", err)
		}
		return b, nil
	}
	return nil, nil
}
