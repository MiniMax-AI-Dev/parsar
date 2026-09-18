package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// RuntimeProviders is trusted operator wiring, not public Environment input.
// Each stable key identifies one provider backend/installation across restarts;
// changing that target requires a new key, preserving the old cleanup adapter.
type RuntimeProviders struct {
	CoreURL   string
	Providers map[string]sandbox.Provider
}

type runtimeLifecycle struct {
	store    *store.Store
	registry *gateway.Registry
	config   RuntimeProviders
	gate     chan struct{}
	ctx      context.Context
	stop     context.CancelFunc
	cursor   string
}

func newRuntimeLifecycle(s *store.Store, registry *gateway.Registry, config *RuntimeProviders) (*runtimeLifecycle, error) {
	if config == nil {
		return nil, nil
	}
	u, err := url.Parse(config.CoreURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(config.Providers) == 0 || registry == nil {
		return nil, sandbox.ErrInvalid
	}
	copied := RuntimeProviders{CoreURL: config.CoreURL, Providers: make(map[string]sandbox.Provider, len(config.Providers))}
	for key, provider := range config.Providers {
		id, err := uuid.Parse(key)
		if err != nil || id == uuid.Nil || id.String() != key || provider == nil {
			return nil, sandbox.ErrInvalid
		}
		copied.Providers[key] = provider
	}
	ctx, stop := context.WithCancel(context.Background())
	return &runtimeLifecycle{store: s, registry: registry, config: copied, gate: make(chan struct{}, 1), ctx: ctx, stop: stop}, nil
}

func (r *runtimeLifecycle) lock(ctx context.Context) error {
	select {
	case r.gate <- struct{}{}:
		if err := r.ctx.Err(); err != nil {
			<-r.gate
			return err
		}
		if err := ctx.Err(); err != nil {
			<-r.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// ProvisionEnvironment is an internal bootstrap operation for an already
// authorized hosted Environment. It does not enable public hosted admission.
func (w *Worker) ProvisionEnvironment(ctx context.Context, tenant, environment, providerKey string) (store.RuntimeAllocation, error) {
	if w.runtimes == nil {
		return store.RuntimeAllocation{}, ErrExecutionUnavailable
	}
	r := w.runtimes
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	detach := context.AfterFunc(r.ctx, cancel)
	defer func() { detach(); cancel() }()
	if err := r.lock(ctx); err != nil {
		return store.RuntimeAllocation{}, err
	}
	defer func() { <-r.gate }()
	provider := r.config.Providers[providerKey]
	if provider == nil {
		return store.RuntimeAllocation{}, sandbox.ErrInvalid
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return store.RuntimeAllocation{}, err
	}
	token := hex.EncodeToString(secret)
	owner, err := r.store.ReserveRuntimeAllocation(ctx, tenant, environment, providerKey, device.HashCredential(token))
	if err != nil || owner.Replayed {
		return owner, err
	}
	if err := r.store.CheckExecutionOwnership(ctx); err != nil {
		return owner, err
	}
	info, err := provider.Create(ctx, sandbox.Bootstrap{
		Reference: runtimeReference(owner), SessionID: owner.SessionID, DeviceID: owner.DeviceID,
		CoreURL: r.config.CoreURL, Credential: token,
	})
	if err != nil {
		// The existing allocation remains discoverable even if the request outcome
		// is unknown. Reconciliation observes it; it never sends Create again.
		return owner, err
	}
	if info.Reference != runtimeReference(owner) || info.ProviderID == "" || info.State != "running" || !info.BootstrapComplete {
		return owner, sandbox.ErrOwnership
	}
	return r.store.ObserveRuntimeRunning(ctx, owner)
}

// ReconcileManagedRuntimes is also callable before serving admission. One scan
// observes existing allocations only; it never retries startup or native work.
func (w *Worker) ReconcileManagedRuntimes(ctx context.Context) error {
	if w.runtimes == nil {
		return nil
	}
	r := w.runtimes
	ctx, cancel := context.WithCancel(ctx)
	detach := context.AfterFunc(r.ctx, cancel)
	defer func() { detach(); cancel() }()
	if err := r.lock(ctx); err != nil {
		return err
	}
	defer func() { <-r.gate }()
	rows, err := r.store.ListRuntimeAllocations(ctx, r.cursor)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		r.cursor = ""
		return nil
	}
	for _, owner := range rows {
		r.cursor = owner.ID
		operation, stop := context.WithTimeout(ctx, 30*time.Second)
		err := r.observe(operation, owner)
		stop()
		if err != nil {
			if ownership := r.store.CheckExecutionOwnership(ctx); ownership != nil {
				return ownership
			}
			// Provider errors can include operator configuration. Log safe identity
			// only; retain the durable owner for the next bounded observation.
			log.Ctx(ctx).Warn("managed Runtime observation incomplete", "allocation_id", owner.ID)
		}
	}
	return nil
}

func (r *runtimeLifecycle) observe(ctx context.Context, owner store.RuntimeAllocation) error {
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		var err error
		owner, err = r.store.RequestRuntimeCleanup(ctx, owner)
		if err != nil {
			return err
		}
	}
	provider := r.config.Providers[owner.ProviderKey]
	if provider == nil {
		return sandbox.ErrInvalid
	}
	if err := r.store.CheckExecutionOwnership(ctx); err != nil {
		return err
	}
	info, err := provider.GetInfo(ctx, runtimeReference(owner))
	if err != nil && !errors.Is(err, sandbox.ErrNotFound) {
		return err
	}
	running := err == nil && info.Reference == runtimeReference(owner) && info.ProviderID != "" && info.State == "running"
	if err == nil && info.Reference != runtimeReference(owner) {
		return sandbox.ErrOwnership
	}
	if running && info.BootstrapComplete && !owner.CreateSettled {
		// Only the adapter can qualify completion of its bootstrap writes.
		owner, err = r.store.SettleRuntimeCreation(ctx, owner)
		if err != nil {
			return err
		}
	}
	if owner.SessionDeleted || owner.Expired || owner.State == "cleanup_pending" {
		owner, err = r.store.RequestRuntimeCleanup(ctx, owner)
		if err != nil {
			return err
		}
		if err := r.store.CheckExecutionOwnership(ctx); err != nil {
			return err
		}
		if err := provider.Kill(ctx, runtimeReference(owner)); err != nil {
			return err
		}
		if !owner.CreateSettled {
			return nil // Keep scanning unknown creation; absence is not a final receipt.
		}
		_, err = r.store.ReleaseRuntimeAllocation(ctx, owner)
		return err
	}
	// A stopped or missing container does not authorize destroying retained
	// workspace/history. Preserve it until explicit cleanup or actual expiry.
	if !running || !info.BootstrapComplete {
		return nil
	}
	owner, err = r.store.ObserveRuntimeRunning(ctx, owner)
	if err != nil {
		return err
	}
	if _, err := r.registry.LookupDevice(owner.DeviceID); err != nil {
		return nil
	}
	renewed, err := provider.Renew(ctx, runtimeReference(owner))
	if err != nil {
		return err
	}
	if renewed.Reference != runtimeReference(owner) || renewed.State != "running" || !renewed.BootstrapComplete {
		return sandbox.ErrOwnership
	}
	_, err = r.store.KeepRuntimeAllocation(ctx, owner)
	return err
}

func runtimeReference(owner store.RuntimeAllocation) sandbox.Reference {
	return sandbox.Reference{TenantID: owner.TenantID, EnvironmentID: owner.EnvironmentID, AllocationID: owner.ID}
}

func (w *Worker) runManagedRuntimes(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.ReconcileManagedRuntimes(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
