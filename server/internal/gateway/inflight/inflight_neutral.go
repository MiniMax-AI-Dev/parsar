// Package inflight — neutral outbound delivery path (N6).
//
// inflight_neutral.go is the platform-agnostic sibling of the Feishu terminal
// hot path in inflight_driver.go. handleInflightConversation routes any
// conversation whose platform is NOT "feishu" here, so the Feishu path (which
// carries multiple production-incident patches: 3-card spam, credential-form
// qkey reuse, permission auto-expire) stays byte-for-byte untouched.
//
// What this path delivers for non-Feishu channels (Slack today):
//   - mid-run "executing" progress cards (first Send, then Edit/PATCH);
//   - terminal Done / Error cards (Edit when a working slot exists, else Send);
//   - permission Allow/Deny cards (independent slot, reuses the Feishu CAS);
//   - prompt_for_user_choice cards (independent slot, same CAS as permission);
//   - credential-form terminal cards (a run that finished missing capability
//     credentials gets the qkey-bearing form as its terminal card);
//   - multi-pod-safe claim (shared CTE) + per-run terminal fingerprint.
//
// What it deliberately does NOT do (explicitly deferred to a later slice):
//   - typing-reaction emoji, <at> user-ping follow-ups;
//   - permission auto-expire re-render (the verdict path still clears the slot);
//   - the "排队中" queued-run placeholder.
//
// It reuses 95% of the Feishu machinery — foldEventsIntoCardState, the working
// slot helpers, slotPayload/latestSeq/zeroRetryWorking, and the same store
// upsert/clear/fingerprint calls — so card-state folding and slot persistence
// have zero drift from the Feishu path. The neutral channel.Channel resolves
// its own per-bot credentials internally, so this path needs none of the
// worker's Feishu token machinery.
package inflight

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// handleNeutralInflightConversation drives one tick for a non-Feishu
// conversation. It mirrors handleInflightConversation's fold → branch shape but
// renders/sends through the neutral channel.Channel and skips every
// Feishu-only surface. A missing channel is a defensive no-op: claim only
// returns platforms with a registered channel (see NewWorker), so this should
// never fire — but if it does we skip rather than loop.
func (w *Worker) handleNeutralInflightConversation(ctx context.Context, c store.FeishuInflightConversation) error {
	ch, ok := w.channelFor(c.Platform, c.SourceAppID)
	if !ok {
		w.logger.Warn("inflight: no neutral channel for platform; skipping",
			"platform", c.Platform, "conversation_id", c.ConversationID)
		return nil
	}

	// Pull the working slot from the metadata blob the claim query already
	// returned. When the slot points at a previous run, keep its AgentRunID
	// for the CAS but drop the stale steps/streaming so the new run's card
	// starts clean (identical reasoning to the Feishu path).
	prev := extractWorkingSlot(c.ConversationMetadata)
	if !hasWorkingSlotForRun(prev, c.AgentRunID) {
		prev = store.WorkingInflightSlot{AgentRunID: prev.AgentRunID}
	}

	events, err := w.store.ListAgentRunEventsAfterSeq(ctx, c.AgentRunID, prev.SeqEmitted, agentEventsTickFetchLimit)
	if err != nil {
		return fmt.Errorf("list agent_run_events: %w", err)
	}
	// pendingPerm drives the independent permission-card slot; the choice slot
	// is walked separately from events below (same shape as the Feishu path).
	// Credential-form cards are a terminal surface, handled in
	// deliverNeutralTerminal.
	steps, streamingText, thinkingText, newSeq, pendingPerm, errorMessage, rawError := foldEventsIntoCardState(prev, events)
	isCompleted := c.RunStatus == "completed"
	isFailed := c.RunStatus == "failed"
	if newSeq == 0 && !isCompleted && !isFailed && !hasWorkingSlotForRun(prev, c.AgentRunID) {
		// Nothing new and no card yet — defer (SELECT/read race window).
		return nil
	}

	target := channel.ReplyTarget{
		ExternalChatID:   c.ExternalChatID,
		ExternalThreadID: c.ExternalThreadID,
		// TenantKey carries the platform workspace id (Slack team_id) so a
		// multi-workspace adapter resolves the per-tenant bot token at send
		// time. Empty for Feishu and single-tenant Slack, which ignore it.
		TenantKey: c.TenantKey,
	}

	if isCompleted || isFailed {
		return w.deliverNeutralTerminal(ctx, ch, c, prev, target, steps, streamingText, thinkingText, errorMessage, rawError)
	}
	if err := w.deliverNeutralProgress(ctx, ch, c, prev, target, events, steps, streamingText, thinkingText, errorMessage, rawError); err != nil {
		return err
	}
	// Independent of the working card: when this tick observed a new
	// permission.asked and no card is pinned yet, send the Allow/Deny card.
	// Log + continue on failure — permission.asked stays in agent_run_events
	// so the next tick retries.
	if pendingPerm != nil {
		if err := w.maybeSendNeutralPermissionCard(ctx, ch, c, target, pendingPerm); err != nil {
			w.logger.Warn("inflight: neutral permission card send failed",
				"conversation_id", c.ConversationID,
				"permission_request_id", pendingPerm.RequestID,
				"err", err.Error())
		}
	}
	// Same independent-slot shape for prompt_for_user_choice. We walk events
	// once more for the ask rather than threading another return out of the
	// fold — identical to the Feishu path's policy.
	if pendingAsk := extractPendingAskFromEvents(events); pendingAsk != nil {
		if err := w.maybeSendNeutralChoiceCard(ctx, ch, c, target, pendingAsk); err != nil {
			w.logger.Warn("inflight: neutral choice card send failed",
				"conversation_id", c.ConversationID,
				"request_id", pendingAsk.RequestID,
				"err", err.Error())
		}
	}
	return nil
}

