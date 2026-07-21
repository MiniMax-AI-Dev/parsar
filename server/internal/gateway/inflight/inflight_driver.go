// Package inflight: inflight-card driver.
//
// DB-poll loop: one Feishu message_id pinned per conversation, PATCHed
// as new agent_run_events arrive, then patched into the terminal
// Done/Error card on completion. Any pod may run the tick; the lock
// lives in conversations.metadata.gateway_inflight via
// expected_old_msg_id optimistic CAS.

package inflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	feishuchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/feishu"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// inflightCutoffWindow caps how far back the driver looks for
// recently-completed runs; older runs are assumed already rendered
// into their terminal card.
const inflightCutoffWindow = 5 * time.Minute

// inflightClaimStaleWindow is the cutoff past which another pod may
// steal a stalled pod's inflight claim. Must be >> tick cadence so a
// healthy pod never loses its claim, and short enough that a dead
// pod's conversations get adopted before the card looks frozen. Tuned
// together with the SQL @stale_before parameter; change both.
const inflightClaimStaleWindow = 30 * time.Second

// permissionStaleWindow is the maximum a permission card may sit
// waiting for a user click before the driver auto-Denys.
const permissionStaleWindow = 5 * time.Minute

// inflightTickBatchLimit bounds how many conversations the driver
// processes per tick so the loop stays responsive under bursty load.
const inflightTickBatchLimit int32 = 32

// agentEventsTickFetchLimit caps how many new events the driver reads
// per (conversation, tick) so a runaway run can't starve siblings.
const agentEventsTickFetchLimit int32 = 200

// InflightTickOnce runs a single pass of the inflight-card driver.
// Returns the number of conversations touched. A per-conversation
// failure logs + continues so one broken conv can't block the fleet.
func (w *Worker) InflightTickOnce(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-inflightCutoffWindow)
	staleBefore := now.Add(-inflightClaimStaleWindow)
	convs, err := w.store.ClaimActiveFeishuInflightConversations(ctx, store.ClaimActiveFeishuInflightConversationsInput{
		Platforms:      w.platforms,
		FinishedCutoff: cutoff,
		StaleBefore:    staleBefore,
		ClaimedBy:      w.claimedBy,
		Limit:          inflightTickBatchLimit,
	})
	if err != nil {
		w.logger.Warn("feishu inflight: claim active failed", "err", err.Error())
		return 0, err
	}
	for _, c := range convs {
		if err := w.handleInflightConversation(ctx, c); err != nil {
			w.logger.Warn("feishu inflight: conversation tick failed", "conversation_id", c.ConversationID, "agent_run_id", c.AgentRunID, "err", err.Error())
			continue
		}
	}

	// Stale permission cards get a forced Deny + "Timed out" patch so the
	// agent run resumes. Runs even with no active conversations above
	// — a stale permission has no driving events.
	expired := w.expireStalePermissionCards(ctx)
	return len(convs) + expired, nil
}

// expireStalePermissionCards walks any permission inflight slot past
// the stale window, forces a Deny via PermissionRouter, patches the
// card into a red "Timed out" shape, and clears the slot. Returns count.
//
// Per-slot failures log + skip. A SubmitPermission failure keeps the
// slot so the next tick retries; without that guardrail a transient
// runtime error would silently leave the agent run paused with no
// card.
func (w *Worker) expireStalePermissionCards(ctx context.Context) int {
	if w.permRouter == nil {
		return 0
	}
	cutoff := time.Now().UTC().Add(-permissionStaleWindow)
	stale, err := w.store.ListStaleFeishuPermissionInflightCards(ctx, cutoff, inflightTickBatchLimit)
	if err != nil {
		w.logger.Warn("feishu inflight: list stale permission slots failed", "err", err.Error())
		return 0
	}
	processed := 0
	for _, conv := range stale {
		if !conv.HasPermission {
			continue
		}
		processed++
		if err := w.expireOnePermissionSlot(ctx, conv); err != nil {
			w.logger.Warn("feishu inflight: expire permission slot failed",
				"conversation_id", conv.ConversationID,
				"permission_request_id", conv.Permission.PermissionRequestID,
				"err", err.Error(),
			)
			continue
		}
	}
	return processed
}

