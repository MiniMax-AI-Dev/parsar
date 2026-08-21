// Sandbox lazy-create path for the agent_daemon connector.
//
// Beyond the "user runs parsar-daemon on their laptop" topology, the
// connector supports a second deployment mode: the server spawns a
// fresh e2b sandbox per agent, runs parsar-daemon inside it, and
// lets that sandbox dial back to the gateway over WS. The connector
// treats both modes identically once the WS session is registered —
// only the cold-start path differs.
//
// This file owns the sandbox half. Wired by main.go only when
// AGENT_DAEMON_SANDBOX_TEMPLATE is set; otherwise the connector runs
// in local-only mode and sandbox-mode agents fail fast at the
// configuration validation layer.
//
// Lifecycle (per agent):
//
//	first prompt  -> Connector hits ErrNotBound -> sees daemon_mode=sandbox
//	             -> SandboxProvider.Acquire(ctx, in):
//	                  1. CreateRuntimePairing(type=agent_daemon)  -> token + runtimeID(=deviceID)
//	                  2. e2b.Create("parsar-sandbox-e2b")        -> sandbox handle
//	                  3. RunCommand(parsar-daemon connect -b + env token)
//	                     -> daemon pairs, then dials WS in background
//	                  4. Registry.WaitForDevice(deviceID, 45s)     -> blocks until WS upgrade lands
//	                  5. Binder.Bind(conversation -> deviceID)     -> persist for next turn
//	                  6. return deviceID
//	          every sandbox prompt -> Acquire verifies/reuses the active sandbox
//	          new conversation against same agent -> Acquire reuses the same sandbox
//	          long idle period -> Reap() kills the sandbox + evicts cache; next Acquire cold-starts again
//	          conversation archived / agent deleted -> Release() kills + evicts immediately
package agentdaemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"strings"
	"sync"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// SandboxAcquireTimeout caps the total cold-start time. 45s covers
// e2b.Create + RunCommand + WaitForDevice with margin for cold cache
// misses on e2b's side. Package-level so tests can lower it.
var SandboxAcquireTimeout = 45 * time.Second

// SandboxConnectTimeout bounds WaitForDevice — how long the daemon
// gets to dial in after `parsar-daemon connect -b` returns. 30s covers
// DNS + TLS + WS handshake.
var SandboxConnectTimeout = 30 * time.Second

// SandboxDefaultTTL is the fallback e2b sandbox TTL requested on Create
// when E2BProviderConfig.TTL is unset.
//
// The default is deliberately provider-neutral. Deployments that need a
// different lifetime can override it with AGENT_DAEMON_SANDBOX_TTL; the
// backing provider remains authoritative about the limits it accepts.
var SandboxDefaultTTL = time.Hour

// SandboxIdleReapThreshold is the fallback lastUsed cutoff Reap honours
// when E2BProviderConfig.TTL is unset.
//
// Kept >= the effective TTL so Reap does not preempt the provider lease.
// Every run refreshes lastUsed through Acquire; Reap is a defence-in-depth
// sweep for inactive entries whose automatic renewal is disabled.
var SandboxIdleReapThreshold = time.Hour

// SandboxSpawnAbandonedAfter is how old a `spawning` sandbox binding
// must be before another acquire may declare it abandoned and take its
// reservation slot (see reclaimAbandonedSpawn).
//
// Must stay comfortably above SandboxAcquireTimeout so a merely slow
// cold start is never stolen mid-flight; 5m leaves ~6x headroom while
// still self-healing long before a human would notice. Package-level so
// tests can lower it.
var SandboxSpawnAbandonedAfter = 5 * time.Minute

// SandboxMaintenanceInterval controls how often persisted leases and idle
// cache entries are scanned. Auto-renew requires a TTL longer than this interval.
var SandboxMaintenanceInterval = 5 * time.Minute

const (
	maxSandboxTTLSeconds int64 = 1<<31 - 1
	// sandboxMaintenanceBatchSize caps how many due leases one maintenance
	// scan claims. Renewals run serially (each with a 10s per-call
	// timeout) inside the caller's ~30s Maintain context, so under
	// provider slowness a scan may only finish a few before the context
	// expires; the rest stay claimed and are retried next tick, and the
	// claim query returns soonest-to-expire first so the most urgent win.
	// In the healthy case renewals are sub-second and the whole batch
	// completes. Move to parallel renewals before the number of
	// simultaneously-due agents makes the serial-under-30s cap the
	// bottleneck.
	sandboxMaintenanceBatchSize = 50
)

// ErrSandboxAcquireFailed is the sentinel a caller can branch on
// when the daemon failed to come up in time. Used by tests; in
// production the wrapping fmt.Errorf includes the underlying cause.
var ErrSandboxAcquireFailed = errors.New("agent_daemon: sandbox acquire failed")

// SandboxProvider is the lazy-create interface. The connector calls
// Acquire when it sees ErrNotBound + daemon_mode==sandbox; the binding
// returned from Acquire is forwarded to Binder.Bind so the next turn
// skips cold-start entirely.
//
// Two implementations:
//
//   - E2BSandboxProvider: production (e2b.Client + Store + Registry + Binder)
//   - NoopSandboxProvider: returns ErrSandboxProviderDisabled; wired
//     when sandbox mode is not configured for the deployment.
type SandboxProvider interface {
	// Acquire returns a deviceID for the given PromptInput. The
	// returned deviceID MUST be a device the gateway.Registry has a
	// live Session for — implementations are responsible for blocking
	// until WaitForDevice succeeds.
	//
	// Cold starts can take several seconds; callers must pass a
	// context with enough headroom.
	Acquire(ctx context.Context, in connector.PromptInput) (deviceID string, err error)

	// SandboxExists probes the provider when local dispatch cannot find the
	// daemon. confirmed is false when no durable sandbox is known.
	SandboxExists(ctx context.Context, agentID string) (exists, confirmed bool, err error)

	// SandboxStatus returns the cached sandbox info for a
	// agent. (zero, false, nil) when not cached.
	//
	// info.ExpiresAt is populated best-effort by querying e2b for the
	// live TTL; a transient e2b error leaves it zero and the admin
	// handler renders zero as "unknown".
	SandboxStatus(ctx context.Context, agentID string) (connector.SandboxInfo, bool, error)

	// Release tears down the sandbox associated with a agent.
	// Idempotent: releasing an unknown agent is a no-op.
	Release(ctx context.Context, agentID string) error

	// Renew bumps the e2b-side TTL back to its persisted or configured
	// lifetime. It falls back to the durable binding on a cache miss so any
	// pod can renew. (zero, false, nil) means no active binding exists.
	Renew(ctx context.Context, agentID string) (expiresAt time.Time, found bool, err error)

	// SandboxRuntimeInfo queries e2b directly for live expiry by
	// sandboxID (not agentID). Bypasses the in-memory cache so
	// any pod can answer. Returns zero time on transient failures.
	SandboxRuntimeInfo(ctx context.Context, sandboxID string) (expiresAt time.Time, err error)

	// Reap evicts sandboxes whose lastUsed is older than the
	// configured idle threshold. Returns the count evicted.
	Reap(ctx context.Context) (evicted int, err error)

	// Maintain claims and renews provider leases that are due according to
	// their persisted auto-renew policy.
	Maintain(ctx context.Context) (renewed int, err error)

	// Recreate terminates the current sandbox binding, if any, and performs
	// one fresh Acquire. Used only after dispatch confirms the runtime is gone.
	Recreate(ctx context.Context, in connector.PromptInput) (deviceID string, err error)
}

// ErrSandboxProviderDisabled is returned by NoopSandboxProvider when
// sandbox mode is requested but the deployment hasn't configured an e2b
// template.
var ErrSandboxProviderDisabled = errors.New("agent_daemon: sandbox mode not configured for this deployment (set AGENT_DAEMON_SANDBOX_TEMPLATE + e2b api key)")

// NoopSandboxProvider is the always-on fallback for deployments that
// don't wire e2b.
type NoopSandboxProvider struct{}

func (NoopSandboxProvider) Acquire(_ context.Context, _ connector.PromptInput) (string, error) {
	return "", ErrSandboxProviderDisabled
}

func (NoopSandboxProvider) SandboxExists(_ context.Context, _ string) (bool, bool, error) {
	return false, false, nil
}

func (NoopSandboxProvider) Release(_ context.Context, _ string) error { return nil }

func (NoopSandboxProvider) SandboxStatus(_ context.Context, _ string) (connector.SandboxInfo, bool, error) {
	return connector.SandboxInfo{}, false, nil
}