// maybeSendNeutralPermissionCard renders and sends the Allow/Deny card through
// the neutral channel and persists an independent permission slot, reusing the
// exact UpsertConversationInflightPermissionCard optimistic-CAS contract the
// Feishu path uses. It is idempotent: a slot already pinned for this request id
// short-circuits, so a re-tick never double-sends. The deferred surfaces (user
// <at> ping, auto-expire re-render) are intentionally omitted here.
func (w *Worker) maybeSendNeutralPermissionCard(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	target channel.ReplyTarget,
	pending *pendingPermission,
) error {
	existing := extractPermissionSlot(c.ConversationMetadata)
	if strings.TrimSpace(existing.PermissionRequestID) == pending.RequestID &&
		strings.TrimSpace(existing.ExternalMsgID) != "" {
		return nil
	}

	card, err := ch.RenderPermission(ctx, target, channel.PermissionRequest{
		Title:     c.AgentName,
		ToolName:  pending.ToolName,
		ToolInput: pending.ToolInput,
		RequestID: pending.RequestID,
	})
	if err != nil {
		return fmt.Errorf("build permission card: %w", err)
	}

	ref, err := ch.Send(ctx, target, card)
	if err != nil {
		return fmt.Errorf("send permission card: %w", err)
	}

	next := store.PermissionInflightSlot{
		ExternalMsgID:       ref.ID,
		AppID:               c.SourceAppID,
		ExternalChatID:      c.ExternalChatID,
		ExternalThreadID:    c.ExternalThreadID,
		AgentRunID:          c.AgentRunID,
		DeviceID:            w.resolveDeviceID(ctx, c.ConversationID),
		PermissionRequestID: pending.RequestID,
		Payload: map[string]any{
			"tool_name":  pending.ToolName,
			"tool_input": pending.ToolInput,
		},
	}
	next = zeroRetryPermission(next)
	if _, err := w.store.UpsertConversationInflightPermissionCard(ctx, store.UpsertConversationInflightPermissionCardInput{
		ConversationID:       c.ConversationID,
		Slot:                 next,
		ExpectedOldRequestID: strings.TrimSpace(existing.PermissionRequestID),
	}); err != nil {
		if errors.Is(err, store.ErrConversationInflightConflict) {
			// Another tick won the permission-send race; the card we sent is
			// an orphan and the next tick resyncs from the winner.
			w.logger.Warn("inflight: lost neutral permission-send race",
				"conversation_id", c.ConversationID, "permission_request_id", pending.RequestID)
			return nil
		}
		return fmt.Errorf("persist permission slot: %w", err)
	}
	return nil
}

