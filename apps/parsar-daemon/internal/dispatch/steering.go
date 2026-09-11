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
			pending := r.queueSteering(ctx, env, input)
			if pending == nil {
				return nil
			}
			ack = *pending
		}
	}
	return r.sendSteeringAck(ctx, env, ack)
}

func (r *Router) sendSteeringAck(ctx context.Context, env proto.Envelope, ack proto.PromptSteerAckPayload) error {
	reply, err := proto.NewEnvelopeWithTrace(proto.TypePromptSteerAck, env.ID, ack, env.Trace)
	if err != nil {
		return err
	}
	return r.sender.Send(ctx, reply)
}

func (r *Router) queueSteering(ctx context.Context, env proto.Envelope, input proto.PromptSteerPayload) *proto.PromptSteerAckPayload {
	ack := proto.PromptSteerAckPayload{InputID: input.InputID}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.sessions[env.ID]
	if state == nil || r.closed {
		ack.ErrorCode, ack.Error = "run_inactive", "The run is no longer active."
		return &ack
	}
	fingerprint := sha256.Sum256([]byte(input.Text))
	if previous, ok := state.steering[input.InputID]; ok {
		if previous.fingerprint != fingerprint {
			ack.ErrorCode, ack.Error = "input_conflict", "This input ID was already used with different text."
			return &ack
		}
		if previous.ack.ErrorCode != "not_ready" && previous.ack.ErrorCode != "busy" {
			return &previous.ack
		}
	} else if len(state.steering) >= maxSteeringInputs {
		ack.ErrorCode, ack.Error = "input_limit", "The active run has reached its steering input limit."
		return &ack
	}
	if state.steering == nil {
		state.steering = make(map[string]steeringReceipt)
	}
	ack.ErrorCode, ack.Error = "not_ready", "The run is still starting."
	// Bind input identity before any retryable state so changed text cannot
	// slip through a startup or in-flight retry.
	state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: ack}
	if state.session == nil {
		return &ack
	}
	steerer, ok := state.session.(agent.Steerer)
	if !ok {
		ack.ErrorCode, ack.Error = "unsupported", "This engine does not support active-turn input."
		return &ack
	}
	if state.steerBusy {
		ack.ErrorCode, ack.Error = "busy", "Another input is awaiting an engine receipt; retry this input later."
		state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: ack}
		return &ack
	}
	ack.ErrorCode, ack.Error = "in_flight", "This input is awaiting an engine receipt."
	state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: ack}
	state.steerBusy = true
	r.shutdownWG.Add(1)
	go func() {
		defer r.shutdownWG.Done()
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		result := steeringResult(input.InputID, steerer.Steer(callCtx, input))
		cancel()
		r.mu.Lock()
		state.steering[input.InputID] = steeringReceipt{fingerprint: fingerprint, ack: result}
		state.steerBusy = false
		r.mu.Unlock()
		sendCtx, sendCancel := context.WithTimeout(ctx, 5*time.Second)
		defer sendCancel()
		if err := r.sendSteeringAck(sendCtx, env, result); err != nil {
			r.log.WarnContext(ctx, "steering receipt delivery failed", "run_id", env.ID, "input_id", input.InputID, "err", err)
		}
	}()
	return nil
}

func steeringResult(inputID string, err error) proto.PromptSteerAckPayload {
	ack := proto.PromptSteerAckPayload{InputID: inputID}
	switch {
	case errors.Is(err, agent.ErrSteeringNotReady):
		ack.ErrorCode, ack.Error = "not_ready", err.Error()
	case errors.Is(err, agent.ErrSteeringInactive):
		ack.ErrorCode, ack.Error = "run_inactive", err.Error()
	case errors.Is(err, agent.ErrSteeringRejected):
		ack.ErrorCode, ack.Error = "rejected", err.Error()
	case err != nil:
		// A timeout or broken connection may follow native acceptance. Preserve
		// the uncertainty and never automatically send this input again.
		ack.ErrorCode, ack.Error = "outcome_unknown", err.Error()
	default:
		ack.Accepted = true
	}
	return ack
}
