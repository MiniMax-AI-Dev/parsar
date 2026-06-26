package transport

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// BackoffPolicy is the exponential-backoff schedule for redialing.
// Defaults: 1s → 2s → 4s → ... capped at 30s, with ±20% jitter so a
// fleet of restarting daemons doesn't thunder onto the gateway.
type BackoffPolicy struct {
	// Initial is the first delay after attempt 1 fails. Zero → 1s.
	Initial time.Duration
	// Max caps any single delay. Zero → 30s.
	Max time.Duration
	// Factor is the multiplier between delays. ≤1 → 2.0.
	Factor float64
	// JitterFraction is ±range as a fraction of the unjittered delay.
	// Zero disables jitter; default 0.2 gives ±20%.
	JitterFraction float64
}

// DefaultBackoff is the production policy.
var DefaultBackoff = BackoffPolicy{
	Initial:        1 * time.Second,
	Max:            30 * time.Second,
	Factor:         2.0,
	JitterFraction: 0.2,
}

// Delay returns the delay before attempt n (1-indexed: attempt 1 = the
// FIRST retry after the initial failure). The result is jittered and
// clamped to [0, Max].
func (p BackoffPolicy) Delay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if p.Initial <= 0 {
		p.Initial = 1 * time.Second
	}
	if p.Max <= 0 {
		p.Max = 30 * time.Second
	}
	if p.Factor <= 1 {
		p.Factor = 2.0
	}
	d := float64(p.Initial)
	for i := 1; i < attempt; i++ {
		d *= p.Factor
		if d >= float64(p.Max) {
			d = float64(p.Max)
			break
		}
	}
	if p.JitterFraction > 0 {
		// rand/v2 yields a uniform float in [0, 1); shift to [-1, 1).
		jitter := (rand.Float64()*2 - 1) * p.JitterFraction * d
		d += jitter
	}
	if d < 0 {
		return 0
	}
	if d > float64(p.Max) {
		d = float64(p.Max)
	}
	return time.Duration(d)
}

// Sleep blocks for d unless ctx fires first. Returns ctx.Err() on
// cancellation so callers can decide whether to bail out of the
// reconnect loop. Returns nil on normal expiry.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ErrPermanent is returned by a DialFn when the error must NOT be
// retried — e.g. 401 bad_credential or 426 incompatible_version.
var ErrPermanent = errors.New("transport: permanent error (do not retry)")

// DialFn is invoked once per attempt. Wrap with ErrPermanent on auth/
// protocol fatalities; any other error triggers backoff + retry.
type DialFn func(ctx context.Context) (*Conn, error)

// Reconnect loops on dial calls with exponential backoff. First attempt
// has no warm-up delay. onAttempt (if non-nil) is invoked before each
// attempt with the attempt index, last delay slept, and the previous
// dial error — surfacing lastErr lets the caller log root causes
// instead of hiding them behind retry bookkeeping.
func Reconnect(ctx context.Context, dial DialFn, policy BackoffPolicy, onAttempt func(attempt int, lastDelay time.Duration, lastErr error)) (*Conn, error) {
	var (
		lastDelay time.Duration
		lastErr   error
	)
	for attempt := 1; ; attempt++ {
		if onAttempt != nil {
			onAttempt(attempt, lastDelay, lastErr)
		}
		conn, err := dial(ctx)
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, ErrPermanent) {
			return nil, err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		lastErr = err
		lastDelay = policy.Delay(attempt)
		if sleepErr := Sleep(ctx, lastDelay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}
