package dispatch

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const preparedCancelTimeout = 10 * time.Second

func (r *Router) sendPreparedCancellation(state *sessionState, handoff *preparedHandoff, env proto.Envelope, deliveryID string) {
	defer r.shutdownWG.Done()
	timer := time.NewTimer(preparedCancelTimeout)
	defer timer.Stop()
	ack := proto.InteractionDecisionAckPayload{ErrorCode: "cancel_timeout"}
	select {
	case <-handoff.releaseDone:
		r.mu.Lock()
		releaseErr, outputErr, outcome := handoff.releaseErr, handoff.outputErr, handoff.outcome
		r.mu.Unlock()
		switch {
		case releaseErr != nil:
			ack.ErrorCode = "cancel_failed"
		case outputErr != nil:
			ack.ErrorCode = "cancel_output_unavailable"
		case outcome == nil:
			ack.ErrorCode = "cancel_outcome_unavailable"
		default:
			ack.Applied, ack.ErrorCode, ack.Outcome = true, "", outcome
		}
	case <-timer.C:
		// A receipt deadline does not release native ownership or capacity.
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
