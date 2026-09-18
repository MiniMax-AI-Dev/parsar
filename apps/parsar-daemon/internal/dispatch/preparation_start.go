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
	if r.workspaceExport != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "resource_unavailable")
	}
	p := r.preparations[input.Handle]
	if p == nil || p.requestID != env.ID {
		r.mu.Unlock()
		return r.rejectPreparation(env, "unknown_preparation")
	}
	if p.workspaceReadOnly {
		r.mu.Unlock()
		return r.rejectPreparation(env, "read_only_preparation")
	}
	if p.status.State == "starting" || p.status.State == "started" {
		matches, status := p.startFingerprint == fingerprint, p.status
		if matches && status.State == "started" {
			state := r.sessions[input.RunID]
			if state != nil && state.preparedHandoff != nil && state.preparedHandoff.phase == preparedHandoffPublishing {
				r.mu.Unlock()
				return nil
			}
		}
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
		pendingIDs: make(map[string]struct{}), pendingAsks: make(map[string]struct{}), traceparent: env.Trace, releaseOnCompletion: true}
	state.preparedHandoff = newPreparedHandoff(p)
	r.sessions[input.RunID] = state
	status := p.status
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.startPreparedExecution(p, state, input, status)
	return nil
}

func (r *Router) startPreparedExecution(p *preparationState, state *sessionState, input proto.ExecutionStartPayload, status proto.PreparationStatusPayload) {
	defer r.shutdownWG.Done()
	if !r.sendPreparation(p.requestID, p.trace, status) {
		r.releasePreparation(p, "failed", "status_delivery_failed", false)
	}
	go r.forwardPreparedOutput(state)
	handoff := state.preparedHandoff
	session, err := handoff.prepared.Start(p.ctx, input.RunID, input.Prompt, state.out)
	r.mu.Lock()
	handoff.session = session
	abortRequested := handoff.releaseCauses&preparedAbortStartCauses != 0
	started := err == nil && session != nil && p.status.State == "starting" && p.ctx.Err() == nil && !r.closed && !abortRequested
	if started {
		handoff.phase = preparedHandoffPublishing
		p.status.State, p.status.Revision = "started", p.status.Revision+1
		p.timer.Stop()
	} else {
		handoff.phase = preparedHandoffStopping
		p.cancel()
		r.clearInteractionRoutesLocked(state)
		p.timer.Stop()
		if p.status.State == "starting" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "start_failed", p.status.Revision+1
		}
		r.requestPreparedReleaseLocked(state, preparedReleaseStartFailure)
	}
	status = p.status
	r.maybeStartPreparedReleaseLocked(state)
	r.mu.Unlock()
	if session == nil {
		close(state.out)
	}
	if !started {
		r.sendPreparation(p.requestID, p.trace, status)
		r.settlePreparedHandoff(state)
		return
	}
	statusDelivered := r.sendPreparation(p.requestID, p.trace, status)
	r.mu.Lock()
	switch {
	case !statusDelivered:
		if p.status.State == "started" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "status_delivery_failed", p.status.Revision+1
		}
		handoff.phase = preparedHandoffStopping
		r.requestPreparedReleaseLocked(state, preparedReleaseStatusFailure)
	case handoff.release != preparedReleaseIdle:
		handoff.phase = preparedHandoffStopping
		r.maybeStartPreparedReleaseLocked(state)
	default:
		handoff.phase = preparedHandoffActive
		state.session = session
		p.prepared, p.owns, p.busy = nil, false, false
	}
	r.mu.Unlock()
	r.settlePreparedHandoff(state)
}
