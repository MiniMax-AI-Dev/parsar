// Package slack — card-action callback decode + ack rendering (PR #4c).
//
// HandleAction decodes a raw Slack block_actions interaction payload into the
// neutral channel.CardAction and renders the neutral channel.ActionAck the
// router returns into Slack's response_url reply shape. The business routing
// (permission verdicts, credential-form submits, user-choice answers) is
// injected via channel.ActionRouter so it stays platform-agnostic — mirroring
// feishu/action.go. Until a router is wired HandleAction decodes and echoes a
// neutral "received" reply so a click never hangs.
//
// Button-only: we read block_actions (button clicks and static-select picks),
// never view_submission/modals — matching the Hermes/OpenClaw reference
// implementations, neither of which opens Slack modals. Slack carries the
// action *name* in each block action's action_id (Feishu carries it in the
// button value's "action" key); ActionKindFor maps that action_id to the same
// neutral CardActionKind the router dispatches on.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// ackReceived mirrors Feishu's default toast so an unrouted click looks the
// same to the user across platforms.
const ackReceived = "Received"

// slackActionKinds maps a Slack block action_id to the neutral CardActionKind.
// Keys are identical to feishu/action.go's feishuActionKinds so both platforms
// converge on the same router dispatch. Unmapped ids classify as Unknown.
var slackActionKinds = map[string]channel.CardActionKind{
	"permission_allow":             channel.CardActionPermissionAllow,
	"permission_deny":              channel.CardActionPermissionDeny,
	"credential_form_submit":       channel.CardActionCredentialSubmit,
	"credential_form_acknowledged": channel.CardActionCredentialAck,
	"ask_user_choice_submit":       channel.CardActionUserChoiceSubmit,
	"ask_user_choice_pick":         channel.CardActionUserChoicePick,
}

// ActionKindFor maps a Slack block action_id to its neutral CardActionKind,
// returning CardActionUnknown for unmapped ids.
func ActionKindFor(actionID string) channel.CardActionKind {
	if mapped, ok := slackActionKinds[strings.TrimSpace(actionID)]; ok {
		return mapped
	}
	return channel.CardActionUnknown
}

// HandleAction decodes the raw Slack block_actions payload, routes it through
// the injected ActionRouter, and renders the resulting ack. With no router
// wired it decodes and echoes a neutral "received" reply with Handled=false.
func (c *Channel) HandleAction(ctx context.Context, payload []byte) (channel.ActionResult, error) {
	action, err := c.decodeAction(payload)
	if err != nil {
		return channel.ActionResult{}, err
	}
	// A bare dropdown pick (ask_user_choice_pick) is a Slack UI state change,
	// not a routable business action: Slack fires a block_actions for every
	// select, but the picked value is only consumed at submit time (folded out
	// of state.values by foldChoiceFormValues). Silently ack it so it neither
	// reaches the router — where it would classify as unrouted and log an
	// error — nor mutates the card. Feishu never delivers a standalone pick, so
	// this is a Slack-transport-only short-circuit.
	if action.Kind == channel.CardActionUserChoicePick {
		return channel.ActionResult{Handled: false}, nil
	}
	if c.actions == nil {
		ack, err := renderSlackAck(channel.ActionAck{ToastKind: "info", ToastContent: ackReceived})
		if err != nil {
			return channel.ActionResult{}, err
		}
		return channel.ActionResult{Ack: ack, Handled: false}, nil
	}
	ack, err := c.actions.RouteAction(ctx, action)
	if err != nil {
		return channel.ActionResult{}, err
	}
	rendered, err := renderSlackAck(ack)
	if err != nil {
		return channel.ActionResult{}, err
	}
	return channel.ActionResult{Ack: rendered, Handled: true}, nil
}

