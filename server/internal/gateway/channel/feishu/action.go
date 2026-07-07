// Package feishu — card-action callback decode + ack rendering (PR #3a.3).
//
// HandleAction decodes a raw Feishu card.action.trigger webhook body into the
// neutral channel.CardAction and renders the neutral channel.ActionAck the
// router returns back into Feishu's native callback response (a toast,
// optionally replacing the source card). The business routing (permission
// verdicts, credential-form submits, user-choice answers) is injected via
// channel.ActionRouter so it stays platform-agnostic; the production binding
// to the inbound manager lands in PR #3c. Until then HandleAction decodes and
// echoes a neutral "received" toast so a click never hangs.
package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// ackReceived mirrors the production default toast (manager.handleCardAction's
// fall-through ack) so an unrouted click looks the same to the user.
const ackReceived = "Received"

// rawCardAction is the minimal projection of a Feishu card.action.trigger
// callback the adapter decodes — a subset of the SDK's
// CardActionTriggerEvent. Decoding from raw bytes keeps the adapter free of
// the Feishu SDK callback type and makes the decode path plain-JSON testable.
type rawCardAction struct {
	Header struct {
		AppID string `json:"app_id"`
	} `json:"header"`
	Event struct {
		Operator struct {
			OpenID string `json:"open_id"`
		} `json:"operator"`
		Action struct {
			Value     map[string]any `json:"value"`
			FormValue map[string]any `json:"form_value"`
		} `json:"action"`
		Context struct {
			OpenMessageID string `json:"open_message_id"`
			OpenChatID    string `json:"open_chat_id"`
		} `json:"context"`
	} `json:"event"`
}

// feishuActionKinds maps the Feishu button `action` value to the neutral
// CardActionKind. Unmapped strings classify as CardActionUnknown.
var feishuActionKinds = map[string]channel.CardActionKind{
	"permission_allow":             channel.CardActionPermissionAllow,
	"permission_deny":              channel.CardActionPermissionDeny,
	"credential_form_submit":       channel.CardActionCredentialSubmit,
	"credential_form_acknowledged": channel.CardActionCredentialAck,
	"ask_user_choice_submit":       channel.CardActionUserChoiceSubmit,
	"ask_user_choice_pick":         channel.CardActionUserChoicePick,
}

// ActionKindFor maps a Feishu card-button `action` value to its neutral
// CardActionKind, returning CardActionUnknown for unmapped strings. It is the
// single source of the Feishu action→kind mapping, shared by the bytes decode
// here (decodeAction, for HTTP-webhook platforms) and the SDK-event decode on
// the production websocket inbound path (feishuinbound, PR #3c) — both
// converge on the same neutral kind the router dispatches on.
func ActionKindFor(action string) channel.CardActionKind {
	if mapped, ok := feishuActionKinds[strings.TrimSpace(action)]; ok {
		return mapped
	}
	return channel.CardActionUnknown
}

// HandleAction decodes the raw Feishu card-action callback, routes it through
// the injected ActionRouter, and renders the resulting ack. With no router
// wired (PR #3a.3 is additive; routing lands in PR #3c) it decodes and echoes
// a neutral "received" toast with Handled=false.
func (c *Channel) HandleAction(ctx context.Context, payload []byte) (channel.ActionResult, error) {
	action, err := c.decodeAction(payload)
	if err != nil {
		return channel.ActionResult{}, err
	}
	if c.actions == nil {
		ack, err := renderFeishuAck(channel.ActionAck{ToastKind: "info", ToastContent: ackReceived})
		if err != nil {
			return channel.ActionResult{}, err
		}
		return channel.ActionResult{Ack: ack, Handled: false}, nil
	}
	ack, err := c.actions.RouteAction(ctx, action)
	if err != nil {
		return channel.ActionResult{}, err
	}
	rendered, err := renderFeishuAck(ack)
	if err != nil {
		return channel.ActionResult{}, err
	}
	return channel.ActionResult{Ack: rendered, Handled: true}, nil
}

// decodeAction turns a raw Feishu card-action webhook body into the neutral
// CardAction. Mirrors the production cardActionMetadata /
// cardActionStringValue / cardActionFormValues extraction: action.value is
// string-coerced into Values, form_value is carried through as FormValues.
func (c *Channel) decodeAction(payload []byte) (channel.CardAction, error) {
	var raw rawCardAction
	if err := json.Unmarshal(payload, &raw); err != nil {
		return channel.CardAction{}, fmt.Errorf("feishu channel: decode card action: %w", err)
	}
	values := make(map[string]string, len(raw.Event.Action.Value))
	for k, v := range raw.Event.Action.Value {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			values[k] = strings.TrimSpace(s)
			continue
		}
		values[k] = strings.TrimSpace(fmt.Sprint(v))
	}
	kind := ActionKindFor(values["action"])
	botID := c.appID
	if botID == "" {
		botID = strings.TrimSpace(raw.Header.AppID)
	}
	return channel.CardAction{
		Kind:              kind,
		Platform:          channel.PlatformFeishu,
		BotID:             botID,
		ExternalMessageID: strings.TrimSpace(raw.Event.Context.OpenMessageID),
		ExternalChatID:    strings.TrimSpace(raw.Event.Context.OpenChatID),
		OperatorID:        strings.TrimSpace(raw.Event.Operator.OpenID),
		Values:            values,
		FormValues:        raw.Event.Action.FormValue,
		Raw:               append(json.RawMessage(nil), payload...),
	}, nil
}

// Feishu card-action response wire shape (mirrors manager.ackToast /
// ackToastWithCard).
type feishuToast struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type feishuRespCard struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type feishuActionResponse struct {
	Toast *feishuToast    `json:"toast,omitempty"`
	Card  *feishuRespCard `json:"card,omitempty"`
}

// renderFeishuAck marshals a neutral ActionAck into Feishu's card-action
// response JSON. An empty ToastKind defaults to "info"; a non-empty
// ReplaceCard becomes the canonical post-callback render (Feishu's
// `card.type:"raw"`), matching ackToastWithCard.
func renderFeishuAck(ack channel.ActionAck) ([]byte, error) {
	resp := feishuActionResponse{}
	if strings.TrimSpace(ack.ToastContent) != "" || strings.TrimSpace(ack.ToastKind) != "" {
		kind := strings.TrimSpace(ack.ToastKind)
		if kind == "" {
			kind = "info"
		}
		resp.Toast = &feishuToast{Type: kind, Content: ack.ToastContent}
	}
	if len(ack.ReplaceCard) > 0 {
		resp.Card = &feishuRespCard{Type: "raw", Data: ack.ReplaceCard}
	}
	return json.Marshal(resp)
}
