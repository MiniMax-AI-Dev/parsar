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
	"github.com/google/uuid"
)

const preparationCapacity = 4
const preparationRecords = 64

// All mutable fields are protected by Router.mu. owns includes resources whose
// cancellation is underway; a slow close cannot bypass the capacity bound.
type preparationState struct {
	requestID        string
	trace            string
	fingerprint      [32]byte
	startFingerprint [32]byte
	status           proto.PreparationStatusPayload
	deadline         time.Time
	timer            *time.Timer
	ctx              context.Context
	cancel           context.CancelFunc
	prepared         agent.Prepared
	stateKey         string
	busy             bool
	owns             bool
	closeErr         error
}

func (r *Router) handleExecutionPrepare(ctx context.Context, env proto.Envelope) error {
	var input proto.ExecutionPreparePayload
	if env.DecodePayload(&input) != nil || strings.TrimSpace(env.ID) == "" {
		return r.rejectPreparation(env, "invalid_request")
	}
	req := input.Configuration
	caps := r.availableCapabilities(req.AgentKind)
	prepare, err := r.registry.ResolvePreparation(req.AgentKind)
	if err != nil || !caps.Preparation {
		return r.rejectPreparation(env, "unsupported_preparation")
	}
	if req.RunID != "" || req.Prompt != "" || req.ConversationID != "" || req.WorkspaceAuthoring || len(req.Attachments) != 0 || req.RemoteEnvironment == nil || strings.TrimSpace(req.AgentStateKey) == "" || !req.StrictResume || !req.ReleaseOnCompletion {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	if validateExecutionEnvironment(req, caps) != nil || (len(req.FunctionTools) > 0 && !caps.FunctionTools) {
		return r.rejectPreparation(env, "unsupported_configuration")
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return r.rejectPreparation(env, "invalid_configuration")
	}
	fingerprint := sha256.Sum256(encoded)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	r.prunePreparationsLocked()
	if old := r.preparationRequests[env.ID]; old != nil {
		status := old.status
		matches := old.fingerprint == fingerprint
		r.mu.Unlock()
		if !matches {
			return r.rejectPreparation(env, "request_conflict")
		}
		r.publishPreparation(old, status)
		return nil
	}
	owned := 0
	for _, p := range r.preparations {
		if p.owns {
			owned++
		}
	}
	if owned >= preparationCapacity || len(r.preparations) >= preparationRecords {
		r.mu.Unlock()
		return r.rejectPreparation(env, "preparation_capacity")
	}
	owner, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p := &preparationState{requestID: env.ID, trace: env.Trace, fingerprint: fingerprint, ctx: owner, cancel: cancel, stateKey: req.AgentStateKey, busy: true, owns: true, deadline: time.Now().Add(r.preparationTimeout)}
	p.status = proto.PreparationStatusPayload{Handle: uuid.NewString(), Revision: 1, State: "preparing", ExpiresAt: p.deadline.UnixMilli()}
	r.preparations[p.status.Handle], r.preparationRequests[p.requestID] = p, p
	p.timer = time.AfterFunc(r.preparationTimeout, func() { r.releasePreparation(p, "expired", "", true) })
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go r.prepareExecution(p, req, prepare)
	return nil
}

func (r *Router) prepareExecution(p *preparationState, req proto.PromptRequestPayload, prepare agent.PreparationFactory) {
	defer r.shutdownWG.Done()
	if !r.sendPreparation(p.requestID, p.trace, proto.PreparationStatusPayload{Handle: p.status.Handle, Revision: 1, State: "preparing", ExpiresAt: p.deadline.UnixMilli()}) {
		r.releasePreparation(p, "failed", "status_delivery_failed", false)
	}
	var prepared agent.Prepared
	var err error
	if p.ctx.Err() == nil {
		prepared, err = prepare(p.ctx, req)
	} else {
		err = p.ctx.Err()
	}
	r.mu.Lock()
	p.busy = false
	p.prepared = prepared
	ready := err == nil && prepared != nil && p.status.State == "preparing" && p.ctx.Err() == nil && !r.closed
	if ready {
		p.status.State, p.status.Revision = "ready", p.status.Revision+1
	} else if p.status.State == "preparing" {
		p.status.State, p.status.ErrorCode, p.status.Revision = "failed", "preparation_failed", p.status.Revision+1
	}
	status := p.status
	if !ready {
		p.busy = true
		p.cancel()
		p.timer.Stop()
	}
	r.mu.Unlock()
	if !ready {
		r.closePreparationResource(p)
	}
	if !r.sendPreparation(p.requestID, p.trace, status) && ready {
		r.releasePreparation(p, "failed", "status_delivery_failed", false)
	}
}

func (r *Router) handleExecutionRelease(_ context.Context, env proto.Envelope) error {
	var input proto.ExecutionReleasePayload
	if env.DecodePayload(&input) != nil || input.Handle == "" {
		return r.rejectPreparation(env, "invalid_release")
	}
	r.mu.Lock()
	p := r.preparations[input.Handle]
	valid := p != nil && p.requestID == env.ID
	r.mu.Unlock()
	if !valid {
		return r.rejectPreparation(env, "unknown_preparation")
	}
	r.releasePreparation(p, "released", "", true)
	return nil
}

func (r *Router) releasePreparation(p *preparationState, state, code string, publish bool) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	closeResource := false
	switch p.status.State {
	case "preparing", "ready", "starting":
		p.status.State, p.status.ErrorCode, p.status.Revision = state, code, p.status.Revision+1
		p.cancel()
		p.timer.Stop()
	}
	if p.owns && !p.busy {
		p.busy = true
		closeResource = true
		r.shutdownWG.Add(1)
	}
	status := p.status
	r.mu.Unlock()
	if closeResource {
		go func() { defer r.shutdownWG.Done(); r.closePreparationResource(p) }()
	}
	if publish {
		r.publishPreparation(p, status)
	}
}

func (r *Router) prunePreparationsLocked() {
	var oldest *preparationState
	for handle, p := range r.preparations {
		if p.owns {
			continue
		}
		if time.Now().After(p.deadline) {
			delete(r.preparations, handle)
			delete(r.preparationRequests, p.requestID)
		} else if oldest == nil || p.deadline.Before(oldest.deadline) {
			oldest = p
		}
	}
	if len(r.preparations) >= preparationRecords && oldest != nil {
		delete(r.preparations, oldest.status.Handle)
		delete(r.preparationRequests, oldest.requestID)
	}
}

func (r *Router) publishPreparation(p *preparationState, status proto.PreparationStatusPayload) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.shutdownWG.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.shutdownWG.Done()
		if !r.sendPreparation(p.requestID, p.trace, status) {
			r.releasePreparation(p, "failed", "status_delivery_failed", false)
		}
	}()
}

func (r *Router) sendPreparation(requestID, trace string, status proto.PreparationStatusPayload) bool {
	ctx, stop := r.shutdownContext(context.Background())
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	env, err := proto.NewEnvelopeWithTrace(proto.TypePreparationStatus, requestID, status, trace)
	return err == nil && r.sender.Send(ctx, env) == nil
}

func (r *Router) rejectPreparation(env proto.Envelope, code string) error {
	r.mu.Lock()
	if !r.closed {
		r.shutdownWG.Add(1)
		go func() {
			defer r.shutdownWG.Done()
			r.sendPreparation(env.ID, env.Trace, proto.PreparationStatusPayload{State: "rejected", ErrorCode: code, Operation: env.Type})
		}()
	}
	r.mu.Unlock()
	return errors.New("dispatch: " + code)
}