func (NoopSandboxProvider) Renew(_ context.Context, _ string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (NoopSandboxProvider) SandboxRuntimeInfo(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}

func (NoopSandboxProvider) Reap(_ context.Context) (int, error)     { return 0, nil }
func (NoopSandboxProvider) Maintain(_ context.Context) (int, error) { return 0, nil }
func (NoopSandboxProvider) Recreate(ctx context.Context, in connector.PromptInput) (string, error) {
	return NoopSandboxProvider{}.Acquire(ctx, in)
}

// ----------------------------------------------------------------------
// E2B-backed implementation
// ----------------------------------------------------------------------

// SandboxBindingPersister persists sandbox lifecycle events to the
// sandboxes table. Nil means memory-only mode (local dev).
//
// Reserve / Finalize / Wait are cross-pod coordination primitives:
// they use the sandboxes table's uk_sandboxes_active_per_agent unique
// index to pick a single cold-start winner and make losers wait.
type SandboxBindingPersister interface {
	CreateSandboxBinding(ctx context.Context, input store.CreateSandboxBindingInput) (store.SandboxBindingRead, error)
	ReserveSandboxBindingSlot(ctx context.Context, input store.ReserveSandboxBindingSlotInput) (store.SandboxBindingRead, bool, error)
	SetSpawningSandboxBindingSandboxID(ctx context.Context, bindingID, sandboxID string) error
	FinalizeSandboxBindingSpawning(ctx context.Context, input store.FinalizeSandboxBindingSpawningInput) error
	WaitForSandboxBindingActive(ctx context.Context, workspaceID, agentID string, pollInterval time.Duration) (store.SandboxBindingRead, error)
	TouchSandboxBinding(ctx context.Context, bindingID string) error
	MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error
	ReclaimAbandonedSandboxBinding(ctx context.Context, bindingID string, createdBefore time.Time) (bool, error)
	ConfigureSandboxBindingLease(ctx context.Context, bindingID string, timeoutSeconds, thresholdSeconds int32, expiresAt time.Time) error
	ClaimSandboxBindingsDueForAutoRenew(ctx context.Context, now time.Time, limit int32) ([]store.SandboxAutoRenewClaim, error)
	CompleteSandboxBindingRenew(ctx context.Context, bindingID string, expiresAt time.Time) error
	FailSandboxBindingRenew(ctx context.Context, bindingID string) error
	GetActiveSandboxBindingByAgentID(ctx context.Context, agentID string) (store.SandboxBindingRead, bool, error)
}

// E2BClient is the slice of e2b.Client the provider actually uses.
type E2BClient interface {
	Create(ctx context.Context, input e2b.CreateInput) (e2b.Sandbox, error)
	Kill(ctx context.Context, sandboxID string) error
	Renew(ctx context.Context, sandboxID string, timeoutSeconds int) error
	GetInfo(ctx context.Context, sandboxID string) (e2b.SandboxRuntimeInfo, error)
	RunCommand(ctx context.Context, input e2b.RunCommandInput) (e2b.CommandResult, error)
}

// RuntimeMinter is the slice of store.Store the provider needs to mint
// runtime + pairing token pairs.
type RuntimeMinter interface {
	CreateRuntimePairing(ctx context.Context, input store.CreateRuntimePairingInput) (store.CreateRuntimePairingResult, error)
	SoftDeleteRuntimeByWorkspaceName(ctx context.Context, workspaceID, name string) error
}

// E2BProviderConfig wires the production provider.
//
//   - Client: an e2b API client with APIKey + SandboxBaseURL configured.
//   - Store: runtime pairing minter.
//   - Registry: the gateway registry to WaitForDevice against.
//   - Binder: persisted conversation->device bindings.
//   - Template: the e2b template id (e.g. "parsar-sandbox-e2b"). The
//     deployment must publish this template before sandbox mode works.
//   - ServerURL: the public URL the daemon inside the sandbox dials
//     back to. Must be reachable from inside the sandbox network.
//   - Connector: which agent CLI runs inside the sandbox. Empty
//     defaults to SandboxConnectorClaude.
//
// DeviceOwnerChecker is the subset of store.Store the sandbox provider
// uses to poll for cross-pod device registration during cold start.
// In multi-pod deployments, the daemon's WS may land on a different
// server instance than the one running coldStart; polling Postgres
// detects this so routeRemoteIfNeeded can forward prompts to the
// owning pod.
type DeviceOwnerChecker interface {
	GetAgentDaemonDeviceOwner(ctx context.Context, deviceID string) (store.AgentDaemonDeviceOwnerRead, bool, error)
}

type E2BProviderConfig struct {
	Client   E2BClient
	Store    RuntimeMinter
	Registry *gateway.Registry
	Binder   binding.Binder
	Bindings SandboxBindingPersister // nil = memory-only (local dev)
	Template string
	// Templates maps a sandbox_size label (e.g. "standard", "xl") to the
	// e2b template id for that size. The agent's agents.config
	// `sandbox_size` field selects which template gets used on cold start.
	// When nil or empty, all acquires fall back to Template.
	//
	// Cache key remains keyed by agent_id only, so an agent at any
	// time has at most one active sandbox. Changing sandbox_size on a hot
	// agent takes effect only on the NEXT cold start (after TTL expiry or
	// manual release) — see the comment in coldStart for the rationale.
	Templates map[string]string
	// DefaultSize is the sandbox_size label used when an agent's config
	// does not specify one. Typically "standard".
	DefaultSize   string
	ServerURL     string
	Connector     SandboxConnector
	PodIPResolver *e2b.PodIPResolver // nil = use domain-based envd URL (requires external gateway)
	OwnerChecker  DeviceOwnerChecker // nil = single-pod mode (only check local registry)
	// SelfPodID is the hostname / pod identifier of the server process.
	// Used by the fast-path health check to decide whether a cached
	// deviceID is registered locally (Registry) or remotely (OwnerChecker).
	// Empty in single-pod / local-dev mode.
	SelfPodID string
	// TTL is the sandbox lifetime requested on Create and restored by
	// Renew. Zero falls back to SandboxDefaultTTL. Carried per-provider
	// (rather than by reassigning the package var) so the value is
	// explicit at the construction site and two providers in one process
	// — or two parallel tests — cannot clobber each other.
	//
	TTL time.Duration
	// AutoRenew enables periodic best-effort lease refresh. The server
	// wires this ON by default (see resolveAgentDaemonSandboxAutoRenew);
	// the Go zero value is off for tests and library callers. Disabled by
	// default because provider support and limits vary.
	AutoRenew bool
	Log       *slog.Logger
}

// ttl is the effective sandbox lifetime for this provider: the
// configured TTL, or SandboxDefaultTTL when unset.
func (p *E2BSandboxProvider) ttl() time.Duration {
	if ttl, ok := NormalizeSandboxTTL(p.cfg.TTL); ok {
		return ttl
	}
	return SandboxDefaultTTL
}

// NormalizeSandboxTTL converts a provider lease to whole seconds within the database range.
func NormalizeSandboxTTL(ttl time.Duration) (time.Duration, bool) {
	if ttl < time.Second {
		return 0, false
	}
	seconds := int64(ttl / time.Second)
	if seconds > maxSandboxTTLSeconds {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func (p *E2BSandboxProvider) ttlFor(in connector.PromptInput) time.Duration {
	if raw, _ := in.AgentConfig["sandbox_ttl"].(string); strings.TrimSpace(raw) != "" {
		if parsed, err := time.ParseDuration(strings.TrimSpace(raw)); err == nil {
			if ttl, ok := NormalizeSandboxTTL(parsed); ok {
				return ttl
			}
		}
		p.cfg.Log.Warn("agent sandbox_ttl ignored: want a duration of at least one second",
			"agent_id", in.AgentID, "value", raw)
	}
	return p.ttl()
}

func (p *E2BSandboxProvider) autoRenewFor(in connector.PromptInput) bool {
	if enabled, ok := in.AgentConfig["sandbox_auto_renew"].(bool); ok {
		return enabled
	}
	return p.cfg.AutoRenew
}

func (p *E2BSandboxProvider) leasePolicyFor(in connector.PromptInput) (time.Duration, bool) {
	ttl := p.ttlFor(in)
	autoRenew := p.autoRenewFor(in)
	if autoRenew && ttl <= SandboxMaintenanceInterval {
		p.cfg.Log.Warn("agent sandbox_auto_renew ignored: sandbox_ttl must exceed the maintenance interval",
			"agent_id", in.AgentID,
			"sandbox_ttl", ttl,
			"maintenance_interval", SandboxMaintenanceInterval)
		autoRenew = false
	}
	return ttl, autoRenew
}

func autoRenewThreshold(ttl time.Duration, enabled bool) int32 {
	if !enabled {
		return 0
	}
	ttl, ok := NormalizeSandboxTTL(ttl)
	if !ok {
		return 0
	}
	if ttl <= SandboxMaintenanceInterval {
		return 0
	}
	threshold := ttl / 4
	if threshold < SandboxMaintenanceInterval {
		threshold = SandboxMaintenanceInterval
	}
	if threshold >= ttl {
		threshold = ttl / 2
	}
	if threshold < time.Second {
		threshold = time.Second
	}
	return int32(threshold / time.Second)
}

// idleReapCutoff is the lastUsed age past which Reap evicts a cache
// entry. Tracks the effective TTL so Reap can never preempt e2b's own
// expiry; falls back to SandboxIdleReapThreshold when TTL is unset.
func (p *E2BSandboxProvider) idleReapCutoff() time.Duration {
	if p.cfg.TTL > 0 {
		return p.cfg.TTL
	}
	return SandboxIdleReapThreshold
}

// sandboxEntry is the per-agent cached sandbox handle.
type sandboxEntry struct {
	deviceID    string
	sandbox     e2b.Sandbox
	workspaceID string
	bindingID   string // sandboxes table UUID; empty = persist failed or not wired
	// ownerPodID records the pod where the daemon's WS session is
	// currently registered. May equal SelfPodID (daemon dialled this
	// pod) or be a different pod id (load-balanced to a sibling).
	// Empty in single-pod / legacy paths.
	ownerPodID string
	createdAt  time.Time
	lastUsed   time.Time
	timeout    time.Duration
	expiresAt  time.Time
	autoRenew  bool
}

// E2BSandboxProvider is the e2b-backed SandboxProvider implementation.
// Concurrency-safe via cacheMu.
type E2BSandboxProvider struct {
	cfg E2BProviderConfig

	cacheMu sync.Mutex
	cache   map[string]*sandboxEntry // key = agent_id

	// inflight serialises concurrent Acquire calls for the same
	// agent so a thundering herd of new conversations only
	// triggers one Create.
	inflight map[string]*acquirePromise
}

// acquirePromise is the per-agent serialisation primitive.
type acquirePromise struct {
	done     chan struct{}
	deviceID string
	err      error
}

// NewE2BSandboxProvider wires the production provider. Returns an
// error if any required field is missing — main.go falls back to
// NoopSandboxProvider when this fails so the connector stays usable
// for local-mode deployments.
func NewE2BSandboxProvider(cfg E2BProviderConfig) (*E2BSandboxProvider, error) {
	if cfg.Client == nil {
		return nil, errors.New("E2BSandboxProvider: Client required")
	}
	if cfg.Store == nil {
		return nil, errors.New("E2BSandboxProvider: Store required")
	}
	if cfg.Registry == nil {
		return nil, errors.New("E2BSandboxProvider: Registry required")
	}
	if cfg.Binder == nil {
		return nil, errors.New("E2BSandboxProvider: Binder required")
	}
	if cfg.Template == "" {
		return nil, errors.New("E2BSandboxProvider: Template required (e.g. parsar-sandbox-e2b)")
	}
	if cfg.ServerURL == "" {
		return nil, errors.New("E2BSandboxProvider: ServerURL required (the URL the daemon inside the sandbox should dial back to)")
	}
	if cfg.Log == nil {
		cfg.Log = obslog.Bg()
	}
	return &E2BSandboxProvider{
		cfg:      cfg,
		cache:    map[string]*sandboxEntry{},
		inflight: map[string]*acquirePromise{},
	}, nil
}

// resolveTemplate picks the (size, e2b template id) pair for a cold
// start by looking at the agent's `sandbox_size` config. The lookup
// precedence is:
//
//  1. AgentConfig["sandbox_size"]          — agent config
//  2. cfg.DefaultSize                      — provider default ("standard")
//
// Whichever size wins is then looked up in cfg.Templates. If the
// resolved size has no entry (e.g. an agent requests "xl" but the
// deployment didn't configure AGENT_DAEMON_SANDBOX_TEMPLATE_XL), we
// degrade to cfg.Template — the canonical standard template — and
// log a warn so the misconfiguration surfaces in mlogs. This keeps a
// missing XL pool from breaking acquires entirely.
//
// Cache key stays agentID-only (see Acquire), so an agent at
// any moment has at most one active sandbox; the new size only takes
// effect after the current sandbox is released and the next cold
// start runs.
func (p *E2BSandboxProvider) resolveTemplate(in connector.PromptInput) (size, templateID string) {
	size = strings.TrimSpace(stringFromMap(in.AgentConfig, "sandbox_size"))
	if size == "" {
		size = p.cfg.DefaultSize
	}
	if size == "" {
		size = "standard"
	}
	if t, ok := p.cfg.Templates[size]; ok && strings.TrimSpace(t) != "" {
		return size, t
	}
	// Misconfigured size: log once per acquire and fall back to the
	// canonical Template so the user gets *some* sandbox rather than
	// a hard failure.
	if size != p.cfg.DefaultSize && size != "standard" {
		p.cfg.Log.Warn("agent_daemon: requested sandbox_size has no template; falling back to default",
			"requested_size", size,
			"default_size", p.cfg.DefaultSize,
			"fallback_template", p.cfg.Template)
	}
	fallbackSize := p.cfg.DefaultSize
	if fallbackSize == "" {
		fallbackSize = "standard"
	}
	return fallbackSize, p.cfg.Template
}

// Acquire returns a deviceID for the agent's sandbox. Cold
// starts go through the full mint-create-login-connect dance; warm
// hits return the cached deviceID after a liveness check and touch.
//
// Concurrency: two Acquire calls for the same agent serialise
// on inflight[agentID] so we never create more than one sandbox
// per agent under contention.
func (p *E2BSandboxProvider) Acquire(ctx context.Context, in connector.PromptInput) (string, error) {
	if in.AgentID == "" {
		return "", errors.New("E2BSandboxProvider.Acquire: AgentID required")
	}
	if in.WorkspaceID == "" {
		return "", errors.New("E2BSandboxProvider.Acquire: WorkspaceID required (needed for runtime pairing)")
	}

	// Fast path: warm cache hit.
	p.cacheMu.Lock()
	if entry, ok := p.cache[in.AgentID]; ok {
		now := time.Now().UTC()
		entry.lastUsed = now
		desiredTTL, desiredAutoRenew := p.leasePolicyFor(in)
		policyChanged := entry.timeout != desiredTTL || entry.autoRenew != desiredAutoRenew
		cachePolicyChanged := policyChanged || entry.expiresAt.IsZero()
		deviceID := entry.deviceID
		sandboxID := entry.sandbox.SandboxID
		bindingID := entry.bindingID
		ownerPodID := entry.ownerPodID
		expiresAt := entry.expiresAt
		if expiresAt.IsZero() {
			expiresAt = now.Add(desiredTTL)
		}
		p.cacheMu.Unlock()
		// Best-effort: confirm the device session is still alive
		// somewhere in the fleet. ownerPodID disambiguates:
		//
		//   - ownerPodID == SelfPodID (or empty in single-pod mode):
		//     check this pod's Registry first. Miss falls back to
		//     OwnerChecker to absorb brief reconnect windows.
		//
		//   - ownerPodID != SelfPodID: the daemon dialled a sibling
		//     pod; this pod's Registry will never have it, go straight
		//     to OwnerChecker.
		//
		// Only when both signals say the device is gone do we evict
		// and cold-start. This matters because cold-start calls
		// SoftDeleteRuntimeByWorkspaceName, which would tear down a
		// healthy remote session under a stale local view.
		alive := p.checkDeviceAlive(ctx, deviceID, ownerPodID)
		if !alive {
			p.cfg.Log.Info("agent_daemon sandbox cache hit but device offline; recreating",
				"agent_id", in.AgentID,
				"device_id", deviceID,
				"cached_owner_pod", ownerPodID,
				"self_pod", p.cfg.SelfPodID)
			p.evict(in.AgentID, sandboxID, bindingID)
			// fall through to cold start
		} else {
			if policyChanged && bindingID != "" && p.cfg.Bindings != nil {
				if leaseErr := p.cfg.Bindings.ConfigureSandboxBindingLease(
					ctx,
					bindingID,
					int32(desiredTTL/time.Second),
					autoRenewThreshold(desiredTTL, desiredAutoRenew),
					expiresAt,
				); leaseErr != nil {
					p.cfg.Log.Warn("agent_daemon sandbox lease policy sync failed",
						"binding_id", bindingID, "err", leaseErr)
				} else {
					p.commitCachedLeasePolicy(in.AgentID, entry, desiredTTL, desiredAutoRenew, expiresAt)
				}
			} else if cachePolicyChanged {
				p.commitCachedLeasePolicy(in.AgentID, entry, desiredTTL, desiredAutoRenew, expiresAt)
			}
			// Best-effort: touch the DB binding so idle sweep sees
			// recent activity.
			if bindingID != "" && p.cfg.Bindings != nil {
				go func() {
					touchCtx, touchCancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer touchCancel()
					_ = p.cfg.Bindings.TouchSandboxBinding(touchCtx, bindingID)
				}()
			}
			return deviceID, nil
		}
	} else {
		p.cacheMu.Unlock()
	}

	// Serialise concurrent cold starts for the same agent.
	p.cacheMu.Lock()
	if promise, ok := p.inflight[in.AgentID]; ok {
		p.cacheMu.Unlock()
		select {
		case <-promise.done:
			return promise.deviceID, promise.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	promise := &acquirePromise{done: make(chan struct{})}
	p.inflight[in.AgentID] = promise
	p.cacheMu.Unlock()

	defer func() {
		p.cacheMu.Lock()
		delete(p.inflight, in.AgentID)
		p.cacheMu.Unlock()
		close(promise.done)
	}()

	// Cross-pod coordination: race to claim the (workspace,
	// agent) slot in the sandboxes table before doing any
	// expensive sandbox work. The uk_sandboxes_active_per_agent unique
	// index decides the winner; losers wait for the winner's row to
	// flip spawning → running and reuse the resulting deviceID.
	//
	// Without this, two pods running Acquire concurrently each call
	// coldStart(), each successfully mints a runtime + e2b sandbox,
	// and only one ends up bound to a conversation — the others are
	// orphans whose daemon times out with "context deadline exceeded".
	//
	// When Bindings is nil (local-dev single-process mode), skip
	// coordination and fall through to in-memory inflight serialisation.
	var (
		deviceID          string
		coldStartErr      error
		reservedBindingID string
	)
	if p.cfg.Bindings != nil {
		bindingID, loserDeviceID, csErr := p.acquireCrossPod(ctx, in)
		if csErr != nil {
			promise.err = csErr
			return "", csErr
		}
		if bindingID == "" {
			// Loser path: winner already finished. Reuse its device.
			promise.deviceID = loserDeviceID
			return loserDeviceID, nil
		}
		reservedBindingID = bindingID
	}

	// Winner path (or single-pod local-dev path with no Bindings):
	// drive cold-start ourselves.
	deviceID, coldStartErr = p.coldStart(ctx, in, reservedBindingID)
	promise.deviceID = deviceID
	promise.err = coldStartErr
	return deviceID, coldStartErr
}

func (p *E2BSandboxProvider) commitCachedLeasePolicy(agentID string, expected *sandboxEntry, ttl time.Duration, autoRenew bool, expiresAt time.Time) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if current := p.cache[agentID]; current == expected {
		current.timeout = ttl
		current.autoRenew = autoRenew
		current.expiresAt = expiresAt
	}
}

// acquireCrossPod runs the cross-pod Reserve/Wait dance against the
// sandboxes table. Returns:
//
//   - ("", deviceID, nil)   — loser path: another pod already won and
//     finished cold-start; caller reuses
//     deviceID without spawning anything.
//   - (bindingID, "", nil)  — winner path: caller now owns the slot
//     and MUST drive coldStart, calling
//     FinalizeSandboxBindingSpawning on success
//     or MarkSandboxBindingKilled on failure.
//   - ("", "", err)         — DB / wait failure; caller propagates.
func (p *E2BSandboxProvider) acquireCrossPod(ctx context.Context, in connector.PromptInput) (string, string, error) {
	cacheKey := "agent_daemon:" + in.AgentID
	_, templateID := p.resolveTemplate(in)
	row, won, err := p.cfg.Bindings.ReserveSandboxBindingSlot(ctx, store.ReserveSandboxBindingSlotInput{
		WorkspaceID: in.WorkspaceID,
		AgentID:     in.AgentID,
		CacheKey:    cacheKey,
		TemplateID:  templateID,
		Metadata: map[string]any{
			"sandbox_kind": "agent_daemon",
			"connector":    string(p.cfg.Connector),
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("%w: reserve binding slot: %v", ErrSandboxAcquireFailed, err)
	}
	if won {
		p.cfg.Log.Info("sandbox slot reserved (cold-start winner)",
			"agent_id", in.AgentID,
			"binding_id", row.ID)
		return row.ID, "", nil
	}
	// The slot is held by someone else. If that holder is an abandoned
	// cold start, clear it and take the slot ourselves — otherwise this
	// agent is wedged permanently (see reclaimAbandonedSpawn).
	if reclaimed, retryRow, retryWon, retryErr := p.reclaimAbandonedSpawn(ctx, in, cacheKey, templateID, row); reclaimed {
		if retryErr != nil {
			return "", "", retryErr
		}
		if retryWon {
			p.cfg.Log.Info("sandbox slot reserved after reclaiming abandoned cold-start",
				"agent_id", in.AgentID,
				"binding_id", retryRow.ID)
			return retryRow.ID, "", nil
		}
		// Someone else won the freed slot; fall through and wait on them.
		row = retryRow
	}
	// Loser: wait for the winner.
	p.cfg.Log.Info("sandbox slot already held; waiting for winner to finish cold-start",
		"agent_id", in.AgentID,
		"binding_id", row.ID,
		"winner_status", row.Status)
	waitCtx, cancel := context.WithTimeout(ctx, SandboxAcquireTimeout)
	defer cancel()
	finalRow, waitErr := p.cfg.Bindings.WaitForSandboxBindingActive(waitCtx, in.WorkspaceID, in.AgentID, 0)
	if waitErr != nil {
		return "", "", fmt.Errorf("%w: wait for winner: %v", ErrSandboxAcquireFailed, waitErr)
	}
	deviceID, _ := finalRow.Metadata["device_id"].(string)
	if deviceID == "" {
		return "", "", fmt.Errorf("%w: winner row has no device_id in metadata", ErrSandboxAcquireFailed)
	}
	p.syncPersistedLeasePolicy(ctx, in, finalRow)
	if touchErr := p.cfg.Bindings.TouchSandboxBinding(ctx, finalRow.ID); touchErr != nil {
		p.cfg.Log.Warn("agent_daemon cross-pod sandbox touch failed",
			"binding_id", finalRow.ID, "err", touchErr)
	}
	// We don't seed p.cache here because this pod never received the envd
	// token. Durable lease operations remain available from any pod.
	p.cfg.Log.Info("sandbox slot resolved via cross-pod wait",
		"agent_id", in.AgentID,
		"binding_id", finalRow.ID,
		"device_id", deviceID)
	return "", deviceID, nil
}

func (p *E2BSandboxProvider) syncPersistedLeasePolicy(ctx context.Context, in connector.PromptInput, row store.SandboxBindingRead) {
	persisted, found, err := p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, in.AgentID)
	if err != nil || !found || persisted.ID != row.ID {
		p.cfg.Log.Warn("agent_daemon cross-pod sandbox lease lookup failed",
			"binding_id", row.ID, "found", found, "err", err)
		return
	}
	row = persisted
	ttl, autoRenew := p.leasePolicyFor(in)
	threshold := autoRenewThreshold(ttl, autoRenew)
	if row.TimeoutSeconds == int32(ttl/time.Second) && row.AutoRenewThresholdSeconds == threshold {
		return
	}
	expiresAt := time.Now().UTC().Add(ttl)
	if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
		expiresAt = *row.ExpiresAt
	}
	if err := p.cfg.Bindings.ConfigureSandboxBindingLease(ctx, row.ID, int32(ttl/time.Second), threshold, expiresAt); err != nil {
		p.cfg.Log.Warn("agent_daemon cross-pod sandbox lease policy sync failed",
			"binding_id", row.ID, "err", err)
	}
}

// reclaimAbandonedSpawn frees the reservation slot when it is held by a
// cold start that can no longer be in flight, then retries the
// reservation once.
//
// Why this is needed: uk_sandboxes_active_per_agent keys on
// (workspace_id, agent_id) WHERE killed_at IS NULL, so a `spawning` row
// holds the agent's only slot until something marks it terminal. The
// loser path (WaitForSandboxBindingActive) treats `spawning` as
// "keep waiting" and only exits on a terminal status or ctx timeout. So
// if the winning process dies between Reserve and
// Finalize/MarkKilled — crash, redeploy, OOM, pod eviction — the row
// stays `spawning` forever and EVERY later prompt for that agent burns
// the full acquire timeout and fails. The agent is wedged with no
// self-healing path short of manual DB surgery.
//
// A cold start is bounded by SandboxAcquireTimeout, so a `spawning` row
// older than SandboxSpawnAbandonedAfter is definitively dead rather than
// slow.
//
// created_at is written from the reserving pod's Go clock, so skew would
// have to approach the generous threshold to matter. The actual reclaim is
// nevertheless a database CAS guarded by both state and creation cutoff.
//
// Returns reclaimed=false when the holder is healthy (caller should wait
// on it as usual). When reclaimed=true the caller must use the returned
// row/won/err instead of the original reservation result.
func (p *E2BSandboxProvider) reclaimAbandonedSpawn(
	ctx context.Context,
	in connector.PromptInput,
	cacheKey, templateID string,
	held store.SandboxBindingRead,
) (reclaimed bool, row store.SandboxBindingRead, won bool, err error) {
	if held.Status != store.SandboxBindingStatusSpawning {
		return false, store.SandboxBindingRead{}, false, nil
	}
	// A zero CreatedAt means "age unknown", not "infinitely old". Fail
	// safe: never steal a slot we cannot prove is stale, otherwise an
	// unset timestamp would let us kill a cold start that is actively
	// in flight.
	if held.CreatedAt.IsZero() {
		return false, store.SandboxBindingRead{}, false, nil
	}
	age := time.Since(held.CreatedAt)
	if age < SandboxSpawnAbandonedAfter {
		return false, store.SandboxBindingRead{}, false, nil
	}

	p.cfg.Log.Warn("reclaiming abandoned sandbox cold-start; its owner never finalized the binding",
		"agent_id", in.AgentID,
		"binding_id", held.ID,
		"sandbox_id", held.SandboxID,
		"age", age,
		"abandoned_after", SandboxSpawnAbandonedAfter)
	reclaimed, reclaimErr := p.cfg.Bindings.ReclaimAbandonedSandboxBinding(
		ctx, held.ID, time.Now().UTC().Add(-SandboxSpawnAbandonedAfter),
	)
	if reclaimErr != nil {
		return true, store.SandboxBindingRead{}, false,
			fmt.Errorf("%w: reclaim abandoned binding %s: %v", ErrSandboxAcquireFailed, held.ID, reclaimErr)
	}
	if !reclaimed {
		// The original owner finalized or another contender reclaimed the
		// row after our read. Do not kill its provider sandbox.
		return false, store.SandboxBindingRead{}, false, nil
	}

	// Best-effort kill of the half-built sandbox after winning the CAS. Skipped for a
	// reservation placeholder, which is not a real e2b id. A leaked
	// sandbox also self-expires via the provider TTL, so a failure here
	// must not block reclaiming the slot.
	if sandboxID := strings.TrimSpace(held.SandboxID); sandboxID != "" && !store.IsPendingSandboxID(sandboxID) {
		killCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if killErr := p.cfg.Client.Kill(killCtx, sandboxID); killErr != nil {
			p.cfg.Log.Warn("abandoned sandbox kill failed (continuing; TTL will expire it)",
				"sandbox_id", sandboxID, "err", killErr)
		}
		cancel()
	}

	row, won, err = p.cfg.Bindings.ReserveSandboxBindingSlot(ctx, store.ReserveSandboxBindingSlotInput{
		WorkspaceID: in.WorkspaceID,
		AgentID:     in.AgentID,
		CacheKey:    cacheKey,
		TemplateID:  templateID,
		Metadata: map[string]any{
			"sandbox_kind": "agent_daemon",
			"connector":    string(p.cfg.Connector),
		},
	})
	if err != nil {
		return true, store.SandboxBindingRead{}, false,
			fmt.Errorf("%w: re-reserve binding slot after reclaim: %v", ErrSandboxAcquireFailed, err)
	}
	return true, row, won, nil
}

// coldStart owns the full mint + create + login + connect + wait
// sequence. On failure best-efforts to kill the half-built sandbox.
//
// reservedBindingID is the sandboxes-table row this pod won during
// Reserve; when non-empty, coldStart finalizes it on success and
// marks it killed_error on failure. When empty (Bindings nil —
// local-dev), the in-memory cache is the only record.
func (p *E2BSandboxProvider) coldStart(ctx context.Context, in connector.PromptInput, reservedBindingID string) (string, error) {
	bootCtx, cancel := context.WithTimeout(ctx, SandboxAcquireTimeout)
	defer cancel()

	// Resolve the sandbox template once at the top of cold start. All
	// three downstream call sites — Reserve (above), e2b.Create, and
	// CreateSandboxBinding — must agree on which template they're
	// claiming/spawning/persisting, so the agent's `sandbox_size`
	// config is read here and then plumbed through identically.
	resolvedSize, templateID := p.resolveTemplate(in)

	// releaseReservation is called by early-exit failure paths before
	// killOnFail is wired. Marks the reserved row killed_error so the
	// next Acquire can immediately retry. `released` tracks state so
	// killOnFail doesn't double-release.
	released := false
	releaseReservation := func(err error) {
		if released || reservedBindingID == "" || p.cfg.Bindings == nil {
			return
		}
		released = true
		relCtx, relCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer relCancel()
		if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(relCtx, reservedBindingID, store.SandboxBindingStatusKilledError); markErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox reservation release failed (loser waits will timeout)",
				"binding_id", reservedBindingID, "trigger_err", err, "err", markErr)
		}
	}

	// 0. Retire any stale runtime row with the same deterministic name
	//    so the next INSERT doesn't hit uk_runtimes_workspace_name_active.
	//    This happens when a previous sandbox was killed but its runtime
	//    row was never soft-deleted (e.g. manual kill, idle reap, crash).
	runtimeName := fmt.Sprintf("sandbox %s", shortID(in.AgentID))
	if err := p.cfg.Store.SoftDeleteRuntimeByWorkspaceName(bootCtx, in.WorkspaceID, runtimeName); err != nil {
		releaseReservation(err)
		return "", fmt.Errorf("%w: retire stale runtime: %v", ErrSandboxAcquireFailed, err)
	}

	// 1. Mint runtime + pairing token. RuntimeID becomes the deviceID
	//    the gateway sees after the daemon logs in.
	pair, err := p.cfg.Store.CreateRuntimePairing(bootCtx, store.CreateRuntimePairingInput{
		WorkspaceID: in.WorkspaceID,
		Type:        "agent_daemon",
		Provider:    store.RuntimeProviderAgentDaemonSandbox,
		Name:        runtimeName,
		// OwnerUserID intentionally empty: sandbox-mode rows are
		// owned by the agent, not a human user.
		TokenTTL: SandboxAcquireTimeout + 30*time.Second,
		Config: map[string]any{
			"created_by":   "sandbox_provider",
			"agent_id":     in.AgentID,
			"sandbox_kind": "agent_daemon_claude_code",
		},
	})
	if err != nil {
		releaseReservation(err)
		return "", fmt.Errorf("%w: mint pairing: %v", ErrSandboxAcquireFailed, err)
	}
	deviceID := pair.Runtime.ID

	// 2. Create the e2b sandbox.
	ttl, autoRenew := p.leasePolicyFor(in)
	sandbox, err := p.cfg.Client.Create(bootCtx, e2b.CreateInput{
		TemplateID:     templateID,
		TimeoutSeconds: int(ttl.Seconds()),
		Metadata: map[string]string{
			"parsar.workspace_id": in.WorkspaceID,
			"parsar.agent_id":     in.AgentID,
			"parsar.device_id":    deviceID,
			"parsar.sandbox_kind": "agent_daemon_claude_code",
			"parsar.sandbox_size": resolvedSize,
		},
	})
	if err != nil {
		releaseReservation(err)
		return "", fmt.Errorf("%w: e2b create: %v", ErrSandboxAcquireFailed, err)
	}
	p.cfg.Log.Info("sandbox created via E2B",
		"sandbox_id", sandbox.SandboxID,
		"domain", sandbox.Domain,
		"envd_version", sandbox.EnvdVersion,
		"agent_id", in.AgentID,
		"sandbox_size", resolvedSize,
		"template_id", templateID)

	// Record the real sandbox id on the reservation immediately, before
	// the rest of cold start (seed → connect → wait → finalize). If this
	// pod crashes mid-cold-start, the durable row then carries a killable
	// id instead of the pending- placeholder, so reclaimAbandonedSpawn can
	// terminate the sandbox instead of leaving it to bill until its TTL.
	// Best-effort: a failure here only widens the pre-existing leak window,
	// so log and continue rather than aborting a working cold start.
	if reservedBindingID != "" && p.cfg.Bindings != nil {
		if idErr := p.cfg.Bindings.SetSpawningSandboxBindingSandboxID(bootCtx, reservedBindingID, sandbox.SandboxID); idErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox: early sandbox_id persist failed (crash before finalize would orphan it until TTL)",
				"binding_id", reservedBindingID, "sandbox_id", sandbox.SandboxID, "err", idErr)
		}
	}

	// Resolve pod IP for direct envd access (bypasses external gateway).
	var envdURL string
	if p.cfg.PodIPResolver != nil {
		if resolved, resolveErr := p.cfg.PodIPResolver.Resolve(bootCtx, sandbox.SandboxID, e2b.DefaultEnvdPort); resolveErr != nil {
			p.cfg.Log.Warn("pod IP resolve failed, falling back to domain-based envd",
				"sandbox_id", sandbox.SandboxID, "err", resolveErr)
		} else {
			envdURL = resolved
			p.cfg.Log.Info("envd direct pod access resolved",
				"sandbox_id", sandbox.SandboxID, "envd_url", envdURL)
		}
	}
	// From here on, any failure path must Kill to avoid leaking the
	// sandbox handle. It must also release the cross-pod reservation
	// so the next Acquire isn't blocked waiting on this dead row.
	killOnFail := func() {
		killCtx, cancelKill := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelKill()
		if killErr := p.cfg.Client.Kill(killCtx, sandbox.SandboxID); killErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox kill on failure path failed (leaking)",
				"sandbox_id", sandbox.SandboxID, "err", killErr)
		}
		if !released && reservedBindingID != "" && p.cfg.Bindings != nil {
			released = true
			if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(killCtx, reservedBindingID, store.SandboxBindingStatusKilledError); markErr != nil {
				p.cfg.Log.Warn("agent_daemon sandbox reservation release failed (loser waits will timeout)",
					"binding_id", reservedBindingID, "err", markErr)
			}
		}
	}

	// 2b. Seed the platform-specific runtime config (e.g. Claude's
	//     ~/.claude/settings.json). MUST run BEFORE parsar-daemon connect:
	//     the agent CLI inside the sandbox boots later and reads
	//     these files the moment it spawns. Failing here aborts the
	//     acquire so we never hand back a sandbox missing spec/memory
	//     injection.
	//
	//     Connector is resolved dynamically from the agent's agent_kind
	//     config so one provider instance serves all runtime types.
	resolvedConnector := ConnectorForAgentKind(resolveAgentKind(in))
	if err := seedPlatformConfig(bootCtx, p.cfg.Client, sandbox, resolvedConnector, envdURL); err != nil {
		p.cfg.Log.Warn("sandbox seed failed — killing sandbox",
			"sandbox_id", sandbox.SandboxID,
			"domain", sandbox.Domain,
			"envd_access_token_set", sandbox.EnvdAccessToken != "",
			"err", err)
		killOnFail()
		return "", fmt.Errorf("%w: seed platform config: %v", ErrSandboxAcquireFailed, err)
	}

	// 3. Run parsar-daemon connect with the one-shot pairing token.
	//    Token is passed via env (not argv) so it does not linger in
	//    process listings or provider command logs. The background
	//    child consumes the token, keeps the runner credential in
	//    memory, and dials WS without writing an auth profile first.
	//    Returns quickly (fork + pidfile); the daemon dials WS in the
	//    background.
	//
	//    The PARSAR_* env block exposes the runtime identity to the `parsar`
	//    CLI (used by hook scripts). PARSAR_RUNNER_TOKEN is the same
	//    string as the daemon pairing token, scoped server-side via
	//    runtime_type checks. Empty fields are omitted rather than set to
	//    "" so hook scripts can treat a missing var as "not set".
	connectCmd := fmt.Sprintf("parsar-daemon connect --device-name %s -b", shellSingleQuote(deviceID))
	connectEnv := map[string]string{
		"PARSAR_DAEMON_CONNECT_URL":   p.cfg.ServerURL,
		"PARSAR_DAEMON_CONNECT_TOKEN": pair.PairingToken,
		// parsar CLI env — same token, presented under the name the CLI
		// reads. Hook scripts shell out to `parsar inject ...` which
		// expects PARSAR_RUNNER_TOKEN, not PARSAR_DAEMON_CONNECT_TOKEN.
		"PARSAR_SERVER_URL":   p.cfg.ServerURL,
		"PARSAR_RUNNER_TOKEN": pair.PairingToken,
		"PARSAR_RUNTIME_ID":   deviceID,
		"PARSAR_WORKSPACE_ID": in.WorkspaceID,
		"PARSAR_AGENT_ID":     in.AgentID,
		"PARSAR_CONNECTOR":    connectorTagFor(resolvedConnector),
	}
	if in.ConversationInitiatorID != "" {
		connectEnv["PARSAR_USER_ID"] = in.ConversationInitiatorID
	}
	if in.ConversationID != "" {
		connectEnv["PARSAR_CONVERSATION_ID"] = in.ConversationID
	}
	// No CWD: both sandbox clients treat an empty value as "leave it to the
	// runtime", which resolves to the image's own WORKDIR and therefore
	// always exists.
	//
	// Deliberately NOT the agent's work_dir. This is only where the
	// short-lived `parsar-daemon connect` process starts; the directory the
	// agent actually works in travels separately as
	// prompt_request.WorkDir. Passing work_dir here would abort cold start
	// for any agent configured with a host path (a local device's
	// /Users/... does not exist inside the sandbox). Hardcoding a path is
	// no better — a custom template without that directory fails the same
	// way.
	connectRes, err := p.cfg.Client.RunCommand(bootCtx, e2b.RunCommandInput{
		Sandbox: sandbox,
		Command: connectCmd,
		Env:     connectEnv,
		Timeout: 20 * time.Second,
		EnvdURL: envdURL,
	})
	if err != nil {
		killOnFail()
		return "", fmt.Errorf("%w: parsar-daemon connect -b: %v", ErrSandboxAcquireFailed, err)
	}
	if !connectRes.Exited || connectRes.Status != "0" {
		killOnFail()
		return "", fmt.Errorf("%w: parsar-daemon connect -b exit=%q stderr=%q",
			ErrSandboxAcquireFailed, connectRes.Status, connectRes.Stderr)
	}
	p.cfg.Log.Info("parsar-daemon connect -b fork succeeded",
		"sandbox_id", sandbox.SandboxID,
		"stdout", connectRes.Stdout,
		"stderr", connectRes.Stderr)

	// 4. Wait for the daemon to dial back through the gateway.
	//    Without this, the connector would race the WS upgrade and
	//    surface "device offline" on the very first prompt.
	//
	//    Multi-pod: the WS may land on a sibling server. Race the
	//    local Registry waiter against a Postgres OwnerStore poll so
	//    cross-pod registration is detected without requiring the WS
	//    to land here.
	waitCtx, cancelWait := context.WithTimeout(bootCtx, SandboxConnectTimeout)
	defer cancelWait()
	ownerPodID, err := p.waitForDevice(waitCtx, deviceID)
	if err != nil {
		// Capture the daemon's connect.log before killing the sandbox
		// so we can surface why the background daemon failed to dial in.
		diagCtx, diagCancel := context.WithTimeout(context.Background(), 5*time.Second)
		diagRes, diagErr := p.cfg.Client.RunCommand(diagCtx, e2b.RunCommandInput{
			Sandbox: sandbox,
			Command: "cat /root/.parsar/parsar-daemon/default/connect.log 2>/dev/null; echo '---PID---'; cat /root/.parsar/parsar-daemon/default/connect.pid 2>/dev/null; echo '---PS---'; ps aux 2>/dev/null | grep parsar-daemon || true",
			CWD:     "/",
			Timeout: 5 * time.Second,
			EnvdURL: envdURL,
		})
		diagCancel()
		daemonLog := "(diagnostic fetch failed)"
		if diagErr == nil {
			daemonLog = diagRes.Stdout + diagRes.Stderr
		}
		p.cfg.Log.Error("daemon dial-in timed out — sandbox diagnostics",
			"sandbox_id", sandbox.SandboxID,
			"device_id", deviceID,
			"daemon_log", daemonLog)

		killOnFail()
		return "", fmt.Errorf("%w: wait for daemon dial-in (deviceID=%s): %v\n--- daemon connect.log ---\n%s",
			ErrSandboxAcquireFailed, deviceID, err, daemonLog)
	}

	// 5. Cache so subsequent Acquires are O(1). Binder.Bind() happens
	//    in the connector after Acquire returns; the provider stays
	//    unaware of conversation_id so a single sandbox can serve
	//    multiple conversations under the same agent.
	//
	//    waitForDevice returns "" when the local Registry waiter won;
	//    substitute SelfPodID so the fast-path health check can compare
	//    against SelfPodID unambiguously. Empty SelfPodID (single-pod)
	//    leaves the entry empty and the fast path stays Registry-only.
	resolvedOwnerPodID := ownerPodID
	if resolvedOwnerPodID == "" {
		resolvedOwnerPodID = p.cfg.SelfPodID
	}
	now := time.Now().UTC()
	entry := &sandboxEntry{
		deviceID:    deviceID,
		sandbox:     sandbox,
		workspaceID: in.WorkspaceID,
		ownerPodID:  resolvedOwnerPodID,
		createdAt:   now,
		lastUsed:    now,
		timeout:     ttl,
		expiresAt:   now.Add(ttl),
		autoRenew:   autoRenew,
	}

	// 5b. Persist to sandboxes table so admin endpoints, multi-pod
	//     queries, and orphan sweeps can see this sandbox.
	//
	//     With a cross-pod reservation in hand, UPDATE the placeholder
	//     row in place (flipping spawning → running). Without one
	//     (local-dev), INSERT a fresh row. Failure is best-effort:
	//     degraded admin visibility, but sandbox is functional.
	if p.cfg.Bindings != nil {
		if reservedBindingID != "" {
			finalizeErr := p.cfg.Bindings.FinalizeSandboxBindingSpawning(bootCtx, store.FinalizeSandboxBindingSpawningInput{
				BindingID: reservedBindingID,
				SandboxID: sandbox.SandboxID,
				Metadata: map[string]any{
					"sandbox_kind": "agent_daemon",
					"device_id":    deviceID,
					"connector":    string(p.cfg.Connector),
				},
			})
			if finalizeErr != nil {
				p.cfg.Log.Warn("sandbox binding finalize failed (loser waits will time out; sandbox functional locally)",
					"agent_id", in.AgentID,
					"binding_id", reservedBindingID,
					"sandbox_id", sandbox.SandboxID,
					"err", finalizeErr)
			} else {
				entry.bindingID = reservedBindingID
			}
		} else {
			binding, bindErr := p.cfg.Bindings.CreateSandboxBinding(bootCtx, store.CreateSandboxBindingInput{
				WorkspaceID: in.WorkspaceID,
				AgentID:     in.AgentID,
				CacheKey:    "agent_daemon:" + in.AgentID,
				SandboxID:   sandbox.SandboxID,
				TemplateID:  templateID,
				Status:      store.SandboxBindingStatusActive,
				Metadata: map[string]any{
					"sandbox_kind": "agent_daemon",
					"device_id":    deviceID,
					"connector":    string(p.cfg.Connector),
				},
			})
			if bindErr != nil {
				p.cfg.Log.Warn("sandbox binding persist failed (sandbox functional, admin visibility degraded)",
					"agent_id", in.AgentID,
					"sandbox_id", sandbox.SandboxID,
					"err", bindErr)
			} else {
				entry.bindingID = binding.ID
			}
		}
		if entry.bindingID != "" {
			threshold := autoRenewThreshold(ttl, autoRenew)
			if leaseErr := p.cfg.Bindings.ConfigureSandboxBindingLease(
				bootCtx,
				entry.bindingID,
				int32(ttl/time.Second),
				threshold,
				entry.expiresAt,
			); leaseErr != nil {
				p.cfg.Log.Warn("sandbox binding lease persist failed",
					"binding_id", entry.bindingID, "err", leaseErr)
			}
		}
	}

	p.cacheMu.Lock()
	p.cache[in.AgentID] = entry
	p.cacheMu.Unlock()

	p.cfg.Log.Info("agent_daemon sandbox acquired",
		"agent_id", in.AgentID,
		"sandbox_id", sandbox.SandboxID,
		"device_id", deviceID)
	return deviceID, nil
}

