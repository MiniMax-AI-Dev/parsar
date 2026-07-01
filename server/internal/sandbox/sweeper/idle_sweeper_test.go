package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeBindings struct {
	mu          sync.Mutex
	rows        []store.SandboxBindingRead
	cutoffs     []time.Time
	listErr     error
	listErrOnce bool // when true, listErr applies to only the first call
}

func (f *fakeBindings) ListIdleSandboxBindings(ctx context.Context, cutoff time.Time, limit int32) ([]store.SandboxBindingRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.listErr != nil {
		err := f.listErr
		if f.listErrOnce {
			f.listErr = nil
		}
		return nil, err
	}
	return f.rows, nil
}

type fakeKiller struct {
	mu        sync.Mutex
	calls     []killCall
	markErr   error
	markErrOn map[string]error // per-binding-id error injection
}

type killCall struct {
	BindingID string
	Status    string
}

func strPtr(s string) *string { return &s }

func (f *fakeKiller) MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, killCall{BindingID: bindingID, Status: status})
	if perKey, ok := f.markErrOn[bindingID]; ok {
		return perKey
	}
	return f.markErr
}

type fakeRunner struct {
	mu           sync.Mutex
	calls        []runnerCall
	killedIfIdle map[string]runnerResult
	defResult    runnerResult
	callCnt      atomic.Int32
}

type runnerCall struct {
	BindingID string
	Cutoff    time.Time
}

type runnerResult struct {
	Killed bool
	Err    error
}

func (f *fakeRunner) KillIfIdleByID(ctx context.Context, bindingID string, cutoff time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, runnerCall{BindingID: bindingID, Cutoff: cutoff})
	f.callCnt.Add(1)
	if res, ok := f.killedIfIdle[bindingID]; ok {
		return res.Killed, res.Err
	}
	return f.defResult.Killed, f.defResult.Err
}

// fakeToucher records DB touch calls — the sweeper invokes this
// when KillIfIdleByID says the runner is still in use.
type fakeToucher struct {
	mu       sync.Mutex
	calls    []string
	touchErr error
}

func (f *fakeToucher) TouchSandboxBinding(ctx context.Context, bindingID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, bindingID)
	return f.touchErr
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestNew_Defaults(t *testing.T) {
	bl := &fakeBindings{}
	kl := &fakeKiller{}
	rk := &fakeRunner{defResult: runnerResult{Killed: true}}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: 30 * time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.opts.BatchLimit != 100 {
		t.Errorf("BatchLimit default: got %d want 100", s.opts.BatchLimit)
	}
	if s.opts.Interval < time.Minute {
		t.Errorf("Interval floor: got %s want >= 1m", s.opts.Interval)
	}
	if s.opts.Clock == nil {
		t.Error("Clock default: got nil want time.Now")
	}
}

func TestNew_RejectsNilDeps(t *testing.T) {
	bl := &fakeBindings{}
	kl := &fakeKiller{}
	rk := &fakeRunner{}
	tc := &fakeToucher{}

	if _, err := New(nil, kl, rk, tc, Options{}); err == nil {
		t.Error("nil BindingLister: want error, got nil")
	}
	if _, err := New(bl, nil, rk, tc, Options{}); err == nil {
		t.Error("nil BindingKiller: want error, got nil")
	}
	if _, err := New(bl, kl, nil, tc, Options{}); err == nil {
		t.Error("nil RunnerKiller: want error, got nil")
	}
	if _, err := New(bl, kl, rk, nil, Options{}); err == nil {
		t.Error("nil BindingToucher: want error, got nil")
	}
}