// decodeAction turns a raw Slack interaction payload into the neutral
// CardAction. Only block_actions are accepted (button-only); the first block
// action supplies the kind (from action_id) and its value/selected_option are
// string-coerced into Values, mirroring feishu/action.go's cardActionStringValue.
// For the multi-input submit cards (user-choice / credential) the picked
// option(s) and typed credentials live in State.Values rather than on the
// clicked Submit button, so they are folded into FormValues in the shape the
// inbound router reads.
func (c *Channel) decodeAction(payload []byte) (channel.CardAction, error) {
	var cb slack.InteractionCallback
	if err := json.Unmarshal(payload, &cb); err != nil {
		return channel.CardAction{}, fmt.Errorf("slack channel: decode interaction: %w", err)
	}
	if cb.Type != slack.InteractionTypeBlockActions {
		return channel.CardAction{}, fmt.Errorf("slack channel: unsupported interaction type %q (button-only)", cb.Type)
	}
	blockActions := cb.ActionCallback.BlockActions
	if len(blockActions) == 0 {
		return channel.CardAction{}, fmt.Errorf("slack channel: block_actions carried no actions")
	}
	ba := blockActions[0]
	kind := ActionKindFor(ba.ActionID)

	btnValue := strings.TrimSpace(ba.Value)
	values := map[string]string{
		"action": strings.TrimSpace(ba.ActionID),
	}
	if btnValue != "" {
		values["value"] = btnValue
	}
	// A static_select pick carries its answer in selected_option, not value.
	if v := strings.TrimSpace(ba.SelectedOption.Value); v != "" {
		values["selected_option"] = v
	}
	// Feishu carries the id under a named action.value key; a Slack button has
	// a single value string, so the kind decides which router-expected key it
	// lands under.
	var formValues map[string]any
	switch kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		if btnValue != "" {
			values["permission_request_id"] = btnValue
		}
	case channel.CardActionCredentialSubmit:
		if btnValue != "" {
			values["qkey"] = btnValue
		}
		// The credential text inputs live in State.Values, not on the clicked
		// Submit button — fold them into FormValues keyed "credential_<kind>"
		// (the kind == the per-field block_id) to match routeCredentialFormSubmit.
		formValues = foldCredentialFormValues(cb.BlockActionState)
	case channel.CardActionUserChoiceSubmit:
		if btnValue != "" {
			values["request_id"] = btnValue
		}
		// The picked option(s) live in State.Values, not on the clicked Submit
		// button — fold them into FormValues keyed "q<idx>" (idx == the
		// choice_<idx> block_id index) to match routeUserChoiceSubmit.
		formValues = foldChoiceFormValues(cb.BlockActionState)
	}

	// BotID carries the Slack team_id so the manager / outbound resolver can
	// mint the per-workspace bot token (kind='slack_bot', metadata->>'team_id').
	// Falls back to the app id, then the api_app_id, so a single-tenant install
	// without a team-scoped secret still resolves the static/env token.
	botID := strings.TrimSpace(cb.Team.ID)
	if botID == "" {
		botID = c.appID
	}
	if botID == "" {
		botID = strings.TrimSpace(cb.APIAppID)
	}

	return channel.CardAction{
		Kind:              kind,
		Platform:          channel.PlatformSlack,
		BotID:             botID,
		ExternalMessageID: strings.TrimSpace(cb.Message.Timestamp),
		ExternalChatID:    strings.TrimSpace(cb.Channel.ID),
		OperatorID:        strings.TrimSpace(cb.User.ID),
		Values:            values,
		FormValues:        formValues,
		Raw:               append(json.RawMessage(nil), payload...),
	}, nil
}

