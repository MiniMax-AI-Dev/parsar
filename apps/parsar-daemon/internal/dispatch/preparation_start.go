package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
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
		if matches {
			if state := r.sessions[input.RunID]; state != nil && state.preparedHandoff != nil &&
				(state.session == nil || state.preparedHandoff.release != nil) {
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
		r.releasePreparation(p, "expired", "", true, true)
		return nil
	}
	if r.sessions[input.RunID] != nil {
		r.mu.Unlock()
		return r.rejectPreparation(env, "run_conflict")
	}
	target, ok := p.prepared.(agent.PreparedCancellation)
	if !ok {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_not_ready")
	}
	p.status.State, p.status.RunID, p.status.Revision = "starting", input.RunID, p.status.Revision+1
	p.startFingerprint, p.busy = fingerprint, true
	state := &sessionState{
		runID: input.RunID, stateKey: p.stateKey, environmentID: p.environmentID,
		out: make(chan proto.Envelope, 64), ctx: p.ctx, ctxCancel: p.cancel,
		pendingIDs: make(map[string]struct{}), pendingAsks: make(map[string]struct{}),
		traceparent: env.Trace, releaseOnCompletion: true,
	}
	state.preparedHandoff = newPreparedHandoff(p, target)
	p.handoff = state.preparedHandoff
	r.sessions[input.RunID] = state
	status := p.status
	// Track the Start owner and output consumer before either can publish.
	r.shutdownWG.Add(2)
	r.mu.Unlock()
	go r.startPreparedExecution(p, state, input, status)
	return nil
}

func (r *Router) startPreparedExecution(p *preparationState, state *sessionState, input proto.ExecutionStartPayload, starting proto.PreparationStatusPayload) {
	defer r.shutdownWG.Done()
	handoff := state.preparedHandoff
	go r.forwardPreparedOutput(state)
	<-handoff.outputReady

	if !r.sendPreparation(p.requestID, p.trace, starting) {
		r.mu.Lock()
		handoff.outputErr = errors.Join(handoff.outputErr, errPreparedStatusDelivery)
		if p.status.State == "starting" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "status_delivery_failed", p.status.Revision+1
		}
		p.timer.Stop()
		r.claimPreparedReleaseLocked(state, true, "", false)
		r.mu.Unlock()
		close(state.out)
		close(handoff.startDone)
		return
	}

	// A release admitted before this point must not cause native work to start.
	r.mu.Lock()
	blocked := handoff.release != nil && handoff.release.aborted()
	r.mu.Unlock()
	var session agent.Session
	var startErr error
	if blocked {
		startErr = context.Canceled
	} else {
		session, startErr = handoff.target.Start(p.ctx, input.RunID, input.Prompt, state.out)
	}

	r.mu.Lock()
	release := handoff.release
	notAborted := release == nil || !release.aborted()
	started := startErr == nil && session != nil && p.status.State == "starting" && p.ctx.Err() == nil && notAborted
	if started {
		p.status.State, p.status.ErrorCode, p.status.Revision = "started", "", p.status.Revision+1
	} else {
		p.timer.Stop()
		if p.status.State == "starting" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "start_failed", p.status.Revision+1
		}
		r.claimPreparedReleaseLocked(state, true, "prepared execution could not start", false)
	}
	status := p.status
	// Keep release behind successful or failed status publication. This also
	// orders an early native Done after the started status.
	if started {
		handoff.operations.RLock()
	}
	r.mu.Unlock()

	// A nil Session leaves output ownership with the Router. A non-nil Session
	// owns the close even when Start also returned an error.
	if session == nil {
		close(state.out)
	}
	deadline := time.Now().Add(5 * time.Second)
	if started && p.deadline.Before(deadline) {
		deadline = p.deadline
	}
	delivered := r.sendPreparationUntil(p.requestID, p.trace, status, deadline)

	r.mu.Lock()
	if started && !time.Now().Before(p.deadline) {
		if p.status.State == "started" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "expired", "", p.status.Revision+1
		}
		r.claimPreparedReleaseLocked(state, true, "", false)
	}
	if started && handoff.release != nil && handoff.release.aborted() && p.status.State == "started" {
		p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "start_failed", p.status.Revision+1
	}
	if !delivered {
		handoff.outputErr = errors.Join(handoff.outputErr, errPreparedStatusDelivery)
		if started && p.status.State == "started" {
			p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "status_delivery_failed", p.status.Revision+1
		}
		r.claimPreparedReleaseLocked(state, true, "", false)
	} else if started && p.status.State == "started" &&
		(handoff.release == nil || !handoff.release.aborted()) {
		handoff.published = true
		p.timer.Stop()
	}
	if handoff.published {
		// Publication transfers resource tracking even when natural completion
		// has already closed input admission.
		if handoff.release == nil {
			state.session = session
		}
		p.prepared = nil
		p.owns = false
		p.busy = false
		p.handoff = nil
	}
	close(handoff.startDone)
	r.mu.Unlock()
	if started {
		handoff.operations.RUnlock()
	}
}
