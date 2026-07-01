// Package teams — card-action callback decode + ack rendering.
//
// A Teams Adaptive Card Action.Submit does NOT arrive as a distinct interaction
// type: it rides an ordinary "message" Activity whose `value` object carries the
// card's submit `data` merged with every input's id→value. So HandleAction
// decodes that value into the neutral channel.CardAction and renders the neutral
// channel.ActionAck the router returns into a Teams reply payload. The business
// routing (permission verdicts, credential submits, user-choice answers) is
// injected via channel.ActionRouter so it stays platform-agnostic — mirroring
// slack/action.go and feishu/action.go. Until a router is wired HandleAction
// decodes and echoes a neutral "received" ack so a click never hangs.
//
// Because the input values are merged into `value` by their card id, the input
// ids ARE the neutral FormValues keys ("q<idx>", "credential_<kind>") — there is
// no separate State.Values fold as in Slack.
package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// ackReceived mirrors Feishu/Slack's default toast so an unrouted click looks
// the same across platforms.
const ackReceived = "操作已收到"

// teamsActionKinds maps a submit `data.action` value to the neutral
// CardActionKind. Keys are identical to slack/feishu so all platforms converge
// on the same router dispatch. Unmapped ids classify as Unknown.
var teamsActionKinds = map[string]channel.CardActionKind{
	"permission_allow":             channel.CardActionPermissionAllow,
	"permission_deny":              channel.CardActionPermissionDeny,
	"credential_form_submit":       channel.CardActionCredentialSubmit,
	"credential_form_acknowledged": channel.CardActionCredentialAck,
	"ask_user_choice_submit":       channel.CardActionUserChoiceSubmit,
	"ask_user_choice_pick":         channel.CardActionUserChoicePick,
}

// choiceKeyRe matches a folded choice-form input id ("q0", "q1", …) so the
// per-question answers are copied into FormValues under the router's key shape.
var choiceKeyRe = regexp.MustCompile(`^q\d+$`)

// ActionKindFor maps a submit data.action value to its neutral CardActionKind,
// returning CardActionUnknown for unmapped values.
func ActionKindFor(action string) channel.CardActionKind {
	if mapped, ok := teamsActionKinds[strings.TrimSpace(action)]; ok {
		return mapped
	}
	return channel.CardActionUnknown
}