// deliverNeutralTerminal renders the Done/Error card and either edits the
// existing working-card message (when a slot is pinned) or sends a fresh one
// (short run that finished before any working card landed). On success it
// stamps the messages-side delivery marker (when an output_message_id exists),
// the per-run terminal fingerprint (always, so the claim CTE drops the run),
// then clears the working slot.
func (w *Worker) deliverNeutralTerminal(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	prev store.WorkingInflightSlot,
	target channel.ReplyTarget,
	steps []gateway.StepInfo,
	streamingText, thinkingText, errorMessage, rawError string,
) error {
	// Credential-form terminal: a run that finished missing capability
	// credentials gets the qkey-bearing form as its terminal card (the submit
	// re-enqueues the same turn) instead of the Done/Error card. Mirrors the
	// Feishu terminal branch's tryBuildCredentialFormCard reconcile, minus the
	// Feishu 24h-edit-window dance. On resolve failure we fall back to the
	// regular terminal card.
	if handled, err := w.maybeDeliverNeutralCredentialForm(ctx, ch, c, prev, target); err != nil {
		w.logger.Warn("inflight: neutral credential form delivery failed; falling back to terminal card",
			"conversation_id", c.ConversationID, "err", err.Error())
	} else if handled {
		return nil
	}

	card, err := w.buildNeutralTerminalCard(ctx, ch, c, steps, streamingText, thinkingText, errorMessage, rawError)
	if err != nil {
		return fmt.Errorf("build terminal card: %w", err)
	}

	var deliveredID string
	if hasWorkingSlotForRun(prev, c.AgentRunID) {
		if err := ch.Edit(ctx, target, gateway.MessageRef{ID: prev.ExternalMsgID}, card); err != nil {
			return fmt.Errorf("edit terminal card: %w", err)
		}
		deliveredID = prev.ExternalMsgID
	} else {
		ref, err := ch.Send(ctx, target, card)
		if err != nil {
			return fmt.Errorf("send terminal card: %w", err)
		}
		deliveredID = ref.ID
	}
	return w.finalizeNeutralTerminalDelivery(ctx, c, deliveredID)
}

// finalizeNeutralTerminalDelivery stamps the messages-side delivery marker
// (when an output_message_id exists), the per-run terminal fingerprint (always,
// so the claim CTE drops the run), then clears the working slot. Shared by the
// regular terminal card and the credential-form terminal so both leave the
// claim set identically.
func (w *Worker) finalizeNeutralTerminalDelivery(ctx context.Context, c store.FeishuInflightConversation, deliveredID string) error {
	// MarkDelivered stamps gateway_delivered_at on the originating messages
	// row so the claim CTE's terminal filter skips this run. Empty
	// OutputMessageID is normal for runs that failed pre-output; the
	// conversation-row fingerprint below covers that path.
	if strings.TrimSpace(c.OutputMessageID) != "" && deliveredID != "" {
		if _, err := w.store.MarkGatewayOutboundDelivered(ctx, store.MarkGatewayOutboundDeliveredInput{
			MessageID: c.OutputMessageID,
		}); err != nil {
			return fmt.Errorf("mark terminal delivered: %w", err)
		}
	}
	// Per-run fingerprint on the conversation row — the critical guard that
	// stops a run with no output_message_id from being re-claimed forever.
	if err := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); err != nil {
		return fmt.Errorf("mark terminal delivered fingerprint: %w", err)
	}
	// Best-effort: with the fingerprint in place the next tick won't re-pick
	// this run even if the slot lingers.
	if err := w.store.ClearConversationInflightSlot(ctx, c.ConversationID, store.InflightSlotWorking, c.AgentRunID); err != nil {
		w.logger.Warn("inflight: clear slot after neutral terminal failed",
			"conversation_id", c.ConversationID, "err", err.Error())
	}
	return nil
}