// expireOnePermissionSlot pushes a forced Deny verdict back through
// the runtime then patches + clears the card. A failed SubmitPermission
// keeps the slot so the next tick retries.
func (w *Worker) expireOnePermissionSlot(ctx context.Context, conv store.ConversationInflightCards) error {
	if err := w.permRouter.SubmitPermission(ctx, PermissionDecision{
		RequestID: conv.Permission.PermissionRequestID,
		Approved:  false,
		Note:      "auto_expired_after_" + permissionStaleWindow.String(),
	}); err != nil {
		return fmt.Errorf("submit deny: %w", err)
	}
	creds, err := w.resolveCredentials(ctx, gateway.PendingOutboundMessage{
		WorkspaceID: conv.WorkspaceID,
		SourceAppID: pickAppID(conv.Permission.AppID, conv.SourceAppID),
	})
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	client, err := w.clientFor(conv.WorkspaceID, creds.AppID)
	if err != nil {
		return fmt.Errorf("client for workspace: %w", err)
	}
	// Resolve the conversation's currently-selected Agent so the
	// timeout card carries the right per-Agent label. The auto-expire
	// path doesn't carry a claim row, so ask the store. Lookup error
	// is non-fatal — empty title falls back to FeishuCardTitle.
	title, lookupErr := w.store.ResolveAgentNameForConversation(ctx, conv.ConversationID)
	if lookupErr != nil {
		w.logger.Warn("feishu inflight: resolve agent name for expired permission failed",
			"conversation_id", conv.ConversationID,
			"err", lookupErr.Error(),
		)
		title = ""
	}
	content, err := gateway.MarshalCard(gateway.BuildNoticeCard(
		title, "**Timed out**\n\nNo response after 5 minutes, auto-denied.",
		gateway.NoticeColorWarning,
	))
	if err != nil {
		return fmt.Errorf("build timeout card: %w", err)
	}
	if msgID := strings.TrimSpace(conv.Permission.ExternalMsgID); msgID != "" {
		if err := client.PatchMessage(ctx, creds.AppSecret, msgID, content); err != nil {
			// Non-fatal: verdict already landed and we still want to clear the slot.
			w.logger.Warn("feishu inflight: patch expired card failed",
				"conversation_id", conv.ConversationID,
				"err", err.Error(),
			)
		}
	}
	if err := w.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPermission, conv.Permission.AgentRunID); err != nil {
		w.logger.Warn("feishu inflight: clear expired slot failed",
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
	return nil
}

// pickAppID returns the first non-empty trimmed value.
func pickAppID(primary, fallback string) string {
	if v := strings.TrimSpace(primary); v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

// handleInflightConversation processes one conversation's outstanding
// event delta: read slot, fetch events past seq_emitted, fold into
// (steps, streaming text), then either send/patch the working card or
// — on terminal — render Done/Error, patch, and clear the slot.
func (w *Worker) handleInflightConversation(ctx context.Context, c store.FeishuInflightConversation) error {
	// Non-Feishu conversations take the isolated neutral path (Slack today).
	// Branching here keeps the Feishu terminal hot path below byte-for-byte
	// unchanged — it carries multiple prod-incident patches we do not want to
	// disturb. Empty platform is treated as Feishu (legacy rows predate the
	// column's universal population).
	if c.Platform != "" && c.Platform != string(channel.PlatformFeishu) {
		return w.handleNeutralInflightConversation(ctx, c)
	}
	if strings.TrimSpace(c.SourceAppID) == "" {
		// Shouldn't be reachable for a Feishu-platform conversation;
		// guard anyway — we'd loop forever otherwise.
		return fmt.Errorf("inflight conversation has empty source_app_id")
	}
	// Pull the slot from the metadata blob the list query returned,
	// saving a second SELECT per conversation.
	prev := extractWorkingSlot(c.ConversationMetadata)
	// When the slot points at a previous run, keep its AgentRunID so
	// the Upsert's CAS can claim it in one shot, but drop the rest —
	// otherwise the new run inherits stale steps/streaming text (e.g.
	// the cancelled previous run's "AskUserQuestion" step) AND the new
	// run's events start at sequence 1 < prev.SeqEmitted, so
	// ListAgentRunEventsAfterSeq filters them out and the card never
	// gets a body.
	if !hasWorkingSlotForRun(prev, c.AgentRunID) {
		prev = store.WorkingInflightSlot{AgentRunID: prev.AgentRunID}
	}

	// The driver needs the full step list on the initial send, so we
	// preserve prev.Payload's steps and append new ones below.
	events, err := w.store.ListAgentRunEventsAfterSeq(ctx, c.AgentRunID, prev.SeqEmitted, agentEventsTickFetchLimit)
	if err != nil {
		return fmt.Errorf("list agent_run_events: %w", err)
	}
	steps, streamingText, thinkingText, newSeq, pendingPerm, errorMessage, rawError := foldEventsIntoCardState(prev, events)
	isCompleted := c.RunStatus == "completed"
	isFailed := c.RunStatus == "failed"
	if newSeq == 0 && !isCompleted && !isFailed && !hasWorkingSlotForRun(prev, c.AgentRunID) {
		// Nothing new and no card yet — defer. The list query's
		// max_event_sequence > seq_emitted filter normally prevents
		// this, but the race window between SELECT and our read is real.
		return nil
	}

	creds, err := w.resolveCredentials(ctx, gateway.PendingOutboundMessage{
		WorkspaceID: c.WorkspaceID,
		SourceAppID: c.SourceAppID,
	})
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	client, err := w.clientFor(c.WorkspaceID, creds.AppID)
	if err != nil {
		return fmt.Errorf("client for workspace: %w", err)
	}

	// Per-conversation neutral Channel handle. Both the terminal card
	// (RenderTerminal, via buildFinalCardForRun) and the mid-run working
	// card (RenderProgress + Send/Edit) render through this single adapter
	// instance so card construction lives in one place. The handle is a
	// thin value over the worker's shared transport — cheap to mint here.
	ch := w.feishuChannel(c.SourceAppID)
	target := channel.ReplyTarget{ExternalChatID: c.ExternalChatID, ExternalThreadID: c.ExternalThreadID}

	// Terminal: render final card and either patch (we own the
	// message_id) or send (short run that finished before any working
	// card landed — we take responsibility so the user sees exactly
	// one card per query).
	if isCompleted || isFailed {
		// When soft-degrade emitted credential gaps for this run, the
		// terminal card is the orange credential-form card (embeds a
		// qkey so the submit callback re-enqueues raw_query), not the
		// regular DoneCard. tryBuildCredentialFormCard reconciles the
		// pending-form slot so repeat ticks reuse the same qkey +
		// Feishu om_… instead of churning new cards.
		formContent, formQkey, formed, formExistingMsgID, formErr := w.tryBuildCredentialFormCard(ctx, c)
		if formErr != nil {
			w.logger.Warn("feishu inflight: build credential form card failed; falling back to regular terminal card",
				"conversation_id", c.ConversationID,
				"run_id", c.AgentRunID,
				"err", formErr.Error(),
			)
		}
		finalContent := ""
		if formed {
			finalContent = formContent
		} else {
			fc, buildErr := buildFinalCardForRun(ctx, ch, w.store, c, steps, streamingText, thinkingText, errorMessage, rawError, w.publicURL)
			if buildErr != nil {
				return fmt.Errorf("build terminal card: %w", buildErr)
			}
			finalContent = fc
		}
		// formStashed is true once the slot is durable: either it just
		// landed via WritePendingCredentialFormSlot, or it was already
		// there from a sibling tick. In both cases we OWE the run a
		// terminal-delivered fingerprint even if the Feishu Send/Patch
		// fails — the slot is what tells the next tick to skip; without
		// the fingerprint, ClaimActiveFeishuInflightConversations re-picks
		// this run and tryBuildCredentialFormCard runs again, finds the
		// same slot (good), but spam-logs the stash. Stamping early
		// keeps the claim filter closed.
		formStashed := formed && formErr == nil
		var deliveredID string
		switch {
		case formed && formExistingMsgID != "":
			// Patch path: a previous tick (this run or a sibling) already
			// landed the card. Same qkey + same fields → same content
			// bytes (BuildCredentialFormCard is pure), so PatchMessage is
			// idempotent. Permanent failures (past Feishu's 24h edit
			// window) clear the slot so the next tick can re-stash and
			// send fresh. Transient failures still stamp the fingerprint
			// so the run leaves the claim set — the slot stays put and
			// the next tick will try the same patch again.
			if err := client.PatchMessage(ctx, creds.AppSecret, formExistingMsgID, finalContent); err != nil {
				permanent := errors.Is(err, gateway.ErrFeishuNon2xx)
				if permanent {
					w.logger.Warn("feishu inflight: patch credential form card failed permanently; clearing slot for next-tick reissue",
						"conversation_id", c.ConversationID,
						"run_id", c.AgentRunID,
						"external_msg_id", formExistingMsgID,
						"err", err.Error(),
					)
					if clearErr := w.store.ClearPendingCredentialFormSlotByConversation(ctx, c.ConversationID); clearErr != nil {
						w.logger.Warn("feishu inflight: clear credential form slot after permanent patch failure errored",
							"conversation_id", c.ConversationID,
							"err", clearErr.Error(),
						)
					}
				} else {
					w.logger.Warn("feishu inflight: patch credential form card transient failure; slot retained for next-tick retry",
						"conversation_id", c.ConversationID,
						"run_id", c.AgentRunID,
						"external_msg_id", formExistingMsgID,
						"err", err.Error(),
					)
				}
				if err := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); err != nil {
					return fmt.Errorf("mark terminal delivered fingerprint: %w", err)
				}
				return nil
			}
			deliveredID = formExistingMsgID
		case hasWorkingSlotForRun(prev, c.AgentRunID):
			if err := client.PatchMessage(ctx, creds.AppSecret, prev.ExternalMsgID, finalContent); err != nil {
				return w.handleUpstreamWorkingFailure(ctx, c, prev, "patch terminal card", err)
			}
			deliveredID = prev.ExternalMsgID
		default:
			// Short run (< 1 tick) OR first time landing a credential
			// form card: no message exists in Feishu yet. We own the
			// send — exactly one card per user query.
			anchor := strings.TrimSpace(c.ExternalThreadID)
			var sendErr error
			if anchor != "" {
				res, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
					MsgType:       "interactive",
					Content:       finalContent,
					ReplyInThread: true,
				})
				sendErr = err
				if err == nil {
					deliveredID = res.MessageID
				}
			} else {
				res, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
					ReceiveIDType: "chat_id",
					ReceiveID:     c.ExternalChatID,
					MsgType:       "interactive",
					Content:       finalContent,
				})
				sendErr = err
				if err == nil {
					deliveredID = res.MessageID
				}
			}
			if sendErr != nil {
				stage := "send terminal card"
				if anchor != "" {
					stage = "send terminal card (reply)"
				}
				// Form-card path: the slot is durable, so the next tick
				// will pick up the same qkey and PATCH instead of SEND.
				// We MUST stamp the fingerprint here so the claim filter
				// closes — without it, the run gets re-claimed every
				// tick and we either spam Send attempts (if the upstream
				// is genuinely flapping) or patch repeatedly (harmless
				// but noisy). The non-form path keeps the old
				// dead-letter retry behaviour via handleUpstreamWorkingFailure.
				if formStashed {
					w.logger.Warn("feishu inflight: credential form first-send failed; slot persisted, next tick will patch",
						"conversation_id", c.ConversationID,
						"run_id", c.AgentRunID,
						"stage", stage,
						"err", sendErr.Error(),
					)
					if err := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); err != nil {
						return fmt.Errorf("mark terminal delivered fingerprint: %w", err)
					}
					return nil
				}
				return w.handleUpstreamWorkingFailure(ctx, c, prev, stage, sendErr)
			}
			// First send of a credential-form card: stamp the om_… onto
			// the slot so the NEXT tick patches this same message
			// instead of sending a sibling card. NotFound means the slot
			// was claimed/swept between WritePendingCredentialFormSlot
			// and now — best-effort, just log.
			if formed && deliveredID != "" {
				if err := w.store.UpdatePendingCredentialFormSlotMessageID(ctx, c.ConversationID, formQkey, deliveredID); err != nil {
					if errors.Is(err, store.ErrPendingCredentialFormNotFound) {
						w.logger.Info("feishu inflight: credential form slot gone before external_msg_id stamp",
							"conversation_id", c.ConversationID,
							"qkey", formQkey,
						)
					} else {
						w.logger.Warn("feishu inflight: stamp credential form external_msg_id failed",
							"conversation_id", c.ConversationID,
							"qkey", formQkey,
							"err", err.Error(),
						)
					}
				}
			}
		}
		// MarkDelivered stamps the messages row so the claim query's
		// terminal-delivery filter (keyed off gateway_delivered_at) skips
		// this run going forward. OutputMessageID being empty is normal
		// for runs that failed pre-output (FailAgentRun); the
		// conversation-row fingerprint below covers that path.
		if strings.TrimSpace(c.OutputMessageID) != "" && deliveredID != "" {
			if _, err := w.store.MarkGatewayOutboundDelivered(ctx, store.MarkGatewayOutboundDeliveredInput{
				MessageID: c.OutputMessageID,
			}); err != nil {
				return fmt.Errorf("mark terminal delivered: %w", err)
			}
		}
		// Per-run fingerprint on the conversation row. Stamped whenever
		// we have anything durable to point at — a delivered Feishu
		// message OR a persisted credential-form slot. The slot case is
		// the critical fix: a transient SendMessage failure used to skip
		// this stamp, leaving the run re-claimable on every subsequent
		// tick (3-card spam observed in prod 2026-06-18). With the slot
		// stamped early, the next tick sees terminal_delivered = run_id
		// and exits the claim set; if the user submits via the slot's
		// qkey, ClaimPendingCredentialFormSlot consumes it and the
		// re-fired inbound starts a new run with its own fingerprint.
		if deliveredID != "" || formStashed {
			if err := w.store.MarkConversationInflightTerminalDelivered(ctx, c.ConversationID, c.AgentRunID); err != nil {
				return fmt.Errorf("mark terminal delivered fingerprint: %w", err)
			}
		}
		// At-mention text follow-up. Both terminal markers above have
		// landed, so the per-run idempotency gate is closed — ping
		// fires at most once per run. Best-effort: sendUserPingText
		// logs and swallows so a flaky ping doesn't reopen the gate.
		if deliveredID != "" {
			var pingMessage string
			switch {
			case formed:
				pingMessage = UserPingCredentialForm
			case isFailed:
				pingMessage = UserPingRunFailed
			default:
				elapsed := time.Duration(0)
				if !c.RunStartedAt.IsZero() {
					finish := c.RunFinishedAt
					if finish.IsZero() {
						finish = time.Now().UTC()
					}
					elapsed = finish.Sub(c.RunStartedAt)
				}
				pingMessage = terminalPingMessage(elapsed)
			}
			w.sendUserPingText(ctx, c, creds, client, pingMessage)
		}
		// ClearSlot is best-effort: with the delivery marker in place
		// above, the next tick won't re-pick this run even if the slot
		// lingers (claim query short-circuits on gateway_delivered_at).
		if err := w.store.ClearConversationInflightSlot(ctx, c.ConversationID, store.InflightSlotWorking, c.AgentRunID); err != nil {
			w.logger.Warn("feishu inflight: clear slot after terminal patch failed", "conversation_id", c.ConversationID, "err", err.Error())
		}
		// Clear the inbound typing-reaction emoji. Best-effort.
		w.asyncClearTypingReaction(c.ConversationID, c.WorkspaceID, c.SourceAppID, c.AgentRunID)
		return nil
	}

	// Mid-run: render working/streaming card; send (first time) or patch.
	// Routed through the neutral Channel (RenderProgress + Send/Edit) so the
	// driver no longer owns the Feishu card-build / IM-call inline. The
	// per-conv Channel (ch) + target were minted above so the terminal path
	// shares the same renderer; permission / user-choice / credential-form
	// cards stay on the raw client until the neutral contract grows seams for
	// them (PR #3c).
	now := time.Now().UTC()
	elapsed := time.Duration(0)
	if !c.RunStartedAt.IsZero() {
		elapsed = now.Sub(c.RunStartedAt)
	}
	card, buildErr := ch.RenderProgress(ctx, target, channel.ProgressState{
		Title:         c.AgentName,
		Steps:         steps,
		StreamingText: streamingText,
		Elapsed:       elapsed,
		Now:           now,
	})
	if buildErr != nil {
		return fmt.Errorf("build mid-run card: %w", buildErr)
	}
	if !hasWorkingSlotForRun(prev, c.AgentRunID) {
		// First send — Channel.Send replies in-thread when a thread anchor is
		// present, else posts to the chat. Capture the message_id into the
		// slot so next tick patches. The reply-vs-chat stage label is derived
		// here only to preserve the dead-letter audit string.
		stage := "send working card"
		if strings.TrimSpace(c.ExternalThreadID) != "" {
			stage = "send working card (reply)"
		}
		ref, err := ch.Send(ctx, target, card)
		if err != nil {
			return w.handleUpstreamWorkingFailure(ctx, c, prev, stage, err)
		}
		deliveredID := ref.ID
		next := store.WorkingInflightSlot{
			ExternalMsgID:    deliveredID,
			AppID:            creds.AppID,
			ExternalChatID:   c.ExternalChatID,
			ExternalThreadID: c.ExternalThreadID,
			AgentRunID:       c.AgentRunID,
			SeqEmitted:       latestSeq(events, prev.SeqEmitted),
			Payload:          slotPayload(steps, streamingText, thinkingText, errorMessage, rawError),
		}
		// Zero out leftover retry state from a previous failed attempt.
		next = zeroRetryWorking(next)
		if _, err := w.store.UpsertConversationInflightWorkingCard(ctx, store.UpsertConversationInflightWorkingCardInput{
			ConversationID: c.ConversationID,
			Slot:           next,
			// prev.AgentRunID, not "": covers conv carrying a stale slot
			// from a previous run that was never cleared.
			ExpectedOldRunID: prev.AgentRunID,
		}); err != nil {
			if errors.Is(err, store.ErrConversationInflightConflict) {
				// Another tick beat us — the card we just sent is now
				// an orphan. Next tick resyncs from the winning slot.
				w.logger.Warn("feishu inflight: lost first-send race", "conversation_id", c.ConversationID, "external_msg_id", deliveredID)
				return nil
			}
			return fmt.Errorf("persist initial inflight slot: %w", err)
		}
		// Fall through to permission handling — a permission.asked may
		// have landed in the same tick as the first tool.call.
	} else {
		// Subsequent ticks: PATCH the existing message_id via Channel.Edit.
		if err := ch.Edit(ctx, target, gateway.MessageRef{ID: prev.ExternalMsgID}, card); err != nil {
			return w.handleUpstreamWorkingFailure(ctx, c, prev, "patch working card", err)
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
				w.logger.Warn("feishu inflight: optimistic-lock conflict on patch", "conversation_id", c.ConversationID, "external_msg_id", prev.ExternalMsgID)
				return nil
			}
			return fmt.Errorf("persist patched inflight slot: %w", err)
		}
	}

	// If this tick observed a new permission.asked AND no permission
	// card is pinned yet, send one alongside the working card. The
	// two cards are independent slots.
	if pendingPerm != nil {
		if err := w.maybeSendPermissionCard(ctx, c, creds, client, pendingPerm); err != nil {
			// Log + continue: the next tick can retry because
			// permission.asked stays in agent_run_events.
			w.logger.Warn("feishu inflight: permission card send failed",
				"conversation_id", c.ConversationID,
				"permission_request_id", pendingPerm.RequestID,
				"err", err.Error(),
			)
		}
	}

	// Same shape for prompt_for_user_choice — independent slot, same
	// fail-soft policy as permission. We walk events ONCE per tick for
	// each slot type rather than threading another return value through
	// foldEventsIntoCardState; the cost is one extra range over a
	// small slice (typically <50 events per tick).
	if pendingAsk := extractPendingAskFromEvents(events); pendingAsk != nil {
		if err := w.maybeSendPromptForUserChoiceCard(ctx, c, creds, client, pendingAsk); err != nil {
			w.logger.Warn("feishu inflight: prompt_for_user_choice card send failed",
				"conversation_id", c.ConversationID,
				"request_id", pendingAsk.RequestID,
				"err", err.Error(),
			)
		}
	}
	return nil
}

