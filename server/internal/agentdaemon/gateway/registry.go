// Package gateway is the server-side hub of the agent_daemon connector.
// It owns the HTTP / WebSocket entry points the daemon dials in to,
// per-device long-lived WebSocket sessions, and a process-local
// registry of deviceID/runID/permID → Session for routing.
//
// The package deliberately does NOT depend on the connector
// implementation — the connector imports it, not the other way around.
package gateway

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrDeviceNotRegistered is returned by Registry lookups when a caller
// asks for a device the gateway has no live session for.
var ErrDeviceNotRegistered = errors.New("agentdaemon gateway: device not registered (offline / never connected)")

// ErrPermissionNotRegistered is returned when SubmitPermission arrives
// for a perm id we don't have a pending mapping for.
var ErrPermissionNotRegistered = errors.New("agentdaemon gateway: permission id not registered (expired / unknown)")

// ErrPromptForUserChoiceNotRegistered is returned when
// SubmitPromptForUserChoice arrives for an ask id we don't have a
// pending mapping for. Same race semantics as the permission variant
// (cancelled / expired / never seen).
var ErrPromptForUserChoiceNotRegistered = errors.New("agentdaemon gateway: prompt_for_user_choice id not registered (expired / unknown)")

// ErrWaitForDeviceTimeout is returned by WaitForDevice when the
// deadline expires before a daemon dials in.
var ErrWaitForDeviceTimeout = errors.New("agentdaemon gateway: timed out waiting for device to register")

// Registry is the process-wide map of live daemon sessions. It is
// concurrency-safe; readers and writers live in different goroutines.
// Four O(1) indexes are maintained: byDevice (primary), byRun (per
// Subscribe), byPerm (per permission_request), byAsk (per
// prompt_for_user_choice).
type Registry struct {
	mu       sync.RWMutex
	byDevice map[string]*Session
	byRun    map[string]*Session
	byPerm   map[string]*Session
	byAsk    map[string]*Session

	// waiters holds buffered(1) channels that WaitForDevice callers
	// are blocked on. Register drains the slice the moment a session
	// is inserted into byDevice. Buffered(1) so a Register that
	// happens between WaitForDevice registering and selecting on the
	// chan still wakes the waiter.
	waiters map[string][]chan *Session
}

// NewRegistry returns an empty registry. The zero value would also
// work but the constructor avoids accidental nil-map panics.
func NewRegistry() *Registry {
	return &Registry{
		byDevice: map[string]*Session{},
		byRun:    map[string]*Session{},
		byPerm:   map[string]*Session{},
		byAsk:    map[string]*Session{},
		waiters:  map[string][]chan *Session{},
	}
}

// Register adds a freshly-upgraded session under its deviceID. The
// latest dial-in wins: if a session was already registered for that
// device, the old one's run/perm indexes are evicted (the caller
// closes the displaced *Session).
func (r *Registry) Register(sess *Session) (previous *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous = r.byDevice[sess.DeviceID]
	if previous != nil && previous != sess {
		// Done under the registry lock so the new session doesn't
		// race against the old one's read loop on the way out.
		for runID, s := range r.byRun {
			if s == previous {
				delete(r.byRun, runID)
			}
		}
		for permID, s := range r.byPerm {
			if s == previous {
				delete(r.byPerm, permID)
			}
		}
		for askID, s := range r.byAsk {
			if s == previous {
				delete(r.byAsk, askID)
			}
		}
	}
	r.byDevice[sess.DeviceID] = sess

	// Signal waiters while still holding the registry lock so a
	// subsequent Deregister can't sneak in and clear byDevice before
	// the waiter resumes.
	if pending, ok := r.waiters[sess.DeviceID]; ok {
		for _, ch := range pending {
			// Non-blocking by construction — channels are buffered(1).
			select {
			case ch <- sess:
			default:
			}
		}
		delete(r.waiters, sess.DeviceID)
	}
	return previous
}

// Deregister removes a device's session if it matches the one currently
// registered. Caller passes the *Session pointer so a stale goroutine
// that wakes up after a reconnect can't evict the new session.
func (r *Registry) Deregister(sess *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.byDevice[sess.DeviceID]; ok && cur == sess {
		delete(r.byDevice, sess.DeviceID)
	}
	for runID, s := range r.byRun {
		if s == sess {
			delete(r.byRun, runID)
		}
	}
	for permID, s := range r.byPerm {
		if s == sess {
			delete(r.byPerm, permID)
		}
	}
	for askID, s := range r.byAsk {
		if s == sess {
			delete(r.byAsk, askID)
		}
	}
}

// LookupDevice returns the registered session for a device, or
// ErrDeviceNotRegistered when the device has never dialled in / has
// dropped.
func (r *Registry) LookupDevice(deviceID string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sess, ok := r.byDevice[deviceID]; ok {
		return sess, nil
	}
	return nil, ErrDeviceNotRegistered
}

