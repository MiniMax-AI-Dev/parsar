// Package agent is the registration surface for agent_kind
// implementations. The dispatch router consults agent.Registry to map
// an inbound prompt_request's AgentKind to its factory.
//
// Lifetime / channel ownership:
//
//   - Factory takes an out chan<- proto.Envelope owned by the dispatch
//     router. The agent SENDS upstream events on it and OWNS the
//     close: it MUST close(out) exactly once when the session is
//     fully done, AFTER emitting a terminal "done" or "error" frame.
//     The router uses the close as the "session terminated" signal.
//
//   - Session.Cancel is best-effort and idempotent: a session that
//     already finished naturally must accept a Cancel call without
//     panicking.
//
//   - Session.SubmitPermission delivers a permission_decision back to
//     the agent. The permID is the daemon-minted "perm_<8hex>"
//     identifier from the matching upstream permission_request.
//     Returns ErrUnknownPermission for unknown / expired permission ids
//     so callers can decide whether to log or surface.
package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Factory builds a Session for one prompt_request. out is the upstream
// channel the agent writes into; the agent owns its close (see package
// doc). ctx is cancelled by the router to wind the session down.
type Factory func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (Session, error)

// Session is one in-flight prompt run. Completion is signalled by
// closing the out channel passed to its Factory.
type Session interface {
	// Cancel signals the session to abort. Idempotent. Actual teardown
	// happens asynchronously and is signalled via the out channel close.
	Cancel(ctx context.Context) error

	// SubmitPermission delivers a human verdict for an outstanding
	// permission_request. Returns ErrUnknownPermission when permID is
	// unknown / already resolved / expired.
	SubmitPermission(ctx context.Context, permID string, decision proto.PermissionDecisionPayload) error

	// SubmitPromptForUserChoice delivers a human's answer for an
	// outstanding prompt_for_user_choice (Claude Code AskUserQuestion).
	// Returns ErrUnknownAsk when askID is unknown / already resolved.
	SubmitPromptForUserChoice(ctx context.Context, askID string, decision proto.PromptForUserChoiceDecisionPayload) error
}

// ErrUnknownPermission is returned by Session.SubmitPermission when
// the permID doesn't match any outstanding request. The router uses
// this to distinguish a benign race from a real forwarding failure.
var ErrUnknownPermission = errors.New("agent: unknown permission id")

// ErrUnknownAsk is returned by Session.SubmitPromptForUserChoice when
// the askID doesn't match any outstanding ask. Same race semantics as
// ErrUnknownPermission.
var ErrUnknownAsk = errors.New("agent: unknown ask id")

var ErrUnsupportedKind = errors.New("agent: unsupported agent_kind")

// Registry maps agent_kind → Factory and keeps the daemon-advertised
// capability descriptor for each kind. Safe for concurrent use.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	kinds     map[string]proto.SupportedAgentKind
}

func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[string]Factory),
		kinds:     make(map[string]proto.SupportedAgentKind),
	}
}

// Register installs f as the factory for kind with a basic available
// descriptor. Panics on empty kind or nil factory.
func (r *Registry) Register(kind string, f Factory) {
	r.RegisterKind(proto.SupportedAgentKind{Kind: kind, Available: true}, f)
}

// RegisterKind installs f and the heartbeat descriptor for an
// agent_kind. Callers may set Available=false when an adapter exists
// but its underlying CLI is not usable.
func (r *Registry) RegisterKind(info proto.SupportedAgentKind, f Factory) {
	kind := info.Kind
	if kind == "" {
		panic("agent.Registry.Register: empty kind")
	}
	if f == nil {
		panic("agent.Registry.Register: nil factory")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[kind] = f
	r.kinds[kind] = info
}

// Resolve returns the factory for kind, or wraps ErrUnsupportedKind.
func (r *Registry) Resolve(kind string) (Factory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.factories[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
	}
	return f, nil
}

// Kinds returns the list of registered kinds sorted lexicographically.
func (r *Registry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// SupportedAgentKinds returns the daemon-advertised capability
// descriptors sorted by kind so heartbeat payloads are stable.
func (r *Registry) SupportedAgentKinds() []proto.SupportedAgentKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]proto.SupportedAgentKind, 0, len(r.factories))
	for kind := range r.factories {
		info := r.kinds[kind]
		if info.Kind == "" {
			info = proto.SupportedAgentKind{Kind: kind, Available: true}
		}
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b proto.SupportedAgentKind) int {
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		return 0
	})
	return out
}
