// Package sweeper releases persistent sandbox bindings whose
// last_active_at has aged past a configured TTL. One goroutine
// per server process — running two would race on the same idle
// rows.
package sweeper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type BindingLister interface {
	ListIdleSandboxBindings(ctx context.Context, idleBefore time.Time, limit int32) ([]store.SandboxBindingRead, error)
}

type BindingKiller interface {
	MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error
}

// RunnerKiller drops a runner only if its in-memory lastAcquired
// is older than cutoff. The provider serializes the check with
// Acquire under one mutex, so a racing Acquire returns killed=false.
//
// Returns:
//   - killed=true, err=nil: runner was idle and is now dropped;
//     caller SHOULD mark the DB row killed.
//   - killed=false, err=nil: runner was touched after cutoff;
//     caller MUST NOT mark killed and should TouchSandboxBinding
//     so the row drops out of the next idle list.
//   - killed=false, err≠nil: provider transient fault; caller
//     should log + skip.
type RunnerKiller interface {
	KillIfIdleByID(ctx context.Context, bindingID string, cutoff time.Time) (killed bool, err error)
}

type BindingToucher interface {
	TouchSandboxBinding(ctx context.Context, bindingID string) error
}

// Options configures one IdleSweeper. Zero-value fields fall
// back to defaults documented per field.
type Options struct {
	// IdleTTL is how long since last_active_at before a binding
	// is eligible for sweep. Zero/negative disables the sweeper
	// (Run exits immediately after logging).
	IdleTTL time.Duration
	// Interval defaults to IdleTTL/6, floored at 1 minute.
	Interval time.Duration
	// BatchLimit caps how many rows one tick processes.
	// Defaults to 100; hard-capped at 1000 inside New.
	BatchLimit int32
	Logger     *slog.Logger
	// Clock is the time source. Nil means time.Now.
	Clock func() time.Time
}

type IdleSweeper struct {
	bindings BindingLister
	killer   BindingKiller
	runner   RunnerKiller
	toucher  BindingToucher
	opts     Options

	// stats are exported for unit tests only.
	stats struct {
		mu                sync.Mutex
		ticks             int
		bindingsSwept     int
		bindingsKillError int
		bindingsSkipped   int
	}
}