// deliverNeutralProgress renders the in-flight working card and either sends it
// (first time, capturing the message ref into the slot) or edits the pinned
// message. Slot persistence reuses the exact upsert + optimistic-CAS contract
// the Feishu path uses, so a lost race resolves identically.
func (w *Worker) deliverNeutralProgress(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	prev store.WorkingInflightSlot,
	target channel.ReplyTarget,
	events []store.AgentRunEvent,
	steps []gateway.StepInfo,
	streamingText, thinkingText, errorMessage, rawError string,
) error {
	now := time.Now().UTC()
	elapsed := time.Duration(0)
	if !c.RunStartedAt.IsZero() {
		elapsed = now.Sub(c.RunStartedAt)
	}
	card, err := ch.RenderProgress(ctx, target, channel.ProgressState{
		Title:         c.AgentName,
		Steps:         steps,
		StreamingText: streamingText,
		Elapsed:       elapsed,
		Now:           now,
	})
	if err != nil {
		return fmt.Errorf("build mid-run card: %w", err)
	}

	if !hasWorkingSlotForRun(prev, c.AgentRunID) {
		// First send — capture the message ref so the next tick edits it.
		ref, err := ch.Send(ctx, target, card)
		if err != nil {
			return fmt.Errorf("send working card: %w", err)
		}
		next := store.WorkingInflightSlot{
			ExternalMsgID:    ref.ID,
			ExternalChatID:   c.ExternalChatID,
			ExternalThreadID: c.ExternalThreadID,
			AgentRunID:       c.AgentRunID,
			SeqEmitted:       latestSeq(events, prev.SeqEmitted),
			Payload:          slotPayload(steps, streamingText, thinkingText, errorMessage, rawError),
		}
		next = zeroRetryWorking(next)
		if _, err := w.store.UpsertConversationInflightWorkingCard(ctx, store.UpsertConversationInflightWorkingCardInput{
			ConversationID:   c.ConversationID,
			Slot:             next,
			ExpectedOldRunID: prev.AgentRunID,
		}); err != nil {
			if errors.Is(err, store.ErrConversationInflightConflict) {
				// Another tick won the first-send race; the card we sent is
				// now an orphan and the next tick resyncs from the winner.
				w.logger.Warn("inflight: lost neutral first-send race",
					"conversation_id", c.ConversationID, "external_msg_id", ref.ID)
				return nil
			}
			return fmt.Errorf("persist initial inflight slot: %w", err)
		}
		return nil
	}

	// Subsequent ticks: PATCH the existing message in place.
	if err := ch.Edit(ctx, target, gateway.MessageRef{ID: prev.ExternalMsgID}, card); err != nil {
		return fmt.Errorf("edit working card: %w", err)
	}
	next := prev
	next.SeqEmitted = latestSeq(events, prev.SeqEmitted)
	next.Payload = slotPayload(steps, streamingText, thinkingText, errorMessage, rawError)
	next = zeroRetryWorking(next)
	if _, err := w.store.UpsertConversationInflightWorkingCard(ctx, store.UpsertConversationInflightWorkingCardInput{
		ConversationID:   c.ConversationID,
		Slot:             next,
		ExpectedOldRunID: prev.AgentRunID,
	}); err != nil {
		if errors.Is(err, store.ErrConversationInflightConflict) {
			w.logger.Warn("inflight: optimistic-lock conflict on neutral patch",
				"conversation_id", c.ConversationID, "external_msg_id", prev.ExternalMsgID)
			return nil
		}
		return fmt.Errorf("persist patched inflight slot: %w", err)
	}
	return nil
}

// buildNeutralTerminalCard is the platform-agnostic twin of
// buildFinalCardForRun: it assembles the same Done/Error TerminalResult and
// renders it through the neutral channel, so Slack's Block Kit card carries the
// identical run state (steps, usage, error copy, guest hint) the Feishu card
// would. The channel owns the native rendering.
func (w *Worker) buildNeutralTerminalCard(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	steps []gateway.StepInfo,
	streamingText, thinkingText, errorMessage, rawError string,
) (channel.Card, error) {
	if c.RunStatus == "failed" {
		msg := strings.TrimSpace(errorMessage)
		if msg == "" {
			msg = "Agent 运行失败，请稍后重试。"
		}
		// Guest register hint is best-effort; a read failure degrades to the
		// generic error card rather than aborting delivery.
		guestHint, err := w.store.GetGuestReplyHintForRun(ctx, c.ConversationID, c.AgentRunID)
		if err != nil {
			guestHint = ""
		}
		return ch.RenderTerminal(ctx, channel.ReplyTarget{}, channel.TerminalResult{
			Success:      false,
			Title:        c.AgentName,
			ErrorMessage: msg,
			RawError:     rawError,
			RunDetailURL: runDetailURL(w.publicURL, c.AgentRunID),
			GuestHint:    guestHint,
		})
	}

	elapsed := time.Duration(0)
	if !c.RunStartedAt.IsZero() {
		finish := c.RunFinishedAt
		if finish.IsZero() {
			finish = time.Now().UTC()
		}
		elapsed = finish.Sub(c.RunStartedAt)
	}
	// Steps + elapsed are prefilled so only usage_logs hits the DB. A read
	// failure degrades (best-effort card) rather than aborting.
	data, err := assembleDoneCardData(ctx, w.store, assembleDoneCardInput{
		WorkspaceID:       c.WorkspaceID,
		ProjectID:         c.ProjectID,
		RunID:             c.AgentRunID,
		PrefilledSteps:    steps,
		PrefilledElapsed:  elapsed,
		PrefilledThinking: thinkingText,
	})
	if err != nil {
		_ = err
	}
	title := strings.TrimSpace(data.AgentName)
	if title == "" {
		title = c.AgentName
	}
	return ch.RenderTerminal(ctx, channel.ReplyTarget{}, channel.TerminalResult{
		Success:       true,
		Title:         title,
		StreamingText: streamingText,
		Steps:         data.Steps,
		Thinking:      data.Thinking,
		Elapsed:       data.Elapsed,
		Usage:         data.Usage,
	})
}

