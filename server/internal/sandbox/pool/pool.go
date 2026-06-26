// Package pool provides an admin-managed set of pre-warmed E2B-compatible
// sandboxes. Admins explicitly batch-spawn entries with per-batch timeouts;
// user prompts Claim an idle entry on cache miss; admin can ManualRenew,
// SetAutoRenewThreshold, or Kill. No auto-spawn / auto-refill; an empty
// pool returns ok=false from Claim so the caller falls back to full spawn.
// In-memory state is authoritative within one server lifetime — restart
// drops everything; persistence layer's startup sweep marks orphaned DB
// rows killed_orphaned. Renew failures log + leave the entry alive so
// admin sees the failure status and decides what to do.
package pool

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	e2bsandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// SandboxFactory is how the pool obtains a fresh blank sandbox. Pool
// callers wire this to opencode.CreateBlankSandbox — keeping the
// signature decoupled from the connector package lets tests inject a
// fake factory without dragging the e2b client setup in.
//
// timeoutSeconds is the admin-chosen per-batch e2b sandbox timeout
// passed through to e2b Create — the e2b-side hard expiry is at
// now + timeoutSeconds unless Renew bumps it. Factory implementations
// MUST respect this value so the pool's expires_at column reflects
// the truth on e2b's side.
//
// The returned kill callback is the caller's responsibility once Claim
// hands the sandbox to a downstream consumer. Within the pool itself
// (i.e. while the sandbox is sitting idle), Kill / Shutdown is the
// sole kill trigger; ManualRenew / auto-renew NEVER kill on failure
// (they log + leave the entry as-is so admin sees the failure status
// and decides what to do).
type SandboxFactory func(ctx context.Context, timeoutSeconds int32) (e2bsandbox.Sandbox, func(), error)

// SandboxRenewer is the per-sandbox renew action invoked by
// ManualRenew + the auto-renew scan goroutine. Wired to
// e2b.Client.Renew in production; tests inject a fake.
//
// timeoutSeconds is the entry's per-batch timeout — Renew rolls the
// e2b-side expiry to now + timeoutSeconds, mirroring what BatchSpawn
// originally requested. Renew error is NOT treated as terminal in
// Phase 3 (Phase 1-2 used to evict + kill on renew failure); admin
// sees the failure via the next status read and decides whether to
// retry or kill manually. The renewer error bubbles up to the caller
// of ManualRenew; the auto-renew scan logs it and moves on.
type SandboxRenewer func(ctx context.Context, sandboxID string, timeoutSeconds int32) error

// Persistence is the write-through hook the pool calls on lifecycle
// events. Phase 3 made it mandatory in production (admin UI reads
// from the DB, not the in-memory state); tests with nil Persist
// degrade gracefully to in-memory-only.
//
// Status values for OnTerminal:
//   - "killed" — admin manual kill, Shutdown drain, or post-bootstrap
//     orphan kill (Claim-then-Bootstrap-failed).
//   - "killed_orphaned" — startup sweep marked a stale entry from a
//     previous server lifetime. Pool calls this only via the store's
//     own SweepOrphanedSandboxPoolEntries (not from inside pool.go).
type Persistence interface {
	// OnSpawn persists a freshly-created entry. expiresAt is when
	// e2b will auto-expire the sandbox unless Renew bumps it;
	// timeoutSeconds is the admin-chosen per-batch timeout that
	// Renew uses to roll expiresAt forward.
	OnSpawn(ctx context.Context, workspaceID, sandboxID, templateID string, expiresAt time.Time, timeoutSeconds int32) error
	// OnRenew updates last_renewed_at + rolls expires_at forward
	// after a successful Renew.
	OnRenew(ctx context.Context, sandboxID string, expiresAt time.Time) error
	// OnClaim records the claim handoff WITHOUT marking the row
	// killed — admin UI keeps claimed rows visible until a real
	// kill. Phase 3 split this out from OnTerminal (which used to
	// receive status='claimed' and route through MarkKilled).
	OnClaim(ctx context.Context, workspaceID, projectAgentID, cacheKey, sandboxID string) error
	// OnTerminal records a real kill (status='killed' /
	// 'killed_orphaned' / 'killed_error').
	OnTerminal(ctx context.Context, sandboxID, status string) error
	// OnSetAutoRenewThreshold persists the per-entry threshold.
	// 0 = off, >0 = trigger seconds of remaining lifetime.
	OnSetAutoRenewThreshold(ctx context.Context, sandboxID string, thresholdSeconds int32) error
}

