// Sandbox lazy-create path for the agent_daemon connector.
//
// Beyond the "user runs parsar-daemon on their laptop" topology, the
// connector supports a second deployment mode: the server spawns a
// fresh e2b sandbox per project_agent, runs parsar-daemon inside it, and
// lets that sandbox dial back to the gateway over WS. The connector
// treats both modes identically once the WS session is registered —
// only the cold-start path differs.
//
// This file owns the sandbox half. Wired by main.go only when
// AGENT_DAEMON_SANDBOX_TEMPLATE is set; otherwise the connector runs
// in local-only mode and sandbox-mode project_agents fail fast at the
// configuration validation layer.
//
// Lifecycle (per project_agent):
//
//	first prompt  -> Connector hits ErrNotBound -> sees daemon_mode=sandbox
//	             -> SandboxProvider.Acquire(ctx, in):
//	                  1. CreateRuntimePairing(type=agent_daemon)  -> token + runtimeID(=deviceID)
//	                  2. e2b.Create("parsar-daemon-claudecode")        -> sandbox handle
//	                  3. RunCommand(parsar-daemon connect -b + env token)
//	                     -> daemon pairs, then dials WS in background
//	                  4. Registry.WaitForDevice(deviceID, 45s)     -> blocks until WS upgrade lands
//	                  5. Binder.Bind(conversation -> deviceID)     -> persist for next turn
//	                  6. return deviceID
//	          subsequent prompts in same conversation -> binder hit, no sandbox work
//	          new conversation against same project_agent -> Acquire reuses the cached sandbox
//	          long idle period -> Reap() kills the sandbox + evicts cache; next Acquire cold-starts again
//	          conversation archived / project_agent deleted -> Release() kills + evicts immediately
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

// SandboxDefaultTTL is the e2b sandbox TTL requested on Create.
// Long-lived by design: an agent_daemon sandbox carries user-installed
// config and claude session state across conversations and must NOT
// be silently recycled while in use. SANDBOX_TIMEOUT_MS defaults to
// 30d so an agent stays usable through normal
// conversational gaps without depending on a per-prompt renew loop.
var SandboxDefaultTTL = 30 * 24 * time.Hour

// SandboxIdleReapThreshold is the lastUsed cutoff Reap honours.
//
// Kept >= SandboxDefaultTTL so Reap can never preempt e2b's own TTL
// expiry: lastUsed is only refreshed on Acquire fast-path hits, and
// already-bound conversations skip Acquire entirely after cold start
// (binder.Resolve takes over). Reap is effectively a defence-in-depth
// sweep for cache entries whose sandbox was killed out-of-band.
var SandboxIdleReapThreshold = 30 * 24 * time.Hour

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

	// SandboxStatus returns the cached sandbox info for a
	// project_agent. (zero, false, nil) when not cached.
	//
	// info.ExpiresAt is populated best-effort by querying e2b for the
	// live TTL; a transient e2b error leaves it zero and the admin
	// handler renders zero as "未知".
	SandboxStatus(ctx context.Context, projectAgentID string) (connector.SandboxInfo, bool, error)

	// Release tears down the sandbox associated with a project_agent.
	// Idempotent: releasing an unknown project_agent is a no-op.
	Release(ctx context.Context, projectAgentID string) error

	// Renew bumps the e2b-side TTL to SandboxDefaultTTL. Returns the
	// refreshed expires_at. (zero, false, nil) when no live cache
	// entry exists — the admin handler maps this to 404.
	Renew(ctx context.Context, projectAgentID string) (expiresAt time.Time, found bool, err error)

	// SandboxRuntimeInfo queries e2b directly for live expiry by
	// sandboxID (not projectAgentID). Bypasses the in-memory cache so
	// any pod can answer. Returns zero time on transient failures.
	SandboxRuntimeInfo(ctx context.Context, sandboxID string) (expiresAt time.Time, err error)

	// Reap evicts sandboxes whose lastUsed is older than the
	// configured idle threshold. Returns the count evicted.
	Reap(ctx context.Context) (evicted int, err error)
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

