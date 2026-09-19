package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (r *Router) handlePermissionDecision(ctx context.Context, env proto.Envelope) error {
	if env.ID == "" {
		return errors.New("dispatch: permission_decision missing perm id (Envelope.ID empty)")
	}
	var payload proto.PermissionDecisionPayload
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("dispatch: decode permission_decision: %w", err)
	}
	if payload.DeliveryID == "" {
		return errors.New("dispatch: permission_decision missing delivery_id")
	}
	fingerprint, err := interactionDecisionFingerprint(payload)
	if err != nil {
		return fmt.Errorf("dispatch: fingerprint permission_decision: %w", err)
	}
	if handled, err := r.replayAppliedInteractionDecision(ctx, env.ID, payload.DeliveryID, proto.TypePermissionDecision, fingerprint); handled {
		return err
	}

	r.mu.Lock()
	runID, known := r.permIndex[env.ID]
	var state *sessionState
	var session agent.Session
	var finishOperation func()
	if known {
		state = r.sessions[runID]
		if state != nil {
			session, finishOperation, _ = r.preparedOperationLocked(state)
		}
	}
	r.mu.Unlock()

	if !known || state == nil {
		// Server's perm timeout / cancel race; common enough that info
		// is right.
		r.log.InfoContext(ctx, "permission_decision for unknown perm (run gone)", "perm_id", env.ID)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_pending", "permission request is no longer pending")
	}
	if session == nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_ready", "permission request is waiting for the native session")
	}
	defer finishOperation()
	ctx, stop := r.shutdownContext(ctx)
	defer stop()

	responder, supported := session.(agent.PermissionResponder)
	if !supported {
		r.dropPermission(state, env.ID)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "unsupported", "runtime does not support permission responses")
	}
	if err := responder.SubmitPermission(ctx, env.ID, payload); err != nil {
		if errors.Is(err, agent.ErrUnknownPermission) {
			r.log.InfoContext(ctx, "agent reports unknown perm (race with cancel)", "perm_id", env.ID, "run_id", runID)
			r.dropPermission(state, env.ID)
			return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_pending", err.Error())
		}
		r.log.WarnContext(ctx, "agent rejected permission decision", "perm_id", env.ID, "run_id", runID, "err", err)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "runtime_error", err.Error())
	}
	r.dropPermission(state, env.ID)
	r.rememberAppliedInteractionDecision(env.ID, proto.TypePermissionDecision, fingerprint)
	return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, true, "", "")
}

func (r *Router) dropPermission(s *sessionState, permissionID string) {
	r.mu.Lock()
	delete(r.permIndex, permissionID)
	delete(s.pendingIDs, permissionID)
	r.mu.Unlock()
}