// maybeSendPromptForUserChoiceCard is the ask-flow twin of
// maybeSendPermissionCard: idempotent on (conversation, request_id),
// independent inflight slot from the working / permission slots.
func (w *Worker) maybeSendPromptForUserChoiceCard(
	ctx context.Context,
	c store.FeishuInflightConversation,
	creds gateway.OutboundCredentials,
	client *gateway.FeishuTenantClient,
	pending *pendingAsk,
) error {
	existing := extractPromptForUserChoiceSlot(c.ConversationMetadata)
	if strings.TrimSpace(existing.RequestID) == pending.RequestID &&
		strings.TrimSpace(existing.ExternalMsgID) != "" {
		return nil
	}

	if len(pending.Questions) == 0 {
		return fmt.Errorf("build prompt_for_user_choice card: no questions in payload")
	}

	cardQuestions := make([]gateway.PromptForUserChoiceCardQuestion, 0, len(pending.Questions))
	for _, q := range pending.Questions {
		opts := make([]gateway.PromptForUserChoiceCardOption, 0, len(q.Options))
		for _, opt := range q.Options {
			opts = append(opts, gateway.PromptForUserChoiceCardOption{
				Label:       opt.Label,
				Description: opt.Description,
			})
		}
		cardQuestions = append(cardQuestions, gateway.PromptForUserChoiceCardQuestion{
			Header:      q.Header,
			Question:    q.Question,
			MultiSelect: q.MultiSelect,
			Options:     opts,
		})
	}
	cardContent, err := gateway.BuildFeishuPromptForUserChoiceCardContent(
		c.AgentName, cardQuestions, pending.RequestID,
	)
	if err != nil {
		return fmt.Errorf("build prompt_for_user_choice card: %w", err)
	}

	var deliveredID string
	if anchor := strings.TrimSpace(c.ExternalThreadID); anchor != "" {
		res, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       cardContent,
			ReplyInThread: true,
		})
		if err != nil {
			return fmt.Errorf("send prompt_for_user_choice card (reply): %w", err)
		}
		deliveredID = res.MessageID
	} else {
		res, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     c.ExternalChatID,
			MsgType:       "interactive",
			Content:       cardContent,
		})
		if err != nil {
			return fmt.Errorf("send prompt_for_user_choice card: %w", err)
		}
		deliveredID = res.MessageID
	}

	next := store.PromptForUserChoiceInflightSlot{
		ExternalMsgID:    deliveredID,
		AppID:            creds.AppID,
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
			w.logger.Warn("feishu inflight: lost prompt_for_user_choice send race",
				"conversation_id", c.ConversationID,
				"request_id", pending.RequestID,
			)
			return nil
		}
		return fmt.Errorf("persist prompt_for_user_choice slot: %w", err)
	}
	// Same "please confirm ↑" follow-up shape as the permission card.
	w.sendUserPingText(ctx, c, creds, client, UserPingPromptForUserChoice)
	return nil
}

