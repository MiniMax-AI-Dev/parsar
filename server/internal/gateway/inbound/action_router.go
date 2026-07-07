// Package inbound — production binding of the neutral card-action
// router seam (PR #3c.1).
//
// The Lark event-websocket dispatcher hands handleCardAction a parsed,
// decrypted SDK event (callback.CardActionTriggerEvent) — there are no raw
// bytes at this seam, so the manager cannot consume the adapter's bytes-based
// channel.Channel.HandleAction. Instead it projects the SDK event into the
// SAME neutral channel.CardAction (cardActionFromSDK, the SDK-transport twin
// of the adapter's decodeAction) and routes recognised kinds through the
// neutral channel.ActionRouter contract. The business routing (permission
// verdicts, and — in 3c.2/3c.3 — credential-form submits + user-choice
// answers) thus becomes platform-agnostic and is reused verbatim by any
// future bytes-based platform (Slack, PR #4), while the Lark SDK stays the
// Feishu transport.
//
// 3c.1 routes only the permission verdict; credential-form and user-choice
// stay on the legacy SDK switch until they are ported.
package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	feishuchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/feishu"
	sharedrouter "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// errCardActionUnrouted signals RouteAction does not (yet) handle a kind, so
// handleCardAction keeps it on the legacy SDK switch. Removed once every kind
// migrates (3c.3).
var errCardActionUnrouted = errors.New("inbound: card action kind not routed")

// managerActionRouter binds the manager's card-action handlers behind the
// neutral channel.ActionRouter contract. It is intentionally distinct from
// Manager.permRouter (the runtime PermissionRouter that receives verdicts):
// this dispatches the card click; permRouter consumes the resulting decision.
type managerActionRouter struct{ m *Manager }

// Compile-time assertion that the binding satisfies the neutral seam.
var _ channel.ActionRouter = managerActionRouter{}

// cardActionRouter returns the neutral router bound to this manager. Cheap to
// mint per call: the binding is a single pointer.
func (m *Manager) cardActionRouter() channel.ActionRouter { return managerActionRouter{m: m} }

// CardActionRouter exposes the manager's neutral card-action router so a
// non-Feishu channel runner (e.g. the Slack Socket Mode runner) can inject it
// as its channel.ActionRouter via WithActionRouter. It is the same binding the
// Feishu SDK path uses internally through cardActionRouter; the Platform field
// on each decoded CardAction keeps the render branch correct.
func (m *Manager) CardActionRouter() channel.ActionRouter { return m.cardActionRouter() }

// RouteAction dispatches a decoded, neutral CardAction to the matching
// manager handler. Unhandled kinds return errCardActionUnrouted so the caller
// can fall back to the legacy SDK path during the staged migration.
func (r managerActionRouter) RouteAction(ctx context.Context, action channel.CardAction) (channel.ActionAck, error) {
	switch action.Kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		return r.m.routePermissionDecision(ctx, action)
	case channel.CardActionCredentialSubmit:
		return r.m.routeCredentialFormSubmit(ctx, action)
	case channel.CardActionUserChoiceSubmit:
		return r.m.routeUserChoiceSubmit(ctx, action)
	default:
		return channel.ActionAck{}, errCardActionUnrouted
	}
}

