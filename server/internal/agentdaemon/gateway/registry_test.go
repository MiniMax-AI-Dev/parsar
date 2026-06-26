package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWaitForDevice_FastPath(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-warm", "wks-1", "0.1.0", reg, nil)
	reg.Register(sess)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	got, err := reg.WaitForDevice(ctx, "dev-warm", 0)
	if err != nil {
		t.Fatalf("WaitForDevice fast-path: %v", err)
	}
	if got != sess {
		t.Fatalf("WaitForDevice fast-path: wrong session %p, want %p", got, sess)
	}
}

func TestWaitForDevice_SignalledByRegister(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-cold", "wks-1", "0.1.0", reg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var got *Session
	var gotErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got, gotErr = reg.WaitForDevice(ctx, "dev-cold", 1*time.Second)
	}()

	// Give the goroutine a beat to register its waiter before
	// triggering Register. A truly deterministic version would expose
	// the waiter list size.
	time.Sleep(20 * time.Millisecond)
	reg.Register(sess)

	wg.Wait()
	if gotErr != nil {
		t.Fatalf("WaitForDevice signalled: %v", gotErr)
	}
	if got != sess {
		t.Fatalf("WaitForDevice signalled: wrong session %p", got)
	}
}

func TestWaitForDevice_Timeout(t *testing.T) {
	reg := NewRegistry()
	start := time.Now()
	_, err := reg.WaitForDevice(context.Background(), "dev-nobody", 50*time.Millisecond)
	if !errors.Is(err, ErrWaitForDeviceTimeout) {
		t.Fatalf("expected ErrWaitForDeviceTimeout, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("returned too quickly (elapsed %v); did the timer fire?", elapsed)
	}
}

// TestWaitForDevice_ContextCancel: a cancelled caller must clean up
// its waiter so a later Register doesn't leak the buffered channel
// into a permanently-detached pending list.
func TestWaitForDevice_ContextCancel(t *testing.T) {
	reg := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())

	var gotErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, gotErr = reg.WaitForDevice(ctx, "dev-cancelled", 0)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", gotErr)
	}

	reg.mu.Lock()
	pending, present := reg.waiters["dev-cancelled"]
	reg.mu.Unlock()
	if present && len(pending) != 0 {
		t.Fatalf("waiter not cleaned up after ctx cancel; pending=%d", len(pending))
	}
}

// TestWaitForDevice_EmptyDeviceID: empty deviceID must error instead
// of blocking forever on a key nobody could Register.
func TestWaitForDevice_EmptyDeviceID(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.WaitForDevice(context.Background(), "", 10*time.Millisecond)
	if !errors.Is(err, ErrDeviceNotRegistered) {
		t.Fatalf("expected ErrDeviceNotRegistered, got %v", err)
	}
}

// TestWaitForDevice_DoubleCheckAfterLock: a Register that lands
// between the RLock check and the write Lock must not leak a waiter
// entry. The race is hard to drive deterministically; this test checks
// the post-race shape.
func TestWaitForDevice_DoubleCheckAfterLock(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-race", "wks-1", "0.1.0", reg, nil)
	reg.Register(sess)

	got, err := reg.WaitForDevice(context.Background(), "dev-race", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForDevice after Register: %v", err)
	}
	if got != sess {
		t.Fatalf("got wrong session %p", got)
	}

	reg.mu.Lock()
	defer reg.mu.Unlock()
	if pending, ok := reg.waiters["dev-race"]; ok && len(pending) > 0 {
		t.Fatalf("leaked %d waiter(s) after fast-path return", len(pending))
	}
}

// TestWaitForDevice_MultipleWaitersSameDevice: two callers may both
// block on the same deviceID; when the device registers, both must
// unblock with the same session.
func TestWaitForDevice_MultipleWaitersSameDevice(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-shared", "wks-1", "0.1.0", reg, nil)

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []*Session{}

	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := reg.WaitForDevice(context.Background(), "dev-shared", 1*time.Second)
			if err != nil {
				t.Errorf("waiter saw error: %v", err)
				return
			}
			mu.Lock()
			results = append(results, got)
			mu.Unlock()
		}()
	}

	time.Sleep(30 * time.Millisecond)
	reg.Register(sess)
	wg.Wait()

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r != sess {
			t.Fatalf("result[%d] = %p, want %p", i, r, sess)
		}
	}
}