// maybeSendNeutralChoiceCard is the ask-flow twin of
// maybeSendNeutralPermissionCard: it renders the prompt_for_user_choice form
// through the neutral channel and persists an independent choice slot via the
// exact UpsertConversationInflightPromptForUserChoiceCard optimistic-CAS the
// Feishu path uses. Idempotent on (conversation, request_id). The store
// question shape is mapped to the channel-local ChoiceQuestion so the channel
// package never imports store; option descriptions are dropped (the neutral
// form carries labels only). The deferred <at> ping follow-up is omitted.
func (w *Worker) maybeSendNeutralChoiceCard(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	target channel.ReplyTarget,
	pending *pendingAsk,
) error {
	existing := extractPromptForUserChoiceSlot(c.ConversationMetadata)
	if strings.TrimSpace(existing.RequestID) == pending.RequestID &&
		strings.TrimSpace(existing.ExternalMsgID) != "" {
		return nil
	}
	if len(pending.Questions) == 0 {
		return fmt.Errorf("build choice card: no questions in payload")
	}

	questions := make([]channel.ChoiceQuestion, 0, len(pending.Questions))
	for _, q := range pending.Questions {
		opts := make([]string, 0, len(q.Options))
		for _, opt := range q.Options {
			opts = append(opts, opt.Label)
		}
		questions = append(questions, channel.ChoiceQuestion{
			Header:      q.Header,
			Question:    q.Question,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		})
	}

	card, err := ch.RenderChoiceForm(ctx, target, channel.ChoiceForm{
		Title:     c.AgentName,
		RequestID: pending.RequestID,
		Questions: questions,
	})
	if err != nil {
		return fmt.Errorf("build choice card: %w", err)
	}

	ref, err := ch.Send(ctx, target, card)
	if err != nil {
		return fmt.Errorf("send choice card: %w", err)
	}

	next := store.PromptForUserChoiceInflightSlot{
		ExternalMsgID:    ref.ID,
		AppID:            c.SourceAppID,
		ExternalChatID:   c.ExternalChatID,
		ExternalThreadID: c.ExternalThreadID,
		AgentRunID:       c.AgentRunID,
		DeviceID:         w.resolveDeviceID(ctx, c.ConversationID),
		RequestID:        pending.RequestID,
		Questions:        pending.Questions,
	}
	if _, err := w.store.UpsertConversationInflightPromptForUserChoiceCard(ctx, store.UpsertConversationInflightPromptForUserChoiceCardInput{
		ConversationID:       c.ConversationID,
		Slot:                 next,
		ExpectedOldRequestID: strings.TrimSpace(existing.RequestID),
	}); err != nil {
		if errors.Is(err, store.ErrConversationInflightConflict) {
			w.logger.Warn("inflight: lost neutral choice-send race",
				"conversation_id", c.ConversationID, "request_id", pending.RequestID)
			return nil
		}
		return fmt.Errorf("persist choice slot: %w", err)
	}
	return nil
}