func TestNew_ClampsBatchLimit(t *testing.T) {
	s, err := New(&fakeBindings{}, &fakeKiller{}, &fakeRunner{}, &fakeToucher{}, Options{
		IdleTTL: time.Minute, BatchLimit: 999999, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.opts.BatchLimit != 1000 {
		t.Errorf("BatchLimit clamp: got %d want 1000", s.opts.BatchLimit)
	}
}

func TestRun_DisabledOnNonPositiveTTL(t *testing.T) {
	bl := &fakeBindings{}
	kl := &fakeKiller{}
	rk := &fakeRunner{}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: 0, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Errorf("Run with TTL=0: got %v want nil", err)
	}
	if len(bl.cutoffs) != 0 {
		t.Errorf("disabled sweeper called List %d times, want 0", len(bl.cutoffs))
	}
}

func TestTick_CleanKill(t *testing.T) {
	row := store.SandboxBindingRead{
		ID:           "binding-1",
		WorkspaceID:  "ws-1",
		AgentID:      strPtr("pa-1"),
		SandboxID:    "sbx-aaa",
		TemplateID:   "tpl-base",
		Status:       store.SandboxBindingStatusActive,
		LastActiveAt: time.Now().Add(-time.Hour),
	}
	bl := &fakeBindings{rows: []store.SandboxBindingRead{row}}
	kl := &fakeKiller{}
	rk := &fakeRunner{defResult: runnerResult{Killed: true}}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{
		IdleTTL: 30 * time.Minute, Interval: 24 * time.Hour, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())

	if len(rk.calls) != 1 || rk.calls[0].BindingID != row.ID {
		t.Errorf("runner.KillIfIdleByID calls: got %v want [{id=%s}]", rk.calls, row.ID)
	}
	if len(kl.calls) != 1 || kl.calls[0].BindingID != row.ID {
		t.Fatalf("killer calls: got %v want [{ID=%s ...}]", kl.calls, row.ID)
	}
	if kl.calls[0].Status != store.SandboxBindingStatusKilled {
		t.Errorf("terminal status on clean kill: got %s want %s",
			kl.calls[0].Status, store.SandboxBindingStatusKilled)
	}
	if len(tc.calls) != 0 {
		t.Errorf("clean kill should NOT touch DB; got %d touch calls", len(tc.calls))
	}
	ticks, swept, killErrs, skipped := s.Stats()
	if ticks != 1 || swept != 1 || killErrs != 0 || skipped != 0 {
		t.Errorf("Stats clean kill: ticks=%d swept=%d killErrs=%d skipped=%d want 1/1/0/0", ticks, swept, killErrs, skipped)
	}
}

func TestTick_RunnerInUse_SkipsAndTouchesDB(t *testing.T) {
	// TOCTOU: prompt is using the runner. KillIfIdleByID returns
	// (false, nil); sweeper must NOT mark DB killed and must touch
	// DB so the same row isn't re-selected forever.
	row := store.SandboxBindingRead{
		ID: "binding-inuse", SandboxID: "sbx-inuse",
		Status: store.SandboxBindingStatusActive,
	}
	bl := &fakeBindings{rows: []store.SandboxBindingRead{row}}
	kl := &fakeKiller{}
	rk := &fakeRunner{killedIfIdle: map[string]runnerResult{"binding-inuse": {Killed: false, Err: nil}}}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())

	if len(kl.calls) != 0 {
		t.Errorf("in-use path MUST NOT mark DB killed; got %d calls (%v)", len(kl.calls), kl.calls)
	}
	if len(tc.calls) != 1 || tc.calls[0] != row.ID {
		t.Errorf("in-use path MUST touch DB to bump last_active_at; got %v want [%s]", tc.calls, row.ID)
	}
	_, swept, killErrs, skipped := s.Stats()
	if swept != 0 || killErrs != 0 || skipped != 1 {
		t.Errorf("Stats in-use: swept=%d killErrs=%d skipped=%d want 0/0/1", swept, killErrs, skipped)
	}
}

func TestTick_RunnerProviderError_LogsAndSkips(t *testing.T) {
	// Transient provider err — must NOT mark DB killed; next tick retries.
	row := store.SandboxBindingRead{
		ID: "binding-err", SandboxID: "sbx-err",
		Status: store.SandboxBindingStatusActive,
	}
	bl := &fakeBindings{rows: []store.SandboxBindingRead{row}}
	kl := &fakeKiller{}
	rk := &fakeRunner{killedIfIdle: map[string]runnerResult{"binding-err": {Killed: false, Err: errors.New("provider transient")}}}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())

	if len(kl.calls) != 0 {
		t.Errorf("provider error MUST NOT mark DB killed (retryable next tick); got %v", kl.calls)
	}
	if len(tc.calls) != 0 {
		t.Errorf("provider error MUST NOT bump DB last_active_at; got %v", tc.calls)
	}
	_, swept, killErrs, skipped := s.Stats()
	if swept != 0 || killErrs != 1 || skipped != 0 {
		t.Errorf("Stats provider err: swept=%d killErrs=%d skipped=%d want 0/1/0", swept, killErrs, skipped)
	}
}

