package codex

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type lifecycleFixture struct {
	mu     sync.Mutex
	states map[string]connectionObservation
	calls  []connectionObservation
}

func (f *lifecycleFixture) replace(ctx context.Context, tenant, environment, generation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states[environment].generation != generation {
		value := connectionObservation{tenant: tenant, environment: environment, generation: generation}
		f.states[environment] = value
		f.calls = append(f.calls, value)
	}
	return nil
}

func (f *lifecycleFixture) observe(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	value := connectionObservation{tenant, environment, generation, revision, connected}
	f.calls = append(f.calls, value)
	previous := f.states[environment]
	if previous.generation == generation && previous.revision < revision {
		f.states[environment] = value
	}
	return nil
}

func (f *lifecycleFixture) state(environment string) connectionObservation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[environment]
}

func awaitLifecycle(t *testing.T, f fixture, environment, generation string, revision int64, connected bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value := f.lifecycle.state(environment)
		if value.generation == generation && value.revision == revision && value.connected == connected {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("lifecycle did not converge", f.lifecycle.state(environment))
}

func awaitLifecycleSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("lifecycle operation did not complete")
	}
}

func TestConnectionLifecycleRequiresBothCallbacks(t *testing.T) {
	f := newFixture(t)
	config := Config{Store: f.source, CheckOwnership: f.source.owner, PublicURL: f.server.URL}
	if _, err := New(config); err == nil {
		t.Fatal("missing lifecycle callbacks accepted")
	}
	config.ReplaceConnection = f.lifecycle.replace
	if _, err := New(config); err == nil {
		t.Fatal("missing observation callback accepted")
	}
	config.ReplaceConnection = nil
	config.ObserveConnection = f.lifecycle.observe
	if _, err := New(config); err == nil {
		t.Fatal("missing replacement callback accepted")
	}
}

func TestConnectionLifecycleTracksSocketReconnectAndShutdown(t *testing.T) {
	f := newFixture(t)
	key := f.keys[0]
	reg := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 0, false)
	executor := dial(t, reg.URL)
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 1, true)
	executor.Close()
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 2, false)
	executor = dial(t, reg.URL)
	defer executor.Close()
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 3, true)
	f.registry.Close()
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 4, false)
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	if len(f.lifecycle.calls) != 5 {
		t.Fatal("socket cleanup emitted duplicate observations", f.lifecycle.calls)
	}
}

