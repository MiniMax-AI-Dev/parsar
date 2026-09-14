package codex

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const observationTimeout = 4 * time.Second

type connectionObservation struct {
	tenant, environment, generation string
	revision                        int64
	connected                       bool
}

// Capture and retain delivery while the socket mutation is still under the lock.
// The existing request or connection owns synchronous delivery outside that lock.
func (r *Registry) connectionObservationLocked(reg *registration, connected bool) *connectionObservation {
	reg.revision++
	r.observations.Add(1)
	return &connectionObservation{reg.key.TenantID, reg.key.EnvironmentID, reg.id, reg.revision, connected}
}

func (r *Registry) replaceGeneration(reg *registration) error {
	defer r.observations.Done()
	ctx, cancel := context.WithTimeout(context.Background(), observationTimeout)
	err := r.replaceConnection(ctx, reg.key.TenantID, reg.key.EnvironmentID, reg.id)
	cancel()
	if err != nil {
		r.lifecycleFailure("replace", reg.key.EnvironmentID, reg.id, err)
	}
	return err
}

func (r *Registry) deliverObservation(observation *connectionObservation) error {
	if observation == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), observationTimeout)
	defer cancel()
	return r.deliverObservationContext(ctx, observation)
}

func (r *Registry) deliverObservationContext(ctx context.Context, observation *connectionObservation) error {
	defer r.observations.Done()
	err := r.observeConnection(ctx, observation.tenant, observation.environment, observation.generation, observation.revision, observation.connected)
	if err != nil {
		r.lifecycleFailure("observe", observation.environment, observation.generation, err)
	}
	return err
}

func (r *Registry) lifecycleFailure(operation, environment, generation string, err error) {
	log.Bg().Error("native executor connection lifecycle write failed", "operation", operation, "environment_id", environment)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrInvalidInput) {
		// A deleted or terminal target cannot authorize a connection, but does not disable peers.
		r.mu.Lock()
		if reg := r.registrations[environment]; reg != nil && (operation == "replace" || reg.id == generation) {
			delete(r.registrations, environment)
			r.closeConnectionLocked(reg, reg.socket)
		}
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	if r.lifecycleErr == nil {
		r.lifecycleErr = err
	}
	r.mu.Unlock()
	// Do not wait here: this call can itself own an observation being drained.
	r.closeConnections()
}

// LifecycleError reports the first persistence failure that closed the registry.
func (r *Registry) LifecycleError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lifecycleErr
}

// Close stops admission and drains accepted lifecycle writes before returning.
// The execution owner must retain its lease until Close completes.
func (r *Registry) Close() {
	r.closeConnections()
	r.observations.Wait()
}

func (r *Registry) closeConnections() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	for hash, credential := range r.harnessKeys {
		credential.stop()
		delete(r.harnessKeys, hash)
	}
	var observations []*connectionObservation
	for environment, reg := range r.registrations {
		if observation := r.closeConnectionLocked(reg, reg.socket); observation != nil {
			observations = append(observations, observation)
		}
		delete(r.registrations, environment)
	}
	r.mu.Unlock()
	// One budget covers the complete shutdown batch, including callbacks waiting
	// for the leased writer. Expired callbacks still settle their delivery count.
	ctx, cancel := context.WithTimeout(context.Background(), observationTimeout)
	defer cancel()
	for _, observation := range observations {
		_ = r.deliverObservationContext(ctx, observation)
	}
}
