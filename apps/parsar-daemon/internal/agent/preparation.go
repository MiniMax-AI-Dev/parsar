package agent

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Prepared owns native resources until Start returns a non-nil Session. The
// preparation owner context spans the eventual Session; Start's context is local
// to that operation. A nil Session leaves preparation cleanup with the caller.
type Prepared interface {
	// Start transfers output ownership only when it returns a non-nil Session.
	// A nil Session leaves the caller as the sole owner of closing out, and the
	// implementation must not retain or write to it after Start returns.
	Start(context.Context, string, string, chan<- proto.Envelope) (Session, error)
	// Close retains unused ownership on error; callers may retry settlement.
	Close() error
}

// PreparedCancellation is required for executable preparations and follows the
// same native resource across Start. Read-only preparations need only Prepared.
type PreparedCancellation interface {
	Prepared
	// Cancel returns after local cleanup and all output writes have stopped.
	// An error retains ownership so callers can retry this exact object serially.
	Cancel(context.Context) error
	CancellationOutcome() proto.DonePayload
}

// A factory may return both a resource and an error when construction failed but
// cleanup remains unconfirmed. The caller must retain and close that resource.
type PreparationFactory func(context.Context, proto.PromptRequestPayload) (Prepared, error)

// RegisterPreparation installs a separate execution-only path. Product factory
// wrappers must not add authoring or capability-download side effects to it.
func (r *Registry) RegisterPreparation(kind string, workspaceRead bool, prepare PreparationFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, exists := r.kinds[kind]
	if !exists || prepare == nil {
		panic("agent.Registry.RegisterPreparation: registered kind and factory required")
	}
	r.preparers[kind] = prepare
	info.Capabilities.Preparation = true
	info.Capabilities.WorkspaceReadPreparation = workspaceRead
	r.kinds[kind] = info
}

func (r *Registry) ResolvePreparation(kind string) (PreparationFactory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f := r.preparers[kind]
	if f == nil {
		return nil, fmt.Errorf("agent: preparation unavailable for %q", kind)
	}
	return f, nil
}