// maybeSendPermissionCard handles the permission inflight slot.
// Idempotent on (conversation, request_id) — repeat calls with the
// same request_id are no-ops. A racing second permission ask
// supersedes the existing slot.
func (w *Worker) maybeSendPermissionCard(
	ctx context.Context,
	c store.FeishuInflightConversation,
	creds gateway.OutboundCredentials,
	client *gateway.FeishuTenantClient,
	pending *pendingPermission,
) error {
	existing := extractPermissionSlot(c.ConversationMetadata)
	if strings.TrimSpace(existing.PermissionRequestID) == pending.RequestID &&
		strings.TrimSpace(existing.ExternalMsgID) != "" {
		return nil
	}
	cardContent, err := gateway.BuildFeishuPermissionCardContent(c.AgentName, pending.ToolName, pending.ToolInput, pending.RequestID)
	if err != nil {
		return fmt.Errorf("build permission card: %w", err)
	}
	var deliveredID string
	if anchor := strings.TrimSpace(c.ExternalThreadID); anchor != "" {
		res, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       cardContent,
			ReplyInThread: true,
		})
		if err != nil {
			return w.handleUpstreamPermissionFailure(ctx, c, existing, "send permission card (reply)", err)
		}
		deliveredID = res.MessageID
	} else {
		res, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     c.ExternalChatID,
			MsgType:       "interactive",
			Content:       cardContent,
		})
		if err != nil {
			return w.handleUpstreamPermissionFailure(ctx, c, existing, "send permission card", err)
		}
		deliveredID = res.MessageID
	}
	next := store.PermissionInflightSlot{
		ExternalMsgID:       deliveredID,
		AppID:               creds.AppID,
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
	// Clear any leftover retry state — the send just succeeded.
	next = zeroRetryPermission(next)
	if _, err := w.store.UpsertConversationInflightPermissionCard(ctx, store.UpsertConversationInflightPermissionCardInput{
		ConversationID:       c.ConversationID,
		Slot:                 next,
		ExpectedOldRequestID: strings.TrimSpace(existing.PermissionRequestID),
	}); err != nil {
		if errors.Is(err, store.ErrConversationInflightConflict) {
			w.logger.Warn("feishu inflight: lost permission-send race",
				"conversation_id", c.ConversationID,
				"permission_request_id", pending.RequestID,
			)
			return nil
		}
		return fmt.Errorf("persist permission slot: %w", err)
	}
	// At-mention text follow-up. Idempotency comes from the request_id
	// guard at the top of this function. Best-effort.
	w.sendUserPingText(ctx, c, creds, client, UserPingPermission)
	return nil
}