// SandboxStatus returns the cached sandbox info for a agent.
// (zero, false, nil) when not cached.
func (p *E2BSandboxProvider) SandboxStatus(ctx context.Context, agentID string) (connector.SandboxInfo, bool, error) {
	p.cacheMu.Lock()
	entry, ok := p.cache[agentID]
	p.cacheMu.Unlock()
	if !ok {
		return connector.SandboxInfo{}, false, nil
	}
	info := connector.SandboxInfo{
		DeviceID:    entry.deviceID,
		SandboxID:   entry.sandbox.SandboxID,
		WorkspaceID: entry.workspaceID,
		CreatedAt:   entry.createdAt,
		LastUsedAt:  entry.lastUsed,
	}
	// Fail-soft: fetch the live e2b TTL so the panel can render an
	// accurate expires_at. A control-plane blip leaves ExpiresAt zero
	// rather than failing the whole status call.
	getCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if runtime, err := p.cfg.Client.GetInfo(getCtx, entry.sandbox.SandboxID); err != nil {
		p.cfg.Log.Warn("agent_daemon sandbox status: e2b GetInfo failed (continuing without expires_at)",
			"sandbox_id", entry.sandbox.SandboxID, "err", err)
	} else {
		info.ExpiresAt = runtime.EndAt
	}
	return info, true, nil
}