// New validates dependencies and applies default options. Pass
// nil for any of bindings/killer/runner/toucher and you get an
// error.
func New(bindings BindingLister, killer BindingKiller, runner RunnerKiller, toucher BindingToucher, opts Options) (*IdleSweeper, error) {
	if bindings == nil {
		return nil, errors.New("sweeper: BindingLister is nil")
	}
	if killer == nil {
		return nil, errors.New("sweeper: BindingKiller is nil")
	}
	if runner == nil {
		return nil, errors.New("sweeper: RunnerKiller is nil")
	}
	if toucher == nil {
		return nil, errors.New("sweeper: BindingToucher is nil")
	}
	if opts.BatchLimit <= 0 {
		opts.BatchLimit = 100
	}
	if opts.BatchLimit > 1000 {
		opts.BatchLimit = 1000
	}
	if opts.Interval <= 0 {
		if opts.IdleTTL > 0 {
			opts.Interval = opts.IdleTTL / 6
		}
		if opts.Interval < time.Minute {
			opts.Interval = time.Minute
		}
	}
	if opts.Logger == nil {
		opts.Logger = obslog.Bg()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &IdleSweeper{
		bindings: bindings,
		killer:   killer,
		runner:   runner,
		toucher:  toucher,
		opts:     opts,
	}, nil
}

// Run blocks until ctx is cancelled. IdleTTL ≤ 0 returns
// immediately after logging.
func (s *IdleSweeper) Run(ctx context.Context) error {
	if s.opts.IdleTTL <= 0 {
		s.opts.Logger.Info("sandbox idle sweeper disabled (TTL ≤ 0)")
		return nil
	}
	s.opts.Logger.Info("sandbox idle sweeper start",
		slog.Duration("ttl", s.opts.IdleTTL),
		slog.Duration("interval", s.opts.Interval),
		slog.Int("batch_limit", int(s.opts.BatchLimit)),
	)
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	// Eager first tick so a freshly-restarted server doesn't
	// wait a full interval before reaping startup-orphaned
	// idle rows that SweepOrphanedSandboxBindings did not catch.
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.opts.Logger.Info("sandbox idle sweeper stop",
				slog.String("reason", ctx.Err().Error()),
			)
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *IdleSweeper) tick(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	// Fresh trace_id per tick so listing + per-row logs group under one grep.
	ctx, _ = obslog.StartBackgroundTrace(ctx, "sandbox.idle_sweeper.tick")
	now := s.opts.Clock().UTC()
	cutoff := now.Add(-s.opts.IdleTTL)
	rows, err := s.bindings.ListIdleSandboxBindings(ctx, cutoff, s.opts.BatchLimit)
	if err != nil {
		s.opts.Logger.ErrorContext(ctx, "sandbox idle sweeper list failed",
			slog.String("err", err.Error()),
			slog.Time("cutoff", cutoff),
		)
		s.incTicks()
		return
	}
	if len(rows) == 0 {
		s.incTicks()
		return
	}
	s.opts.Logger.InfoContext(ctx, "sandbox idle sweeper picked up bindings",
		slog.Int("count", len(rows)),
		slog.Time("cutoff", cutoff),
	)
	for _, row := range rows {
		s.killOne(ctx, row, cutoff)
	}
	s.incTicks()
}

func (s *IdleSweeper) killOne(ctx context.Context, row store.SandboxBindingRead, cutoff time.Time) {
	killed, killErr := s.runner.KillIfIdleByID(ctx, row.ID, cutoff)
	if killErr != nil {
		// Transient fault — do NOT mark DB killed; next tick retries.
		s.opts.Logger.WarnContext(ctx, "sandbox idle sweeper runner KillIfIdleByID failed",
			slog.String("binding_id", row.ID),
			slog.String("sandbox_id", row.SandboxID),
			slog.String("err", killErr.Error()),
		)
		s.stats.mu.Lock()
		s.stats.bindingsKillError++
		s.stats.mu.Unlock()
		return
	}
	if !killed {
		// Runner is still in use — bump DB last_active_at so the row
		// drops out of the next tick's candidate set instead of being
		// re-selected and re-skipped forever.
		if touchErr := s.toucher.TouchSandboxBinding(ctx, row.ID); touchErr != nil {
			s.opts.Logger.WarnContext(ctx, "sandbox idle sweeper DB touch on skip failed",
				slog.String("binding_id", row.ID),
				slog.String("err", touchErr.Error()),
			)
		}
		s.opts.Logger.InfoContext(ctx, "sandbox idle sweeper skipped binding (still in use)",
			slog.String("binding_id", row.ID),
			slog.String("sandbox_id", row.SandboxID),
		)
		s.stats.mu.Lock()
		s.stats.bindingsSkipped++
		s.stats.mu.Unlock()
		return
	}
	terminalStatus := store.SandboxBindingStatusKilled
	if err := s.killer.MarkSandboxBindingKilled(ctx, row.ID, terminalStatus); err != nil {
		s.opts.Logger.ErrorContext(ctx, "sandbox idle sweeper mark killed failed",
			slog.String("binding_id", row.ID),
			slog.String("sandbox_id", row.SandboxID),
			slog.String("status", terminalStatus),
			slog.String("err", err.Error()),
		)
		return
	}
	s.opts.Logger.InfoContext(ctx, "sandbox idle sweeper killed binding",
		slog.String("binding_id", row.ID),
		slog.String("sandbox_id", row.SandboxID),
		slog.String("workspace_id", row.WorkspaceID),
		slog.Any("agent_id", row.AgentID),
		slog.String("status", terminalStatus),
		slog.Time("last_active_at", row.LastActiveAt),
	)
	s.stats.mu.Lock()
	s.stats.bindingsSwept++
	s.stats.mu.Unlock()
}

func (s *IdleSweeper) incTicks() {
	s.stats.mu.Lock()
	s.stats.ticks++
	s.stats.mu.Unlock()
}

// Stats returns a snapshot of internal counters. Tests only.
func (s *IdleSweeper) Stats() (ticks, swept, killErrors, skipped int) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	return s.stats.ticks, s.stats.bindingsSwept, s.stats.bindingsKillError, s.stats.bindingsSkipped
}

func (s *IdleSweeper) String() string {
	return fmt.Sprintf("IdleSweeper{ttl=%s interval=%s batch=%d}",
		s.opts.IdleTTL, s.opts.Interval, s.opts.BatchLimit)
}