// Persistence terminal status constants. Mirrors the
// sandbox_pool.status CHECK enum; declaring them here lets pool.go
// callers reference symbolic values without importing the store
// package.
const (
	PersistenceStatusKilled = "killed"
	// PersistenceStatusClaimed is the in-memory status name; it is
	// NOT a terminal status (claim does not kill the row), so it
	// is not passed to OnTerminal — see OnClaim above. Defined for
	// symmetry with the in-memory entry status field.
	PersistenceStatusClaimed = "claimed"
)

// EntryStatus is the in-memory status of a pool entry. Mirrors the
// sandbox-pool view status enum so the admin Stats snapshot can report
// counts per status without a DB round trip.
const (
	EntryStatusIdle     = "idle"
	EntryStatusRenewing = "renewing"
	EntryStatusClaimed  = "claimed"
)

// Options configures one Pool. All fields except Factory are optional;
// zero values give Phase 3 defaults.
type Options struct {
	// Factory is required. Called by BatchSpawn to mint blank
	// sandboxes with the admin-chosen per-batch timeout.
	Factory SandboxFactory

	// Renewer is OPTIONAL but strongly recommended. nil disables
	// ManualRenew + the auto-renew scan (operations return an
	// error / scan no-ops). Wire it to e2b.Client.Renew in
	// production.
	Renewer SandboxRenewer

	// AutoRenewScanInterval is how often the auto-renew scan
	// goroutine wakes up to check entries with threshold > 0.
	// Zero defaults to defaultAutoRenewScanInterval (60s). The
	// scan walks the in-memory entry map and calls Renewer on
	// any row where ExpiresAt.Sub(now) <= AutoRenewThreshold.
	AutoRenewScanInterval time.Duration

	// Persist is the write-through hook for admin UI + post-restart
	// cleanup. nil disables persistence entirely (in-memory only).
	// Production wiring REQUIRES Persist non-nil — admin UI reads
	// from the persisted listing, not from Stats().
	Persist Persistence

	// Logger receives one-line lifecycle traces. Nil suppresses
	// logging.
	Logger func(format string, args ...any)
}

const (
	defaultAutoRenewScanInterval = 60 * time.Second
	// persistOpTimeout bounds each fire-and-forget Persist call so
	// a slow backend can't pile up goroutines indefinitely.
	persistOpTimeout = 5 * time.Second
)

// Stats is a snapshot of pool state. Used by admin endpoints + tests
// to observe pool health without touching internal mutex state.
type Stats struct {
	// Idle is the count of entries currently available for Claim.
	Idle int
	// Renewing is the count of entries mid-Renew (auto or manual).
	// These are not Claim-able until the renewer returns.
	Renewing int
	// Claimed is the count of entries that have been handed off
	// but not yet killed. Still tracked in the in-memory map so
	// Stats matches the persistent listing.
	Claimed int
	// Started reports whether Start has been called.
	Started bool
	// Closed reports whether Shutdown has been called.
	Closed bool
}

// Total is a convenience for admin UI consumers. Always equal to
// Idle + Renewing + Claimed.
func (s Stats) Total() int {
	return s.Idle + s.Renewing + s.Claimed
}

// EntrySnapshot is one entry's externally-visible state, returned by
// ListEntries. Mirrors the persistent SandboxPoolEntryRead shape so
// admin handlers can hand it back to the UI without a DB round trip
// when in-memory state is authoritative.
type EntrySnapshot struct {
	WorkspaceID               string
	SandboxID                 string
	TemplateID                string
	Status                    string
	CreatedAt                 time.Time
	LastRenewedAt             time.Time
	ExpiresAt                 time.Time
	TimeoutSeconds            int32
	AutoRenewThresholdSeconds int32
}

// Pool maintains an admin-managed set of blank sandboxes. Construct
// via New; do not zero-value (the inner mutex + map would be nil and
// Start would race on first use).
type Pool struct {
	mu sync.Mutex

	entries map[string]*poolEntry

	factory               SandboxFactory
	renewer               SandboxRenewer
	persist               Persistence
	autoRenewScanInterval time.Duration

	started bool
	closed  bool

	// shutdownCh is closed by Shutdown to signal the scan
	// goroutine to exit.
	shutdownCh chan struct{}

	// loopDone is closed once the scan goroutine has returned.
	// Shutdown waits on this so callers see a fully-drained pool
	// when Shutdown returns.
	loopDone chan struct{}

	logf func(format string, args ...any)
}

