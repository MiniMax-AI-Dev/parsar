package dispatch

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const preparedCancelTimeout = 10 * time.Second

// Router.mu protects cancelDone creation. Closing cancelDone publishes cancelErr;
// closing settled publishes result. Start owns cleanup until both operations end.
type preparedStartCancellation struct {
	prepared   agent.Prepared
	cancelDone chan struct{}
	cancelErr  error
	settled    chan struct{}
	result     proto.InteractionDecisionAckPayload
}

func (r *Router) cancelPreparedStartLocked(state *sessionState, env proto.Envelope, deliveryID string) {
	pending := state.preparationStart
	state.ctxCancel()
	if pending.cancelDone == nil {
		pending.cancelDone = make(chan struct{})
		go func() {
			defer close(pending.cancelDone)
			if prepared, ok := pending.prepared.(agent.PreparedCancellation); ok {
				ctx, cancel := context.WithTimeout(context.Background(), preparedCancelTimeout)
				defer cancel()
				pending.cancelErr = prepared.Cancel(ctx)
			}
		}()
	}
	if deliveryID != "" {
		r.shutdownWG.Add(1)
		go r.sendPreparedCancellation(state, pending, env, deliveryID)
	}
}

func (r *Router) sendPreparedCancellation(state *sessionState, pending *preparedStartCancellation, env proto.Envelope, deliveryID string) {
	defer r.shutdownWG.Done()
	timer := time.NewTimer(preparedCancelTimeout)
	defer timer.Stop()
	ack := proto.InteractionDecisionAckPayload{ErrorCode: "cancel_timeout"}
	select {
	case <-pending.settled:
		ack = pending.result
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

func (r *Router) finishPreparedCancellation(p *preparationState, state *sessionState, session agent.Session, pending *preparedStartCancellation) {
	defer r.cleanupSession(state)
	// Cancellation already owns release. A late Done must not release the
	// Session again or make this failed transfer available for steering.
	r.mu.Lock()
	state.releaseOnCompletion = false
	r.mu.Unlock()
	var forwarded chan error
	if session != nil {
		forwarded = make(chan error, 1)
		go func() { forwarded <- r.forwardSessionOutput(state, true) }()
	}
	<-pending.cancelDone
	prepared, supported := pending.prepared.(agent.PreparedCancellation)
	if session != nil && (!supported || pending.cancelErr != nil) {
		_ = session.Cancel(context.Background())
	}
	var forwardErr error
	if forwarded != nil {
		forwardErr = <-forwarded
	} else {
		forwardErr = r.forwardSessionOutput(state, false)
	}
	r.closePreparationResource(p)
	ack := proto.InteractionDecisionAckPayload{ErrorCode: "cancel_outcome_unavailable"}
	switch {
	case pending.cancelErr != nil:
		ack.ErrorCode = "cancel_failed"
	case forwardErr != nil:
		ack.ErrorCode = "cancel_output_unavailable"
	case supported:
		outcome := prepared.CancellationOutcome()
		ack.Applied, ack.ErrorCode, ack.Outcome = true, "", &outcome
	}
	pending.result = ack
	close(pending.settled)
}
