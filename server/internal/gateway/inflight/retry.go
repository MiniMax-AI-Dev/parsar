package inflight

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// driverBackoffSchedule: attempt N (1-indexed) sleeps schedule[N-1]
// before the driver picks the conversation up again. After draining
// the schedule the driver dead-letters.
//
// Fixed values (not exponential) because Feishu's transient errors
// usually clear in seconds; 5 attempts ≈ 10min total wall-clock is a
// reasonable budget — rides out a maintenance window but ops sees the
// dead-letter notice while the user is still looking at the chat.
var driverBackoffSchedule = []time.Duration{
	1 * time.Second,
	5 * time.Second,
	30 * time.Second,
	5 * time.Minute,
	5 * time.Minute,
}

var maxDriverAttempts = len(driverBackoffSchedule)

const lastErrorTruncate = 512

// deadLetterKind builds the metadata.kind dedup key for a dead-letter
// notice. SendSystemNoticeMessage dedups on (conversation_id, kind),
// so a fixed string would silently swallow every dead-letter after
// the first. Per-run discriminator keeps later failed runs visible.
func deadLetterKind(slot, runID string) string {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "feishu_outbound_dead_letter_" + slot
	}
	return "feishu_outbound_dead_letter_" + slot + "_" + runID
}

// nextRetryAfter returns the next-retry timestamp for a slot whose
// attemptN-th (1-indexed) attempt just failed. Callers must branch
// on attemptN >= maxDriverAttempts BEFORE calling.
func nextRetryAfter(now time.Time, attemptN int) time.Time {
	if attemptN < 1 {
		attemptN = 1
	}
	idx := attemptN - 1
	if idx >= len(driverBackoffSchedule) {
		idx = len(driverBackoffSchedule) - 1
	}
	return now.Add(driverBackoffSchedule[idx]).UTC()
}

// truncateError shortens err's message so metadata doesn't grow
// unbounded across retries. Full error still goes to warn-level logs.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) <= lastErrorTruncate {
		return msg
	}
	// Middle-truncate so both failure prefix and upstream tail stay visible.
	head := lastErrorTruncate / 2
	tail := lastErrorTruncate - head - 1
	return msg[:head] + "…" + msg[len(msg)-tail:]
}

// upstreamFailure records a working-slot send/patch failure: bumps
// attempts, stashes truncated error, computes next retry, persists via
// optimistic-lock Upsert.
//
// deadLetter==true means the failure tripped maxDriverAttempts; the
// caller should run dead-letter side effects (notice + audit + ClearSlot)
// instead of waiting for retry.
//
// persistErr non-nil means the Upsert itself failed — propagate; the
// in-memory counter didn't survive the write.
// ErrConversationInflightConflict means another tick won the race;
// returns (false, nil) so the winning slot becomes source of truth.
func (w *Worker) upstreamFailure(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	prev store.WorkingInflightSlot,
	sendErr error,
) (deadLetter bool, persistErr error) {
	if sendErr == nil {
		return false, fmt.Errorf("upstreamFailure called with nil err — programmer error")
	}
	next := prev
	next.Attempts = prev.Attempts + 1
	next.LastError = truncateError(sendErr)
	now := time.Now().UTC()
	if next.Attempts >= maxDriverAttempts {
		// Dead-letter — zero NextRetryAt. Caller ClearSlots immediately.
		next.NextRetryAt = time.Time{}
	} else {
		next.NextRetryAt = nextRetryAfter(now, next.Attempts)
	}
	// expectedOld: prev.AgentRunID is "" when the first send failed
	// before the slot existed; matches the "no slot yet" state.
	expectedOld := prev.AgentRunID
	if _, err := w.store.UpsertConversationInflightWorkingCard(ctx, store.UpsertConversationInflightWorkingCardInput{
		ConversationID:   conv.ConversationID,
		Slot:             next,
		ExpectedOldRunID: expectedOld,
	}); err != nil {
		if err == store.ErrConversationInflightConflict {
			// Lost the race; the winning slot is now source of truth.
			return false, nil
		}
		return false, fmt.Errorf("persist retry state: %w", err)
	}
	return next.Attempts >= maxDriverAttempts, nil
}

