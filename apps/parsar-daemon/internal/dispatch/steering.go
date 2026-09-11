package dispatch

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Retain all attempts for the active run; reject overflow rather than evicting
// receipts and risking a duplicate native input. Durable recovery is server-owned.
const maxSteeringInputs = 256

type steeringReceipt struct {
	fingerprint [32]byte
	ack         proto.PromptSteerAckPayload
}

func (r *Router) handlePromptSteer(ctx context.Context, env proto.Envelope) error {
	var input proto.PromptSteerPayload
	ack := proto.PromptSteerAckPayload{}
	if err := env.DecodePayload(&input); err != nil {
		ack.ErrorCode, ack.Error = "invalid_input", "Invalid steering payload."
	} else {
		ack.InputID = input.InputID
		if env.ID == "" || strings.TrimSpace(input.InputID) == "" || len(input.InputID) > 256 || strings.TrimSpace(input.Text) == "" {
			ack.ErrorCode, ack.Error = "invalid_input", "Run ID, input ID (up to 256 bytes), and non-empty text are required."
		} else {
			ack = r.steer(ctx, env.ID, input)
		}
	}
	reply, err := proto.NewEnvelopeWithTrace(proto.TypePromptSteerAck, env.ID, ack, env.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}

func (r *Router) steer(ctx context.Context, runID string, input proto.PromptSteerPayload) proto.PromptSteerAckPayload {
	ack := proto.PromptSteerAckPayload{InputID: input.InputID}
	r.mu.Lock()
	state := r.sessions[runID]
	var session agent.Session
	if state != nil {
		session = state.session
	}
	r.mu.Unlock()
	if state == nil {
		ack.ErrorCode, ack.Error = "run_inactive", "The run is no longer active."
		return ack
	}
	if session == nil {
		ack.ErrorCode, ack.Error = "not_ready", "The run is still starting."
		return ack
	}
	steerer, ok := session.(agent.Steerer)
	if !ok {
		ack.ErrorCode, ack.Error = "unsupported", "This engine does not support active-turn input."
		return ack
	}
	// Handle calls are serialized by the transport read loop. The pump can
	// remove state from the router, but never reads or writes these receipts.
	fingerprint := sha256.Sum256([]byte(input.Text))
	if previous, ok := state.steering[input.InputID]; ok {
		if previous.fingerprint == fingerprint {
			return previous.ack
		}
		ack.ErrorCode, ack.Error = "input_conflict", "This input ID was already used with different text."
		return ack
	}
	if len(state.steering) >= maxSteeringInputs {
		ack.ErrorCode, ack.Error = "input_limit", "The active run has reached its steering input limit."
		return ack
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := steerer.Steer(callCtx, input)
	switch {
	case errors.Is(err, agent.ErrSteeringNotReady):
		ack.ErrorCode, ack.Error = "not_ready", err.Error()
		return ack
	case errors.Is(err, agent.ErrSteeringInactive):
		ack.ErrorCode, ack.Error = "run_inactive", err.Error()
		return ack
	case err != nil:
		// A timeout or broken connection may follow native acceptance. Preserve
		// the uncertainty and never automatically send this input again.
		ack.ErrorCode, ack.Error = "outcome_unknown", err.Error()
	default:
		ack.Accepted = true
	}
	if state.steering == nil {
		state.steering = make(map[string]steeringReceipt)
	}
	state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: ack}
	return ack
}