// LookupRun returns the session currently handling a runID, or nil
// when the run has not been subscribed yet / has completed.
func (r *Registry) LookupRun(runID string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byRun[runID]
}

// AttachRun adds the runID -> session mapping. Idempotent.
func (r *Registry) AttachRun(runID string, sess *Session) {
	if runID == "" || sess == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byRun[runID] = sess
}

// DetachRun removes a runID -> session mapping.
func (r *Registry) DetachRun(runID string) {
	if runID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byRun, runID)
}

// AttachPermission records a permID -> session mapping so a later
// SubmitPermission can route the decision frame to the right device.
func (r *Registry) AttachPermission(permID string, sess *Session) {
	if permID == "" || sess == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byPerm[permID] = sess
}

// DetachPermission clears the permID -> session mapping. Idempotent.
func (r *Registry) DetachPermission(permID string) {
	if permID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byPerm, permID)
}

// LookupPermission returns the session that owns a pending permID,
// or ErrPermissionNotRegistered when the permission has expired /
// been cancelled.
func (r *Registry) LookupPermission(permID string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sess, ok := r.byPerm[permID]; ok {
		return sess, nil
	}
	return nil, ErrPermissionNotRegistered
}

// AttachPromptForUserChoice records askID → session so a later
// SubmitPromptForUserChoice can route the decision back to the right
// daemon. Mirrors AttachPermission.
func (r *Registry) AttachPromptForUserChoice(askID string, sess *Session) {
	if askID == "" || sess == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byAsk[askID] = sess
}

// DetachPromptForUserChoice clears the askID → session mapping.
// Idempotent.
func (r *Registry) DetachPromptForUserChoice(askID string) {
	if askID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byAsk, askID)
}

// LookupPromptForUserChoice returns the session that owns a pending
// askID, or ErrPromptForUserChoiceNotRegistered when the ask has
// expired or been cancelled.
func (r *Registry) LookupPromptForUserChoice(askID string) (*Session, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if sess, ok := r.byAsk[askID]; ok {
		return sess, nil
	}
	return nil, ErrPromptForUserChoiceNotRegistered
}

// Devices returns a snapshot of registered device ids in arbitrary order.
func (r *Registry) Devices() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byDevice))
	for id := range r.byDevice {
		out = append(out, id)
	}
	return out
}

// PendingWaiters returns the pending WaitForDevice waiter channels
// for a given deviceID. Diagnostics only.
func (r *Registry) PendingWaiters(deviceID string) []chan *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.waiters[deviceID]
}

// WaitForDevice blocks until a session for deviceID is registered, or
// until (timeout, ctx) bound expires. Returns the *Session on success.
//
// Fast path returns immediately if already registered. Slow path
// registers a buffered(1) channel under r.waiters[deviceID] which
// Register drains the moment a matching session arrives. timeout of
// 0 means "use the context deadline only".
func (r *Registry) WaitForDevice(ctx context.Context, deviceID string, timeout time.Duration) (*Session, error) {
	if deviceID == "" {
		return nil, ErrDeviceNotRegistered
	}

	r.mu.RLock()
	if sess, ok := r.byDevice[deviceID]; ok {
		r.mu.RUnlock()
		return sess, nil
	}
	r.mu.RUnlock()

	// Buffer = 1 so Register's non-blocking send always lands even
	// if we haven't yet entered the select below.
	waiter := make(chan *Session, 1)
	r.mu.Lock()
	// Double-check — Register could have landed between the RUnlock
	// above and the Lock here.
	if sess, ok := r.byDevice[deviceID]; ok {
		r.mu.Unlock()
		return sess, nil
	}
	r.waiters[deviceID] = append(r.waiters[deviceID], waiter)
	r.mu.Unlock()

	defer r.removeWaiter(deviceID, waiter)

	var timer *time.Timer
	var timerC <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case sess := <-waiter:
		if sess == nil {
			// Register never sends nil; defensive guard in case a
			// future refactor closes the channel cleanly.
			return nil, ErrDeviceNotRegistered
		}
		return sess, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timerC:
		return nil, ErrWaitForDeviceTimeout
	}
}

// removeWaiter purges a waiter channel from the pending list. Safe to
// call after Register has already drained the slice. Idempotent.
func (r *Registry) removeWaiter(deviceID string, ch chan *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending, ok := r.waiters[deviceID]
	if !ok {
		return
	}
	filtered := pending[:0]
	for _, c := range pending {
		if c != ch {
			filtered = append(filtered, c)
		}
	}
	if len(filtered) == 0 {
		delete(r.waiters, deviceID)
	} else {
		r.waiters[deviceID] = filtered
	}
}