// Renew bumps the e2b TTL for the agent's sandbox back to its cached or
// persisted lifetime. Durable lookup lets any pod service the request.
// (zero, false, nil) means no active sandbox exists.
func (p *E2BSandboxProvider) Renew(ctx context.Context, agentID string) (time.Time, bool, error) {
	if strings.TrimSpace(agentID) == "" {
		return time.Time{}, false, nil
	}
	p.cacheMu.Lock()
	entry, ok := p.cache[agentID]
	if ok {
		entry.lastUsed = time.Now().UTC()
	}
	sandboxID := ""
	bindingID := ""
	timeout := p.ttl()
	if ok {
		sandboxID = entry.sandbox.SandboxID
		bindingID = entry.bindingID
		if entry.timeout > 0 {
			timeout = entry.timeout
		}
	}
	p.cacheMu.Unlock()
	if !ok {
		if p.cfg.Bindings == nil {
			return time.Time{}, false, nil
		}
		bindingRow, found, lookupErr := p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, agentID)
		if lookupErr != nil {
			return time.Time{}, false, lookupErr
		}
		if !found || store.IsPendingSandboxID(bindingRow.SandboxID) {
			return time.Time{}, false, nil
		}
		sandboxID = bindingRow.SandboxID
		bindingID = bindingRow.ID
		if bindingRow.TimeoutSeconds > 0 {
			timeout = time.Duration(bindingRow.TimeoutSeconds) * time.Second
		}
	}

	renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.cfg.Client.Renew(renewCtx, sandboxID, int(timeout.Seconds())); err != nil {
		return time.Time{}, true, fmt.Errorf("e2b renew sandbox %s: %w", sandboxID, err)
	}

	// Re-query e2b for canonical expires_at — the timeout endpoint
	// doesn't echo post-renew endAt, only GetInfo. A read failure
	// here doesn't undo the renew; we still return ok and let the
	// panel re-fetch via SandboxStatus.
	getCtx, cancelGet := context.WithTimeout(ctx, 3*time.Second)
	defer cancelGet()
	runtime, err := p.cfg.Client.GetInfo(getCtx, sandboxID)
	expiresAt := time.Now().UTC().Add(timeout)
	if err != nil {
		p.cfg.Log.Warn("agent_daemon sandbox renew: e2b GetInfo after Renew failed (renew itself succeeded)",
			"sandbox_id", sandboxID, "err", err)
	} else {
		expiresAt = runtime.EndAt
	}
	if bindingID != "" && p.cfg.Bindings != nil {
		if persistErr := p.cfg.Bindings.CompleteSandboxBindingRenew(ctx, bindingID, expiresAt); persistErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox renew: persist expiry failed",
				"binding_id", bindingID, "err", persistErr)
		}
	}
	p.cacheMu.Lock()
	if cached, exists := p.cache[agentID]; exists {
		cached.expiresAt = expiresAt
	}
	p.cacheMu.Unlock()
	p.cfg.Log.Info("agent_daemon sandbox renewed",
		"agent_id", agentID,
		"sandbox_id", sandboxID,
		"expires_at", expiresAt)
	return expiresAt, true, nil
}