func (NoopSandboxProvider) Reap(_ context.Context) (int, error) { return 0, nil }

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
	FinalizeSandboxBindingSpawning(ctx context.Context, input store.FinalizeSandboxBindingSpawningInput) error
	WaitForSandboxBindingActive(ctx context.Context, workspaceID, projectAgentID string, pollInterval time.Duration) (store.SandboxBindingRead, error)
	TouchSandboxBinding(ctx context.Context, bindingID string) error
	MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error
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
//   - Template: the e2b template id (e.g. "parsar-daemon-claudecode"). The
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
	Client        E2BClient
	Store         RuntimeMinter
	Registry      *gateway.Registry
	Binder        binding.Binder
	Bindings      SandboxBindingPersister // nil = memory-only (local dev)
	Template      string
	// Templates maps a sandbox_size label (e.g. "standard", "xl") to the
	// e2b template id for that size. The agent's project_agents.config
	// `sandbox_size` field selects which template gets used on cold start.
	// When nil or empty, all acquires fall back to Template.
	//
	// Cache key remains keyed by project_agent_id only, so an agent at any
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
	Log       *slog.Logger
}

// sandboxEntry is the per-project_agent cached sandbox handle.
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
}

// E2BSandboxProvider is the e2b-backed SandboxProvider implementation.
// Concurrency-safe via cacheMu.
type E2BSandboxProvider struct {
	cfg E2BProviderConfig

	cacheMu sync.Mutex
	cache   map[string]*sandboxEntry // key = project_agent_id

	// inflight serialises concurrent Acquire calls for the same
	// project_agent so a thundering herd of new conversations only
	// triggers one Create.
	inflight map[string]*acquirePromise
}