func TestTick_ListErrorSkipsTickButCountsIt(t *testing.T) {
	bl := &fakeBindings{listErr: errors.New("conn refused")}
	kl := &fakeKiller{}
	rk := &fakeRunner{}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())

	if len(kl.calls) != 0 {
		t.Errorf("on List error: killer.MarkSandboxBindingKilled called %d times, want 0", len(kl.calls))
	}
	if rk.callCnt.Load() != 0 {
		t.Errorf("on List error: runner.KillIfIdleByID called %d times, want 0", rk.callCnt.Load())
	}
	ticks, _, _, _ := s.Stats()
	if ticks != 1 {
		t.Errorf("tick counter: got %d want 1 (still counts a failed tick)", ticks)
	}
}

func TestTick_NoRowsIsQuiet(t *testing.T) {
	bl := &fakeBindings{rows: nil}
	kl := &fakeKiller{}
	rk := &fakeRunner{}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{IdleTTL: time.Minute, Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())
	if len(kl.calls)+len(rk.calls)+len(tc.calls) != 0 {
		t.Errorf("empty list should not call any dep; got killer=%d runner=%d touch=%d",
			len(kl.calls), len(rk.calls), len(tc.calls))
	}
}

func TestTick_CutoffEqualsNowMinusTTL_AndIsPassedToProvider(t *testing.T) {
	// The cutoff the sweeper computes MUST be forwarded verbatim to
	// runner.KillIfIdleByID, otherwise the provider's in-memory check
	// (lastAcquired < cutoff) drifts from the DB list filter and races
	// re-appear.
	fixed := time.Date(2026, 5, 28, 15, 0, 0, 0, time.UTC)
	row := store.SandboxBindingRead{ID: "b-cutoff"}
	bl := &fakeBindings{rows: []store.SandboxBindingRead{row}}
	rk := &fakeRunner{defResult: runnerResult{Killed: true}}
	s, err := New(bl, &fakeKiller{}, rk, &fakeToucher{}, Options{
		IdleTTL: 30 * time.Minute,
		Clock:   func() time.Time { return fixed },
		Logger:  quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.tick(context.Background())
	wantCutoff := fixed.Add(-30 * time.Minute)
	if len(bl.cutoffs) != 1 || !bl.cutoffs[0].Equal(wantCutoff) {
		t.Errorf("List cutoff: got %v want [%s]", bl.cutoffs, wantCutoff)
	}
	if len(rk.calls) != 1 || !rk.calls[0].Cutoff.Equal(wantCutoff) {
		t.Errorf("provider cutoff: got %v want %s", rk.calls, wantCutoff)
	}
}

func TestRun_EagerFirstTickThenContextCancel(t *testing.T) {
	row := store.SandboxBindingRead{ID: "b-eager"}
	bl := &fakeBindings{rows: []store.SandboxBindingRead{row}}
	kl := &fakeKiller{}
	rk := &fakeRunner{defResult: runnerResult{Killed: true}}
	tc := &fakeToucher{}
	s, err := New(bl, kl, rk, tc, Options{
		IdleTTL:  time.Minute,
		Interval: time.Hour, // ensure only the eager tick fires
		Logger:   quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ticks, swept, _, _ := s.Stats()
	if ticks != 1 || swept != 1 {
		t.Errorf("eager first tick: ticks=%d swept=%d want 1/1", ticks, swept)
	}
}

func TestNew_DefaultIntervalAtLeastOneMinute(t *testing.T) {
	// 30s TTL gives interval=5s without the floor — the floor
	// prevents a misconfiguration from spinning the goroutine.
	s, err := New(&fakeBindings{}, &fakeKiller{}, &fakeRunner{}, &fakeToucher{}, Options{
		IdleTTL: 30 * time.Second, Logger: quietLogger(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.opts.Interval < time.Minute {
		t.Errorf("Interval floor: got %s want >= 1m for TTL=30s", s.opts.Interval)
	}
}
