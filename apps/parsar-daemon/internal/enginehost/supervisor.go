package enginehost

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// Supervisor keeps at most one resident engine server per spec key and
// shares it across prompts. Specs may also declare an exclusive state key;
// configuration variants for that state are replaced rather than overlapped.
// Safe for concurrent use.
//
// Concurrency model: one mutex guards the key map, every instance's lease
// count and every idle timer. Launching a server is slow (it waits for
// readiness), so a launch does NOT hold the mutex; instead the launching
// goroutine installs a pending entry that other Acquire calls for the
// same key wait on. Calls for different configuration keys that share one
// state key wait for the current leases to drain and for the old process to
// exit. That protects single-writer session stores from overlap.
type Supervisor struct {
	mu       sync.Mutex
	entries  map[string]*entry
	states   map[string]*entry
	logger   *slog.Logger
	stopping bool
}

// processSupervisor owns resident engine servers for the daemon process.
// Adapters use Acquire rather than creating per-engine supervisors so daemon
// teardown stays independent of the concrete engines that are registered.
var processSupervisor = NewSupervisor(nil)

// Acquire returns a lease from the process-wide engine server supervisor.
func Acquire(ctx context.Context, spec ServerSpec) (*Lease, error) {
	return processSupervisor.Acquire(ctx, spec)
}

// Shutdown stops every resident engine server owned by this daemon process.
func Shutdown() { processSupervisor.Shutdown() }

// entry is either a launch in flight or a live instance. ready is closed
// when the launch settles; inst and err are valid only after that.
type entry struct {
	key      string
	stateKey string
	ready    chan struct{}
	changed  chan struct{}
	inst     *instance
	err      error
	// retiring prevents a configuration variant that is being replaced from
	// accepting a new lease while its process is still shutting down.
	retiring bool

	// spec timings are captured at launch so a later Release reclaims
	// with the idle window the launching caller asked for.
	idleTimeout time.Duration
}

func NewSupervisor(logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = obslog.Bg()
	}
	return &Supervisor{
		entries: make(map[string]*entry),
		states:  make(map[string]*entry),
		logger:  logger,
	}
}

// Lease is a live claim on a resident engine server. BaseURL is valid
// until Release; the instance is not reclaimed while any lease is open.
type Lease struct {
	sup  *Supervisor
	key  string
	inst *instance
	once sync.Once
}

// BaseURL is the loopback origin of the engine server, e.g.
// "http://127.0.0.1:51234". It carries no trailing slash.
func (l *Lease) BaseURL() string {
	if l == nil || l.inst == nil {
		return ""
	}
	return l.inst.baseURL
}

// Exited is closed when the engine server process terminates. Adapters
// select on it so a crashed engine fails the run instead of hanging on a
// request that will never be answered.
func (l *Lease) Exited() <-chan struct{} {
	if l == nil || l.inst == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return l.inst.exited
}

// Diagnostics returns the retained output tail, for error messages.
func (l *Lease) Diagnostics() string {
	if l == nil || l.inst == nil {
		return ""
	}
	return l.inst.stderr.String()
}

// Release drops this claim. Idempotent. When it drops the last claim the
// instance stays warm for the spec's idle timeout, then is stopped.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() { l.sup.release(l.key, l.inst) })
}