// maybeDeliverNeutralCredentialForm renders + delivers the credential-form
// terminal card for a run that finished missing capability credentials. It
// returns handled=false (with the resolve error, if any) when there is no
// recoverable form to ship, so the caller falls back to the regular terminal
// card. Once resolveCredentialFormFields stashes a durable slot, every exit
// returns handled=true and stamps the per-run terminal fingerprint — even on a
// delivery failure — so the claim filter closes and the next tick patches the
// same message instead of re-sending (mirroring the Feishu formStashed
// contract). On the first landing it stamps qkey→external_msg_id so a re-tick
// edits in place.
func (w *Worker) maybeDeliverNeutralCredentialForm(
	ctx context.Context,
	ch channel.Channel,
	c store.FeishuInflightConversation,
	prev store.WorkingInflightSlot,
	target channel.ReplyTarget,
) (bool, error) {
	fields, qkey, ok, existingMsgID, err := w.resolveCredentialFormFields(ctx, c)
	if err != nil || !ok {
		return false, err
	}

	card, err := ch.RenderCredentialForm(ctx, target, channel.CredentialForm{
		Title:  c.AgentName,
		Qkey:   qkey,
		Fields: fields,
	})
	if err != nil {
		// Slot is durable; stamp the fingerprint so the run leaves the claim
		// set (the form is recoverable via the web UI) rather than re-looping.
		if ferr := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); ferr != nil {
			return true, fmt.Errorf("mark terminal delivered fingerprint: %w", ferr)
		}
		return true, fmt.Errorf("render credential form: %w", err)
	}

	var deliveredID string
	var derr error
	switch {
	case existingMsgID != "":
		// A previous tick already landed the form — patch it in place. The
		// qkey + fields are stable so the rendered card is idempotent.
		derr = ch.Edit(ctx, target, gateway.MessageRef{ID: existingMsgID}, card)
		deliveredID = existingMsgID
	case hasWorkingSlotForRun(prev, c.AgentRunID):
		// Swap the working card for the form terminal.
		derr = ch.Edit(ctx, target, gateway.MessageRef{ID: prev.ExternalMsgID}, card)
		deliveredID = prev.ExternalMsgID
	default:
		// Short run (< 1 tick) or first landing: no message exists yet.
		ref, e := ch.Send(ctx, target, card)
		derr = e
		if e == nil {
			deliveredID = ref.ID
		}
	}
	if derr != nil {
		// Slot durable → next tick patches. Stamp the fingerprint, keep slot.
		w.logger.Warn("inflight: neutral credential form delivery failed; slot persisted for next-tick patch",
			"conversation_id", c.ConversationID, "qkey", qkey, "err", derr.Error())
		if ferr := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); ferr != nil {
			return true, fmt.Errorf("mark terminal delivered fingerprint: %w", ferr)
		}
		return true, nil
	}

	// First landing: stamp the external_msg_id onto the slot so the next tick
	// edits this same message instead of sending a sibling. NotFound means the
	// slot was swept between the stash and now — best-effort, just log.
	if existingMsgID == "" && deliveredID != "" {
		if err := w.store.UpdatePendingCredentialFormSlotMessageID(ctx, c.ConversationID, qkey, deliveredID); err != nil {
			if !errors.Is(err, store.ErrPendingCredentialFormNotFound) {
				w.logger.Warn("inflight: stamp neutral credential form external_msg_id failed",
					"conversation_id", c.ConversationID, "qkey", qkey, "err", err.Error())
			}
		}
	}
	if err := w.finalizeNeutralTerminalDelivery(ctx, c, deliveredID); err != nil {
		return true, err
	}
	return true, nil
}