type poolEntry struct {
	workspaceID string
	sandbox     e2bsandbox.Sandbox
	templateID  string
	kill        func()

	createdAt     time.Time
	lastRenewedAt time.Time

	timeoutSeconds            int32
	expiresAt                 time.Time
	autoRenewThresholdSeconds int32

	// status is one of EntryStatus* constants.
	status string

	// renewing flag — gates Claim from popping an entry that is
	// currently inside a Renewer call (network-bound, lock released).
	// Set to true before releasing the lock for renew, cleared when
	// re-acquiring. Same shape as Phase 2's idleEntry.renewing.
	renewing bool
}

// ErrPoolClosed is returned by mutating operations after Shutdown has
// run. Callers should treat this as a permanent state for this server
// lifetime.
var ErrPoolClosed = errors.New("sandbox pool: closed")

// ErrEntryNotFound is returned by ManualRenew / SetAutoRenewThreshold
// / Kill when the sandbox id is not in the in-memory entry map.
// Distinguishable from other failure modes so admin handlers can
// translate to HTTP 404.
var ErrEntryNotFound = errors.New("sandbox pool: entry not found")

// ErrRenewerNotConfigured is returned by ManualRenew when the pool
// was constructed without a Renewer. Distinguished so the admin UI
// can render a clear "renew disabled in this deployment" hint.
var ErrRenewerNotConfigured = errors.New("sandbox pool: renewer not configured")

// New constructs a Pool. Factory must be non-nil — Phase 3 has no
// "disabled pool" mode (the disabled-via-Size=0 trick from Phase 1
// is gone). Callers that don't want a pool simply don't construct
// one.
func New(opts Options) (*Pool, error) {
	if opts.Factory == nil {
		return nil, errors.New("sandbox pool: Factory is required")
	}
	scanInterval := opts.AutoRenewScanInterval
	if scanInterval <= 0 {
		scanInterval = defaultAutoRenewScanInterval
	}
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Pool{
		entries:               make(map[string]*poolEntry),
		factory:               opts.Factory,
		renewer:               opts.Renewer,
		persist:               opts.Persist,
		autoRenewScanInterval: scanInterval,
		shutdownCh:            make(chan struct{}),
		loopDone:              make(chan struct{}),
		logf:                  logf,
	}, nil
}

// Start launches the auto-renew scan goroutine. Idempotent on re-call
// (returns nil silently); calling after Shutdown returns ErrPoolClosed.
// When Renewer is nil the scan goroutine still starts but no-ops —
// keeps Shutdown's loopDone wait symmetric.
func (p *Pool) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	if p.started {
		p.mu.Unlock()
		return nil
	}
	p.started = true
	p.mu.Unlock()

	go p.runAutoRenew(ctx)
	p.logf("sandbox pool: started scanInterval=%s renewerConfigured=%t persistConfigured=%t",
		p.autoRenewScanInterval, p.renewer != nil, p.persist != nil)
	return nil
}

// runAutoRenew is the single background goroutine. Each tick it
// walks the entry map and calls Renewer on any entry where the
// remaining lifetime has dropped below its auto-renew threshold.
func (p *Pool) runAutoRenew(ctx context.Context) {
	defer close(p.loopDone)
	timer := time.NewTimer(p.autoRenewScanInterval)
	defer timer.Stop()
	for {
		select {
		case <-p.shutdownCh:
			return
		case <-ctx.Done():
			return
		case <-timer.C:
			p.autoRenewStep(ctx)
			timer.Reset(p.autoRenewScanInterval)
		}
	}
}

