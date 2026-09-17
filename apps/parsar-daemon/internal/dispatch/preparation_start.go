package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (r *Router) handleExecutionStart(_ context.Context, env proto.Envelope) error {
	var input proto.ExecutionStartPayload
	if env.DecodePayload(&input) != nil || input.Handle == "" || strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.Prompt) == "" {
		return r.rejectPreparation(env, "invalid_start")
	}
	encoded, _ := json.Marshal(input)
	fingerprint := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	p := r.preparations[input.Handle]
	if p == nil || p.requestID != env.ID {
		r.mu.Unlock()
		return r.rejectPreparation(env, "unknown_preparation")
	}
	if p.status.State == "starting" || p.status.State == "started" {
		matches, status := p.startFingerprint == fingerprint, p.status
		r.mu.Unlock()
		if !matches {
			return r.rejectPreparation(env, "start_conflict")
		}
		r.publishPreparation(p, status)
		return nil
	}
	if p.status.State != "ready" || p.ctx.Err() != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_not_ready")
	}
	if !time.Now().Before(p.deadline) {
		r.mu.Unlock()
		r.releasePreparation(p, "expired", "", true)
		return nil
	}
	if r.sessions[input.RunID] != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "run_conflict")
	}
	p.status.State, p.status.RunID, p.status.Revision = "starting", input.RunID, p.status.Revision+1
	p.startFingerprint, p.busy = fingerprint, true
	state := &sessionState{runID: input.RunID, stateKey: p.stateKey, environmentID: p.environmentID, out: make(chan proto.Envelope, 64), ctx: p.ctx, ctxCancel: p.cancel,
		pendingIDs: make(map[string]struct{}), pendingAsks: make(map[string]struct{}), traceparent: env.Trace, releaseOnCompletion: true,
		preparationStart: &preparedStartCancellation{prepared: p.prepared, settled: make(chan struct{})}}
	r.sessions[input.RunID] = state
	status := p.status
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.startPreparedExecution(p, state, input, status)
	return nil
}

func (r *Router) startPreparedExecution(p *preparationState, state *sessionState, input proto.ExecutionStartPayload, status proto.PreparationStatusPayload) {
	if !r.sendPreparation(p.requestID, p.trace, status) {
		r.releasePreparation(p, "failed", "status_delivery_failed", false)
	}
	session, err := p.prepared.Start(p.ctx, input.RunID, input.Prompt, state.out)
	r.mu.Lock()
	cancellation := state.preparationStart
	started := err == nil && session != nil && p.status.State == "starting" && p.ctx.Err() == nil && !r.closed
	if started {
		state.session, state.preparationStart = session, nil
		p.prepared, p.owns, p.busy = nil, false, false
		p.status.State, p.status.Revision = "started", p.status.Revision+1
		p.timer.Stop()
	} else {
		if cancellation.cancelDone == nil {
			delete(r.sessions, state.runID)
		}
		p.cancel()
		p.timer.Stop()
		if p.status.State == "starting" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "start_failed", p.status.Revision+1
		}
	}
	status = p.status
	r.mu.Unlock()
	if !started {
		defer r.shutdownWG.Done()
		if cancellation.cancelDone != nil {
			r.finishPreparedCancellation(p, state, session, cancellation)
			r.sendPreparation(p.requestID, p.trace, status)
			return
		}
		if session != nil {
			_ = session.Cancel(context.Background())
			r.drain(state.out)
		}
		r.closePreparationResource(p)
		r.sendPreparation(p.requestID, p.trace, status)
		ctx, stop := r.shutdownContext(context.Background())
		defer stop()
		r.emitTerminalError(ctx, state.runID, "prepared execution could not start")
		return
	}
	if !r.sendPreparation(p.requestID, p.trace, status) {
		state.ctxCancel()
		_ = session.Cancel(context.Background())
	}
	// Transfer the tracked goroutine and output ownership to the existing pump.
	r.pump(state)
}