// ----- card-state folding -----

func extractWorkingSlot(metadata map[string]any) store.WorkingInflightSlot {
	if metadata == nil {
		return store.WorkingInflightSlot{}
	}
	inflight, ok := metadata["gateway_inflight"].(map[string]any)
	if !ok {
		return store.WorkingInflightSlot{}
	}
	working, ok := inflight["working"].(map[string]any)
	if !ok {
		return store.WorkingInflightSlot{}
	}
	var slot store.WorkingInflightSlot
	if v, ok := working["external_msg_id"].(string); ok {
		slot.ExternalMsgID = v
	}
	if v, ok := working["app_id"].(string); ok {
		slot.AppID = v
	}
	if v, ok := working["external_chat_id"].(string); ok {
		slot.ExternalChatID = v
	}
	if v, ok := working["external_thread_id"].(string); ok {
		slot.ExternalThreadID = v
	}
	if v, ok := working["agent_run_id"].(string); ok {
		slot.AgentRunID = v
	}
	if v, ok := working["seq_emitted"].(float64); ok {
		slot.SeqEmitted = int64(v)
	}
	if v, ok := working["payload"].(map[string]any); ok {
		slot.Payload = v
	}
	if v, ok := working["attempts"].(float64); ok {
		slot.Attempts = int(v)
	}
	if v, ok := working["last_error"].(string); ok {
		slot.LastError = v
	}
	if v, ok := working["next_retry_at"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			slot.NextRetryAt = t.UTC()
		}
	}
	return slot
}

// hasWorkingSlotForRun reports whether the slot has a Feishu
// message_id pinned AND that pin belongs to currentRunID. A slot
// owned by a previous run reads as false so the driver enters
// first-send for the new run.
func hasWorkingSlotForRun(slot store.WorkingInflightSlot, currentRunID string) bool {
	if strings.TrimSpace(slot.ExternalMsgID) == "" {
		return false
	}
	return strings.TrimSpace(slot.AgentRunID) == strings.TrimSpace(currentRunID)
}

func extractPermissionSlot(metadata map[string]any) store.PermissionInflightSlot {
	if metadata == nil {
		return store.PermissionInflightSlot{}
	}
	inflight, ok := metadata["gateway_inflight"].(map[string]any)
	if !ok {
		return store.PermissionInflightSlot{}
	}
	perm, ok := inflight["permission"].(map[string]any)
	if !ok {
		return store.PermissionInflightSlot{}
	}
	var slot store.PermissionInflightSlot
	if v, ok := perm["external_msg_id"].(string); ok {
		slot.ExternalMsgID = v
	}
	if v, ok := perm["app_id"].(string); ok {
		slot.AppID = v
	}
	if v, ok := perm["external_chat_id"].(string); ok {
		slot.ExternalChatID = v
	}
	if v, ok := perm["external_thread_id"].(string); ok {
		slot.ExternalThreadID = v
	}
	if v, ok := perm["agent_run_id"].(string); ok {
		slot.AgentRunID = v
	}
	if v, ok := perm["permission_request_id"].(string); ok {
		slot.PermissionRequestID = v
	}
	if v, ok := perm["payload"].(map[string]any); ok {
		slot.Payload = v
	}
	if v, ok := perm["updated_at"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			slot.UpdatedAt = t
		}
	}
	if v, ok := perm["attempts"].(float64); ok {
		slot.Attempts = int(v)
	}
	if v, ok := perm["last_error"].(string); ok {
		slot.LastError = v
	}
	if v, ok := perm["next_retry_at"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			slot.NextRetryAt = t.UTC()
		}
	}
	return slot
}

// extractPromptForUserChoiceSlot pulls the ask-flow slot off the
// conversation metadata. Uses round-trip JSON instead of the field-by-
// field hand-roll because the slot struct is larger (Options is a
// slice of structs) — fewer fingers, fewer typos.
func extractPromptForUserChoiceSlot(metadata map[string]any) store.PromptForUserChoiceInflightSlot {
	if metadata == nil {
		return store.PromptForUserChoiceInflightSlot{}
	}
	inflight, ok := metadata["gateway_inflight"].(map[string]any)
	if !ok {
		return store.PromptForUserChoiceInflightSlot{}
	}
	raw, ok := inflight["prompt_for_user_choice"]
	if !ok || raw == nil {
		return store.PromptForUserChoiceInflightSlot{}
	}
	var slot store.PromptForUserChoiceInflightSlot
	data, err := json.Marshal(raw)
	if err != nil {
		return store.PromptForUserChoiceInflightSlot{}
	}
	_ = json.Unmarshal(data, &slot)
	return slot
}