// autoRenewStep walks the entry map, picks entries due for renew,
// and runs Renewer on each. Snapshots ids under the lock, runs the
// network-bound Renewer with the lock released, then re-acquires
// the lock to clear the renewing flag + update timestamps. Renewer
// failures are logged — the entry stays alive so admin sees the
// failure status and decides what to do (Phase 1-2 evicted+killed
// on renew failure; Phase 3 admin-managed mode reverses that).
func (p *Pool) autoRenewStep(ctx context.Context) {
	if p.renewer == nil {
		return
	}
	now := time.Now().UTC()

	type renewTarget struct {
		sandboxID      string
		timeoutSeconds int32
	}
	var targets []renewTarget

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for _, entry := range p.entries {
		if entry.status != EntryStatusIdle {
			continue
		}
		if entry.renewing {
			continue
		}
		if entry.autoRenewThresholdSeconds <= 0 {
			continue
		}
		remaining := entry.expiresAt.Sub(now)
		threshold := time.Duration(entry.autoRenewThresholdSeconds) * time.Second
		if remaining > threshold {
			continue
		}
		entry.renewing = true
		entry.status = EntryStatusRenewing
		targets = append(targets, renewTarget{
			sandboxID:      entry.sandbox.SandboxID,
			timeoutSeconds: entry.timeoutSeconds,
		})
	}
	p.mu.Unlock()

	for _, t := range targets {
		select {
		case <-ctx.Done():
			p.markAutoRenewDone(t.sandboxID, false, time.Time{})
			continue
		case <-p.shutdownCh:
			p.markAutoRenewDone(t.sandboxID, false, time.Time{})
			continue
		default:
		}
		if err := p.renewer(ctx, t.sandboxID, t.timeoutSeconds); err != nil {
			p.logf("sandbox pool: auto-renew failed sandbox=%s err=%v", t.sandboxID, err)
			p.markAutoRenewDone(t.sandboxID, false, time.Time{})
			continue
		}
		newExpires := time.Now().UTC().Add(time.Duration(t.timeoutSeconds) * time.Second)
		p.markAutoRenewDone(t.sandboxID, true, newExpires)
		p.emitOnRenew(t.sandboxID, newExpires)
	}
}

// markAutoRenewDone resets the renewing flag (and on success, rolls
// expires_at + last_renewed_at forward). Called after every renew
// attempt, success or failure. Status flips back to idle so Claim
// can see the entry again.
func (p *Pool) markAutoRenewDone(sandboxID string, ok bool, newExpires time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry, found := p.entries[sandboxID]
	if !found {
		return
	}
	entry.renewing = false
	// status may have moved to 'claimed' or been Killed mid-renew;
	// only reset to 'idle' when still in renewing state.
	if entry.status == EntryStatusRenewing {
		entry.status = EntryStatusIdle
	}
	if ok {
		entry.lastRenewedAt = time.Now().UTC()
		entry.expiresAt = newExpires
	}
}

// BatchSpawn is the admin-driven entry-creation path. count must be
// > 0 and timeoutSeconds > 0. Returns the ids of the successfully
// created entries and a slice of errors for the failures — the batch
// is partial-success: one Factory error does not abort the rest.
// Errors are returned in creation order so admin UI can render
// "1 of 5 failed (slot 3: e2b 502)".
//
// Calling BatchSpawn after Shutdown returns ErrPoolClosed without
// calling Factory.
func (p *Pool) BatchSpawn(ctx context.Context, workspaceID string, count int, timeoutSeconds int32) ([]string, []error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, []error{fmt.Errorf("sandbox pool: BatchSpawn workspaceID is required")}
	}
	if count <= 0 {
		return nil, []error{fmt.Errorf("sandbox pool: BatchSpawn count must be > 0, got %d", count)}
	}
	if timeoutSeconds <= 0 {
		return nil, []error{fmt.Errorf("sandbox pool: BatchSpawn timeoutSeconds must be > 0, got %d", timeoutSeconds)}
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, []error{ErrPoolClosed}
	}
	p.mu.Unlock()

	created := make([]string, 0, count)
	var errs []error
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("sandbox pool: BatchSpawn slot %d: %w", i, ctx.Err()))
			return created, errs
		case <-p.shutdownCh:
			errs = append(errs, fmt.Errorf("sandbox pool: BatchSpawn slot %d: %w", i, ErrPoolClosed))
			return created, errs
		default:
		}
		sandbox, kill, err := p.factory(ctx, timeoutSeconds)
		if err != nil {
			errs = append(errs, fmt.Errorf("sandbox pool: BatchSpawn slot %d: %w", i, err))
			continue
		}
		now := time.Now().UTC()
		expiresAt := now.Add(time.Duration(timeoutSeconds) * time.Second)
		entry := &poolEntry{
			workspaceID:    workspaceID,
			sandbox:        sandbox,
			templateID:     sandbox.TemplateID,
			kill:           kill,
			createdAt:      now,
			lastRenewedAt:  now,
			timeoutSeconds: timeoutSeconds,
			expiresAt:      expiresAt,
			status:         EntryStatusIdle,
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			// Shutdown happened during Factory call. Kill the
			// orphan sandbox; it is not in the entry map so
			// Shutdown's drain won't catch it.
			if kill != nil {
				go kill()
			}
			errs = append(errs, fmt.Errorf("sandbox pool: BatchSpawn slot %d: %w", i, ErrPoolClosed))
			return created, errs
		}
		p.entries[sandbox.SandboxID] = entry
		entryCount := len(p.entries)
		p.mu.Unlock()
		created = append(created, sandbox.SandboxID)
		p.logf("sandbox pool: batch-spawned sandbox=%s timeout=%ds total=%d",
			sandbox.SandboxID, timeoutSeconds, entryCount)
		p.emitOnSpawn(workspaceID, sandbox.SandboxID, sandbox.TemplateID, expiresAt, timeoutSeconds)
	}
	return created, errs
}