// IsCardAction reports whether a verified inbound Activity is an Adaptive Card
// Action.Submit (a message activity carrying a `value` with an "action" key)
// rather than a routable text message. The runner calls it to fork an inbound
// between Normalize and HandleAction.
func IsCardAction(verified []byte) bool {
	var act activity
	if err := json.Unmarshal(verified, &act); err != nil {
		return false
	}
	if len(act.Value) == 0 {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(act.Value, &data); err != nil {
		return false
	}
	return strings.TrimSpace(asString(data["action"])) != ""
}

// HandleAction decodes the raw Activity's submit value, routes it through the
// injected ActionRouter, and renders the resulting ack. With no router wired it
// decodes and echoes a neutral "received" reply with Handled=false.
func (c *Channel) HandleAction(ctx context.Context, payload []byte) (channel.ActionResult, error) {
	action, err := c.decodeAction(payload)
	if err != nil {
		return channel.ActionResult{}, err
	}
	if c.actions == nil {
		ack, err := renderTeamsAck(channel.ActionAck{ToastKind: "info", ToastContent: ackReceived})
		if err != nil {
			return channel.ActionResult{}, err
		}
		return channel.ActionResult{Ack: ack, Handled: false}, nil
	}
	ack, err := c.actions.RouteAction(ctx, action)
	if err != nil {
		return channel.ActionResult{}, err
	}
	rendered, err := renderTeamsAck(ack)
	if err != nil {
		return channel.ActionResult{}, err
	}
	return channel.ActionResult{Ack: rendered, Handled: true}, nil
}

// decodeAction turns a raw Activity carrying an Action.Submit `value` into the
// neutral CardAction. The submit data.action supplies the kind; the reserved
// routing keys (permission_request_id / request_id / qkey) land in Values, and
// the folded input ids ("q<idx>", "credential_<kind>") land in FormValues in the
// shape the inbound router reads.
func (c *Channel) decodeAction(payload []byte) (channel.CardAction, error) {
	var act activity
	if err := json.Unmarshal(payload, &act); err != nil {
		return channel.CardAction{}, fmt.Errorf("teams channel: decode activity: %w", err)
	}
	if len(act.Value) == 0 {
		return channel.CardAction{}, fmt.Errorf("teams channel: activity carried no card action value")
	}
	var data map[string]any
	if err := json.Unmarshal(act.Value, &data); err != nil {
		return channel.CardAction{}, fmt.Errorf("teams channel: decode action value: %w", err)
	}

	actionName := strings.TrimSpace(asString(data["action"]))
	kind := ActionKindFor(actionName)

	values := map[string]string{"action": actionName}
	for _, k := range []string{"permission_request_id", "request_id", "qkey", "value"} {
		if s := strings.TrimSpace(asString(data[k])); s != "" {
			values[k] = s
		}
	}

	var formValues map[string]any
	for k, v := range data {
		switch {
		case strings.HasPrefix(k, "credential_"):
			if formValues == nil {
				formValues = map[string]any{}
			}
			formValues[k] = strings.TrimSpace(asString(v))
		case choiceKeyRe.MatchString(k):
			if formValues == nil {
				formValues = map[string]any{}
			}
			formValues[k] = choiceAnswer(asString(v))
		}
	}

	botID := strings.TrimSpace(act.Recipient.ID)
	if botID == "" {
		botID = c.appID
	}
	operator := firstNonEmpty(act.From.AADObjectID, act.From.ID)

	return channel.CardAction{
		Kind:              kind,
		Platform:          channel.PlatformTeams,
		BotID:             botID,
		ExternalMessageID: strings.TrimSpace(act.ID),
		ExternalChatID:    strings.TrimSpace(act.Conv.ID),
		OperatorID:        strings.TrimSpace(operator),
		Values:            values,
		FormValues:        formValues,
		Raw:               append(json.RawMessage(nil), payload...),
	}, nil
}

// choiceAnswer normalizes an Input.ChoiceSet submit value into the shape
// extractPromptForUserChoiceFormAnswer reads: a multi-select ChoiceSet submits
// a comma-joined string, which becomes a []any of trimmed values; a single-select
// stays a plain string.
func choiceAnswer(raw string) any {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, ",") {
		return raw
	}
	parts := strings.Split(raw, ",")
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// renderTeamsAck marshals a neutral ActionAck into the reply payload the runner
// posts back to the conversation. A neutral Result (the channel renders it)
// becomes a replacement card; a pre-rendered ReplaceCard is passed through;
// otherwise a non-empty toast becomes a plain-text reply. The bytes are a
// teamsWireMessage {text, card} the outbound Send path understands.
func renderTeamsAck(ack channel.ActionAck) ([]byte, error) {
	switch {
	case ack.Result != nil:
		fallback, card := renderActionResultCard(ack.Result)
		cardJSON, err := json.Marshal(card)
		if err != nil {
			return nil, fmt.Errorf("teams channel: encode ack card: %w", err)
		}
		return json.Marshal(teamsWireMessage{Text: fallback, Card: cardJSON})
	case len(ack.ReplaceCard) > 0:
		// Already a native card object; wrap it verbatim.
		return json.Marshal(teamsWireMessage{Card: append(json.RawMessage(nil), ack.ReplaceCard...)})
	default:
		text := strings.TrimSpace(ack.ToastContent)
		if text == "" {
			text = ackReceived
		}
		return json.Marshal(teamsWireMessage{Text: text})
	}
}

// asString coerces a decoded JSON value (which Action.Submit may deliver as a
// string, number or bool) into a trimmed string, mirroring Feishu's
// cardActionStringValue.
func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case float64:
		// Adaptive Card numeric inputs arrive as JSON numbers.
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%t", t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}
