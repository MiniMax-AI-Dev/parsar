package agent

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Prepared owns native resources until Start transfers them to a Session. The
// preparation owner context spans the eventual Session; Start's context is local
// to that operation. The caller closes abandoned or failed preparations.
type Prepared interface {
	Start(context.Context, string, string, chan<- proto.Envelope) (Session, error)
	Close() error
}

// PreparedCancellation optionally follows cancellation across Start's resource transfer.
type PreparedCancellation interface {
	Prepared
	Cancel(context.Context) error
	CancellationOutcome() proto.DonePayload
}

type PreparationFactory func(context.Context, proto.PromptRequestPayload) (Prepared, error)

// RegisterPreparation installs a separate execution-only path. Product factory
// wrappers must not add authoring or capability-download side effects to it.
func (r *Registry) RegisterPreparation(kind string, prepare PreparationFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, exists := r.kinds[kind]
	if !exists || prepare == nil {
		panic("agent.Registry.RegisterPreparation: registered kind and factory required")
	}
	r.preparers[kind] = prepare
	info.Capabilities.Preparation = true
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