// Maintain renews due bound sandboxes claimed atomically from Postgres. It is
// safe to run on every server pod; only the pod that wins the renewing state
// transition calls the provider.
func (p *E2BSandboxProvider) Maintain(ctx context.Context) (int, error) {
	if p.cfg.Bindings == nil {
		return 0, nil
	}
	claims, err := p.cfg.Bindings.ClaimSandboxBindingsDueForAutoRenew(ctx, time.Now().UTC(), sandboxMaintenanceBatchSize)
	if err != nil {
		return 0, err
	}
	renewed := 0
	for _, claim := range claims {
		if err := ctx.Err(); err != nil {
			return renewed, err
		}
		if store.IsPendingSandboxID(claim.SandboxID) {
			_ = p.cfg.Bindings.FailSandboxBindingRenew(ctx, claim.BindingID)
			continue
		}
		timeout := time.Duration(claim.TimeoutSeconds) * time.Second
		renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		renewErr := p.cfg.Client.Renew(renewCtx, claim.SandboxID, int(claim.TimeoutSeconds))
		cancel()
		if renewErr != nil {
			if errors.Is(renewErr, context.Canceled) || errors.Is(renewErr, context.DeadlineExceeded) {
				p.cfg.Log.Warn("agent_daemon scheduled sandbox renew interrupted; claim will be retried",
					"sandbox_id", claim.SandboxID, "agent_id", claim.AgentID, "err", renewErr)
				if err := ctx.Err(); err != nil {
					return renewed, err
				}
				continue
			}
			if e2b.IsNotFound(renewErr) {
				// The sandbox is already gone (expired or killed out of
				// band); renewing is impossible. Free the slot so the next
				// prompt cold-starts a fresh one instead of retrying a dead
				// sandbox forever.
				if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(ctx, claim.BindingID, store.SandboxBindingStatusKilledOrphaned); markErr != nil {
					p.cfg.Log.Warn("agent_daemon scheduled renew: mark gone sandbox killed failed",
						"binding_id", claim.BindingID, "err", markErr)
				}
				p.cacheMu.Lock()
				delete(p.cache, claim.AgentID)
				p.cacheMu.Unlock()
				p.cfg.Log.Warn("agent_daemon scheduled sandbox renew: sandbox gone, freed slot",
					"sandbox_id", claim.SandboxID, "agent_id", claim.AgentID)
				continue
			}
			if e2b.IsUnsupported(renewErr) {
				// The provider cannot renew at all. Disable auto-renew for
				// this binding so we stop hammering it; the sandbox still
				// lives out its original TTL.
				_ = p.cfg.Bindings.FailSandboxBindingRenew(ctx, claim.BindingID)
				p.cacheMu.Lock()
				if cached, ok := p.cache[claim.AgentID]; ok {
					cached.autoRenew = false
				}
				p.cacheMu.Unlock()
				p.cfg.Log.Warn("agent_daemon scheduled sandbox renew not supported by provider; auto-renew disabled for this binding",
					"sandbox_id", claim.SandboxID, "agent_id", claim.AgentID, "err", renewErr)
				continue
			}
			// Transient provider failure (5xx, rate limit, network). Leave
			// the row in 'renewing' so the maintenance recovery branch
			// re-claims it (~2 min) and keeps trying. A one-off hiccup must
			// never permanently disable renewal — that would let the
			// sandbox expire silently mid-conversation.
			p.cfg.Log.Warn("agent_daemon scheduled sandbox renew failed; will retry",
				"sandbox_id", claim.SandboxID, "agent_id", claim.AgentID, "err", renewErr)
			continue
		}
		expiresAt := time.Now().UTC().Add(timeout)
		getCtx, getCancel := context.WithTimeout(ctx, 3*time.Second)
		if runtime, infoErr := p.cfg.Client.GetInfo(getCtx, claim.SandboxID); infoErr == nil && !runtime.EndAt.IsZero() {
			expiresAt = runtime.EndAt
		}
		getCancel()
		if err := p.cfg.Bindings.CompleteSandboxBindingRenew(ctx, claim.BindingID, expiresAt); err != nil {
			p.cfg.Log.Warn("agent_daemon scheduled sandbox renew persist failed",
				"binding_id", claim.BindingID, "err", err)
			continue
		}
		p.cacheMu.Lock()
		if cached, ok := p.cache[claim.AgentID]; ok {
			cached.expiresAt = expiresAt
		}
		p.cacheMu.Unlock()
		renewed++
	}
	return renewed, nil
}