// upstreamFailurePermission is the permission-slot analogue of
// upstreamFailure. Same semantics, different slot.
func (w *Worker) upstreamFailurePermission(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	prev store.PermissionInflightSlot,
	sendErr error,
) (deadLetter bool, persistErr error) {
	if sendErr == nil {
		return false, fmt.Errorf("upstreamFailurePermission called with nil err — programmer error")
	}
	next := prev
	next.Attempts = prev.Attempts + 1
	next.LastError = truncateError(sendErr)
	now := time.Now().UTC()
	if next.Attempts >= maxDriverAttempts {
		next.NextRetryAt = time.Time{}
	} else {
		next.NextRetryAt = nextRetryAfter(now, next.Attempts)
	}
	expectedOld := prev.PermissionRequestID
	if _, err := w.store.UpsertConversationInflightPermissionCard(ctx, store.UpsertConversationInflightPermissionCardInput{
		ConversationID:       conv.ConversationID,
		Slot:                 next,
		ExpectedOldRequestID: expectedOld,
	}); err != nil {
		if err == store.ErrConversationInflightConflict {
			return false, nil
		}
		return false, fmt.Errorf("persist permission retry state: %w", err)
	}
	return next.Attempts >= maxDriverAttempts, nil
}

// upstreamSuccess: zero retry triad (Attempts / LastError / NextRetryAt)
// on the working slot. We don't ClearSlot here — the slot still holds
// message_id and other persistent fields.
func zeroRetryWorking(slot store.WorkingInflightSlot) store.WorkingInflightSlot {
	slot.Attempts = 0
	slot.LastError = ""
	slot.NextRetryAt = time.Time{}
	return slot
}

// zeroRetryPermission mirrors zeroRetryWorking for the permission slot.
func zeroRetryPermission(slot store.PermissionInflightSlot) store.PermissionInflightSlot {
	slot.Attempts = 0
	slot.LastError = ""
	slot.NextRetryAt = time.Time{}
	return slot
}

// handleUpstreamWorkingFailure is the canonical "send/patch against
// Feishu failed" handler. Wraps error with stage, calls
// upstreamFailure, dispatches dead-letter when budget exhausted, logs.
//
// nil return = retry state persisted (mid-backoff or dead-lettered);
// driver stops processing this conversation until next_retry_at.
// non-nil = persisting the retry state itself failed; tick retries.
func (w *Worker) handleUpstreamWorkingFailure(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	prev store.WorkingInflightSlot,
	stage string,
	upstreamErr error,
) error {
	wrapped := fmt.Errorf("%s: %w", stage, upstreamErr)
	deadLetter, persistErr := w.upstreamFailure(ctx, conv, prev, wrapped)
	if persistErr != nil {
		// Couldn't write retry counter — propagate so next tick
		// retries. Do NOT dead-letter on a persist failure.
		return persistErr
	}
	if deadLetter {
		// Re-construct the latest slot since we discarded the Upsert
		// result; keeps the helper API narrow.
		latest := prev
		latest.Attempts = prev.Attempts + 1
		latest.LastError = truncateError(wrapped)
		w.deadLetterWorking(ctx, conv, latest)
		w.logger.Warn("feishu inflight: working slot dead-lettered",
			"conversation_id", conv.ConversationID,
			"agent_run_id", conv.AgentRunID,
			"attempts", latest.Attempts,
			"stage", stage,
			"err", upstreamErr.Error(),
		)
		return nil
	}
	w.logger.Warn("feishu inflight: working slot transient failure",
		"conversation_id", conv.ConversationID,
		"agent_run_id", conv.AgentRunID,
		"attempt", prev.Attempts+1,
		"stage", stage,
		"err", upstreamErr.Error(),
	)
	return nil
}

// handleUpstreamPermissionFailure mirrors handleUpstreamWorkingFailure
// for the permission slot's send/patch path.
func (w *Worker) handleUpstreamPermissionFailure(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	prev store.PermissionInflightSlot,
	stage string,
	upstreamErr error,
) error {
	wrapped := fmt.Errorf("%s: %w", stage, upstreamErr)
	deadLetter, persistErr := w.upstreamFailurePermission(ctx, conv, prev, wrapped)
	if persistErr != nil {
		return persistErr
	}
	if deadLetter {
		latest := prev
		latest.Attempts = prev.Attempts + 1
		latest.LastError = truncateError(wrapped)
		w.deadLetterPermission(ctx, conv, latest)
		w.logger.Warn("feishu inflight: permission slot dead-lettered",
			"conversation_id", conv.ConversationID,
			"agent_run_id", conv.AgentRunID,
			"attempts", latest.Attempts,
			"stage", stage,
			"err", upstreamErr.Error(),
		)
		return nil
	}
	w.logger.Warn("feishu inflight: permission slot transient failure",
		"conversation_id", conv.ConversationID,
		"agent_run_id", conv.AgentRunID,
		"attempt", prev.Attempts+1,
		"stage", stage,
		"err", upstreamErr.Error(),
	)
	return nil
}