// foldEventsIntoCardState walks new events in sequence order and
// produces the card-renderer tuple.
//
// Steps come from "tool.call" only (we ignore "tool.result" so the
// card shows the currently-running tool). Streaming text accumulates
// from "message.delta". Thinking text accumulates from
// "message.thinking" but stays separate — renderers must keep it
// behind a disclosure, not splice it into the reply body.
//
// The fold preserves prev.Payload contents so patches don't lose
// history when a tick consumes only one new event.
func foldEventsIntoCardState(prev store.WorkingInflightSlot, events []store.AgentRunEvent) ([]gateway.StepInfo, string, string, int64, *pendingPermission, string, string) {
	steps := stepsFromPayload(prev.Payload)
	streamingText := streamingTextFromPayload(prev.Payload)
	thinkingText := thinkingTextFromPayload(prev.Payload)
	errorMessage := errorMessageFromPayload(prev.Payload)
	rawError := rawErrorFromPayload(prev.Payload)
	newSeq := prev.SeqEmitted
	var perm *pendingPermission
	for _, ev := range events {
		if ev.Sequence > newSeq {
			newSeq = ev.Sequence
		}
		switch ev.EventKind {
		case "tool.call":
			if step, ok := stepFromToolCallPayload(ev.Payload, ev.OccurredAt); ok {
				steps = append(steps, step)
			}
		case "tool.result":
			// Pair with the matching tool.call by id and backfill
			// EndedAt. Walk in reverse — the result almost always
			// belongs to one of the last few calls, so the loop exits
			// fast. No-op when id is missing (defensive) or no call
			// matches (e.g. the call landed in a prior run that the
			// slot was cleared from).
			id, _ := ev.Payload["id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].ID == id && steps[i].EndedAt.IsZero() {
					steps[i].EndedAt = ev.OccurredAt
					break
				}
			}
		case "message.delta":
			if delta, ok := ev.Payload["delta"].(string); ok && delta != "" {
				streamingText += delta
			}
		case "message.thinking":
			if t, ok := ev.Payload["thinking"].(string); ok && t != "" {
				thinkingText += t
			}
		case "permission.asked":
			// Multiple permission.asked events in one tick would be a
			// bug upstream; we tolerate by keeping the latest.
			if p, ok := pendingPermissionFromPayload(ev.Payload); ok {
				perm = p
			}
		case "run.failed":
			// Prefer user_visible_message; fall back to raw error.
			// Empty falls through to the renderer's generic copy.
			if v, ok := ev.Payload["user_visible_message"].(string); ok && strings.TrimSpace(v) != "" {
				errorMessage = strings.TrimSpace(v)
			} else if v, ok := ev.Payload["error"].(string); ok && strings.TrimSpace(v) != "" {
				errorMessage = strings.TrimSpace(v)
			}
			// Stash the un-mapped error separately so the terminal card
			// can append it under the generic mapped copy without
			// re-deriving it from prev.Payload on the next tick.
			if v, ok := ev.Payload["error"].(string); ok && strings.TrimSpace(v) != "" {
				rawError = strings.TrimSpace(v)
			}
		}
	}
	return steps, streamingText, thinkingText, newSeq, perm, errorMessage, rawError
}

func errorMessageFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	v, _ := payload["error_message"].(string)
	return strings.TrimSpace(v)
}

func rawErrorFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	v, _ := payload["raw_error"].(string)
	return strings.TrimSpace(v)
}

// runDetailURL builds the absolute URL to the run-detail page
// ("/?admin=runs&id=<run_id>"); empty string when either piece is
// missing, which BuildFeishuErrorCardContent treats as "skip the link".
func runDetailURL(publicURL, runID string) string {
	base := strings.TrimSpace(publicURL)
	id := strings.TrimSpace(runID)
	if base == "" || id == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/?admin=runs&id=" + id
}

// pendingPermission carries the minimum fields the driver needs to
// render and route a permission approval.
type pendingPermission struct {
	RequestID string
	ToolName  string
	ToolInput string
}

// pendingAsk is the ask-flow twin of pendingPermission: the minimum
// snapshot the outbound driver needs to render a
// prompt_for_user_choice card and to write the inflight slot.
type pendingAsk struct {
	RequestID string
	Questions []store.PromptForUserChoiceQuestion
	ToolUseID string
}

// extractPendingAskFromEvents walks new events for the latest
// prompt_for_user_choice.asked. Mirrors the permission.asked branch in
// foldEventsIntoCardState but lives outside the fold so the existing
// fold signature doesn't churn.
//
// Multiple ask events in one tick keep the latest — same tolerance as
// permission. Returns nil when no qualifying event is in the batch.
func extractPendingAskFromEvents(events []store.AgentRunEvent) *pendingAsk {
	var out *pendingAsk
	for _, ev := range events {
		if ev.EventKind != "prompt_for_user_choice.asked" {
			continue
		}
		if p, ok := pendingAskFromPayload(ev.Payload); ok {
			out = p
		}
	}
	return out
}

func pendingAskFromPayload(payload map[string]any) (*pendingAsk, bool) {
	if payload == nil {
		return nil, false
	}
	requestID, _ := payload["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, false
	}
	toolUseID, _ := payload["tool_use_id"].(string)

	// New multi-question shape — preferred when present.
	questions := decodePromptForUserChoiceQuestionList(payload["questions"])

	// Fall back to the legacy single-question fields. Old runs that
	// emitted prompt_for_user_choice.asked before this version stamped
	// only these.
	if len(questions) == 0 {
		q, _ := payload["question"].(string)
		header, _ := payload["header"].(string)
		multiSelect, _ := payload["multi_select"].(bool)
		opts := decodePromptForUserChoiceOptionList(payload["options"])
		if strings.TrimSpace(q) == "" && len(opts) == 0 {
			return nil, false
		}
		questions = []store.PromptForUserChoiceQuestion{{
			Header:      strings.TrimSpace(header),
			Question:    strings.TrimSpace(q),
			MultiSelect: multiSelect,
			Options:     opts,
		}}
	}

	return &pendingAsk{
		RequestID: requestID,
		Questions: questions,
		ToolUseID: strings.TrimSpace(toolUseID),
	}, true
}

// decodePromptForUserChoiceQuestionList parses payload["questions"]
// (the new multi-question shape) into typed store records.
func decodePromptForUserChoiceQuestionList(raw any) []store.PromptForUserChoiceQuestion {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	out := make([]store.PromptForUserChoiceQuestion, 0, len(list))
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		q, _ := m["question"].(string)
		id, _ := m["id"].(string)
		header, _ := m["header"].(string)
		multiSelect, _ := m["multi_select"].(bool)
		opts := decodePromptForUserChoiceOptionList(m["options"])
		if strings.TrimSpace(q) == "" && len(opts) == 0 {
			continue
		}
		out = append(out, store.PromptForUserChoiceQuestion{
			ID:          strings.TrimSpace(id),
			Header:      strings.TrimSpace(header),
			Question:    strings.TrimSpace(q),
			MultiSelect: multiSelect,
			Options:     opts,
		})
	}
	return out
}

func decodePromptForUserChoiceOptionList(raw any) []store.PromptForUserChoiceOption {
	rawOptions, _ := raw.([]any)
	options := make([]store.PromptForUserChoiceOption, 0, len(rawOptions))
	for _, opt := range rawOptions {
		om, ok := opt.(map[string]any)
		if !ok {
			continue
		}
		label, _ := om["label"].(string)
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		description, _ := om["description"].(string)
		options = append(options, store.PromptForUserChoiceOption{
			Label:       label,
			Description: description,
		})
	}
	return options
}

func pendingPermissionFromPayload(payload map[string]any) (*pendingPermission, bool) {
	if payload == nil {
		return nil, false
	}
	requestID, _ := payload["request_id"].(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, false
	}
	out := &pendingPermission{RequestID: requestID}
	if v, ok := payload["action"].(string); ok {
		out.ToolName = strings.TrimSpace(v)
	}
	if out.ToolName == "" {
		if v, ok := payload["resource"].(string); ok {
			out.ToolName = strings.TrimSpace(v)
		}
	}
	if out.ToolName == "" {
		out.ToolName = "unnamed tool"
	}
	if v, ok := payload["detail"].(string); ok {
		out.ToolInput = strings.TrimSpace(v)
	}
	if out.ToolInput == "" {
		// Permission payload sometimes nests tool arguments under
		// "payload"; surface them as a JSON preview.
		if nested, ok := payload["payload"].(map[string]any); ok && len(nested) > 0 {
			if raw, err := jsonForPreview(nested); err == nil {
				out.ToolInput = raw
			}
		}
	}
	return out, true
}