// ManualRenew is the admin-issued renew. Returns:
//   - ErrPoolClosed if Shutdown has run
//   - ErrRenewerNotConfigured if Renewer was nil at construction
//   - ErrEntryNotFound if the sandbox id is unknown
//   - the Renewer's error verbatim on failure (entry left alive;
//     admin can decide whether to retry or kill)
//   - nil on success
//
// On success persists the new expires_at and bumps last_renewed_at.
// Refuses to renew entries that are already claimed (the owner runs
// its own renew lifecycle from that point).
func (p *Pool) ManualRenew(ctx context.Context, sandboxID string) error {
	if p.renewer == nil {
		return ErrRenewerNotConfigured
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	entry, found := p.entries[sandboxID]
	if !found {
		p.mu.Unlock()
		return ErrEntryNotFound
	}
	if entry.status == EntryStatusClaimed {
		p.mu.Unlock()
		return fmt.Errorf("sandbox pool: cannot renew claimed entry %s", sandboxID)
	}
	if entry.renewing {
		p.mu.Unlock()
		return fmt.Errorf("sandbox pool: entry %s already mid-renew", sandboxID)
	}
	entry.renewing = true
	prevStatus := entry.status
	entry.status = EntryStatusRenewing
	timeoutSec := entry.timeoutSeconds
	p.mu.Unlock()

	err := p.renewer(ctx, sandboxID, timeoutSec)
	if err != nil {
		p.mu.Lock()
		if entry.status == EntryStatusRenewing {
			entry.status = prevStatus
		}
		entry.renewing = false
		p.mu.Unlock()
		return err
	}
	newExpires := time.Now().UTC().Add(time.Duration(timeoutSec) * time.Second)
	p.mu.Lock()
	if entry.status == EntryStatusRenewing {
		entry.status = prevStatus
	}
	entry.renewing = false
	entry.lastRenewedAt = time.Now().UTC()
	entry.expiresAt = newExpires
	p.mu.Unlock()
	p.logf("sandbox pool: manual-renewed sandbox=%s newExpires=%s", sandboxID, newExpires.Format(time.RFC3339))
	p.emitOnRenew(sandboxID, newExpires)
	return nil
}

// SetAutoRenewThreshold updates the per-entry auto-renew trigger.
// threshold == 0 disables auto-renew for the entry; > 0 sets the
// remaining-lifetime trigger in seconds. Returns ErrEntryNotFound
// for unknown ids. Persisted via OnSetAutoRenewThreshold.
//
// Setting threshold on a claimed entry is allowed — admin may want
// to pre-stage a value for after the entry is killed and a new one
// reuses the id (it won't reuse, but the API is symmetric).
func (p *Pool) SetAutoRenewThreshold(ctx context.Context, sandboxID string, thresholdSeconds int32) error {
	if thresholdSeconds < 0 {
		return fmt.Errorf("sandbox pool: SetAutoRenewThreshold thresholdSeconds must be >= 0, got %d", thresholdSeconds)
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	entry, found := p.entries[sandboxID]
	if !found {
		p.mu.Unlock()
		return ErrEntryNotFound
	}
	entry.autoRenewThresholdSeconds = thresholdSeconds
	p.mu.Unlock()
	p.logf("sandbox pool: set auto-renew threshold sandbox=%s threshold=%ds", sandboxID, thresholdSeconds)
	p.emitOnSetAutoRenewThreshold(sandboxID, thresholdSeconds)
	return nil
}

// Kill is the admin-initiated terminal teardown for one entry.
// Idempotent: a kill on an unknown / already-removed id returns
// ErrEntryNotFound but does NOT error if the entry is mid-renew
// (the kill goes through; the in-flight renewer call will return
// after the kill and its result is discarded by markAutoRenewDone).
//
// Kills a claimed entry too — admin override. After this the
// downstream runner's Close will idempotently no-op on the e2b side.
func (p *Pool) Kill(ctx context.Context, sandboxID string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrPoolClosed
	}
	entry, found := p.entries[sandboxID]
	if !found {
		p.mu.Unlock()
		return ErrEntryNotFound
	}
	delete(p.entries, sandboxID)
	kill := entry.kill
	p.mu.Unlock()
	if kill != nil {
		// Run kill in a goroutine so admin call returns quickly;
		// e2b Kill is idempotent so a parallel Shutdown drain
		// trying the same id is harmless.
		go kill()
	}
	p.logf("sandbox pool: killed sandbox=%s", sandboxID)
	p.emitOnTerminal(sandboxID, PersistenceStatusKilled)
	return nil
}

// Claim returns one idle sandbox. ok=false means the pool is empty
// of idle entries (or every entry is mid-Renew / already claimed) —
// caller should fall back to the full-spawn path. The claimed entry
// stays in the in-memory map with status='claimed' so admin UI keeps
// seeing it; the entry will be removed by an explicit Kill (admin or
// runner-Close path).
//
// Calling Claim after Shutdown returns ok=false (treat as fallback).
//
// Selects the entry with the EARLIEST expires_at — using up the
// sandboxes closest to expiry first so renew cycles cluster on
// entries that have the most remaining lifetime. Deterministic for
// tests via iteration order from the sort.
func (p *Pool) Claim(ctx context.Context, workspaceID, projectAgentID, cacheKey string) (sandbox e2bsandbox.Sandbox, kill func(), ok bool) {
	workspaceID = strings.TrimSpace(workspaceID)
	projectAgentID = strings.TrimSpace(projectAgentID)
	cacheKey = strings.TrimSpace(cacheKey)
	if workspaceID == "" || projectAgentID == "" || cacheKey == "" {
		return e2bsandbox.Sandbox{}, nil, false
	}
	p.mu.Lock()
	if p.closed || len(p.entries) == 0 {
		p.mu.Unlock()
		return e2bsandbox.Sandbox{}, nil, false
	}
	candidates := make([]*poolEntry, 0, len(p.entries))
	for _, e := range p.entries {
		if e.workspaceID != workspaceID {
			continue
		}
		if e.status != EntryStatusIdle {
			continue
		}
		if e.renewing {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		p.mu.Unlock()
		return e2bsandbox.Sandbox{}, nil, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].expiresAt.Before(candidates[j].expiresAt)
	})
	pick := candidates[0]
	pick.status = EntryStatusClaimed
	pickSandbox := pick.sandbox
	pickKill := pick.kill
	p.mu.Unlock()

	p.logf("sandbox pool: claimed sandbox=%s", pickSandbox.SandboxID)
	p.emitOnClaim(workspaceID, projectAgentID, cacheKey, pickSandbox.SandboxID)
	return pickSandbox, pickKill, true
}