// handlePromptForUserChoiceDecision is the ask-side twin of
// handlePermissionDecision. The server forwards the human's answer
// here; we look up the owning session via askIndex and ask the agent
// to write a matching tool_result back into its CLI.
//
// On both success and ErrUnknownAsk we drop the ask from askIndex /
// pendingAsks so a stale retry can't waste cycles. Timer-fired cancels
// inside the session don't currently call back into the router, so
// those entries linger until cleanupSession — acceptable because they
// can't double-fire (the session's own pendingAskTable.Take already
// guards that); cleanupSession removes the routing entry when the run
// stream closes, even if the underlying CLI remains in the idle pool.
func (r *Router) handlePromptForUserChoiceDecision(ctx context.Context, env proto.Envelope) error {
	if env.ID == "" {
		return errors.New("dispatch: prompt_for_user_choice_decision missing ask id (Envelope.ID empty)")
	}
	var payload proto.PromptForUserChoiceDecisionPayload
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("dispatch: decode prompt_for_user_choice_decision: %w", err)
	}
	if payload.DeliveryID == "" {
		return errors.New("dispatch: prompt_for_user_choice_decision missing delivery_id")
	}
	fingerprint, err := interactionDecisionFingerprint(payload)
	if err != nil {
		return fmt.Errorf("dispatch: fingerprint prompt_for_user_choice_decision: %w", err)
	}
	if handled, err := r.replayAppliedInteractionDecision(ctx, env.ID, payload.DeliveryID, proto.TypePromptForUserChoiceDecision, fingerprint); handled {
		return err
	}

	r.mu.Lock()
	runID, known := r.askIndex[env.ID]
	var state *sessionState
	var session agent.Session
	var finishOperation func()
	if known {
		state = r.sessions[runID]
		if state != nil {
			session, finishOperation, _ = r.preparedOperationLocked(state)
		}
	}
	r.mu.Unlock()

	if !known || state == nil {
		r.log.InfoContext(ctx, "prompt_for_user_choice_decision for unknown ask (run gone)", "ask_id", env.ID)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_pending", "user-input request is no longer pending")
	}
	if session == nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_ready", "user-input request is waiting for the native session")
	}
	defer finishOperation()
	ctx, stop := r.shutdownContext(ctx)
	defer stop()

	responder, supported := session.(agent.UserChoiceResponder)
	if !supported {
		r.dropAsk(state, env.ID)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "unsupported", "runtime does not support user-choice responses")
	}
	err = responder.SubmitPromptForUserChoice(ctx, env.ID, payload)
	if err != nil {
		if errors.Is(err, agent.ErrUnknownAsk) {
			r.log.InfoContext(ctx, "agent reports unknown ask (race with cancel)", "ask_id", env.ID, "run_id", runID)
			r.dropAsk(state, env.ID)
			return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "not_pending", err.Error())
		}
		// Keep the routing entry for transient runtime failures. Codex, for
		// example, restores its pending request when a JSON-RPC reply write
		// fails, so dropping the ask here would turn a retryable error into a
		// permanent not_pending response on the next attempt.
		r.log.WarnContext(ctx, "agent rejected user-input decision", "ask_id", env.ID, "run_id", runID, "err", err)
		return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, false, "runtime_error", err.Error())
	}
	r.dropAsk(state, env.ID)
	r.rememberAppliedInteractionDecision(env.ID, proto.TypePromptForUserChoiceDecision, fingerprint)
	return r.sendInteractionDecisionAck(ctx, env.ID, payload.DeliveryID, true, "", "")
}

func (r *Router) replayAppliedInteractionDecision(ctx context.Context, requestID, deliveryID, kind string, fingerprint [32]byte) (bool, error) {
	key := appliedInteractionDecisionKey(requestID, kind)
	r.mu.Lock()
	applied, ok := r.applied[key]
	r.mu.Unlock()
	if !ok {
		return false, nil
	}
	if applied.requestID != requestID || applied.kind != kind || applied.fingerprint != fingerprint {
		return true, r.sendInteractionDecisionAck(ctx, requestID, deliveryID, false, "decision_conflict", "request was already applied with a different decision")
	}
	return true, r.sendInteractionDecisionAck(ctx, requestID, deliveryID, true, "", "")
}

func (r *Router) rememberAppliedInteractionDecision(requestID, kind string, fingerprint [32]byte) {
	now := time.Now().UTC()
	key := appliedInteractionDecisionKey(requestID, kind)
	r.mu.Lock()
	if len(r.applied) >= 1024 {
		cutoff := now.Add(-time.Hour)
		for id, entry := range r.applied {
			if entry.recordedAt.Before(cutoff) {
				delete(r.applied, id)
			}
		}
	}
	if len(r.applied) >= 1024 {
		for id := range r.applied {
			delete(r.applied, id)
			break
		}
	}
	r.applied[key] = appliedInteractionDecision{
		requestID: requestID, kind: kind, fingerprint: fingerprint, recordedAt: now,
	}
	r.mu.Unlock()
}

func appliedInteractionDecisionKey(requestID, kind string) string {
	return kind + "\x00" + requestID
}

