package dispatch

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const preparedCancelTimeout = 10 * time.Second

func (r *Router) sendPreparedCancellation(state *sessionState, handoff *preparedHandoff, release *preparedRelease, attempt *preparedReleaseAttempt, env proto.Envelope, deliveryID string) {
	defer r.shutdownWG.Done()
	timer := time.NewTimer(preparedCancelTimeout)
	defer timer.Stop()
	ack := proto.InteractionDecisionAckPayload{ErrorCode: "cancel_timeout"}
	select {
	case <-attempt.done:
		r.mu.Lock()
		releaseErr := attempt.err
		r.mu.Unlock()
		if releaseErr != nil {
			ack.ErrorCode = "cancel_failed"
			break
		}
		select {
		case <-release.settled:
			r.mu.Lock()
			outputErr, interrupted, outcome := handoff.outputErr, handoff.shutdownInterrupted, release.outcome
			r.mu.Unlock()
			switch {
			case outputErr != nil || interrupted:
				ack.ErrorCode = "cancel_output_unavailable"
			case outcome == nil:
				ack.ErrorCode = "cancel_outcome_unavailable"
			default:
				ack.Applied, ack.ErrorCode, ack.Outcome = true, "", outcome
			}
		case <-timer.C:
		case <-r.shutdownCh:
			return
		}
	case <-timer.C:
		// The caller deadline never changes release ownership or capacity.
	case <-r.shutdownCh:
		return
	}
	ack.DeliveryID = deliveryID
	ctx, stop := r.shutdownContext(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.sendCancellationAck(ctx, env, ack); err != nil {
		r.log.WarnContext(ctx, "prepared cancellation receipt failed", "run_id", state.runID, "err", err)
	}
}
