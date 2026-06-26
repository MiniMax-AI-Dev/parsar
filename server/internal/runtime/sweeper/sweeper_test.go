package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	cutoffs []time.Time
	swept   int64
	err     error
}

func (f *fakeStore) SweepStaleRuntimes(ctx context.Context, cutoff time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.err != nil {
		return 0, f.err
	}
	return f.swept, nil
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestNew_Defaults(t *testing.T) {
	s, err := New(&fakeStore{}, Options{StaleAfter: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.opts.Interval != 15*time.Second {
		t.Fatalf("default interval = %s, want 15s", s.opts.Interval)
	}
	if s.opts.Clock == nil {
		t.Fatal("Clock default is nil")
	}
}

func TestNew_RejectsNilStore(t *testing.T) {
	if _, err := New(nil, Options{}); err == nil {
		t.Fatal("nil store: want error, got nil")
	}
}

func TestRun_DisabledWhenStaleAfterNonPositive(t *testing.T) {
	fs := &fakeStore{}
	s, err := New(fs, Options{StaleAfter: 0, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fs.cutoffs) != 0 {
		t.Fatalf("disabled sweeper called store %d times, want 0", len(fs.cutoffs))
	}
}

func TestTick_UsesCutoffAndCountsSweptRows(t *testing.T) {
	fixed := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	fs := &fakeStore{swept: 3}
	s, err := New(fs, Options{
		StaleAfter: time.Minute,
		Clock:      func() time.Time { return fixed },
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())
	wantCutoff := fixed.Add(-time.Minute)
	if len(fs.cutoffs) != 1 || !fs.cutoffs[0].Equal(wantCutoff) {
		t.Fatalf("cutoffs = %v, want [%s]", fs.cutoffs, wantCutoff)
	}
	ticks, swept, errs := s.Stats()
	if ticks != 1 || swept != 3 || errs != 0 {
		t.Fatalf("Stats = ticks:%d swept:%d errors:%d, want 1/3/0", ticks, swept, errs)
	}
}

func TestTick_ErrorCountsAndContinues(t *testing.T) {
	fs := &fakeStore{err: errors.New("db down")}
	s, err := New(fs, Options{StaleAfter: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())
	ticks, swept, errs := s.Stats()
	if ticks != 1 || swept != 0 || errs != 1 {
		t.Fatalf("Stats = ticks:%d swept:%d errors:%d, want 1/0/1", ticks, swept, errs)
	}
}

func TestRun_EagerFirstTickThenContextCancel(t *testing.T) {
	fs := &fakeStore{swept: 1}
	s, err := New(fs, Options{
		StaleAfter: time.Minute,
		Interval:   time.Hour,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ticks, swept, errs := s.Stats()
	if ticks != 1 || swept != 1 || errs != 0 {
		t.Fatalf("eager tick Stats = ticks:%d swept:%d errors:%d, want 1/1/0", ticks, swept, errs)
	}
}

func TestNew_IntervalFloor(t *testing.T) {
	s, err := New(&fakeStore{}, Options{
		StaleAfter: 20 * time.Second,
		Logger:     quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.opts.Interval != 15*time.Second {
		t.Fatalf("interval floor = %s, want 15s", s.opts.Interval)
	}
}
