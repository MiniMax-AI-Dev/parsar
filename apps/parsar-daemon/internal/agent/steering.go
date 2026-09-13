package agent

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Steerer optionally delivers additional text to the session's active turn.
type Steerer interface {
	Steer(context.Context, proto.PromptSteerPayload) error
}

// DurableSteerer reports one complete write synchronously, then waits for the native receipt.
type DurableSteerer interface {
	SteerWithReceipt(context.Context, proto.PromptSteerPayload, func()) error
}

// ErrSteeringNotReady means no input was sent because the turn is starting.
var ErrSteeringNotReady = errors.New("agent: turn is not ready for input")

// ErrSteeringInactive means no input was sent because the run ended or was cancelled.
var ErrSteeringInactive = errors.New("agent: run is no longer active")

// ErrSteeringRejected means the engine explicitly rejected the input.
var ErrSteeringRejected = errors.New("agent: input rejected")