// Acquire returns a lease on the server for spec.Key, launching it if
// there is no live one. A launch already in flight for the same key is
// awaited rather than duplicated.
func (s *Supervisor) Acquire(ctx context.Context, spec ServerSpec) (*Lease, error) {
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if spec.Logger == nil {
		spec.Logger = s.logger
	}
	for {
		lease, wait, err := s.tryAcquire(ctx, spec)
		if err != nil {
			return nil, err
		}
		if lease != nil {
			return lease, nil
		}
		if wait == nil {
			// The cached entry was dead or failed and has been evicted.
			// Retry immediately; the next pass launches a replacement.
			continue
		}
		// Another caller is launching this key, or a different configuration
		// still owns the same mutable state. Wait for that entry to change,
		// then retry under the lock.
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// tryAcquire performs one attempt. It returns exactly one of: a lease, a
// channel to wait on, an error, or all-nil meaning "a stale entry was
// evicted, retry immediately".
func (s *Supervisor) tryAcquire(ctx context.Context, spec ServerSpec) (*Lease, <-chan struct{}, error) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return nil, nil, context.Canceled
	}
	stateKey := strings.TrimSpace(spec.StateKey)
	if stateKey == "" {
		stateKey = spec.Key
	}
	if owner, ok := s.states[stateKey]; ok && owner.key != spec.Key {
		select {
		case <-owner.ready:
			if owner.err != nil || owner.inst == nil || !owner.inst.alive() {
				s.removeEntryLocked(owner)
				s.mu.Unlock()
				return nil, nil, nil
			}
			if owner.inst.leases > 0 {
				wait := owner.changed
				s.mu.Unlock()
				return nil, wait, nil
			}
			if owner.inst.idleTimer != nil {
				owner.inst.idleTimer.Stop()
				owner.inst.idleTimer = nil
			}
			owner.retiring = true
			wait := owner.inst.exited
			owner.inst.stop()
			s.mu.Unlock()
			return nil, wait, nil
		default:
			wait := owner.ready
			s.mu.Unlock()
			return nil, wait, nil
		}
	}
	if existing, ok := s.entries[spec.Key]; ok {
		select {
		case <-existing.ready:
			// Settled. A failed or dead instance is discarded here so the
			// caller's next pass launches a fresh one.
			if existing.err != nil || existing.inst == nil || !existing.inst.alive() {
				s.removeEntryLocked(existing)
				s.mu.Unlock()
				return nil, nil, nil
			}
			if existing.retiring {
				wait := existing.inst.exited
				s.mu.Unlock()
				return nil, wait, nil
			}
			lease := s.attachLocked(existing, spec.Key)
			s.mu.Unlock()
			return lease, nil, nil
		default:
			wait := existing.ready
			s.mu.Unlock()
			return nil, wait, nil
		}
	}

	e := &entry{
		key:         spec.Key,
		stateKey:    stateKey,
		ready:       make(chan struct{}),
		changed:     make(chan struct{}),
		idleTimeout: spec.idleTimeout(),
	}
	s.entries[spec.Key] = e
	s.states[stateKey] = e
	s.mu.Unlock()

	// Launch outside the lock. The pending entry is already published, so
	// concurrent Acquire calls for this key queue on e.ready.
	inst, err := newInstance(ctx, spec)

	s.mu.Lock()
	e.inst, e.err = inst, err
	close(e.ready)
	s.notifyChangedLocked(e)
	if err != nil || inst == nil {
		s.removeEntryLocked(e)
		s.mu.Unlock()
		return nil, nil, err
	}
	if s.stopping {
		s.removeEntryLocked(e)
		s.mu.Unlock()
		inst.stop()
		return nil, nil, context.Canceled
	}
	lease := s.attachLocked(e, spec.Key)
	s.mu.Unlock()
	return lease, nil, nil
}

// attachLocked adds a lease to a live instance. Caller holds s.mu.
func (s *Supervisor) attachLocked(e *entry, key string) *Lease {
	e.inst.leases++
	s.notifyChangedLocked(e)
	if e.inst.idleTimer != nil {
		e.inst.idleTimer.Stop()
		e.inst.idleTimer = nil
	}
	return &Lease{sup: s, key: key, inst: e.inst}
}

func (s *Supervisor) release(key string, inst *instance) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inst == nil {
		return
	}
	e, ok := s.entries[key]
	inst.leases--
	if inst.leases > 0 {
		if ok && e.inst == inst {
			s.notifyChangedLocked(e)
		}
		return
	}
	inst.leases = 0
	if !ok || e.inst != inst {
		// Already superseded or dropped; nothing keeps this process.
		inst.stop()
		return
	}
	s.notifyChangedLocked(e)
	if s.stopping || e.idleTimeout < 0 || !inst.alive() {
		s.removeEntryLocked(e)
		inst.stop()
		return
	}
	inst.idleTimer = time.AfterFunc(e.idleTimeout, func() { s.reclaim(key, inst) })
}

// reclaim stops an instance whose idle window expired, unless a lease was
// taken in the meantime.
func (s *Supervisor) reclaim(key string, inst *instance) {
	s.mu.Lock()
	e, ok := s.entries[key]
	if !ok || e.inst != inst || inst.leases > 0 {
		s.mu.Unlock()
		return
	}
	s.removeEntryLocked(e)
	inst.idleTimer = nil
	s.mu.Unlock()

	s.logger.Info("enginehost: reclaiming idle engine server", "key", key, "port", inst.port)
	inst.stop()
}

// Shutdown stops every resident server and rejects further Acquire calls.
// Used on daemon teardown so engine servers do not outlive the daemon.
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	s.stopping = true
	pending := make([]*entry, 0, len(s.entries))
	for _, e := range s.entries {
		pending = append(pending, e)
		if e.inst != nil && e.inst.idleTimer != nil {
			e.inst.idleTimer.Stop()
			e.inst.idleTimer = nil
		}
		s.removeEntryLocked(e)
	}
	s.mu.Unlock()

	for _, e := range pending {
		select {
		case <-e.ready:
			if e.inst != nil {
				e.inst.stop()
			}
		default:
			// A launch in flight observes s.stopping when it settles and
			// stops its own instance.
		}
	}
}

func (s *Supervisor) removeEntryLocked(e *entry) {
	if e == nil {
		return
	}
	if s.entries[e.key] == e {
		delete(s.entries, e.key)
	}
	if s.states[e.stateKey] == e {
		delete(s.states, e.stateKey)
	}
	s.notifyChangedLocked(e)
}

func (s *Supervisor) notifyChangedLocked(e *entry) {
	if e == nil || e.changed == nil {
		return
	}
	close(e.changed)
	e.changed = make(chan struct{})
}