// SandboxExists distinguishes an explicitly deleted provider sandbox from a
// transient registry or provider failure. Only an E2B 404 confirms absence.
func (p *E2BSandboxProvider) SandboxExists(ctx context.Context, agentID string) (bool, bool, error) {
	var sandboxID string
	p.cacheMu.Lock()
	if entry := p.cache[agentID]; entry != nil {
		sandboxID = entry.sandbox.SandboxID
	}
	p.cacheMu.Unlock()
	if sandboxID == "" && p.cfg.Bindings != nil {
		row, found, err := p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, agentID)
		if err != nil {
			return false, false, err
		}
		if !found || store.IsPendingSandboxID(row.SandboxID) {
			return false, false, nil
		}
		sandboxID = row.SandboxID
	}
	if strings.TrimSpace(sandboxID) == "" || store.IsPendingSandboxID(sandboxID) {
		return false, false, nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := p.cfg.Client.GetInfo(probeCtx, sandboxID); err != nil {
		if e2b.IsNotFound(err) {
			return false, true, nil
		}
		return false, true, err
	}
	return true, true, nil
}

// Recreate clears a confirmed-dead binding and performs exactly one fresh
// Acquire. It works even when the sandbox was created by a different pod.
func (p *E2BSandboxProvider) Recreate(ctx context.Context, in connector.PromptInput) (string, error) {
	p.cacheMu.Lock()
	entry := p.cache[in.AgentID]
	delete(p.cache, in.AgentID)
	p.cacheMu.Unlock()

	var row store.SandboxBindingRead
	found := false
	if p.cfg.Bindings != nil {
		var err error
		row, found, err = p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, in.AgentID)
		if err != nil {
			if entry != nil {
				p.restoreReapCandidate(in.AgentID, entry)
			}
			return "", fmt.Errorf("sandbox recreate: lookup binding: %w", err)
		}
	}
	if entry != nil {
		if !found {
			row.ID = entry.bindingID
			row.SandboxID = entry.sandbox.SandboxID
			row.Metadata = map[string]any{"device_id": entry.deviceID}
			found = true
		}
	}
	if found {
		if sandboxID := strings.TrimSpace(row.SandboxID); sandboxID != "" && !store.IsPendingSandboxID(sandboxID) {
			killCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			killErr := p.cfg.Client.Kill(killCtx, sandboxID)
			cancel()
			if killErr != nil && !e2b.IsNotFound(killErr) {
				if entry != nil {
					p.restoreReapCandidate(in.AgentID, entry)
				}
				return "", fmt.Errorf("sandbox recreate: kill old sandbox %s: %w", sandboxID, killErr)
			}
		}
		if deviceID, _ := row.Metadata["device_id"].(string); deviceID != "" {
			_ = p.cfg.Binder.InvalidateDevice(ctx, deviceID)
		}
		if row.ID != "" && p.cfg.Bindings != nil {
			if err := p.cfg.Bindings.MarkSandboxBindingKilled(ctx, row.ID, store.SandboxBindingStatusKilledError); err != nil {
				return "", fmt.Errorf("sandbox recreate: release old binding: %w", err)
			}
		}
	}
	return p.Acquire(ctx, in)
}

