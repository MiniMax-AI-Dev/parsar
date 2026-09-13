package dispatch

import (
	"context"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (r *Router) steerDurably(sendCtx context.Context, state *sessionState, env proto.Envelope, input proto.PromptSteerPayload, fingerprint [32]byte) error {
	ctx, cancel := context.WithCancel(state.ctx)
	defer cancel()
	stopShutdown := context.AfterFunc(sendCtx, cancel)
	defer stopShutdown()
	timer := time.AfterFunc(steeringCallTimeout, cancel)
	defer timer.Stop()
	var once sync.Once
	return state.session.(agent.DurableSteerer).SteerWithReceipt(ctx, input, func() {
		once.Do(func() {
			if !timer.Stop() || ctx.Err() != nil {
				return
			}
			ack := proto.PromptSteerAckPayload{InputID: input.InputID, Written: true}
			if !r.publishSteeringReceipt(sendCtx, state, env, input, fingerprint, ack) {
				cancel()
			}
		})
	})
}

func (r *Router) publishSteeringReceipt(ctx context.Context, state *sessionState, env proto.Envelope, input proto.PromptSteerPayload, fingerprint [32]byte, ack proto.PromptSteerAckPayload) bool {
	r.mu.Lock()
	state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: ack, durable: input.DurableReceipt}
	r.mu.Unlock()
	sendCtx, cancel := context.WithTimeout(ctx, steeringSendTimeout)
	defer cancel()
	if err := r.sendSteeringAck(sendCtx, env, ack); err != nil {
		r.log.WarnContext(ctx, "steering receipt delivery failed", "run_id", env.ID, "input_id", input.InputID, "err", err)
		return false
	}
	return true
}
