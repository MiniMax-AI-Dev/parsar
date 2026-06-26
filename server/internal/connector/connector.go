package connector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrNotSupported is returned by a connector method when the operation
// is not advertised in Capabilities(). Callers must check Capabilities()
// before invoking optional methods; ErrNotSupported is the safety-net.
var ErrNotSupported = errors.New("connector: capability not supported")

// ErrUnknownConnector is returned by Registry.Get when no connector is
// registered for the requested type.
var ErrUnknownConnector = errors.New("connector: unknown type")

// ErrDuplicateConnector is returned by Registry.Register when a connector
// of the given type is already registered.
var ErrDuplicateConnector = errors.New("connector: type already registered")

// Capabilities is the static feature declaration of a connector.
type Capabilities struct {
	Sync bool

	Streaming bool

	// Cancellation aborts an in-flight prompt; it does NOT destroy the
	// session (see Close).
	Cancellation bool

	Permissions bool

	// Usage is true if the connector reports real token/cost in
	// PromptOutput.Usage (vs. placeholder).
	Usage bool

	// Audit is true if the connector emits tool_call_before /
	// tool_call_after with Args/Result populated.
	Audit bool

	// Auth is true if the connector can refresh credentials on demand.
	Auth bool
}

// String returns a stable comma-separated label of the truthy
// capability flags.
func (c Capabilities) String() string {
	flags := []struct {
		name string
		on   bool
	}{
		{"sync", c.Sync},
		{"streaming", c.Streaming},
		{"cancellation", c.Cancellation},
		{"permissions", c.Permissions},
		{"usage", c.Usage},
		{"audit", c.Audit},
		{"auth", c.Auth},
	}
	parts := make([]string, 0, len(flags))
	for _, f := range flags {
		if f.on {
			parts = append(parts, f.name)
		}
	}
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, ",")
}

// AgentConnector is the single surface every backing agent system must
// implement to be reachable from Parsar.
//
// Lifetime semantics:
//
//   - Type() and Capabilities() are static; they MUST not change after
//     registration.
//   - Prompt is called once per agent_run; the connector decides whether
//     to spin up a fresh session or reuse a per-conversation one.
//   - Cancel may arrive after Prompt has started but before it returns;
//     the connector should make the running Prompt return promptly with
//     a context-cancelled error.
//   - Close is called when the conversation is archived/deleted. It is
//     idempotent and MUST be safe to call on an unknown conversation.
type AgentConnector interface {
	// Type returns the connector_type string this connector handles
	// (e.g. "agent_daemon", "http", "a2a"). Must match the values
	// allowed by the agents/agent_runs.connector_type column.
	Type() string

	Capabilities() Capabilities

	Prompt(ctx context.Context, in PromptInput) (PromptOutput, error)

	// StreamPrompt is the asynchronous event-stream path. Connectors
	// that do not implement it MUST return (nil, ErrNotSupported).
	StreamPrompt(ctx context.Context, in PromptInput) (<-chan PromptEvent, error)

	// Cancel aborts the in-flight prompt for the given conversation.
	// Returns ErrNotSupported when Capabilities.Cancellation is false.
	// Cancelling a conversation with no in-flight prompt is a no-op.
	Cancel(ctx context.Context, conversationID string) error

	// Abort aborts a specific agent run/session. Unknown or already-
	// cleaned-up bindings are a no-op.
	Abort(ctx context.Context, input AbortInput) error

	// SubmitPermission delivers a human approval verdict back to the
	// connector. Returns ErrNotSupported when Capabilities.Permissions
	// is false.
	SubmitPermission(ctx context.Context, decision PermissionDecision) error

	// SubmitPromptForUserChoice delivers the human's pick for an
	// outstanding AskUserQuestion. Returns ErrNotSupported on
	// connectors that don't intercept the tool. Implementations should
	// be idempotent on (RequestID): a duplicate submit after a
	// successful one returns a clear "not pending" error so callers
	// can surface a 410.
	SubmitPromptForUserChoice(ctx context.Context, decision PromptForUserChoiceDecision) error

	// Close releases any resources held for a conversation. Idempotent.
	// Required even for connectors with no per-conversation state.
	Close(ctx context.Context, conversationID string) error
}

type AbortInput struct {
	ConversationID string
	RunID          string
}

// Registry is the lookup table from connector_type to AgentConnector.
// Concurrency-safe; Register is typically called at server boot from a
// single goroutine, while Get / Types is read from request handlers.
type Registry struct {
	mu    sync.RWMutex
	conns map[string]AgentConnector
}

func NewRegistry() *Registry {
	return &Registry{conns: map[string]AgentConnector{}}
}

// Register adds a connector under its declared Type(). Returns
// ErrDuplicateConnector when the type is already registered.
func (r *Registry) Register(conn AgentConnector) error {
	if conn == nil {
		return fmt.Errorf("connector: cannot register nil connector")
	}
	connType := strings.TrimSpace(conn.Type())
	if connType == "" {
		return fmt.Errorf("connector: connector returned empty Type()")
	}
	if !conn.Capabilities().Sync {
		return fmt.Errorf("connector %q: Capabilities.Sync must be true (Prompt is mandatory)", connType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.conns[connType]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateConnector, connType)
	}
	r.conns[connType] = conn
	return nil
}

// MustRegister panics on error; convenient at boot.
func (r *Registry) MustRegister(conn AgentConnector) {
	if err := r.Register(conn); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(connType string) (AgentConnector, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.conns[connType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownConnector, connType)
	}
	return conn, nil
}

// Types returns the list of registered connector types in sorted order.
func (r *Registry) Types() []string {
	r.mu.RLock()
	types := make([]string, 0, len(r.conns))
	for t := range r.conns {
		types = append(types, t)
	}
	r.mu.RUnlock()
	sort.Strings(types)
	return types
}
