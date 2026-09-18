package dispatch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (r *Router) handleFunctionResult(ctx context.Context, env proto.Envelope) error {
	var result proto.FunctionResultPayload
	if err := env.DecodePayload(&result); err != nil {
		return err
	}
	if env.ID == "" || result.CallID == "" || result.DeliveryID == "" {
		return errors.New("function result requires run, call and delivery identities")
	}
	if err := result.ValidateContent(); err != nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "invalid_result", err.Error())
	}
	decision := result
	decision.DeliveryID = ""
	encoded, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	fingerprint := sha256.Sum256(encoded)
	// Scope receipt replay to both identities, even when native call IDs repeat across Runs.
	kind := proto.TypeFunctionResult + "\x00" + result.CallID
	if handled, err := r.replayAppliedInteractionDecision(ctx, env.ID, result.DeliveryID, kind, fingerprint); handled {
		return err
	}
	r.mu.Lock()
	state := r.sessions[env.ID]
	session, finishOperation, ready := r.preparedOperationLocked(state)
	var submitter agent.FunctionResultSubmitter
	if ready {
		submitter, _ = session.(agent.FunctionResultSubmitter)
	}
	r.mu.Unlock()
	if state != nil && !ready {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "not_ready", "function call is waiting for the native session")
	}
	if finishOperation != nil {
		defer finishOperation()
		var stop context.CancelFunc
		ctx, stop = r.shutdownContext(ctx)
		defer stop()
	}
	if submitter == nil {
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, "not_pending", "function call is no longer pending")
	}
	if err := submitter.SubmitFunctionResult(ctx, result); err != nil {
		code := "runtime_error"
		if errors.Is(err, agent.ErrUnknownFunctionCall) {
			code = "not_pending"
		}
		return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, false, code, "function result was not applied")
	}
	r.rememberAppliedInteractionDecision(env.ID, kind, fingerprint)
	return r.sendInteractionDecisionAck(ctx, env.ID, result.DeliveryID, true, "", "")
}
