package api

import "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"

// WithExecutionPolicy supplies the same immutable qualification used by the
// execution Dispatcher. Omission uses the built-in engine registrations.
func WithExecutionPolicy(policy execution.Policy) Option {
	return func(h *Handler) { h.policy = policy }
}
