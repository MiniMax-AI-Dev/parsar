// Package sweeper keeps runtime liveness honest when a runner stops
// heartbeating without an explicit disconnect.
package sweeper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// Store is the one write the sweeper needs from the runtime store.
type Store interface {
	SweepStaleRuntimes(ctx context.Context, cutoff time.Time) (int64, error)
}

// Options configures one runtime heartbeat sweeper. A non-positive
// StaleAfter disables Run. Interval is floored to 15s so a bad config
// cannot spin the process.
type Options struct {
	StaleAfter time.Duration
	Interval   time.Duration
	Logger     *slog.Logger
	Clock      func() time.Time
}

// Sweeper periodically demotes online runtimes to offline when their
// last heartbeat is older than the configured cutoff.
type Sweeper struct {
	store Store
	opts  Options

	stats struct {
		mu     sync.Mutex
		ticks  int
		swept  int64
		errors int
	}
}

func New(store Store, opts Options) (*Sweeper, error) {
	if store == nil {
		return nil, errors.New("runtime sweeper: Store is nil")
	}
	if opts.Interval <= 0 && opts.StaleAfter > 0 {
		opts.Interval = opts.StaleAfter / 4
	}
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Second
	}
	if opts.Interval < 15*time.Second {
		opts.Interval = 15 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = obslog.Bg()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Sweeper{store: store, opts: opts}, nil
}

// Run blocks until ctx is cancelled. It runs an eager first tick so a
// restarted server cleans stale rows without waiting a full interval.
func (s *Sweeper) Run(ctx context.Context) error {
	if s.opts.StaleAfter <= 0 {
		s.opts.Logger.Info("runtime heartbeat sweeper disabled")
		return nil
	}
	s.opts.Logger.Info("runtime heartbeat sweeper start",
		slog.Duration("stale_after", s.opts.StaleAfter),
		slog.Duration("interval", s.opts.Interval),
	)
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()

	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.opts.Logger.Info("runtime heartbeat sweeper stop",
				slog.String("reason", ctx.Err().Error()),
			)
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Sweeper) tick(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	// Fresh trace per tick so all log lines under the store call group
	// under one trace_id.
	ctx, _ = obslog.StartBackgroundTrace(ctx, "runtime.sweeper.tick")
	cutoff := s.opts.Clock().UTC().Add(-s.opts.StaleAfter)
	swept, err := s.store.SweepStaleRuntimes(ctx, cutoff)
	if err != nil {
		s.opts.Logger.ErrorContext(ctx, "runtime heartbeat sweep failed",
			slog.String("err", err.Error()),
			slog.Time("cutoff", cutoff),
		)
		s.inc(0, true)
		return
	}
	if swept > 0 {
		s.opts.Logger.InfoContext(ctx, "runtime heartbeat sweep demoted stale runtimes",
			slog.Int64("count", swept),
			slog.Time("cutoff", cutoff),
		)
	}
	s.inc(swept, false)
}

func (s *Sweeper) inc(swept int64, hadError bool) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.ticks++
	s.stats.swept += swept
	if hadError {
		s.stats.errors++
	}
}

// Stats returns a snapshot for tests.
func (s *Sweeper) Stats() (ticks int, swept int64, errors int) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	return s.stats.ticks, s.stats.swept, s.stats.errors
}

func (s *Sweeper) String() string {
	return fmt.Sprintf("Sweeper{stale_after=%s interval=%s}", s.opts.StaleAfter, s.opts.Interval)
}
