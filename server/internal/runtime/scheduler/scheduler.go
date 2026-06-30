package scheduler

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

// Store is the narrow store surface the scheduler needs, so unit tests can
// drive the worker with a fake instead of a live *store.Store + Postgres.
type Store interface {
	ClaimDueScheduledTasks(ctx context.Context, now, staleBefore time.Time, claimedBy string, limit int32) ([]store.DueScheduledTask, error)
	FireScheduledTaskRun(ctx context.Context, taskID string, nextRunAt time.Time) (store.FireScheduledTaskResult, error)
}

type Options struct {
	Interval        time.Duration
	ClaimStaleAfter time.Duration
	ClaimBatch      int32
	ClaimedBy       string
	Logger          *slog.Logger
	Clock           func() time.Time
}

type Scheduler struct {
	store Store
	opts  Options
	stats struct {
		mu      sync.Mutex
		ticks   int
		fired   int
		skipped int
		errors  int
	}
}

func New(s Store, opts Options) (*Scheduler, error) {
	if s == nil {
		return nil, errors.New("scheduler: Store is nil")
	}
	if opts.Interval < 15*time.Second {
		opts.Interval = 15 * time.Second
	}
	if opts.ClaimStaleAfter <= 0 {
		opts.ClaimStaleAfter = 60 * time.Second
	}
	if opts.ClaimBatch <= 0 {
		opts.ClaimBatch = 50
	}
	if opts.ClaimedBy == "" {
		opts.ClaimedBy = "scheduler"
	}
	if opts.Logger == nil {
		opts.Logger = obslog.Bg()
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Scheduler{store: s, opts: opts}, nil
}

func (s *Scheduler) Run(ctx context.Context) error {
	s.opts.Logger.Info("scheduled task scheduler start",
		slog.Duration("interval", s.opts.Interval),
		slog.String("claimed_by", s.opts.ClaimedBy))
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			s.opts.Logger.Info("scheduled task scheduler stop", slog.String("reason", ctx.Err().Error()))
			return nil
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	ctx, _ = obslog.StartBackgroundTrace(ctx, "scheduler.tick")
	now := s.opts.Clock().UTC()
	staleBefore := now.Add(-s.opts.ClaimStaleAfter)
	due, err := s.store.ClaimDueScheduledTasks(ctx, now, staleBefore, s.opts.ClaimedBy, s.opts.ClaimBatch)
	if err != nil {
		s.opts.Logger.ErrorContext(ctx, "scheduler claim failed", slog.String("err", err.Error()))
		s.bump(0, 0, 1)
		return
	}
	var fired, skipped, errs int
	for _, t := range due {
		next, err := NextRun(t.CronExpr, t.Timezone, now)
		if err != nil {
			s.opts.Logger.ErrorContext(ctx, "scheduler next-run compute failed", slog.String("task_id", t.ID), slog.String("err", err.Error()))
			errs++
			continue
		}
		res, err := s.store.FireScheduledTaskRun(ctx, t.ID, next)
		if err != nil {
			s.opts.Logger.ErrorContext(ctx, "scheduler fire failed", slog.String("task_id", t.ID), slog.String("err", err.Error()))
			errs++
			continue
		}
		switch {
		case res.Disabled:
			s.opts.Logger.InfoContext(ctx, "scheduled task auto-disabled", slog.String("task_id", t.ID))
			skipped++
		case res.Skipped:
			s.opts.Logger.InfoContext(ctx, "scheduled task run skipped", slog.String("task_id", t.ID), slog.String("reason", res.SkipReason))
			skipped++
		default:
			fired++
		}
	}
	s.bump(fired, skipped, errs)
}

func (s *Scheduler) bump(fired, skipped, errs int) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	s.stats.ticks++
	s.stats.fired += fired
	s.stats.skipped += skipped
	s.stats.errors += errs
}

func (s *Scheduler) Stats() (ticks, fired, skipped, errors int) {
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()
	return s.stats.ticks, s.stats.fired, s.stats.skipped, s.stats.errors
}

func (s *Scheduler) String() string {
	return fmt.Sprintf("Scheduler{interval=%s claim_stale=%s batch=%d}", s.opts.Interval, s.opts.ClaimStaleAfter, s.opts.ClaimBatch)
}