// resolveCredentialFormFields is the platform-neutral store half extracted from
// the Feishu tryBuildCredentialFormCard: it lists the run's missing-credential
// notices, de-dupes them into form fields, validates a recoverable inbound
// (raw_query + sender ids + target agent, with the same loop guard against a
// re-enqueued submit), then mints a qkey and stashes the pending-form slot.
// WritePendingCredentialFormSlot is insert-or-noop, so a sibling tick's slot
// wins and we return its qkey + external_msg_id. Returns ok=false (fall back to
// the Done card) whenever there is nothing recoverable to ship. Both the Feishu
// native builder and the neutral channel render consume the returned fields.
func (w *Worker) resolveCredentialFormFields(ctx context.Context, c store.FeishuInflightConversation) ([]gateway.CredentialFormField, string, bool, string, error) {
	notices, err := w.store.ListCapabilityCredentialMissingForRun(ctx, c.ConversationID, c.AgentRunID)
	if err != nil {
		return nil, "", false, "", fmt.Errorf("list credential missing notices: %w", err)
	}
	if len(notices) == 0 {
		return nil, "", false, "", nil
	}
	// De-duplicate by (kind, capability_id).
	type fieldKey struct {
		kind       string
		capability string
	}
	seen := make(map[fieldKey]struct{})
	fields := make([]gateway.CredentialFormField, 0, len(notices))
	for _, n := range notices {
		kind := strings.TrimSpace(n.CredentialKind)
		if kind == "" {
			continue
		}
		key := fieldKey{kind: kind, capability: n.CapabilityID}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, gateway.CredentialFormField{
			Kind:           kind,
			Label:          kind,
			CapabilityName: n.CapabilityName,
		})
	}
	if len(fields) == 0 {
		return nil, "", false, "", nil
	}
	// Stash the inbound's raw_query so the submit handler can re-enqueue
	// the same turn after the user binds credentials. Without an
	// inbound, fall back to DoneCard rather than ship a form that can't
	// auto-resume.
	inbound, err := w.store.GetInboundUserMessageForRun(ctx, c.ConversationID, c.AgentRunID)
	if err != nil {
		return nil, "", false, "", fmt.Errorf("get inbound user message: %w", err)
	}
	// SenderOpenID is required because the submit callback compares it against
	// the operator id to verify the click came from the inbound's original
	// sender (without this pin ANY chat member could submit credentials on the
	// initiator's behalf). Missing it → no recoverable inbound, fall back.
	if strings.TrimSpace(inbound.RawQuery) == "" || strings.TrimSpace(inbound.SenderUserID) == "" || strings.TrimSpace(inbound.SenderOpenID) == "" {
		w.logger.Info("inflight: skip form card — no recoverable inbound for run",
			"conversation_id", c.ConversationID,
			"run_id", c.AgentRunID,
			"has_open_id", strings.TrimSpace(inbound.SenderOpenID) != "",
		)
		return nil, "", false, "", nil
	}
	// Loop guard: if the inbound was itself re-enqueued from a form submit AND
	// the resolver STILL emits a missing-credential notice, the user bound the
	// wrong / typo'd credential — a second form would re-enter the same
	// dead-end. Bail out so the user fixes it via the web UI ("我的凭据").
	if strings.TrimSpace(inbound.ReenqueuedFrom) == "credential_form_submit" {
		w.logger.Info("inflight: skip form card — turn already retried via form submit",
			"conversation_id", c.ConversationID,
			"run_id", c.AgentRunID,
		)
		return nil, "", false, "", nil
	}
	targetAgent := strings.TrimSpace(inbound.TargetAgentID)
	if targetAgent == "" {
		w.logger.Info("inflight: skip form card — inbound message has no target_agent_id",
			"conversation_id", c.ConversationID,
			"run_id", c.AgentRunID,
		)
		return nil, "", false, "", nil
	}
	// Mint a fresh qkey for the proposed stash. WritePendingCredentialFormSlot
	// is insert-or-noop: if a prior tick already stashed a slot on this
	// conversation, our payload is discarded and the existing slot wins. We use
	// the returned slot's qkey + external_msg_id so the card we build (and the
	// patch/send decision in the caller) matches the persisted state.
	proposedQkey, err := store.MintFeishuCredentialQkey()
	if err != nil {
		return nil, "", false, "", fmt.Errorf("mint qkey: %w", err)
	}
	persisted, err := w.store.WritePendingCredentialFormSlot(ctx, c.ConversationID, store.PendingCredentialFormSlot{
		Qkey:            proposedQkey,
		InitiatorOpenID: inbound.SenderOpenID,
		InitiatorUserID: inbound.SenderUserID,
		AgentID:         targetAgent,
		RawQuery:        inbound.RawQuery,
		// ExpiresAt left zero so the store sets the now+1h default.
	})
	if err != nil {
		return nil, "", false, "", fmt.Errorf("stash pending credential form: %w", err)
	}
	reused := persisted.Qkey != proposedQkey
	w.logger.Info("inflight: stash pending credential form",
		"conversation_id", c.ConversationID,
		"qkey", persisted.Qkey,
		"reused_existing_slot", reused,
		"existing_external_msg_id_present", strings.TrimSpace(persisted.ExternalMsgID) != "",
		"origin_run_id", c.AgentRunID,
		"origin_inbound_message", inbound.MessageID,
		"target_agent_id", targetAgent,
	)
	return fields, persisted.Qkey, true, strings.TrimSpace(persisted.ExternalMsgID), nil
}
