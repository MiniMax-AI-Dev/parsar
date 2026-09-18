package dispatch

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type cancellationOutcomeProvider interface {
	CancellationOutcome() proto.DonePayload
}

func (r *Router) releaseCompletedSession(state *sessionState) (bool, error) {
	r.mu.Lock()
	if !state.releaseOnCompletion || state.session == nil {
		r.mu.Unlock()
		return false, nil
	}
	state.releaseOnCompletion = false
	state.retain = false
	session := state.session
	r.mu.Unlock()
	receiptErr := r.finishSteering(state)
	err := session.Cancel(context.Background())
	state.ctxCancel()
	return true, errors.Join(receiptErr, err)
}

func (r *Router) handlePromptCancel(ctx context.Context, env proto.Envelope) error {
	var request proto.PromptCancelPayload
	if err := env.DecodePayload(&request); err != nil {
		return err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	state := r.sessions[env.ID]
	if state != nil {
		state.retain = false
	}
	var cancelSession func(context.Context) error
	if state != nil && state.preparationStart != nil {
		r.cancelPreparedStartLocked(state, env, request.DeliveryID)
		r.mu.Unlock()
		return nil
	}
	if state != nil && state.session != nil {
		cancelSession = state.session.Cancel
	}
	r.mu.Unlock()
	ack := proto.InteractionDecisionAckPayload{DeliveryID: request.DeliveryID, ErrorCode: "run_inactive"}
	if state != nil && cancelSession == nil {
		ack.ErrorCode = "not_ready"
	} else if cancelSession != nil {
		if err := cancelSession(ctx); err != nil {
			r.log.WarnContext(ctx, "session.Cancel failed", "run_id", env.ID, "err", err)
			ack.ErrorCode = "cancel_failed"
		} else {
			ack.Applied, ack.ErrorCode = true, ""
			if provider, ok := state.session.(cancellationOutcomeProvider); ok {
				outcome := provider.CancellationOutcome()
				ack.Outcome = &outcome
			}
		}
		state.ctxCancel()
	}
	return r.sendCancellationAck(ctx, env, ack)
}

func (r *Router) sendCancellationAck(ctx context.Context, env proto.Envelope, ack proto.InteractionDecisionAckPayload) error {
	if ack.DeliveryID == "" {
		return nil
	}
	reply, err := proto.NewEnvelopeWithTrace(proto.TypeInteractionDecisionAck, env.ID, ack, env.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}
