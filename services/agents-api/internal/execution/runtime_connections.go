package execution

import (
	"context"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Access is serialized by the existing lifecycle gate. Durable generations fence
// old observations; this map only remembers the currently observed socket.
type runtimeConnection struct {
	peer       *gateway.Session
	generation string
	revision   int64
	connected  bool
}

func (r *runtimeLifecycle) observeConnection(ctx context.Context, owner store.RuntimeAllocation) error {
	bound, err := r.store.GetSessionDevice(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	if bound.ID != owner.DeviceID || bound.EnvironmentID != owner.EnvironmentID {
		return store.ErrDeviceBindingConflict
	}
	peer, err := r.registry.LookupDevice(owner.DeviceID)
	connected := err == nil && !peer.IsClosed()
	current := r.connections[owner.ID]
	if connected {
		// Initial connection is published only after bootstrap ownership is
		// settled. Native execution readiness remains a separate preparation.
		if !owner.CreateSettled || owner.State != "running" {
			return nil
		}
		if current == nil || current.peer != peer {
			generation := uuid.NewString()
			if err := r.store.ReplaceEnvironmentConnection(ctx, owner.TenantID, owner.EnvironmentID, generation); err != nil {
				return err
			}
			current = &runtimeConnection{peer: peer, generation: generation}
			r.connections[owner.ID] = current
		}
	}
	if current == nil || current.connected == connected {
		return nil
	}
	current.revision++
	if err := r.store.ObserveEnvironmentConnection(ctx, owner.TenantID, owner.EnvironmentID, current.generation, current.revision, connected); err != nil {
		return err
	}
	current.connected = connected
	return nil
}