func TestConnectionLifecycleReplacementFencesDelayedLoss(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	f := newFixture(t, func(config *Config) {
		observe := config.ObserveConnection
		config.ObserveConnection = func(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
			blocked := false
			if !connected {
				once.Do(func() {
					blocked = true
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
				})
			}
			err := observe(ctx, tenant, environment, generation, revision, connected)
			if blocked {
				close(finished)
			}
			return err
		}
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	key := f.keys[0]
	first := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	executor := dial(t, first.URL)
	awaitLifecycle(t, f, key.EnvironmentID, first.ExecutorRegistrationID, 1, true)
	executor.Close()
	awaitLifecycleSignal(t, entered)
	next := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	successor := dial(t, next.URL)
	defer successor.Close()
	awaitLifecycle(t, f, key.EnvironmentID, next.ExecutorRegistrationID, 1, true)
	releaseOnce.Do(func() { close(release) })
	awaitLifecycleSignal(t, finished)
	awaitLifecycle(t, f, key.EnvironmentID, next.ExecutorRegistrationID, 1, true)
	awaitPresence(t, f, key.EnvironmentID, true)
}

func TestConnectionLifecycleCloseDrainsOutOfOrderWrites(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	f := newFixture(t, func(config *Config) {
		observe := config.ObserveConnection
		config.ObserveConnection = func(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
			if connected {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return observe(ctx, tenant, environment, generation, revision, connected)
		}
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	key := f.keys[0]
	reg := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	executor := dial(t, reg.URL)
	defer executor.Close()
	awaitLifecycleSignal(t, entered)
	closed := make(chan struct{})
	go func() { f.registry.Close(); close(closed) }()
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 2, false)
	select {
	case <-closed:
		t.Fatal("Close returned while an accepted observation was still in flight")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	awaitLifecycleSignal(t, closed)
	awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 2, false)
	expectClosed(t, executor)
}

func TestConnectionLifecycleReplacementWritesOutsideRegistryMutex(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	var releaseOnce sync.Once
	f := newFixture(t, func(config *Config) {
		replace := config.ReplaceConnection
		config.ReplaceConnection = func(ctx context.Context, tenant, environment, generation string) error {
			if armed.Swap(false) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return replace(ctx, tenant, environment, generation)
		}
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	key := f.keys[0]
	first := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	executor := dial(t, first.URL)
	defer executor.Close()
	awaitLifecycle(t, f, key.EnvironmentID, first.ExecutorRegistrationID, 1, true)
	armed.Store(true)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 503)
	}()
	awaitLifecycleSignal(t, entered)
	closed := make(chan struct{})
	go func() { f.registry.Close(); close(closed) }()
	awaitLifecycle(t, f, key.EnvironmentID, first.ExecutorRegistrationID, 2, false)
	select {
	case <-closed:
		t.Fatal("Close did not retain the accepted replacement")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	awaitLifecycleSignal(t, finished)
	awaitLifecycleSignal(t, closed)
	f.registry.mu.Lock()
	defer f.registry.mu.Unlock()
	if len(f.registry.registrations) != 0 {
		t.Fatal("late replacement reopened a closed registry")
	}
}

func TestConnectionLifecycleFailureRetiresTargetOrClosesRegistry(t *testing.T) {
	for _, failure := range []error{store.ErrNotFound, store.ErrInvalidInput, errors.New("observation storage unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			var armed atomic.Bool
			failed := make(chan struct{})
			f := newFixture(t, func(config *Config) {
				observe := config.ObserveConnection
				config.ObserveConnection = func(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
					if connected && armed.Swap(false) {
						defer close(failed)
						return failure
					}
					return observe(ctx, tenant, environment, generation, revision, connected)
				}
			})
			other := f.keys[1]
			first := f.register(t, other.EnvironmentID, f.tokens[1], nativeRequest(), 200)
			healthy := dial(t, first.URL)
			defer healthy.Close()
			awaitLifecycle(t, f, other.EnvironmentID, first.ExecutorRegistrationID, 1, true)
			key := f.keys[0]
			reg := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
			armed.Store(true)
			executor := dial(t, reg.URL)
			defer executor.Close()
			awaitLifecycleSignal(t, failed)
			expectClosed(t, executor)
			wantClosed := !errors.Is(failure, store.ErrNotFound) && !errors.Is(failure, store.ErrInvalidInput)
			f.registry.mu.Lock()
			closed := f.registry.closed
			_, retained := f.registry.registrations[key.EnvironmentID]
			f.registry.mu.Unlock()
			if closed != wantClosed || retained {
				t.Fatal("lifecycle failure did not close the expected scope", closed, retained)
			}
			if wantClosed {
				expectClosed(t, healthy)
				if !errors.Is(f.registry.LifecycleError(), failure) {
					t.Fatal("persistence failure was not retained")
				}
			} else {
				connected, err := f.registry.Connected(t.Context(), other.TenantID, other.EnvironmentID)
				if err != nil || !connected || f.registry.LifecycleError() != nil {
					t.Fatal("target retirement disabled another Environment", err)
				}
			}
			f.registry.Close()
		})
	}
}

func TestConnectionLifecycleReplaceFailureDoesNotPublish(t *testing.T) {
	for _, failure := range []error{store.ErrNotFound, store.ErrInvalidInput, errors.New("replacement storage unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			var armed atomic.Bool
			f := newFixture(t, func(config *Config) {
				replace := config.ReplaceConnection
				config.ReplaceConnection = func(ctx context.Context, tenant, environment, generation string) error {
					if armed.Swap(false) {
						return failure
					}
					return replace(ctx, tenant, environment, generation)
				}
			})
			key := f.keys[0]
			first := f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 200)
			executor := dial(t, first.URL)
			defer executor.Close()
			awaitLifecycle(t, f, key.EnvironmentID, first.ExecutorRegistrationID, 1, true)
			armed.Store(true)
			f.register(t, key.EnvironmentID, f.tokens[0], nativeRequest(), 503)
			expectClosed(t, executor)
			f.registry.mu.Lock()
			_, retained := f.registry.registrations[key.EnvironmentID]
			f.registry.mu.Unlock()
			if retained {
				t.Fatal("failed replacement left its predecessor or an unpublished successor usable")
			}
			f.registry.Close()
		})
	}
}

func TestConnectionLifecycleShutdownSharesOneDeadline(t *testing.T) {
	var mu sync.Mutex
	var deadlines []time.Time
	f := newFixture(t, func(config *Config) {
		observe := config.ObserveConnection
		config.ObserveConnection = func(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
			if !connected {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("shutdown observation has no deadline")
				}
				mu.Lock()
				deadlines = append(deadlines, deadline)
				mu.Unlock()
			}
			return observe(ctx, tenant, environment, generation, revision, connected)
		}
	})
	for i, key := range f.keys {
		reg := f.register(t, key.EnvironmentID, f.tokens[i], nativeRequest(), 200)
		executor := dial(t, reg.URL)
		defer executor.Close()
		awaitLifecycle(t, f, key.EnvironmentID, reg.ExecutorRegistrationID, 1, true)
	}
	f.registry.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(deadlines) != 2 || !deadlines[0].Equal(deadlines[1]) {
		t.Fatal("shutdown granted a fresh timeout to each Environment", deadlines)
	}
}