// deadLetterWorking runs side effects when the working slot exhausts
// its retry budget: system-notice message, audit event, ClearSlot.
// Side-effect failures are logged but not propagated.
func (w *Worker) deadLetterWorking(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	slot store.WorkingInflightSlot,
) {
	reason := strings.TrimSpace(slot.LastError)
	if reason == "" {
		reason = "unknown delivery failure"
	}
	noticeText := fmt.Sprintf(
		"Feishu delivery dropped after %d attempts: %s",
		slot.Attempts,
		reason,
	)
	if _, err := w.store.SendSystemNoticeMessage(ctx, store.SendSystemNoticeMessageInput{
		ConversationID: conv.ConversationID,
		WorkspaceID:    conv.WorkspaceID,
		// Per-run discriminator — SendSystemNoticeMessage dedups on
		// (conversation_id, metadata.kind), so a fixed kind would
		// swallow notices from later failed runs.
		Kind:        deadLetterKind("working", conv.AgentRunID),
		Content:     noticeText,
		SourceRunID: conv.AgentRunID,
	}); err != nil {
		w.logger.Warn("feishu inflight: dead-letter notice failed",
			"conversation_id", conv.ConversationID,
			"agent_run_id", conv.AgentRunID,
			"err", err.Error(),
		)
	}
	w.emitDeadLetterAudit(conv, slot.Attempts, slot.LastError, "feishu_outbound.dead_letter")
	if err := w.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotWorking, conv.AgentRunID); err != nil {
		w.logger.Warn("feishu inflight: dead-letter clear slot failed",
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
	// Clear the inbound typing reaction. Dead-letter notice now in
	// the channel; leaving the emoji would signal "still working" on
	// top of the failure notice. Best-effort.
	w.asyncClearTypingReaction(conv.ConversationID, conv.WorkspaceID, conv.SourceAppID, conv.AgentRunID)
}

// deadLetterPermission mirrors deadLetterWorking. Notice wording
// reflects "interactive prompt the user can't act on".
func (w *Worker) deadLetterPermission(
	ctx context.Context,
	conv store.FeishuInflightConversation,
	slot store.PermissionInflightSlot,
) {
	reason := strings.TrimSpace(slot.LastError)
	if reason == "" {
		reason = "unknown delivery failure"
	}
	noticeText := fmt.Sprintf(
		"Feishu permission card delivery dropped after %d attempts: %s",
		slot.Attempts,
		reason,
	)
	if _, err := w.store.SendSystemNoticeMessage(ctx, store.SendSystemNoticeMessageInput{
		ConversationID: conv.ConversationID,
		WorkspaceID:    conv.WorkspaceID,
		Kind:           deadLetterKind("permission", conv.AgentRunID),
		Content:        noticeText,
		SourceRunID:    conv.AgentRunID,
	}); err != nil {
		w.logger.Warn("feishu inflight: dead-letter (permission) notice failed",
			"conversation_id", conv.ConversationID,
			"agent_run_id", conv.AgentRunID,
			"err", err.Error(),
		)
	}
	w.emitDeadLetterAudit(conv, slot.Attempts, slot.LastError, "feishu_outbound.dead_letter_permission")
	if err := w.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPermission, conv.AgentRunID); err != nil {
		w.logger.Warn("feishu inflight: dead-letter (permission) clear slot failed",
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
}

// emitDeadLetterAudit writes an audit record for a dead-letter event.
// Best-effort: nil ingester or audit.ErrDropped logs a warning and
// moves on — never block the driver tick on audit emit.
func (w *Worker) emitDeadLetterAudit(
	conv store.FeishuInflightConversation,
	attempts int,
	lastError string,
	eventType string,
) {
	if w.audit == nil {
		return
	}
	ev := audit.Event{
		OccurredAt:  time.Now().UTC(),
		Source:      audit.SourceRuntime,
		EventType:   eventType,
		ActorType:   audit.ActorTypeSystem,
		TargetType:  "conversation",
		TargetID:    conv.ConversationID,
		WorkspaceID: conv.WorkspaceID,
		Payload: map[string]any{
			"agent_run_id":     conv.AgentRunID,
			"attempts":         attempts,
			"last_error":       lastError,
			"external_chat_id": conv.ExternalChatID,
			"external_app_id":  conv.SourceAppID,
		},
	}
	if err := w.audit.Emit(ev); err != nil {
		w.logger.Warn("feishu inflight: dead-letter audit emit failed",
			"conversation_id", conv.ConversationID,
			"event_type", eventType,
			"err", err.Error(),
		)
	}
}