// Shutdown closes the pool, stops the auto-renew goroutine, and kills
// every idle / renewing entry via its kill callback. Claimed entries
// are NOT killed by Shutdown — their downstream runners own kill
// responsibility from the moment Claim handed off. Idempotent;
// concurrent Shutdown calls all wait on the same loopDone channel.
//
// ctx is honoured for the wait on loopDone; if ctx expires before the
// scan goroutine drains, Shutdown returns ctx.Err() but the close
// signal has still been delivered and the goroutine will exit on its
// next select tick.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	wasStarted := p.started
	// Snapshot entries to drain. Drop claimed entries from the
	// drain list (their downstream owners will handle teardown).
	var toKill []*poolEntry
	for _, entry := range p.entries {
		if entry.status == EntryStatusClaimed {
			continue
		}
		toKill = append(toKill, entry)
	}
	// Wipe the map so Stats() reports zero post-shutdown.
	p.entries = make(map[string]*poolEntry)
	p.mu.Unlock()

	if wasStarted {
		close(p.shutdownCh)
		select {
		case <-p.loopDone:
		case <-ctx.Done():
			p.logf("sandbox pool: shutdown ctx expired before loop drained")
		}
	}

	for _, entry := range toKill {
		if entry.kill != nil {
			entry.kill()
		}
		p.emitOnTerminal(entry.sandbox.SandboxID, PersistenceStatusKilled)
	}
	p.logf("sandbox pool: shutdown complete drained=%d", len(toKill))
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// Stats returns a snapshot under the pool mutex. Cheap; safe to call
// from admin endpoints on every request.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := Stats{
		Started: p.started,
		Closed:  p.closed,
	}
	for _, e := range p.entries {
		switch e.status {
		case EntryStatusIdle:
			s.Idle++
		case EntryStatusRenewing:
			s.Renewing++
		case EntryStatusClaimed:
			s.Claimed++
		}
	}
	return s
}