// jsonForPreview marshals a map into a single-line JSON string for
// the PermissionCard's code-block fence.
func jsonForPreview(payload map[string]any) (string, error) {
	if payload == nil {
		return "", nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func stepFromToolCallPayload(payload map[string]any, occurredAt time.Time) (gateway.StepInfo, bool) {
	if payload == nil {
		return gateway.StepInfo{}, false
	}
	// Defensive: "after" arrives as tool.result in the persistence path.
	if stage, ok := payload["stage"].(string); ok && stage != "" && stage != "before" {
		return gateway.StepInfo{}, false
	}
	name, _ := payload["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return gateway.StepInfo{}, false
	}
	id, _ := payload["id"].(string)
	id = strings.TrimSpace(id)
	label := name
	if args, ok := payload["args"].(map[string]any); ok {
		if hint := summariseToolArgs(name, args); hint != "" {
			label = name + " · " + hint
		}
	}
	return gateway.StepInfo{Tool: name, Label: label, ID: id, StartedAt: occurredAt}, true
}

func summariseToolArgs(tool string, args map[string]any) string {
	switch tool {
	case "Read", "Edit", "Write", "NotebookEdit":
		if path, ok := args["file_path"].(string); ok && path != "" {
			return trimMiddle(path, 60)
		}
	case "Bash":
		if cmd, ok := args["command"].(string); ok && cmd != "" {
			first := cmd
			if idx := strings.IndexAny(first, "\r\n"); idx > 0 {
				first = first[:idx]
			}
			return trimMiddle(first, 60)
		}
	case "Grep", "Glob":
		if pattern, ok := args["pattern"].(string); ok && pattern != "" {
			return trimMiddle(pattern, 60)
		}
	case "WebFetch", "WebSearch":
		if url, ok := args["url"].(string); ok && url != "" {
			return trimMiddle(url, 60)
		}
		if q, ok := args["query"].(string); ok && q != "" {
			return trimMiddle(q, 60)
		}
	case "Skill":
		// Skill tool's primary arg is the skill name. Without this
		// case the row collapses to a bare "Skill" — the user can't
		// tell which skill ran (issue: card showed `Skill 0s` only).
		if name, ok := args["skill"].(string); ok && name != "" {
			return trimMiddle(name, 60)
		}
	}
	return ""
}

// trimMiddle keeps a short prefix + ellipsis + short suffix so a long
// path / command stays recognisable.
func trimMiddle(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	half := (n - 1) / 2
	return s[:half] + "…" + s[len(s)-half:]
}

// slotPayload encodes the driver's per-tick view of the working
// slot's jsonb payload. Empty streaming/thinking/error strings are
// omitted to keep the jsonb tree compact. Per-step timestamps are
// stored as RFC3339Nano so payload survives a process restart without
// losing duration precision.
func slotPayload(steps []gateway.StepInfo, streamingText, thinkingText, errorMessage, rawError string) map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		entry := map[string]any{"tool": s.Tool, "label": s.Label}
		if s.ID != "" {
			entry["id"] = s.ID
		}
		if !s.StartedAt.IsZero() {
			entry["started_at"] = s.StartedAt.UTC().Format(time.RFC3339Nano)
		}
		if !s.EndedAt.IsZero() {
			entry["ended_at"] = s.EndedAt.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, entry)
	}
	payload := map[string]any{"steps": out}
	if streamingText != "" {
		payload["streaming_text"] = streamingText
	}
	if thinkingText != "" {
		payload["thinking_text"] = thinkingText
	}
	if strings.TrimSpace(errorMessage) != "" {
		payload["error_message"] = errorMessage
	}
	if strings.TrimSpace(rawError) != "" {
		payload["raw_error"] = rawError
	}
	return payload
}

func stepsFromPayload(payload map[string]any) []gateway.StepInfo {
	if payload == nil {
		return nil
	}
	rawList, ok := payload["steps"].([]any)
	if !ok {
		return nil
	}
	out := make([]gateway.StepInfo, 0, len(rawList))
	for _, raw := range rawList {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tool, _ := entry["tool"].(string)
		label, _ := entry["label"].(string)
		id, _ := entry["id"].(string)
		step := gateway.StepInfo{Tool: tool, Label: label, ID: id}
		if v, ok := entry["started_at"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				step.StartedAt = t
			}
		}
		if v, ok := entry["ended_at"].(string); ok && v != "" {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				step.EndedAt = t
			}
		}
		out = append(out, step)
	}
	return out
}

func streamingTextFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload["streaming_text"].(string); ok {
		return v
	}
	return ""
}

// thinkingTextFromPayload pulls the model's accumulated reasoning
// trace stashed under "thinking_text" so subsequent ticks resume
// appending without re-reading every event.
func thinkingTextFromPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	if v, ok := payload["thinking_text"].(string); ok {
		return v
	}
	return ""
}

func latestSeq(events []store.AgentRunEvent, fallback int64) int64 {
	max := fallback
	for _, ev := range events {
		if ev.Sequence > max {
			max = ev.Sequence
		}
	}
	return max
}

// ----- card builders for the driver -----

// buildFinalCardForRun renders the terminal Done/Error card. errorMessage
// is the user-visible failure string from a run.failed event; empty
// falls back to the generic copy. rawError is the un-mapped error from
// the same event, appended under the generic mapped copies; see
// gateway.BuildErrorCard for when it surfaces. publicURL is the
// operator-configured PublicURL used to deep-link to the run-detail
// page; empty suppresses the link.
func buildFinalCardForRun(ctx context.Context, ch *feishuchannel.Channel, s doneCardAssemblyStore, c store.FeishuInflightConversation, steps []gateway.StepInfo, streamingText string, thinkingText string, errorMessage string, rawError string, publicURL string) (string, error) {
	if c.RunStatus == "failed" {
		msg := strings.TrimSpace(errorMessage)
		if msg == "" {
			msg = "Agent run failed. Please retry later."
		}
		// Guests (visibility=public + unregistered) crash on missing
		// capability credentials with no credential-form recovery path;
		// the hint stamped at routing time is their only register prompt.
		// Read failures degrade rather than abort — better to show the
		// generic error card than nothing at all.
		guestHint, err := s.GetGuestReplyHintForRun(ctx, c.ConversationID, c.AgentRunID)
		if err != nil {
			guestHint = ""
		}
		// Render the error card through the neutral adapter. RenderTerminal
		// mirrors BuildFeishuErrorCardContent byte-for-byte (same fallback
		// copy), so this stays identical to the legacy inline path.
		card, err := ch.RenderTerminal(ctx, channel.ReplyTarget{}, channel.TerminalResult{
			Success:      false,
			Title:        c.AgentName,
			ErrorMessage: msg,
			RawError:     rawError,
			RunDetailURL: runDetailURL(publicURL, c.AgentRunID),
			GuestHint:    guestHint,
		})
		if err != nil {
			return "", err
		}
		return string(card.Payload), nil
	}
	elapsed := time.Duration(0)
	if !c.RunStartedAt.IsZero() {
		finish := c.RunFinishedAt
		if finish.IsZero() {
			finish = time.Now().UTC()
		}
		elapsed = finish.Sub(c.RunStartedAt)
	}
	// Pull usage rollup for the footer. Steps + elapsed are prefilled
	// so only usage_logs hits the DB. On failure we degrade rather
	// than abort: the renderer's short fallback beats a stale
	// "executing" card on the user's screen.
	data, err := assembleDoneCardData(ctx, s, assembleDoneCardInput{
		WorkspaceID:       c.WorkspaceID,
		RunID:             c.AgentRunID,
		PrefilledSteps:    steps,
		PrefilledElapsed:  elapsed,
		PrefilledThinking: thinkingText,
	})
	if err != nil {
		// Render with whatever we have. Logging would require a
		// Worker reference we don't carry; the caller logs the
		// higher-level patch error.
		_ = err
	}
	// Prefer DoneCard-side AgentName (read from agent_runs); fall
	// back to the claim-row's AgentName when LoadDoneCardRunData
	// failed.
	title := strings.TrimSpace(data.AgentName)
	if title == "" {
		title = c.AgentName
	}
	// Render the Done card through the neutral adapter. RenderTerminal
	// mirrors MarshalCard(BuildDoneCard(...)) (it TrimSpaces StreamingText
	// internally), so output bytes match the legacy inline path.
	card, err := ch.RenderTerminal(ctx, channel.ReplyTarget{}, channel.TerminalResult{
		Success:       true,
		Title:         title,
		StreamingText: streamingText,
		Steps:         data.Steps,
		Thinking:      data.Thinking,
		Elapsed:       data.Elapsed,
		Usage:         data.Usage,
	})
	if err != nil {
		return "", err
	}
	return string(card.Payload), nil
}