// cardActionFromSDK projects a Feishu SDK card.action.trigger event into the
// neutral channel.CardAction. It mirrors the adapter's bytes-based
// decodeAction (action.value → string-coerced Values; form_value →
// FormValues) so both transports produce an identical neutral action.
//
// FormValues MAY include credential cleartext — callers MUST NOT log it.
func cardActionFromSDK(appID string, event *callback.CardActionTriggerEvent) channel.CardAction {
	meta := cardActionMetadata(appID, event)
	var values map[string]string
	if event != nil && event.Event != nil && event.Event.Action != nil {
		values = make(map[string]string, len(event.Event.Action.Value))
		for k, v := range event.Event.Action.Value {
			if v == nil {
				continue
			}
			if s, ok := v.(string); ok {
				values[k] = strings.TrimSpace(s)
				continue
			}
			values[k] = strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return channel.CardAction{
		Kind:              feishuchannel.ActionKindFor(meta.Action),
		Platform:          channel.PlatformFeishu,
		BotID:             meta.AppID,
		ExternalMessageID: meta.OpenMessageID,
		ExternalChatID:    meta.OpenChatID,
		OperatorID:        meta.OperatorOpenID,
		Values:            values,
		FormValues:        cardActionFormValues(event),
	}
}

// sdkResponseFromAck renders a neutral ActionAck into the Lark SDK callback
// response — the inverse of the adapter's renderFeishuAck (which targets the
// webhook JSON wire). Mirrors ackToast / ackToastWithCard so the user-visible
// toast + optional source-card replacement are byte-identical to the legacy
// path. An empty ToastKind defaults to "info" (matching renderFeishuAck).
func sdkResponseFromAck(ack channel.ActionAck) *callback.CardActionTriggerResponse {
	kind := strings.TrimSpace(ack.ToastKind)
	if kind == "" {
		kind = "info"
	}
	if len(ack.ReplaceCard) > 0 {
		var card map[string]any
		if err := json.Unmarshal(ack.ReplaceCard, &card); err == nil {
			return ackToastWithCard(kind, ack.ToastContent, card)
		}
	}
	return ackToast(kind, ack.ToastContent)
}

// routePermissionDecision is the neutral rewrite of the former
// handlePermissionDecisionAction: it parses the permission_request_id off the
// card button, forwards the verdict to the runtime, patches the card into its
// green/red result shape, and clears the inflight slot — returning the toast
// to render as a neutral ActionAck instead of an SDK response.
//
// Failure modes (all return a toast, never an error — a click must not hang):
//   - missing request id → generic ack toast (defensive)
//   - slot already cleared → "This request has already been handled or expired" toast
//   - SubmitPermission error → retryable toast; slot stays
//   - PatchMessage failure → log warn but still clear the slot since the
//     verdict landed on the runtime
func (m *Manager) routePermissionDecision(ctx context.Context, action channel.CardAction) (channel.ActionAck, error) {
	requestID := strings.TrimSpace(action.Values["permission_request_id"])
	if requestID == "" {
		m.logger.Warn("feishu permission callback missing permission_request_id",
			"open_message_id", action.ExternalMessageID,
			"operator_open_id", action.OperatorID,
		)
		return channel.ActionAck{ToastKind: "info", ToastContent: "Received"}, nil
	}
	approved := action.Kind == channel.CardActionPermissionAllow

	conv, err := m.store.FindConversationByPermissionRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			m.logger.Info("feishu permission callback: slot already cleared",
				"permission_request_id", requestID,
				"operator_open_id", action.OperatorID,
			)
			return channel.ActionAck{ToastKind: "info", ToastContent: "This request has already been handled or expired"}, nil
		}
		m.logger.Warn("feishu permission callback: lookup failed",
			"permission_request_id", requestID,
			"err", err.Error(),
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Query failed, please retry later"}, nil
	}
	if !conv.HasPermission {
		m.logger.Info("feishu permission callback: no permission slot",
			"permission_request_id", requestID,
			"conversation_id", conv.ConversationID,
		)
		return channel.ActionAck{ToastKind: "info", ToastContent: "This request has already been handled or expired"}, nil
	}

	if m.permRouter == nil {
		m.logger.Warn("feishu permission callback: permission router not configured",
			"permission_request_id", requestID,
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Service not configured; contact an administrator"}, nil
	}
	if err := m.permRouter.SubmitPermission(ctx, PermissionDecision{
		RequestID:  requestID,
		Approved:   approved,
		OperatorID: action.OperatorID,
	}); err != nil {
		m.logger.Warn("feishu permission callback: SubmitPermission failed",
			"permission_request_id", requestID,
			"approved", approved,
			"err", err.Error(),
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Update failed, please retry later"}, nil
	}

	// Patch the card into its green / red result shape under the bot that
	// owns the conversation. Feishu-only: the manager builds the native card
	// inline, so the legacy callback response stays byte-identical. Non-Feishu
	// platforms render the neutral ActionResultCard returned below instead.
	if action.Platform == channel.PlatformFeishu {
		if err := m.patchPermissionResultCard(ctx, conv, approved); err != nil {
			// Verdict landed on the runtime; patch failure just leaves
			// the card in its waiting shape. Log loud and clear the slot.
			m.logger.Warn("feishu permission callback: patch result card failed",
				"permission_request_id", requestID,
				"conversation_id", conv.ConversationID,
				"err", err.Error(),
			)
		}
	}
	if err := m.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPermission, conv.Permission.AgentRunID); err != nil {
		m.logger.Warn("feishu permission callback: clear slot failed",
			"permission_request_id", requestID,
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
	toastKind, toastContent := "info", "Rejected"
	if approved {
		toastKind, toastContent = "success", "Allowed"
	}
	ack := channel.ActionAck{ToastKind: toastKind, ToastContent: toastContent}
	if action.Platform != channel.PlatformFeishu {
		ack.Result = &channel.ActionResultCard{
			Kind:     action.Kind,
			Title:    m.resolveActionResultTitle(ctx, conv.ConversationID),
			Approved: approved,
		}
	}
	return ack, nil
}

// resolveActionResultTitle looks up the agent name used as the neutral
// ActionResultCard title for non-Feishu result rendering. A lookup miss
// degrades to "" so the channel applies its own default — mirroring the
// empty-title fallback the Feishu result-card builders use.
func (m *Manager) resolveActionResultTitle(ctx context.Context, conversationID string) string {
	title, err := m.store.ResolveAgentNameForConversation(ctx, conversationID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(title)
}

// replaceCardJSON marshals a card map into the neutral ActionAck.ReplaceCard
// envelope. sdkResponseFromAck round-trips it back into the Lark
// ackToastWithCard `card.type:"raw"` payload, so the post-callback render is
// byte-identical to the legacy inline-card path. A marshal failure (never
// expected for a well-formed builder map) degrades to a toast-only ack rather
// than hanging the click.
func (m *Manager) replaceCardJSON(card map[string]any, logTag string) json.RawMessage {
	raw, err := json.Marshal(card)
	if err != nil {
		m.logger.Warn("feishu card action: marshal replace card failed",
			"tag", logTag,
			"err", err.Error(),
		)
		return nil
	}
	return raw
}

// routeCredentialFormSubmit is the neutral rewrite of the former
// handleCredentialFormSubmitAction: it reads the qkey + credential form
// values off the neutral CardAction, enforces the same authorization gate
// (operator == stash initiator, chat match), claims the slot atomically,
// persists the encrypted credentials, re-enqueues the original query, and
// returns the post-submit card as a neutral ActionAck — keeping the SDK
// callback response byte-identical to the legacy path.
//
// Authorization gate (runs BEFORE any write):
//  1. action.OperatorID must equal stash.initiator_open_id, else ANY group
//     member could submit a form rendered for another user and have
//     credentials persisted under that user's account.
//  2. action.ExternalChatID must equal stash.external_chat_id when both are
//     present.
//
// Concurrency: ClaimPendingCredentialFormSlot captures the pre-image under a
// FOR UPDATE row lock and clears the slot — exactly one racing pod wins.
//
// Invariant: NEVER log credential plaintext. Only the submitted KINDS are
// logged for observability.
func (m *Manager) routeCredentialFormSubmit(ctx context.Context, action channel.CardAction) (channel.ActionAck, error) {
	qkey := strings.TrimSpace(action.Values["qkey"])
	if qkey == "" {
		m.logger.Warn("feishu credential form submit: missing qkey",
			"open_message_id", action.ExternalMessageID,
			"operator_open_id", action.OperatorID,
		)
		return channel.ActionAck{ToastKind: "info", ToastContent: "Received"}, nil
	}
	formValues := action.FormValues
	if len(formValues) == 0 {
		return channel.ActionAck{ToastKind: "error", ToastContent: "Please fill in the credentials before submitting"}, nil
	}

	// Extract (kind, plaintext) pairs from form_value. Fields not prefixed
	// "credential_" are ignored.
	type kindBinding struct {
		kind      string
		plaintext string
	}
	const fieldPrefix = "credential_"
	bindings := make([]kindBinding, 0, len(formValues))
	kindsForLog := make([]string, 0, len(formValues))
	for name, raw := range formValues {
		if !strings.HasPrefix(name, fieldPrefix) {
			continue
		}
		kind := strings.TrimSpace(strings.TrimPrefix(name, fieldPrefix))
		if kind == "" {
			continue
		}
		plaintext, _ := raw.(string)
		plaintext = strings.TrimSpace(plaintext)
		if plaintext == "" {
			m.logger.Warn("feishu credential form submit: empty credential value",
				"qkey", qkey,
				"kind", kind,
			)
			return channel.ActionAck{ToastKind: "error", ToastContent: "Please fill in each credential before submitting"}, nil
		}
		bindings = append(bindings, kindBinding{kind: kind, plaintext: plaintext})
		kindsForLog = append(kindsForLog, kind)
	}
	if len(bindings) == 0 {
		return channel.ActionAck{ToastKind: "error", ToastContent: "Please fill in the credentials before submitting"}, nil
	}

	// Atomic claim — winning pod gets the slot, losers see NotFound. The
	// slot's host conversation IDs come back in the same call.
	claimed, err := m.store.ClaimPendingCredentialFormSlot(ctx, qkey)
	if err != nil {
		if errors.Is(err, store.ErrPendingCredentialFormNotFound) {
			m.logger.Info("feishu credential form submit: slot not found (expired or already processed)",
				"qkey", qkey,
				"operator_open_id", action.OperatorID,
			)
			return channel.ActionAck{ToastKind: "info", ToastContent: "This request has expired; please resend the message"}, nil
		}
		m.logger.Warn("feishu credential form submit: claim failed",
			"qkey", qkey,
			"err", err.Error(),
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Query failed, please retry later"}, nil
	}
	slot := claimed.Slot

	// Enforce auth AFTER the claim so the qkey is consumed exactly once even
	// when the auth check fails — otherwise an attacker could repeatedly poke
	// the callback to keep the stash alive.
	stashedOpenID := strings.TrimSpace(slot.InitiatorOpenID)
	if stashedOpenID == "" || stashedOpenID != strings.TrimSpace(action.OperatorID) {
		m.logger.Warn("feishu credential form submit: operator mismatch",
			"qkey", qkey,
			"operator_open_id", action.OperatorID,
			"stash_initiator_open_id_present", stashedOpenID != "",
			"conversation_id", claimed.ConversationID,
		)
		const toast = "Credentials can only be submitted by the requester"
		ack := channel.ActionAck{ToastKind: "error", ToastContent: toast}
		if action.Platform == channel.PlatformFeishu {
			rejectCard := gateway.BuildCredentialFormRejectedCard(
				m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form reject"),
				gateway.CredentialFormRejectOperatorMismatch,
			)
			ack.ReplaceCard = m.replaceCardJSON(rejectCard, "credential reject operator mismatch")
		} else {
			ack.Result = &channel.ActionResultCard{
				Kind:         action.Kind,
				Title:        m.resolveActionResultTitle(ctx, claimed.ConversationID),
				Rejected:     true,
				RejectReason: toast,
			}
		}
		return ack, nil
	}
	stashedChat := strings.TrimSpace(claimed.ExternalChatID)
	clickedChat := strings.TrimSpace(action.ExternalChatID)
	// Enforce only when both sides have a chat id — clickedChat may be empty
	// on Feishu DMs for some SDK versions; the open_id check above suffices.
	if stashedChat != "" && clickedChat != "" && stashedChat != clickedChat {
		m.logger.Warn("feishu credential form submit: chat mismatch",
			"qkey", qkey,
			"stash_chat_id", stashedChat,
			"clicked_chat_id", clickedChat,
			"operator_open_id", action.OperatorID,
		)
		const toast = "Please submit credentials in the original conversation"
		ack := channel.ActionAck{ToastKind: "error", ToastContent: toast}
		if action.Platform == channel.PlatformFeishu {
			rejectCard := gateway.BuildCredentialFormRejectedCard(
				m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form reject"),
				gateway.CredentialFormRejectChatMismatch,
			)
			ack.ReplaceCard = m.replaceCardJSON(rejectCard, "credential reject chat mismatch")
		} else {
			ack.Result = &channel.ActionResultCard{
				Kind:         action.Kind,
				Title:        m.resolveActionResultTitle(ctx, claimed.ConversationID),
				Rejected:     true,
				RejectReason: toast,
			}
		}
		return ack, nil
	}

	m.logger.Info("feishu credential form submit accepted",
		"qkey", qkey,
		"submitted_kinds", strings.Join(kindsForLog, ","),
		"conversation_id", claimed.ConversationID,
		"initiator_user_id", slot.InitiatorUserID,
		"open_message_id", action.ExternalMessageID,
	)

	// Encrypt up front so an Encrypt failure aborts before we hit the DB tx.
	// Each payload writes both "api_key" and "value" so
	// capability_runtime.credentialPayloadValue (api_key → token →
	// access_token → value) keeps working.
	now := time.Now().UTC()
	credentialInputs := make([]store.CreateUserCredentialInput, 0, len(bindings))
	for _, b := range bindings {
		envelope, err := m.secrets.Encrypt(map[string]any{
			"api_key": b.plaintext,
			"value":   b.plaintext,
		})
		if err != nil {
			m.logger.Warn("feishu credential form submit: encrypt failed",
				"qkey", qkey,
				"kind", b.kind,
				"err", err.Error(),
			)
			return channel.ActionAck{ToastKind: "error", ToastContent: "Saving credentials failed, please retry later"}, nil
		}
		credentialInputs = append(credentialInputs, store.CreateUserCredentialInput{
			UserID:         slot.InitiatorUserID,
			Kind:           b.kind,
			DisplayName:    b.kind,
			EncryptedValue: envelope,
			KeyVersion:     "v1",
			Now:            now,
		})
	}

	// Tx-wrapped multi-kind write; any per-kind failure rolls back.
	results, err := m.store.ReplaceUserCredentials(ctx, slot.InitiatorUserID, credentialInputs)
	if err != nil {
		m.logger.Warn("feishu credential form submit: persist user credentials failed",
			"qkey", qkey,
			"submitted_kinds", strings.Join(kindsForLog, ","),
			"err", err.Error(),
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Saving credentials failed, please retry later"}, nil
	}

	replacedCount := 0
	for _, r := range results {
		if r.Replaced {
			replacedCount++
		}
	}

	// Routing target comes from the slot itself. An empty value means the
	// slot pre-dates this field.
	targetAgentID := strings.TrimSpace(slot.AgentID)
	if targetAgentID == "" {
		m.logger.Warn("feishu credential form submit: slot missing agent_id",
			"qkey", qkey,
			"conversation_id", claimed.ConversationID,
			"external_chat_id", claimed.ExternalChatID,
			"external_thread_id", claimed.ExternalThreadID,
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Credentials saved, but conversation routing was lost; please @-mention the Agent again"}, nil
	}

	// Re-enqueue the original raw_query, busting gateway dedup by using
	// `qkey:<qkey>` as the external_message_id (mint-once unique).
	rerunExternalMessageID := "qkey:" + qkey
	rerunMetadata := map[string]any{
		"source":           "gateway",
		"gateway":          "feishu",
		"reenqueued_qkey":  qkey,
		"reenqueued_from":  "credential_form_submit",
		"external_chat_id": claimed.ExternalChatID,
	}
	if strings.TrimSpace(claimed.ExternalThreadID) != "" {
		rerunMetadata["external_thread_id"] = claimed.ExternalThreadID
	}
	if _, err := m.store.CreateInboundIMMessage(ctx, store.CreateInboundIMMessageInput{
		ConversationTitle: sharedrouter.ConversationTitle(slot.RawQuery),
		Text:              slot.RawQuery,
		Source:            "gateway",
		Gateway:           "feishu",
		SenderOpenID:      slot.InitiatorOpenID,
		InitiatorUserID:   slot.InitiatorUserID,
		ExternalChatID:    claimed.ExternalChatID,
		ExternalThreadID:  claimed.ExternalThreadID,
		ExternalMessageID: rerunExternalMessageID,
		TargetAgentID:     targetAgentID,
		SourceAppID:       claimed.SourceAppID,
		Metadata:          rerunMetadata,
	}); err != nil {
		m.logger.Warn("feishu credential form submit: re-enqueue inbound failed",
			"qkey", qkey,
			"err", err.Error(),
		)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Credentials saved, but restarting the conversation failed; please resend the message"}, nil
	}

	// Build the toast preview shared by both platforms, then either return the
	// finalized native card (Feishu, byte-identical legacy render) or the
	// neutral result for the channel to render (non-Feishu).
	toast := "Received, resuming the conversation"
	if replacedCount > 0 {
		toast = fmt.Sprintf("Replaced %d existing credential(s), resuming the conversation", replacedCount)
	}
	ack := channel.ActionAck{ToastKind: "success", ToastContent: toast}
	if action.Platform == channel.PlatformFeishu {
		// Return the finalized card on the callback response itself — Feishu
		// uses `response.card` as the canonical post-callback render.
		finalizedCard := gateway.BuildCredentialFormSubmittedCard(
			m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form submit"),
		)
		ack.ReplaceCard = m.replaceCardJSON(finalizedCard, "credential submitted")
	} else {
		ack.Result = &channel.ActionResultCard{
			Kind:    action.Kind,
			Title:   m.resolveActionResultTitle(ctx, claimed.ConversationID),
			Summary: toast,
		}
	}
	return ack, nil
}

// routeUserChoiceSubmit is the neutral rewrite of the former
// handlePromptForUserChoiceSubmitAction + routePromptForUserChoiceDecision:
// it reads the request_id + per-question form answers off the neutral
// CardAction, looks the slot up by request_id, pairs each question with the
// matching answer, forwards the decision to the runtime, and returns the
// done card as a neutral ActionAck — keeping the SDK callback response
// byte-identical to the legacy path.
//
// Missing answers become an empty string; an all-blank submit is treated as
// Cancelled so the agent gets a stop-signal instead of a half-answered
// tool_result. A click must never hang, so every branch returns a toast and
// a nil error.
func (m *Manager) routeUserChoiceSubmit(ctx context.Context, action channel.CardAction) (channel.ActionAck, error) {
	requestID := strings.TrimSpace(action.Values["request_id"])
	if requestID == "" {
		m.logger.Warn("feishu prompt_for_user_choice submit missing request_id",
			"open_message_id", action.ExternalMessageID,
			"operator_open_id", action.OperatorID,
		)
		return channel.ActionAck{ToastKind: "info", ToastContent: "Please retry later"}, nil
	}

	conv, err := m.store.FindConversationByPromptForUserChoiceRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			m.logger.Info("feishu prompt_for_user_choice callback: slot already cleared",
				"request_id", requestID,
				"operator_open_id", action.OperatorID,
			)
			return channel.ActionAck{ToastKind: "info", ToastContent: "This request has already been handled or expired"}, nil
		}
		m.logger.Warn("feishu prompt_for_user_choice callback: lookup failed",
			"request_id", requestID, "err", err.Error())
		return channel.ActionAck{ToastKind: "error", ToastContent: "Query failed, please retry later"}, nil
	}
	if !conv.HasPromptForUserChoice {
		m.logger.Info("feishu prompt_for_user_choice callback: no slot",
			"request_id", requestID, "conversation_id", conv.ConversationID)
		return channel.ActionAck{ToastKind: "info", ToastContent: "This request has already been handled or expired"}, nil
	}

	if m.pfucRouter == nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: router not configured",
			"request_id", requestID)
		return channel.ActionAck{ToastKind: "error", ToastContent: "Service not configured; contact an administrator"}, nil
	}

	questions := conv.PromptForUserChoice.EffectiveQuestions()
	answers := make([]PromptForUserChoiceQuestionAnswer, 0, len(questions))
	anyAnswer := false
	for idx, q := range questions {
		answer := extractPromptForUserChoiceFormAnswer(action.FormValues, idx)
		if answer != "" {
			anyAnswer = true
		}
		answers = append(answers, PromptForUserChoiceQuestionAnswer{
			Header: q.Header,
			Answer: answer,
		})
	}

	decision := PromptForUserChoiceDecision{
		RequestID:       requestID,
		QuestionAnswers: answers,
		OperatorID:      action.OperatorID,
	}
	if !anyAnswer {
		decision.Cancelled = true
		decision.Reason = "cancelled"
	}
	if err := m.pfucRouter.SubmitPromptForUserChoice(ctx, decision); err != nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: SubmitPromptForUserChoice failed",
			"request_id", requestID, "err", err.Error())
		return channel.ActionAck{ToastKind: "error", ToastContent: "Update failed, please retry later"}, nil
	}

	// Build the done card inline AND return it on the callback response —
	// Feishu treats response.card as the canonical post-callback render.
	// Non-Feishu platforms get the neutral result for the channel to render.
	feishu := action.Platform == channel.PlatformFeishu
	var replaceCard json.RawMessage
	if feishu {
		doneCard := m.buildPromptForUserChoiceDoneCardMap(ctx, conv, answers)
		replaceCard = m.replaceCardJSON(doneCard, "prompt_for_user_choice done")
	}

	if err := m.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPromptForUserChoice, conv.PromptForUserChoice.AgentRunID); err != nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: clear slot failed",
			"request_id", requestID, "conversation_id", conv.ConversationID, "err", err.Error())
	}

	toastKind, toastContent := "success", "Recorded: "+summarizePromptForUserChoiceAnswers(answers)
	if !anyAnswer {
		toastKind, toastContent = "info", "Cancelled"
	}
	ack := channel.ActionAck{ToastKind: toastKind, ToastContent: toastContent}
	if feishu {
		ack.ReplaceCard = replaceCard
	} else {
		ack.Result = &channel.ActionResultCard{
			Kind:    action.Kind,
			Title:   m.resolveActionResultTitle(ctx, conv.ConversationID),
			Summary: toastContent,
		}
	}
	return ack, nil
}