// ListEntries returns the current entry snapshots, newest-first.
// Caller-side admin handler can either trust this (in-memory is
// authoritative when Persist == nil) or render the persistent
// listing alongside (Persist != nil). No pagination here — the
// admin handler paginates over the persistent listing instead.
func (p *Pool) ListEntries() []EntrySnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]EntrySnapshot, 0, len(p.entries))
	for _, e := range p.entries {
		out = append(out, EntrySnapshot{
			WorkspaceID:               e.workspaceID,
			SandboxID:                 e.sandbox.SandboxID,
			TemplateID:                e.templateID,
			Status:                    e.status,
			CreatedAt:                 e.createdAt,
			LastRenewedAt:             e.lastRenewedAt,
			ExpiresAt:                 e.expiresAt,
			TimeoutSeconds:            e.timeoutSeconds,
			AutoRenewThresholdSeconds: e.autoRenewThresholdSeconds,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// emitOnSpawn / emitOnRenew / emitOnClaim / emitOnTerminal /
// emitOnSetAutoRenewThreshold are fire-and-forget wrappers around
// the Persistence hook. Each runs in a fresh goroutine so a slow /
// failing backend does not block the caller; errors are logged at
// WARN level. Nil persist disables the hook entirely with no overhead.
func (p *Pool) emitOnSpawn(workspaceID, sandboxID, templateID string, expiresAt time.Time, timeoutSeconds int32) {
	if p.persist == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistOpTimeout)
		defer cancel()
		if err := p.persist.OnSpawn(ctx, workspaceID, sandboxID, templateID, expiresAt, timeoutSeconds); err != nil {
			p.logf("sandbox pool: persist OnSpawn failed sandbox=%s err=%v", sandboxID, err)
		}
	}()
}

func (p *Pool) emitOnRenew(sandboxID string, expiresAt time.Time) {
	if p.persist == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistOpTimeout)
		defer cancel()
		if err := p.persist.OnRenew(ctx, sandboxID, expiresAt); err != nil {
			p.logf("sandbox pool: persist OnRenew failed sandbox=%s err=%v", sandboxID, err)
		}
	}()
}

func (p *Pool) emitOnClaim(workspaceID, projectAgentID, cacheKey, sandboxID string) {
	if p.persist == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistOpTimeout)
		defer cancel()
		if err := p.persist.OnClaim(ctx, workspaceID, projectAgentID, cacheKey, sandboxID); err != nil {
			p.logf("sandbox pool: persist OnClaim failed sandbox=%s err=%v", sandboxID, err)
		}
	}()
}

func (p *Pool) emitOnTerminal(sandboxID, status string) {
	if p.persist == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistOpTimeout)
		defer cancel()
		if err := p.persist.OnTerminal(ctx, sandboxID, status); err != nil {
			p.logf("sandbox pool: persist OnTerminal failed sandbox=%s status=%s err=%v", sandboxID, status, err)
		}
	}()
}

func (p *Pool) emitOnSetAutoRenewThreshold(sandboxID string, thresholdSeconds int32) {
	if p.persist == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), persistOpTimeout)
		defer cancel()
		if err := p.persist.OnSetAutoRenewThreshold(ctx, sandboxID, thresholdSeconds); err != nil {
			p.logf("sandbox pool: persist OnSetAutoRenewThreshold failed sandbox=%s err=%v", sandboxID, err)
		}
	}()
}