// tryBuildCredentialFormCard returns the orange credential-form card
// when credential-missing notices exist for the current run, and
// reconciles the pending-form slot so subsequent inflight ticks reuse
// the same qkey + Feishu message_id instead of churning new cards.
//
// Returns (content, qkey, true, prevExternalMsgID, nil) on the form
// path. prevExternalMsgID is the Feishu `om_…` already pinned to the
// existing slot (when a sibling tick beat us to the slot or this is a
// retry pass); empty means the caller owns the first send. Returns
// (_, _, false, _, nil) when no gaps exist or the inbound can't be
// auto-resumed (falls through to DoneCard). Returns (_, _, false, _,
// err) on I/O failure — caller logs and degrades to DoneCard.
func (w *Worker) tryBuildCredentialFormCard(ctx context.Context, c store.FeishuInflightConversation) (string, string, bool, string, error) {
	fields, qkey, ok, existingMsgID, err := w.resolveCredentialFormFields(ctx, c)
	if err != nil || !ok {
		return "", "", false, "", err
	}
	card := gateway.BuildCredentialFormCard(c.AgentName, fields, qkey)
	content, err := gateway.MarshalCard(card)
	if err != nil {
		return "", "", false, "", fmt.Errorf("marshal form card: %w", err)
	}
	return content, qkey, true, existingMsgID, nil
}

// resolveReactionRowForRun picks the inbound-reaction row to undo for
// a given run. Tries agent_run.trigger_message_id first (precise pin
// to the inbound that triggered this run), falling back to the
// conversation-latest lookup for legacy rows where the trigger isn't
// a Feishu inbound. The undo MUST be tied to the specific run, not
// the latest inbound — otherwise a fast-typing user firing a second
// message before the first run finishes would see the terminal clear
// the wrong reaction.
//
// Returns store.ErrUnknownMessage when neither path finds a row — the
// async caller treats that as "nothing to undo" and exits silently.
func (w *Worker) resolveReactionRowForRun(ctx context.Context, conversationID, agentRunID string) (store.FeishuInboundReactionRow, error) {
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID != "" {
		row, err := w.store.FindFeishuInboundReactionByAgentRun(ctx, agentRunID)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, store.ErrUnknownMessage) {
			// Unexpected DB error — log and fall back to conversation-
			// latest rather than bubble. Telemetry surfaces if the
			// fallback becomes the dominant path.
			w.logger.Warn("feishu driver: per-run reaction lookup errored, falling back to conversation-latest",
				"conversation_id", conversationID, "agent_run_id", agentRunID, "err", err.Error())
		}
	}
	return w.store.FindLatestFeishuInboundReactionByConversation(ctx, conversationID)
}

func (w *Worker) asyncClearTypingReaction(conversationID, workspaceID, sourceAppID, agentRunID string) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		row, err := w.resolveReactionRowForRun(ctx, conversationID, agentRunID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownMessage) {
				// No reaction-bearing inbound to undo (command echo on
				// a fresh chat, Agent self-sent, or AddReaction failed
				// in the inbound webhook). Silent no-op.
				return
			}
			w.logger.Warn("feishu driver: lookup reaction inbound failed",
				"conversation_id", conversationID, "agent_run_id", agentRunID, "err", err.Error())
			return
		}
		if strings.TrimSpace(row.ReactionID) == "" || strings.TrimSpace(row.ExternalMessageID) == "" {
			// Row matched but the reaction subtree is missing the id
			// or the user's external message_id — nothing to DELETE.
			// Clean up the metadata stub so the next terminal doesn't
			// keep matching the same orphan row.
			if err := w.store.ClearFeishuInboundReaction(ctx, row.MessageID); err != nil {
				w.logger.Warn("feishu driver: clear orphan reaction metadata failed",
					"local_message_id", row.MessageID, "err", err.Error())
			}
			return
		}
		// Conversation's source_app_id is the authoritative routing
		// key; fall back to the inbound's stored app_id only when the
		// driver-side was empty.
		appID := strings.TrimSpace(sourceAppID)
		if appID == "" {
			appID = strings.TrimSpace(row.AppID)
		}
		if appID == "" {
			w.logger.Warn("feishu driver: skip reaction delete, no app_id available",
				"local_message_id", row.MessageID, "conversation_id", conversationID)
			return
		}
		creds, err := w.resolveCredentials(ctx, gateway.PendingOutboundMessage{
			SourceAppID: appID,
			WorkspaceID: strings.TrimSpace(workspaceID),
		})
		if err != nil {
			w.logger.Warn("feishu driver: resolve credentials for reaction delete failed",
				"app_id", appID, "err", err.Error())
			return
		}
		client, err := w.clientFor(strings.TrimSpace(workspaceID), creds.AppID)
		if err != nil {
			w.logger.Warn("feishu driver: build reaction-delete client failed",
				"app_id", appID, "err", err.Error())
			return
		}
		if err := client.DeleteReaction(ctx, creds.AppSecret, row.ExternalMessageID, row.ReactionID); err != nil {
			w.logger.Warn("feishu driver: delete reaction failed",
				"external_message_id", row.ExternalMessageID, "reaction_id", row.ReactionID, "err", err.Error())
			// Fall through to clear the metadata anyway: leaving the
			// id in place would have every future terminal in this
			// conversation re-try the same already-gone id. We accept
			// "reaction lingers visually" over "hammer Feishu with 404".
		}
		if err := w.store.ClearFeishuInboundReaction(ctx, row.MessageID); err != nil {
			w.logger.Warn("feishu driver: clear reaction metadata failed",
				"local_message_id", row.MessageID, "err", err.Error())
		}
	}()
}