// SandboxRuntimeInfo queries e2b for a sandbox's live expiry, bypassing
// the in-memory cache.
//
// SandboxStatus is cache-only and returns ok=false on any pod that
// didn't itself cold-start the sandbox; in multi-pod a GET /sandbox
// usually lands on a sibling pod. This method only needs sandboxID
// (durable, stored in the sandboxes table) and goes straight to e2b
// so any pod can answer.
//
// Returns zero time + nil error on transient e2b failures so the admin
// handler can fold expires_at in as optional metadata.
func (p *E2BSandboxProvider) SandboxRuntimeInfo(ctx context.Context, sandboxID string) (time.Time, error) {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return time.Time{}, nil
	}
	// A binding that is still cold-starting carries a reservation
	// placeholder, not a real sandbox id. The admin status poller reads
	// sandbox_id straight off the row, so without this guard every poll
	// (~2.5s) sends a malformed id to e2b and logs a 400 WARN until the
	// sandbox finishes spawning. There is no expiry to report yet.
	if store.IsPendingSandboxID(sandboxID) {
		return time.Time{}, nil
	}
	getCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	runtime, err := p.cfg.Client.GetInfo(getCtx, sandboxID)
	if err != nil {
		p.cfg.Log.Warn("agent_daemon sandbox runtime-info: e2b GetInfo failed",
			"sandbox_id", sandboxID, "err", err)
		return time.Time{}, err
	}
	return runtime.EndAt, nil
}

// Release tears down the sandbox associated with an agent. It falls back to
// the durable binding when the request lands on a pod without the cache entry.
func (p *E2BSandboxProvider) Release(ctx context.Context, agentID string) error {
	if agentID == "" {
		return nil
	}
	p.cacheMu.Lock()
	entry, ok := p.cache[agentID]
	if ok {
		delete(p.cache, agentID)
	}
	p.cacheMu.Unlock()

	var sandboxID, bindingID, deviceID string
	if ok {
		sandboxID = entry.sandbox.SandboxID
		bindingID = entry.bindingID
		deviceID = entry.deviceID
	} else if p.cfg.Bindings != nil {
		row, found, err := p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, agentID)
		if err != nil {
			return fmt.Errorf("agent_daemon sandbox release: lookup binding: %w", err)
		}
		if !found {
			return nil
		}
		sandboxID = row.SandboxID
		bindingID = row.ID
		deviceID, _ = row.Metadata["device_id"].(string)
	}
	if bindingID == "" && !ok {
		return nil
	}
	if sandboxID != "" && !store.IsPendingSandboxID(sandboxID) {
		if err := p.cfg.Client.Kill(ctx, sandboxID); err != nil && !e2b.IsNotFound(err) {
			if entry != nil {
				p.restoreReapCandidate(agentID, entry)
			}
			return fmt.Errorf("agent_daemon sandbox release: kill %s: %w", sandboxID, err)
		}
	}
	// Invalidate session bindings only after the provider accepted the kill.
	if deviceID != "" {
		if err := p.cfg.Binder.InvalidateDevice(ctx, deviceID); err != nil {
			p.cfg.Log.Warn("agent_daemon sandbox release: invalidate device binding failed",
				"device_id", deviceID, "err", err)
		}
	}
	// Best-effort: mark the DB binding killed so admin queries
	// reflect the state change immediately.
	if bindingID != "" && p.cfg.Bindings != nil {
		markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer markCancel()
		if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(markCtx, bindingID, store.SandboxBindingStatusKilled); markErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox release: mark binding killed failed",
				"binding_id", bindingID, "err", markErr)
		}
	}
	p.cfg.Log.Info("agent_daemon sandbox released",
		"agent_id", agentID,
		"sandbox_id", sandboxID,
		"device_id", deviceID)
	return nil
}

