package transport_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
)

func TestBackoffPolicyDelayGrowsExponentiallyAndCaps(t *testing.T) {
	// No jitter for deterministic assertion.
	p := transport.BackoffPolicy{Initial: time.Second, Max: 10 * time.Second, Factor: 2.0}
	want := []time.Duration{
		1 * time.Second,  // attempt 1
		2 * time.Second,  // attempt 2
		4 * time.Second,  // attempt 3
		8 * time.Second,  // attempt 4
		10 * time.Second, // attempt 5 (capped)
		10 * time.Second, // attempt 6 (stays capped)
	}
	for i, w := range want {
		got := p.Delay(i + 1)
		if got != w {
			t.Errorf("Delay(attempt=%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestBackoffPolicyJitterStaysWithinFraction(t *testing.T) {
	p := transport.BackoffPolicy{Initial: time.Second, Max: 30 * time.Second, Factor: 2.0, JitterFraction: 0.2}
	// Attempt 4 unjittered = 8s. ±20% means [6.4s, 9.6s].
	for range 100 {
		got := p.Delay(4)
		if got < (6400*time.Millisecond) || got > (9600*time.Millisecond) {
			t.Fatalf("Delay(4) = %v, want in [6.4s, 9.6s]", got)
		}
	}
}

func TestBackoffPolicyZeroDefaultsAreSensible(t *testing.T) {
	// All-zero policy must still produce a positive delay.
	p := transport.BackoffPolicy{}
	got := p.Delay(1)
	if got <= 0 || got > 2*time.Second {
		t.Errorf("default policy Delay(1) = %v, want roughly 1s ± jitter", got)
	}
}

func TestSleepReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transport.Sleep(ctx, 1*time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("Sleep returned %v, want context.Canceled", err)
	}
}

func TestSleepNoOpForZeroDuration(t *testing.T) {
	start := time.Now()
	if err := transport.Sleep(context.Background(), 0); err != nil {
		t.Errorf("Sleep(0): %v", err)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("Sleep(0) took %v, want <50ms", elapsed)
	}
}

func TestReconnectReturnsOnFirstSuccess(t *testing.T) {
	calls := 0
	dial := func(_ context.Context) (*transport.Conn, error) {
		calls++
		return nil, nil // success path
	}
	conn, err := transport.Reconnect(context.Background(), dial, transport.BackoffPolicy{Initial: time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if conn != nil {
		t.Errorf("expected nil Conn from stub dial, got %v", conn)
	}
	if calls != 1 {
		t.Errorf("dial called %d times, want 1", calls)
	}
}

func TestReconnectBailsOnPermanentError(t *testing.T) {
	calls := 0
	dial := func(_ context.Context) (*transport.Conn, error) {
		calls++
		return nil, fmt.Errorf("auth dead: %w", transport.ErrPermanent)
	}
	_, err := transport.Reconnect(context.Background(), dial, transport.BackoffPolicy{Initial: time.Millisecond}, nil)
	if !errors.Is(err, transport.ErrPermanent) {
		t.Errorf("Reconnect err = %v, want ErrPermanent chain", err)
	}
	if calls != 1 {
		t.Errorf("dial called %d times on permanent err, want 1 (no retry)", calls)
	}
}

func TestReconnectRetriesUntilSuccess(t *testing.T) {
	calls := 0
	dial := func(_ context.Context) (*transport.Conn, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("flaky")
		}
		return nil, nil
	}
	var seenAttempts []int
	var seenErrs []error
	_, err := transport.Reconnect(
		context.Background(), dial,
		transport.BackoffPolicy{Initial: time.Millisecond, Max: time.Millisecond, Factor: 2.0},
		func(attempt int, _ time.Duration, lastErr error) {
			seenAttempts = append(seenAttempts, attempt)
			seenErrs = append(seenErrs, lastErr)
		},
	)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if calls != 3 {
		t.Errorf("dial called %d times, want 3", calls)
	}
	if got, want := seenAttempts, []int{1, 2, 3}; !equalInts(got, want) {
		t.Errorf("onAttempt sequence = %v, want %v", got, want)
	}
	// First callback has no prior error; subsequent ones see the
	// "flaky" error so the daemon log can show WHY it's retrying.
	if seenErrs[0] != nil {
		t.Errorf("onAttempt[0] lastErr = %v, want nil on first attempt", seenErrs[0])
	}
	for i := 1; i < len(seenErrs); i++ {
		if seenErrs[i] == nil || seenErrs[i].Error() != "flaky" {
			t.Errorf("onAttempt[%d] lastErr = %v, want %q", i, seenErrs[i], "flaky")
		}
	}
}

func TestReconnectHonoursContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(_ context.Context) (*transport.Conn, error) {
		cancel()
		return nil, errors.New("transient")
	}
	_, err := transport.Reconnect(ctx, dial, transport.BackoffPolicy{Initial: time.Hour}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Reconnect err = %v, want context.Canceled", err)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