// interactionDecisionFingerprint excludes the transport delivery id. Each
// retry gets a fresh delivery id so a late ack cannot satisfy a newer waiter,
// while the request plus decision content remains stable for daemon replay.
func interactionDecisionFingerprint(payload any) ([32]byte, error) {
	switch decision := payload.(type) {
	case proto.PermissionDecisionPayload:
		decision.DeliveryID = ""
		encoded, err := json.Marshal(decision)
		if err != nil {
			return [32]byte{}, err
		}
		return sha256.Sum256(encoded), nil
	case proto.PromptForUserChoiceDecisionPayload:
		decision.DeliveryID = ""
		encoded, err := json.Marshal(decision)
		if err != nil {
			return [32]byte{}, err
		}
		return sha256.Sum256(encoded), nil
	default:
		return [32]byte{}, fmt.Errorf("unsupported interaction decision %T", payload)
	}
}

func (r *Router) sendInteractionDecisionAck(ctx context.Context, requestID, deliveryID string, applied bool, errorCode, message string) error {
	env, err := proto.NewEnvelope(proto.TypeInteractionDecisionAck, requestID, proto.InteractionDecisionAckPayload{
		DeliveryID: deliveryID,
		Applied:    applied,
		ErrorCode:  errorCode,
		Error:      message,
	})
	if err != nil {
		return fmt.Errorf("dispatch: build interaction decision ack: %w", err)
	}
	if err := r.sender.Send(ctx, env); err != nil {
		return fmt.Errorf("dispatch: send interaction decision ack: %w", err)
	}
	return nil
}

// dropAsk clears askID from both the router-level askIndex and the
// session's pendingAsks set. Safe to call with an askID that's already
// gone — both deletes are no-ops then.
func (r *Router) dropAsk(s *sessionState, askID string) {
	r.mu.Lock()
	delete(r.askIndex, askID)
	delete(s.pendingAsks, askID)
	r.mu.Unlock()
}

// indexPermissionFrame records interaction identities before forwarding them.
// Prepared output may arrive before successful Session publication; decisions
// remain retryable until state.session becomes available.
func (r *Router) indexPermissionFrame(s *sessionState, env proto.Envelope) {
	switch env.Type {
	case proto.TypePermissionRequest:
		var p proto.PermissionRequestPayload
		if err := env.DecodePayload(&p); err != nil {
			return
		}
		requestID := strings.TrimSpace(p.RequestID)
		if requestID == "" {
			requestID = strings.TrimSpace(env.ID)
		}
		if requestID == "" {
			return
		}
		r.mu.Lock()
		if r.interactionRouteOpenLocked(s) {
			r.permIndex[requestID] = s.runID
			s.pendingIDs[requestID] = struct{}{}
		}
		r.mu.Unlock()
	case proto.TypePermissionCancel:
		if env.ID == "" {
			return
		}
		r.mu.Lock()
		delete(r.permIndex, env.ID)
		delete(s.pendingIDs, env.ID)
		r.mu.Unlock()
	case proto.TypePromptForUserChoice:
		// env.ID is the run id (so the server-side dispatch can fan
		// this frame to the run's subscriber); the ask id rides on
		// the payload. Decode just enough to seed the index.
		var p proto.PromptForUserChoicePayload
		if err := env.DecodePayload(&p); err != nil || p.AskID == "" {
			return
		}
		r.mu.Lock()
		if r.interactionRouteOpenLocked(s) {
			r.askIndex[p.AskID] = s.runID
			s.pendingAsks[p.AskID] = struct{}{}
		}
		r.mu.Unlock()
	}
}

func (r *Router) clearInteractionRoutesLocked(s *sessionState) {
	for permissionID := range s.pendingIDs {
		delete(r.permIndex, permissionID)
		delete(s.pendingIDs, permissionID)
	}
	for askID := range s.pendingAsks {
		delete(r.askIndex, askID)
		delete(s.pendingAsks, askID)
	}
}

// drain consumes everything left on ch until the agent closes it.
// Events are dropped — by the time we're draining, either transport
// is dead or the router is shutting down.