// acquirePromise is the per-project_agent serialisation primitive.
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
		return nil, errors.New("E2BSandboxProvider: Template required (e.g. parsar-daemon-claudecode)")
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
//  1. ProjectAgentConfig["sandbox_size"]   — workspace-level override
//  2. AgentConfig["sandbox_size"]          — agent-template default
//  3. cfg.DefaultSize                      — provider default ("standard")
//
// Whichever size wins is then looked up in cfg.Templates. If the
// resolved size has no entry (e.g. an agent requests "xl" but the
// deployment didn't configure AGENT_DAEMON_SANDBOX_TEMPLATE_XL), we
// degrade to cfg.Template — the canonical standard template — and
// log a warn so the misconfiguration surfaces in mlogs. This keeps a
// missing XL pool from breaking acquires entirely.
//
// Cache key stays projectAgentID-only (see Acquire), so an agent at
// any moment has at most one active sandbox; the new size only takes
// effect after the current sandbox is released and the next cold
// start runs.
func (p *E2BSandboxProvider) resolveTemplate(in connector.PromptInput) (size, templateID string) {
	size = strings.TrimSpace(stringFromMap(in.ProjectAgentConfig, "sandbox_size"))
	if size == "" {
		size = strings.TrimSpace(stringFromMap(in.AgentConfig, "sandbox_size"))
	}
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

// Acquire returns a deviceID for the project_agent's sandbox. Cold
// starts go through the full mint-create-login-connect dance; warm
// hits return the cached deviceID after a touch + Renew.
//
// Concurrency: two Acquire calls for the same project_agent serialise
// on inflight[projectAgentID] so we never create more than one sandbox
// per project_agent under contention.
func (p *E2BSandboxProvider) Acquire(ctx context.Context, in connector.PromptInput) (string, error) {
	if in.ProjectAgentID == "" {
		return "", errors.New("E2BSandboxProvider.Acquire: ProjectAgentID required")
	}
	if in.WorkspaceID == "" {
		return "", errors.New("E2BSandboxProvider.Acquire: WorkspaceID required (needed for runtime pairing)")
	}

	// Fast path: warm cache hit.
	p.cacheMu.Lock()
	if entry, ok := p.cache[in.ProjectAgentID]; ok {
		entry.lastUsed = time.Now().UTC()
		deviceID := entry.deviceID
		sandboxID := entry.sandbox.SandboxID
		bindingID := entry.bindingID
		ownerPodID := entry.ownerPodID
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
				"project_agent_id", in.ProjectAgentID,
				"device_id", deviceID,
				"cached_owner_pod", ownerPodID,
				"self_pod", p.cfg.SelfPodID)
			p.evict(in.ProjectAgentID, sandboxID, bindingID)
			// fall through to cold start
		} else {
			// Renew the sandbox TTL on every cache hit so an active
			// conversation doesn't get evicted mid-thread.
			renewCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := p.cfg.Client.Renew(renewCtx, sandboxID, int(SandboxDefaultTTL.Seconds())); err != nil {
				p.cfg.Log.Warn("agent_daemon sandbox renew failed (continuing)",
					"sandbox_id", sandboxID, "err", err)
			}
			cancel()
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

	// Serialise concurrent cold starts for the same project_agent.
	p.cacheMu.Lock()
	if promise, ok := p.inflight[in.ProjectAgentID]; ok {
		p.cacheMu.Unlock()
		select {
		case <-promise.done:
			return promise.deviceID, promise.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	promise := &acquirePromise{done: make(chan struct{})}
	p.inflight[in.ProjectAgentID] = promise
	p.cacheMu.Unlock()

	defer func() {
		p.cacheMu.Lock()
		delete(p.inflight, in.ProjectAgentID)
		p.cacheMu.Unlock()
		close(promise.done)
	}()

	// Cross-pod coordination: race to claim the (workspace,
	// project_agent) slot in the sandboxes table before doing any
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
	cacheKey := "agent_daemon:" + in.ProjectAgentID
	_, templateID := p.resolveTemplate(in)
	row, won, err := p.cfg.Bindings.ReserveSandboxBindingSlot(ctx, store.ReserveSandboxBindingSlotInput{
		WorkspaceID:    in.WorkspaceID,
		ProjectAgentID: in.ProjectAgentID,
		CacheKey:       cacheKey,
		TemplateID:     templateID,
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
			"project_agent_id", in.ProjectAgentID,
			"binding_id", row.ID)
		return row.ID, "", nil
	}
	// Loser: wait for the winner.
	p.cfg.Log.Info("sandbox slot already held; waiting for winner to finish cold-start",
		"project_agent_id", in.ProjectAgentID,
		"binding_id", row.ID,
		"winner_status", row.Status)
	waitCtx, cancel := context.WithTimeout(ctx, SandboxAcquireTimeout)
	defer cancel()
	finalRow, waitErr := p.cfg.Bindings.WaitForSandboxBindingActive(waitCtx, in.WorkspaceID, in.ProjectAgentID, 0)
	if waitErr != nil {
		return "", "", fmt.Errorf("%w: wait for winner: %v", ErrSandboxAcquireFailed, waitErr)
	}
	deviceID, _ := finalRow.Metadata["device_id"].(string)
	if deviceID == "" {
		return "", "", fmt.Errorf("%w: winner row has no device_id in metadata", ErrSandboxAcquireFailed)
	}
	// We don't seed p.cache here: this pod never created the sandbox,
	// has no envd token, and can't Renew/Touch it. The next Acquire
	// will Reserve, see the existing binding (still running), and
	// Wait again — cheap.
	p.cfg.Log.Info("sandbox slot resolved via cross-pod wait",
		"project_agent_id", in.ProjectAgentID,
		"binding_id", finalRow.ID,
		"device_id", deviceID)
	return "", deviceID, nil
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
	runtimeName := fmt.Sprintf("sandbox %s", shortID(in.ProjectAgentID))
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
		// owned by the project_agent, not a human user.
		TokenTTL: SandboxAcquireTimeout + 30*time.Second,
		Config: map[string]any{
			"created_by":       "sandbox_provider",
			"project_agent_id": in.ProjectAgentID,
			"sandbox_kind":     "agent_daemon_claude_code",
		},
	})
	if err != nil {
		releaseReservation(err)
		return "", fmt.Errorf("%w: mint pairing: %v", ErrSandboxAcquireFailed, err)
	}
	deviceID := pair.Runtime.ID

	// 2. Create the e2b sandbox.
	sandbox, err := p.cfg.Client.Create(bootCtx, e2b.CreateInput{
		TemplateID:     templateID,
		TimeoutSeconds: int(SandboxDefaultTTL.Seconds()),
		Metadata: map[string]string{
			"parsar.workspace_id":     in.WorkspaceID,
			"parsar.project_agent_id": in.ProjectAgentID,
			"parsar.device_id":        deviceID,
			"parsar.sandbox_kind":     "agent_daemon_claude_code",
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
		"project_agent_id", in.ProjectAgentID,
		"sandbox_size", resolvedSize,
		"template_id", templateID)

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
	if err := seedPlatformConfig(bootCtx, p.cfg.Client, sandbox, p.cfg.Connector, envdURL); err != nil {
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
	//    runtime_type checks. Empty fields (e.g. ProjectID for
	//    workspace-only agents) are omitted rather than set to "" so
	//    hook scripts can treat `os.environ.get("PARSAR_PROJECT_ID")` as
	//    the "is this project scoped?" signal.
	connectCmd := fmt.Sprintf("parsar-daemon connect --device-name %s -b", shellSingleQuote(deviceID))
	connectEnv := map[string]string{
		"PARSAR_DAEMON_CONNECT_URL":   p.cfg.ServerURL,
		"PARSAR_DAEMON_CONNECT_TOKEN": pair.PairingToken,
		// parsar CLI env — same token, presented under the name the CLI
		// reads. Hook scripts shell out to `parsar inject ...` which
		// expects PARSAR_RUNNER_TOKEN, not PARSAR_DAEMON_CONNECT_TOKEN.
		"PARSAR_SERVER_URL":       p.cfg.ServerURL,
		"PARSAR_RUNNER_TOKEN":     pair.PairingToken,
		"PARSAR_RUNTIME_ID":       deviceID,
		"PARSAR_WORKSPACE_ID":     in.WorkspaceID,
		"PARSAR_PROJECT_AGENT_ID": in.ProjectAgentID,
		"PARSAR_CONNECTOR":        connectorTagFor(p.cfg.Connector),
	}
	if in.ConversationInitiatorID != "" {
		connectEnv["PARSAR_USER_ID"] = in.ConversationInitiatorID
	}
	if in.ProjectID != "" {
		connectEnv["PARSAR_PROJECT_ID"] = in.ProjectID
	}
	if in.ConversationID != "" {
		connectEnv["PARSAR_CONVERSATION_ID"] = in.ConversationID
	}
	connectRes, err := p.cfg.Client.RunCommand(bootCtx, e2b.RunCommandInput{
		Sandbox: sandbox,
		Command: connectCmd,
		CWD:     "/workspace",
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
	//    multiple conversations under the same project_agent.
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
					"project_agent_id", in.ProjectAgentID,
					"binding_id", reservedBindingID,
					"sandbox_id", sandbox.SandboxID,
					"err", finalizeErr)
			} else {
				entry.bindingID = reservedBindingID
			}
		} else {
			binding, bindErr := p.cfg.Bindings.CreateSandboxBinding(bootCtx, store.CreateSandboxBindingInput{
				WorkspaceID:    in.WorkspaceID,
				ProjectAgentID: in.ProjectAgentID,
				CacheKey:       "agent_daemon:" + in.ProjectAgentID,
				SandboxID:      sandbox.SandboxID,
				TemplateID:     templateID,
				Status:         store.SandboxBindingStatusActive,
				Metadata: map[string]any{
					"sandbox_kind": "agent_daemon",
					"device_id":    deviceID,
					"connector":    string(p.cfg.Connector),
				},
			})
			if bindErr != nil {
				p.cfg.Log.Warn("sandbox binding persist failed (sandbox functional, admin visibility degraded)",
					"project_agent_id", in.ProjectAgentID,
					"sandbox_id", sandbox.SandboxID,
					"err", bindErr)
			} else {
				entry.bindingID = binding.ID
			}
		}
	}

	p.cacheMu.Lock()
	p.cache[in.ProjectAgentID] = entry
	p.cacheMu.Unlock()

	p.cfg.Log.Info("agent_daemon sandbox acquired",
		"project_agent_id", in.ProjectAgentID,
		"sandbox_id", sandbox.SandboxID,
		"device_id", deviceID)
	return deviceID, nil
}

// SandboxStatus returns the cached sandbox info for a project_agent.
// (zero, false, nil) when not cached.
func (p *E2BSandboxProvider) SandboxStatus(ctx context.Context, projectAgentID string) (connector.SandboxInfo, bool, error) {
	p.cacheMu.Lock()
	entry, ok := p.cache[projectAgentID]
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

// Renew bumps the e2b TTL for the project_agent's sandbox to
// SandboxDefaultTTL. (zero, false, nil) when no live cache entry exists
// (admin panel renders "no sandbox"). Touches lastUsed so the cache
// survives the next Reap window.
func (p *E2BSandboxProvider) Renew(ctx context.Context, projectAgentID string) (time.Time, bool, error) {
	if strings.TrimSpace(projectAgentID) == "" {
		return time.Time{}, false, nil
	}
	p.cacheMu.Lock()
	entry, ok := p.cache[projectAgentID]
	if ok {
		entry.lastUsed = time.Now().UTC()
	}
	sandboxID := ""
	if ok {
		sandboxID = entry.sandbox.SandboxID
	}
	p.cacheMu.Unlock()
	if !ok {
		return time.Time{}, false, nil
	}

	renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := p.cfg.Client.Renew(renewCtx, sandboxID, int(SandboxDefaultTTL.Seconds())); err != nil {
		return time.Time{}, true, fmt.Errorf("e2b renew sandbox %s: %w", sandboxID, err)
	}

	// Re-query e2b for canonical expires_at — the timeout endpoint
	// doesn't echo post-renew endAt, only GetInfo. A read failure
	// here doesn't undo the renew; we still return ok and let the
	// panel re-fetch via SandboxStatus.
	getCtx, cancelGet := context.WithTimeout(ctx, 3*time.Second)
	defer cancelGet()
	runtime, err := p.cfg.Client.GetInfo(getCtx, sandboxID)
	if err != nil {
		p.cfg.Log.Warn("agent_daemon sandbox renew: e2b GetInfo after Renew failed (renew itself succeeded)",
			"sandbox_id", sandboxID, "err", err)
		return time.Time{}, true, nil
	}
	p.cfg.Log.Info("agent_daemon sandbox renewed",
		"project_agent_id", projectAgentID,
		"sandbox_id", sandboxID,
		"expires_at", runtime.EndAt)
	return runtime.EndAt, true, nil
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

// Release tears down the sandbox associated with a project_agent:
// lookup → evict cache → kill → drop binder rows pointing at the device.
func (p *E2BSandboxProvider) Release(ctx context.Context, projectAgentID string) error {
	if projectAgentID == "" {
		return nil
	}
	p.cacheMu.Lock()
	entry, ok := p.cache[projectAgentID]
	if ok {
		delete(p.cache, projectAgentID)
	}
	p.cacheMu.Unlock()
	if !ok {
		return nil
	}
	// Best-effort device invalidation in binder so a stale binding
	// row doesn't keep pointing at the dead device.
	if err := p.cfg.Binder.InvalidateDevice(ctx, entry.deviceID); err != nil {
		p.cfg.Log.Warn("agent_daemon sandbox release: invalidate device binding failed",
			"device_id", entry.deviceID, "err", err)
	}
	if err := p.cfg.Client.Kill(ctx, entry.sandbox.SandboxID); err != nil {
		return fmt.Errorf("agent_daemon sandbox release: kill %s: %w", entry.sandbox.SandboxID, err)
	}
	// Best-effort: mark the DB binding killed so admin queries
	// reflect the state change immediately.
	if entry.bindingID != "" && p.cfg.Bindings != nil {
		markCtx, markCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer markCancel()
		if markErr := p.cfg.Bindings.MarkSandboxBindingKilled(markCtx, entry.bindingID, store.SandboxBindingStatusKilled); markErr != nil {
			p.cfg.Log.Warn("agent_daemon sandbox release: mark binding killed failed",
				"binding_id", entry.bindingID, "err", markErr)
		}
	}
	p.cfg.Log.Info("agent_daemon sandbox released",
		"project_agent_id", projectAgentID,
		"sandbox_id", entry.sandbox.SandboxID,
		"device_id", entry.deviceID)
	return nil
}

// Reap walks the cache and evicts entries whose lastUsed is older than
// SandboxIdleReapThreshold. Returns the count evicted. Failures on
// individual kills are logged but never abort the rest of the sweep.
func (p *E2BSandboxProvider) Reap(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-SandboxIdleReapThreshold)
	type victim struct {
		projectAgentID string
		entry          *sandboxEntry
	}
	var victims []victim
	p.cacheMu.Lock()
	for pid, entry := range p.cache {
		if entry.lastUsed.Before(cutoff) {
			victims = append(victims, victim{projectAgentID: pid, entry: entry})
			delete(p.cache, pid)
		}
	}
	p.cacheMu.Unlock()
	if len(victims) == 0 {
		return 0, nil
	}
	for _, v := range victims {
		if err := p.cfg.Binder.InvalidateDevice(ctx, v.entry.deviceID); err != nil {
			p.cfg.Log.Warn("agent_daemon reap: invalidate device binding failed",
				"device_id", v.entry.deviceID, "err", err)
		}
		killCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := p.cfg.Client.Kill(killCtx, v.entry.sandbox.SandboxID); err != nil {
			p.cfg.Log.Warn("agent_daemon reap: kill sandbox failed (already evicted from cache)",
				"sandbox_id", v.entry.sandbox.SandboxID, "err", err)
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
			"project_agent_id", v.projectAgentID,
			"sandbox_id", v.entry.sandbox.SandboxID,
			"device_id", v.entry.deviceID,
			"idle_for", time.Since(v.entry.lastUsed))
	}
	return len(victims), nil
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
func (p *E2BSandboxProvider) evict(projectAgentID, sandboxID, bindingID string) {
	p.cacheMu.Lock()
	delete(p.cache, projectAgentID)
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
