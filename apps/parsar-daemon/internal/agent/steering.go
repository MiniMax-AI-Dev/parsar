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

// ErrSteeringNotReady means no input was sent because the turn is starting.
var ErrSteeringNotReady = errors.New("agent: turn is not ready for input")

// ErrSteeringInactive means no input was sent because the run ended or was cancelled.
var ErrSteeringInactive = errors.New("agent: run is no longer active")
