package dispatch

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type cancellationOutcomeProvider interface {
	CancellationOutcome() proto.DonePayload
}

func (r *Router) releaseCompletedSession(state *sessionState) error {
	r.mu.Lock()
	state.retain = false
	r.mu.Unlock()
	err := state.session.Cancel(context.Background())
	state.ctxCancel()
	return err
}

func (r *Router) handlePromptCancel(ctx context.Context, env proto.Envelope) error {
	var request proto.PromptCancelPayload
	if err := env.DecodePayload(&request); err != nil {
		return err
	}
	r.mu.Lock()
	state := r.sessions[env.ID]
	if state != nil {
		state.retain = false
	}
	var cancelSession func(context.Context) error
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
	if request.DeliveryID == "" {
		return nil
	}
	reply, err := proto.NewEnvelopeWithTrace(proto.TypeInteractionDecisionAck, env.ID, ack, env.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}