// foldChoiceFormValues walks the block_actions State.Values for the
// prompt_for_user_choice card and projects each question's picked option(s)
// into the neutral FormValues map keyed "q<idx>" — the shape
// routeUserChoiceSubmit reads per question index. RenderChoiceForm tags each
// select with block_id "choice_<idx>" and action_id ask_user_choice_pick;
// a single-select pick rides in SelectedOption, a multi-select in
// SelectedOptions (projected to []any of label strings). Returns nil when the
// state carries no usable pick so an empty submit still flows through as a
// cancel.
func foldChoiceFormValues(state *slack.BlockActionStates) map[string]any {
	if state == nil {
		return nil
	}
	out := map[string]any{}
	for blockID, actions := range state.Values {
		idx, ok := strings.CutPrefix(blockID, "choice_")
		if !ok {
			continue
		}
		ba, ok := actions["ask_user_choice_pick"]
		if !ok {
			continue
		}
		if len(ba.SelectedOptions) > 0 {
			picks := make([]any, 0, len(ba.SelectedOptions))
			for _, opt := range ba.SelectedOptions {
				if v := strings.TrimSpace(opt.Value); v != "" {
					picks = append(picks, v)
				}
			}
			if len(picks) > 0 {
				out["q"+idx] = picks
			}
			continue
		}
		if v := strings.TrimSpace(ba.SelectedOption.Value); v != "" {
			out["q"+idx] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// foldCredentialFormValues walks the block_actions State.Values for the
// missing-credential card and projects each text input into the neutral
// FormValues map keyed "credential_<kind>" — the shape
// routeCredentialFormSubmit reads, where the kind is the per-field block_id
// (the capability name) and action_id is credential_value. Returns nil when no
// field carries text so the router reports the empty-form case.
func foldCredentialFormValues(state *slack.BlockActionStates) map[string]any {
	if state == nil {
		return nil
	}
	out := map[string]any{}
	for blockID, actions := range state.Values {
		ba, ok := actions["credential_value"]
		if !ok {
			continue
		}
		kind := strings.TrimSpace(blockID)
		if kind == "" {
			continue
		}
		if v := strings.TrimSpace(ba.Value); v != "" {
			out["credential_"+kind] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// slackActionResponse is the response_url reply shape Slack accepts for a
// block_actions interaction: an ephemeral toast-equivalent, or an in-place
// message replacement carrying the router's replacement card.
type slackActionResponse struct {
	ResponseType    string          `json:"response_type,omitempty"` // "ephemeral"
	Text            string          `json:"text,omitempty"`
	ReplaceOriginal bool            `json:"replace_original,omitempty"`
	Blocks          json.RawMessage `json:"blocks,omitempty"`
}

// renderSlackAck marshals a neutral ActionAck into Slack's response_url reply
// JSON. A neutral Result (the channel renders it) takes priority and becomes an
// in-place replacement; a ReplaceCard (a pre-rendered Block Kit {text, blocks}
// payload, used by the Feishu legacy path) likewise becomes a replacement;
// otherwise a non-empty toast becomes an ephemeral message — the closest Slack
// analog to Feishu's toast.
func renderSlackAck(ack channel.ActionAck) ([]byte, error) {
	resp := slackActionResponse{}
	switch {
	case ack.Result != nil:
		msg := renderActionResultCard(ack.Result)
		resp.ReplaceOriginal = true
		resp.Text = msg.Text
		if len(msg.Blocks) > 0 {
			blocks, err := json.Marshal(msg.Blocks)
			if err != nil {
				return nil, fmt.Errorf("slack channel: encode result blocks: %w", err)
			}
			resp.Blocks = blocks
		}
	case len(ack.ReplaceCard) > 0:
		var wm slackWireMessage
		if err := json.Unmarshal(ack.ReplaceCard, &wm); err != nil {
			return nil, fmt.Errorf("slack channel: decode replace card: %w", err)
		}
		resp.ReplaceOriginal = true
		resp.Text = wm.Text
		if len(wm.Blocks.BlockSet) > 0 {
			blocks, err := json.Marshal(wm.Blocks.BlockSet)
			if err != nil {
				return nil, fmt.Errorf("slack channel: encode replace blocks: %w", err)
			}
			resp.Blocks = blocks
		}
	case strings.TrimSpace(ack.ToastContent) != "":
		resp.ResponseType = "ephemeral"
		resp.Text = ack.ToastContent
	}
	return json.Marshal(resp)
}
