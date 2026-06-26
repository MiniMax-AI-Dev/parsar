// Sibling of inflight_driver.go: sends a one-shot "排队中（第 N 位）"
// notice per queued agent_run. Stays out of the inflight slot (which
// the running sibling owns) and uses metadata.queue_card_sent_at as
// the idempotency marker — ClaimPendingQueuedFeishuRuns filters on it.

package feishuoutbound

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// queueCardCutoffWindow bounds how far back the driver will look for
// queued runs; matches inflightCutoffWindow so a queued run that's
// outlived the sibling inflight slot it'd inherit gets the same
// "we've forgotten about it" treatment.
const queueCardCutoffWindow = 5 * time.Minute

// queueCardClaimStaleWindow lets a crashed pod's claim be re-stolen.
// Much larger than the ~1-2s tick cadence so healthy pods keep their
// own claims.
const queueCardClaimStaleWindow = 30 * time.Second

const queueCardTickBatchLimit int32 = 32

// QueueCardTickOnce runs a single pass over queued runs missing a
// placeholder card. Per-run failures log and continue; the marker is
// only stamped on success so a failed send naturally retries on the
// next tick. A top-level LIST error short-circuits.
func (w *Worker) QueueCardTickOnce(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-queueCardCutoffWindow)
	staleBefore := now.Add(-queueCardClaimStaleWindow)
	runs, err := w.store.ClaimPendingQueuedFeishuRuns(ctx, store.ClaimPendingQueuedFeishuRunsInput{
		Cutoff:      cutoff,
		StaleBefore: staleBefore,
		ClaimedBy:   w.claimedBy,
		Limit:       queueCardTickBatchLimit,
	})
	if err != nil {
		w.logger.Warn("feishu queue card: claim pending failed", "err", err.Error())
		return 0, err
	}
	sent := 0
	for _, run := range runs {
		if err := w.deliverQueueCard(ctx, run); err != nil {
			w.logger.Warn("feishu queue card: deliver failed",
				"conversation_id", run.ConversationID,
				"run_id", run.RunID,
				"err", err.Error(),
			)
			continue
		}
		sent++
	}
	return sent, nil
}

// deliverQueueCard renders + sends a single placeholder card and
// stamps the idempotency marker. Returns any error so the caller can
// log it and the row stays selectable for the next tick — re-sending
// a queue card is preferable to silently dropping the stamp.
func (w *Worker) deliverQueueCard(ctx context.Context, run store.PendingQueuedFeishuRun) error {
	if strings.TrimSpace(run.SourceAppID) == "" {
		return fmt.Errorf("queued run has empty source_app_id")
	}
	position, posErr := w.store.QueuePositionForRun(ctx, run.RunID)
	if posErr != nil {
		// Degrade to "排队中" without a number rather than skip the send.
		w.logger.Warn("feishu queue card: position lookup failed (degrading)",
			"run_id", run.RunID, "err", posErr.Error())
		position = 0
	}
	creds, err := w.resolveCredentials(ctx, gateway.PendingOutboundMessage{
		WorkspaceID: run.WorkspaceID,
		SourceAppID: run.SourceAppID,
	})
	if err != nil {
		return fmt.Errorf("resolve credentials: %w", err)
	}
	client, err := w.clientFor(run.WorkspaceID, creds.AppID)
	if err != nil {
		return fmt.Errorf("client for workspace: %w", err)
	}
	content, err := gateway.MarshalCard(gateway.BuildQueueCard(run.AgentName, position))
	if err != nil {
		return fmt.Errorf("marshal queue card: %w", err)
	}
	if anchor := strings.TrimSpace(run.ExternalThreadID); anchor != "" {
		if _, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       content,
			ReplyInThread: true,
		}); err != nil {
			return fmt.Errorf("reply queue card: %w", err)
		}
	} else {
		if _, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
			ReceiveIDType: "chat_id",
			ReceiveID:     run.ExternalChatID,
			MsgType:       "interactive",
			Content:       content,
		}); err != nil {
			return fmt.Errorf("send queue card: %w", err)
		}
	}
	if err := w.store.StampQueueCardSent(ctx, run.RunID, time.Now().UTC()); err != nil {
		return fmt.Errorf("stamp queue card sent: %w", err)
	}
	return nil
}