// Reap walks the cache and evicts entries whose lastUsed is older than
// the provider's idle cutoff (see idleReapCutoff). Returns the count
// evicted. Failures on individual kills are logged but never abort the
// rest of the sweep.
func (p *E2BSandboxProvider) Reap(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	type victim struct {
		agentID string
		entry   *sandboxEntry
	}
	var victims []victim
	p.cacheMu.Lock()
	for pid, entry := range p.cache {
		idleCutoff := entry.timeout
		if idleCutoff <= 0 {
			idleCutoff = p.idleReapCutoff()
		}
		// Auto-renew is an explicit request to keep the sandbox lease alive
		// across conversational gaps, so the idle reaper must not defeat it.
		if !entry.autoRenew && entry.lastUsed.Before(now.Add(-idleCutoff)) {
			victims = append(victims, victim{agentID: pid, entry: entry})
			delete(p.cache, pid)
		}
	}
	p.cacheMu.Unlock()
	if len(victims) == 0 {
		return 0, nil
	}
	evicted := 0
	for _, v := range victims {
		if p.cfg.Bindings != nil {
			row, found, lookupErr := p.cfg.Bindings.GetActiveSandboxBindingByAgentID(ctx, v.agentID)
			if lookupErr != nil {
				p.restoreReapCandidate(v.agentID, v.entry)
				p.cfg.Log.Warn("agent_daemon reap: binding lookup failed; deferring eviction",
					"agent_id", v.agentID, "err", lookupErr)
				continue
			}
			if found {
				if row.SandboxID != "" && row.SandboxID != v.entry.sandbox.SandboxID {
					p.cfg.Log.Info("agent_daemon reap: dropped stale cache entry after cross-pod replacement",
						"agent_id", v.agentID,
						"cached_sandbox_id", v.entry.sandbox.SandboxID,
						"active_sandbox_id", row.SandboxID)
					evicted++
					continue
				}
				if row.TimeoutSeconds > 0 {
					v.entry.timeout = time.Duration(row.TimeoutSeconds) * time.Second
				}
				v.entry.autoRenew = row.AutoRenewThresholdSeconds > 0
				if row.LastActiveAt.After(v.entry.lastUsed) {
					v.entry.lastUsed = row.LastActiveAt
				}
				if row.ID != "" {
					v.entry.bindingID = row.ID
				}
				idleCutoff := v.entry.timeout
				if idleCutoff <= 0 {
					idleCutoff = p.idleReapCutoff()
				}
				if v.entry.autoRenew || !v.entry.lastUsed.Before(now.Add(-idleCutoff)) {
					p.restoreReapCandidate(v.agentID, v.entry)
					continue
				}
			}
		}
		if err := p.cfg.Binder.InvalidateDevice(ctx, v.entry.deviceID); err != nil {
			p.cfg.Log.Warn("agent_daemon reap: invalidate device binding failed",
				"device_id", v.entry.deviceID, "err", err)
		}
		killCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := p.cfg.Client.Kill(killCtx, v.entry.sandbox.SandboxID); err != nil {
			cancel()
			p.restoreReapCandidate(v.agentID, v.entry)
			p.cfg.Log.Warn("agent_daemon reap: kill sandbox failed; eviction will be retried",
				"sandbox_id", v.entry.sandbox.SandboxID, "err", err)
			continue
		}
		cancel()
		// Best-effort: mark the DB binding killed.
		if v.entry.bindingID != "" && p.cfg.Bindings != nil {
			markCtx, markCancel := context.WithTimeout(ctx, 5*time.Second)
			if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(markCtx, v.entry.bindingID, store.SandboxBindingStatusKilled); markErr != nil {
				p.cfg.Log.Warn("agent_daemon reap: mark binding killed failed",
					"binding_id", v.entry.bindingID, "err", markErr)
			}
			markCancel()
		}
		p.cfg.Log.Info("agent_daemon sandbox reaped (idle)",
			"agent_id", v.agentID,
			"sandbox_id", v.entry.sandbox.SandboxID,
			"device_id", v.entry.deviceID,
			"idle_for", time.Since(v.entry.lastUsed))
		evicted++
	}
	return evicted, nil
}

func (p *E2BSandboxProvider) restoreReapCandidate(agentID string, entry *sandboxEntry) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if _, exists := p.cache[agentID]; !exists {
		p.cache[agentID] = entry
	}
}

// checkDeviceAlive verifies that a cached deviceID still has a live WS
// session somewhere in the fleet. cachedOwnerPodID is the pod where
// the daemon landed when the entry was created.
//
// Decision matrix:
//
//	cachedOwnerPodID == "" / SelfPodID:
//	  Try local Registry first. Miss + OwnerChecker wired → fall back to
//	  OwnerStore to absorb daemon-reconnect windows.
//
//	cachedOwnerPodID != SelfPodID:
//	  Skip the local Registry (can never have a remote-owned device)
//	  and consult OwnerChecker directly.
//
// Returns false when the device is genuinely gone or when OwnerChecker
// disagrees within the timeout. Errors / not-found / expired-lease all
// fail closed.
func (p *E2BSandboxProvider) checkDeviceAlive(ctx context.Context, deviceID, cachedOwnerPodID string) bool {
	local := cachedOwnerPodID == "" || cachedOwnerPodID == p.cfg.SelfPodID
	if local {
		if _, err := p.cfg.Registry.LookupDevice(deviceID); err == nil {
			return true
		}
	}

	if p.cfg.OwnerChecker == nil {
		// Single-pod mode (no DB-backed owner store wired). Local miss
		// is authoritative.
		return false
	}

	qctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	owner, found, err := p.cfg.OwnerChecker.GetAgentDaemonDeviceOwner(qctx, deviceID)
	if err != nil {
		p.cfg.Log.Warn("agent_daemon: owner-store lookup failed during cache health check",
			"device_id", deviceID, "err", err)
		return false
	}
	if !found {
		return false
	}
	if owner.Status != store.AgentDaemonOwnerStatusConnected {
		return false
	}
	if !owner.LeaseExpiresAt.After(time.Now().UTC()) {
		return false
	}
	if !local {
		p.cfg.Log.Info("agent_daemon: cache health check satisfied by remote owner",
			"device_id", deviceID,
			"owner_pod", owner.OwnerPodID,
			"self_pod", p.cfg.SelfPodID)
	}
	return true
}

// evict drops a cache entry and best-efforts to kill the sandbox.
// Used when a cached entry's device has gone offline.
func (p *E2BSandboxProvider) evict(agentID, sandboxID, bindingID string) {
	p.cacheMu.Lock()
	delete(p.cache, agentID)
	p.cacheMu.Unlock()
	if sandboxID == "" {
		return
	}
	killCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.cfg.Client.Kill(killCtx, sandboxID); err != nil {
		p.cfg.Log.Warn("agent_daemon evict: best-effort kill failed",
			"sandbox_id", sandboxID, "err", err)
	}
	// Best-effort: mark the DB binding killed.
	if bindingID != "" && p.cfg.Bindings != nil {
		markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer markCancel()
		if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(markCtx, bindingID, store.SandboxBindingStatusKilled); markErr != nil {
			p.cfg.Log.Warn("agent_daemon evict: mark binding killed failed",
				"binding_id", bindingID, "err", markErr)
		}
	}
}

// shortID returns the first 8 chars of an id for human-readable
// sandbox names.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// shellSingleQuote quotes a value for embedding in a bash command. The
// value is single-quoted so $-expansion and backtick expansion are
// disabled; embedded single quotes are escaped via '\”.
func shellSingleQuote(s string) string {
	// Common case: alnum + safe punctuation, no quoting needed.
	safe := true
	for _, r := range s {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' {
			continue
		}
		safe = false
		break
	}
	if safe {
		return s
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}

// waitForDevice blocks until the daemon with deviceID registers,
// either on this pod (in-memory Registry) or on any pod (Postgres
// OwnerStore). Single-pod (OwnerChecker == nil) delegates to
// Registry.WaitForDevice. Multi-pod races the in-memory waiter
// against a 1-second Postgres poll.
//
// Returns the pod id hosting the WS session: "" when the local
// Registry waiter wins (daemon dialled this pod), or the remote pod
// id when the OwnerStore poll wins.
func (p *E2BSandboxProvider) waitForDevice(ctx context.Context, deviceID string) (string, error) {
	// Fast path: already registered locally.
	if _, err := p.cfg.Registry.LookupDevice(deviceID); err == nil {
		return "", nil
	}

	// Single-pod fallback: no OwnerStore configured.
	if p.cfg.OwnerChecker == nil {
		_, err := p.cfg.Registry.WaitForDevice(ctx, deviceID, SandboxConnectTimeout)
		return "", err
	}

	// Multi-pod: race local Registry waiter against OwnerStore poll.
	type result struct {
		ownerPodID string
		err        error
	}
	done := make(chan result, 2)

	// Goroutine 1: local in-memory waiter (instant if WS lands here).
	go func() {
		_, err := p.cfg.Registry.WaitForDevice(ctx, deviceID, SandboxConnectTimeout)
		select {
		case done <- result{ownerPodID: "", err: err}:
		default:
		}
	}()

	// Goroutine 2: poll OwnerStore for cross-pod registration.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				select {
				case done <- result{err: ctx.Err()}:
				default:
				}
				return
			case <-ticker.C:
				owner, ok, err := p.cfg.OwnerChecker.GetAgentDaemonDeviceOwner(ctx, deviceID)
				if err != nil {
					p.cfg.Log.Warn("waitForDevice: owner store poll error (retrying)",
						"device_id", deviceID, "err", err)
					continue
				}
				if ok && owner.Status == store.AgentDaemonOwnerStatusConnected &&
					owner.LeaseExpiresAt.After(time.Now().UTC()) {
					p.cfg.Log.Info("waitForDevice: device registered on remote pod",
						"device_id", deviceID,
						"owner_pod", owner.OwnerPodID,
						"generation", owner.Generation)
					select {
					case done <- result{ownerPodID: owner.OwnerPodID, err: nil}:
					default:
					}
					return
				}
			}
		}
	}()

	r := <-done
	return r.ownerPodID, r.err
}
