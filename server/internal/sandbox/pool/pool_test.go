package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	e2bsandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// fakeFactory tracks Create calls and lets a test inject either a
// success path (returning a deterministic sandbox + a counted kill
// callback) or an error path. failures=N means the first N calls
// return errors before successes start.
type fakeFactory struct {
	mu             sync.Mutex
	calls          int
	killCnt        *int32 // shared across all sandboxes returned
	failures       int    // number of leading errors before success
	lastTimeoutArg int32  // captures last factory call's timeout arg
	timeoutArgs    []int32
}

func (f *fakeFactory) Factory() SandboxFactory {
	return func(ctx context.Context, timeoutSeconds int32) (e2bsandbox.Sandbox, func(), error) {
		f.mu.Lock()
		f.calls++
		callIdx := f.calls
		failNow := f.failures > 0
		if failNow {
			f.failures--
		}
		f.lastTimeoutArg = timeoutSeconds
		f.timeoutArgs = append(f.timeoutArgs, timeoutSeconds)
		f.mu.Unlock()
		if failNow {
			return e2bsandbox.Sandbox{}, nil, errors.New("fake factory: induced error")
		}
		sandbox := e2bsandbox.Sandbox{
			SandboxID:  fmt.Sprintf("sbx-%d", callIdx),
			TemplateID: "blank",
		}
		killCnt := f.killCnt
		kill := func() {
			if killCnt != nil {
				atomic.AddInt32(killCnt, 1)
			}
		}
		return sandbox, kill, nil
	}
}

// callCount returns the synchronized call counter.
func (f *fakeFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeRenewer records every Renew call by sandbox id; injectable
// failure list lets tests pin error paths.
type fakeRenewer struct {
	mu          sync.Mutex
	calls       map[string]int
	failOnce    map[string]bool
	timeoutArgs map[string][]int32
}

func newFakeRenewer() *fakeRenewer {
	return &fakeRenewer{
		calls:       map[string]int{},
		failOnce:    map[string]bool{},
		timeoutArgs: map[string][]int32{},
	}
}

func (f *fakeRenewer) Renewer() SandboxRenewer {
	return func(_ context.Context, sandboxID string, timeoutSeconds int32) error {
		f.mu.Lock()
		f.calls[sandboxID]++
		f.timeoutArgs[sandboxID] = append(f.timeoutArgs[sandboxID], timeoutSeconds)
		fail := f.failOnce[sandboxID]
		if fail {
			delete(f.failOnce, sandboxID)
		}
		f.mu.Unlock()
		if fail {
			return errors.New("fake renewer: induced failure")
		}
		return nil
	}
}

func (f *fakeRenewer) callCountFor(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[id]
}

// fakePersistence records every callback for assertion in pool tests.
type fakePersistence struct {
	mu                  sync.Mutex
	spawn               map[string]int
	spawnExpiresAt      map[string]time.Time
	spawnTimeoutSec     map[string]int32
	renew               map[string]int
	renewExpiresAt      map[string]time.Time
	claim               map[string]int
	terminal            map[string][]string
	autoRenewThresholds map[string][]int32
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{
		spawn:               map[string]int{},
		spawnExpiresAt:      map[string]time.Time{},
		spawnTimeoutSec:     map[string]int32{},
		renew:               map[string]int{},
		renewExpiresAt:      map[string]time.Time{},
		claim:               map[string]int{},
		terminal:            map[string][]string{},
		autoRenewThresholds: map[string][]int32{},
	}
}

func (f *fakePersistence) OnSpawn(_ context.Context, _ string, sandboxID, _ string, expiresAt time.Time, timeoutSeconds int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawn[sandboxID]++
	f.spawnExpiresAt[sandboxID] = expiresAt
	f.spawnTimeoutSec[sandboxID] = timeoutSeconds
	return nil
}

func (f *fakePersistence) OnRenew(_ context.Context, sandboxID string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renew[sandboxID]++
	f.renewExpiresAt[sandboxID] = expiresAt
	return nil
}

func (f *fakePersistence) OnClaim(_ context.Context, _, _, _, sandboxID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claim[sandboxID]++
	return nil
}

func (f *fakePersistence) OnTerminal(_ context.Context, sandboxID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminal[sandboxID] = append(f.terminal[sandboxID], status)
	return nil
}

func (f *fakePersistence) OnSetAutoRenewThreshold(_ context.Context, sandboxID string, thresholdSeconds int32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoRenewThresholds[sandboxID] = append(f.autoRenewThresholds[sandboxID], thresholdSeconds)
	return nil
}

func (f *fakePersistence) spawnCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawn[id]
}

func (f *fakePersistence) renewCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renew[id]
}

func (f *fakePersistence) claimCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claim[id]
}

func (f *fakePersistence) terminalCount(id, status string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := 0
	for _, s := range f.terminal[id] {
		if s == status {
			c++
		}
	}
	return c
}

// waitForCondition polls f every 10ms up to deadline, returns true
// if f ever returns true. Used to bridge between the
// fire-and-forget persistence goroutines and assertion code.
func waitForCondition(f func() bool, deadline time.Duration) bool {
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		if f() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f()
}

// TestNewRejectsBadOptions pins construction-time validation: Factory
// is required; Renewer and Persist are optional.
func TestNewRejectsBadOptions(t *testing.T) {
	t.Parallel()
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected error when Factory is nil")
	}
	if _, err := New(Options{Factory: (&fakeFactory{}).Factory()}); err != nil {
		t.Fatalf("Factory alone should suffice: %v", err)
	}
}

// TestBatchSpawnCreatesEntries pins the admin batch-spawn happy path:
// N successful Factory calls produce N idle entries, the persist hook
// receives one OnSpawn per id with the correct expires_at + timeout,
// and the factory sees the per-batch timeout in every call.
func TestBatchSpawnCreatesEntries(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	pp := newFakePersistence()
	p, err := New(Options{Factory: ff.Factory(), Persist: pp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, errs := p.BatchSpawn(context.Background(), "ws-a", 3, 1800)
	if len(errs) > 0 {
		t.Fatalf("BatchSpawn errs: %v", errs)
	}
	if len(created) != 3 {
		t.Fatalf("created=%d, want 3", len(created))
	}
	s := p.Stats()
	if s.Idle != 3 || s.Total() != 3 {
		t.Errorf("stats=%+v, want Idle=3 Total=3", s)
	}

	// Factory received the batch timeout on each call.
	for i, got := range ff.timeoutArgs {
		if got != 1800 {
			t.Errorf("factory call %d timeout=%d, want 1800", i, got)
		}
	}
	// Persist OnSpawn fired for each id (fire-and-forget — wait briefly).
	for _, id := range created {
		if !waitForCondition(func() bool { return pp.spawnCount(id) == 1 }, time.Second) {
			t.Errorf("OnSpawn never observed for %s", id)
		}
		if got := pp.spawnTimeoutSec[id]; got != 1800 {
			t.Errorf("OnSpawn timeout=%d for %s, want 1800", got, id)
		}
	}
}

// TestBatchSpawnPartialSuccess pins the partial-success contract: a
// Factory error on slot i does NOT abort the batch — the surviving
// slots still get created and the caller gets a per-slot error.
func TestBatchSpawnPartialSuccess(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{failures: 1} // first call errors, rest succeed
	p, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, errs := p.BatchSpawn(context.Background(), "ws-a", 3, 600)
	if len(created) != 2 {
		t.Errorf("created=%d, want 2 (one factory failure)", len(created))
	}
	if len(errs) != 1 {
		t.Errorf("errs=%d, want 1 (one failure surfaced)", len(errs))
	}
	if ff.callCount() != 3 {
		t.Errorf("factory calls=%d, want 3 (batch didn't abort)", ff.callCount())
	}
}

// TestBatchSpawnRejectsBadInputs pins input validation.
func TestBatchSpawnRejectsBadInputs(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	p, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	if _, errs := p.BatchSpawn(context.Background(), "ws-a", 0, 600); len(errs) != 1 {
		t.Errorf("count=0 should error, got errs=%v", errs)
	}
	if _, errs := p.BatchSpawn(context.Background(), "ws-a", 1, 0); len(errs) != 1 {
		t.Errorf("timeout=0 should error, got errs=%v", errs)
	}
	if ff.callCount() != 0 {
		t.Errorf("factory called=%d on rejected inputs, want 0", ff.callCount())
	}
}

// TestManualRenewSuccess pins ManualRenew: renewer called with the
// entry's timeout, expires_at rolls forward, persist OnRenew fires.
func TestManualRenewSuccess(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	fr := newFakeRenewer()
	pp := newFakePersistence()
	p, err := New(Options{Factory: ff.Factory(), Renewer: fr.Renewer(), Persist: pp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 600)
	id := created[0]
	if err := p.ManualRenew(context.Background(), id); err != nil {
		t.Fatalf("ManualRenew: %v", err)
	}
	if fr.callCountFor(id) != 1 {
		t.Errorf("renewer calls=%d, want 1", fr.callCountFor(id))
	}
	if !waitForCondition(func() bool { return pp.renewCount(id) == 1 }, time.Second) {
		t.Error("OnRenew never observed")
	}
}

// TestManualRenewErrorPaths pins the three failure modes:
// not-found, claimed, renewer-no-renewer-configured.
func TestManualRenewErrorPaths(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	// Pool A: no renewer configured.
	pA, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New A: %v", err)
	}
	if err := pA.ManualRenew(context.Background(), "sbx-anything"); !errors.Is(err, ErrRenewerNotConfigured) {
		t.Errorf("expected ErrRenewerNotConfigured, got %v", err)
	}

	// Pool B: renewer configured.
	fr := newFakeRenewer()
	pB, err := New(Options{Factory: ff.Factory(), Renewer: fr.Renewer()})
	if err != nil {
		t.Fatalf("New B: %v", err)
	}
	if err := pB.Start(context.Background()); err != nil {
		t.Fatalf("Start B: %v", err)
	}
	defer func() { _ = pB.Shutdown(context.Background()) }()

	if err := pB.ManualRenew(context.Background(), "sbx-missing"); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}

	// Claim then attempt renew on claimed entry.
	created, _ := pB.BatchSpawn(context.Background(), "ws-a", 1, 600)
	id := created[0]
	if _, _, ok := pB.Claim(context.Background(), "ws-a", "pa-a", "cache-a"); !ok {
		t.Fatal("Claim should have succeeded")
	}
	if err := pB.ManualRenew(context.Background(), id); err == nil {
		t.Error("ManualRenew on claimed entry should error")
	}
}

// TestSetAutoRenewThreshold pins the admin PATCH path: in-memory
// entry receives the threshold + persist OnSetAutoRenewThreshold
// fires.
func TestSetAutoRenewThreshold(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	pp := newFakePersistence()
	p, err := New(Options{Factory: ff.Factory(), Persist: pp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 600)
	id := created[0]
	if err := p.SetAutoRenewThreshold(context.Background(), id, 120); err != nil {
		t.Fatalf("SetAutoRenewThreshold: %v", err)
	}
	if !waitForCondition(func() bool {
		pp.mu.Lock()
		defer pp.mu.Unlock()
		return len(pp.autoRenewThresholds[id]) == 1 && pp.autoRenewThresholds[id][0] == 120
	}, time.Second) {
		t.Error("OnSetAutoRenewThreshold never observed with value 120")
	}
	// Negative threshold rejected.
	if err := p.SetAutoRenewThreshold(context.Background(), id, -1); err == nil {
		t.Error("expected error on negative threshold")
	}
	// Missing entry returns ErrEntryNotFound.
	if err := p.SetAutoRenewThreshold(context.Background(), "sbx-missing", 60); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
}

// TestAutoRenewGoroutineTriggers pins the auto-renew scan: when the
// entry's remaining lifetime drops below its threshold, the scan
// calls Renewer and rolls expiry forward. Uses a very short scan
// interval + tiny timeout so the test stays under 1s.
func TestAutoRenewGoroutineTriggers(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	fr := newFakeRenewer()
	p, err := New(Options{
		Factory:               ff.Factory(),
		Renewer:               fr.Renewer(),
		AutoRenewScanInterval: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	// 2-second timeout — well above the scan window — so the auto-renew
	// trigger fires because the *threshold* is 5s, not because the entry
	// is genuinely close to expiry. ManualRenew on a freshly-spawned
	// entry with timeout=2s leaves expires_at ≈ now+2s; threshold=5s
	// means "renew when remaining <= 5s" which is always true.
	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 2)
	id := created[0]
	if err := p.SetAutoRenewThreshold(context.Background(), id, 5); err != nil {
		t.Fatalf("SetAutoRenewThreshold: %v", err)
	}

	// Wait for at least one auto-renew tick to land.
	if !waitForCondition(func() bool { return fr.callCountFor(id) >= 1 }, time.Second) {
		t.Errorf("auto-renew never fired; renewer.calls=%d", fr.callCountFor(id))
	}
}

// TestAutoRenewSkipsClaimedEntries pins that claimed entries are
// NOT auto-renewed — the downstream runner owns renewal from claim.
func TestAutoRenewSkipsClaimedEntries(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	fr := newFakeRenewer()
	p, err := New(Options{
		Factory:               ff.Factory(),
		Renewer:               fr.Renewer(),
		AutoRenewScanInterval: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 2)
	id := created[0]
	if err := p.SetAutoRenewThreshold(context.Background(), id, 5); err != nil {
		t.Fatalf("SetAutoRenewThreshold: %v", err)
	}
	if _, _, ok := p.Claim(context.Background(), "ws-a", "pa-a", "cache-a"); !ok {
		t.Fatal("Claim should have succeeded")
	}

	// Wait through several scan ticks; renewer must NOT have been
	// called because the entry is claimed.
	time.Sleep(150 * time.Millisecond)
	if fr.callCountFor(id) != 0 {
		t.Errorf("auto-renew fired on claimed entry; calls=%d, want 0", fr.callCountFor(id))
	}
}

// TestClaimPicksEarliestExpiry pins the Claim ordering: earliest
// expires_at wins so renew cycles cluster on younger entries.
func TestClaimPicksEarliestExpiry(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	p, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	// Spawn 2 batches with different timeouts; the smaller timeout
	// gives the entry an earlier expires_at.
	earlyBatch, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 60)
	lateBatch, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 600)
	if len(earlyBatch) != 1 || len(lateBatch) != 1 {
		t.Fatal("expected 2 entries spawned")
	}
	earlyID := earlyBatch[0]

	sandbox, _, ok := p.Claim(context.Background(), "ws-a", "pa-a", "cache-a")
	if !ok {
		t.Fatal("Claim should have succeeded")
	}
	if sandbox.SandboxID != earlyID {
		t.Errorf("Claim picked %s, want earliest=%s", sandbox.SandboxID, earlyID)
	}
}

// TestClaimMarksClaimedKeepsEntry: claim does NOT remove the entry —
// status flips to 'claimed' and persist OnClaim fires (NOT OnTerminal).
func TestClaimMarksClaimedKeepsEntry(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	pp := newFakePersistence()
	p, err := New(Options{Factory: ff.Factory(), Persist: pp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 60)
	id := created[0]
	if _, _, ok := p.Claim(context.Background(), "ws-a", "pa-a", "cache-a"); !ok {
		t.Fatal("Claim should have succeeded")
	}

	s := p.Stats()
	if s.Idle != 0 || s.Claimed != 1 || s.Total() != 1 {
		t.Errorf("post-claim stats=%+v, want Idle=0 Claimed=1 Total=1", s)
	}
	if !waitForCondition(func() bool { return pp.claimCount(id) == 1 }, time.Second) {
		t.Error("OnClaim never observed")
	}
	if got := pp.terminalCount(id, PersistenceStatusKilled); got != 0 {
		t.Errorf("OnTerminal('killed') fired %d times on claim, want 0", got)
	}
}

// TestKillRemovesEntry pins the admin kill: kill cb fires, entry
// leaves the map, OnTerminal records the kill.
func TestKillRemovesEntry(t *testing.T) {
	t.Parallel()
	var killCnt int32
	ff := &fakeFactory{killCnt: &killCnt}
	pp := newFakePersistence()
	p, err := New(Options{Factory: ff.Factory(), Persist: pp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 60)
	id := created[0]
	if err := p.Kill(context.Background(), id); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if !waitForCondition(func() bool { return atomic.LoadInt32(&killCnt) == 1 }, time.Second) {
		t.Errorf("kill cb never invoked; killCnt=%d", atomic.LoadInt32(&killCnt))
	}
	if !waitForCondition(func() bool { return pp.terminalCount(id, PersistenceStatusKilled) == 1 }, time.Second) {
		t.Error("OnTerminal('killed') never observed")
	}
	if s := p.Stats(); s.Total() != 0 {
		t.Errorf("post-kill stats=%+v, want Total=0", s)
	}
	// Kill on missing id is ErrEntryNotFound.
	if err := p.Kill(context.Background(), "sbx-gone"); !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
}

// TestShutdownDrainsIdleSkipsClaimed pins the Shutdown invariant:
// idle/renewing entries are killed; claimed entries are left alone
// (downstream runner owns them).
func TestShutdownDrainsIdleSkipsClaimed(t *testing.T) {
	t.Parallel()
	var killCnt int32
	ff := &fakeFactory{killCnt: &killCnt}
	p, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 3, 60)
	if len(created) != 3 {
		t.Fatalf("created=%d, want 3", len(created))
	}
	// Claim one; the kill cb count it carries should NOT be invoked
	// at Shutdown (the runner owns it now).
	if _, _, ok := p.Claim(context.Background(), "ws-a", "pa-a", "cache-a"); !ok {
		t.Fatal("Claim should have succeeded")
	}

	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// 2 idle entries should have been killed; 1 claimed entry left alone.
	if got := atomic.LoadInt32(&killCnt); got != 2 {
		t.Errorf("kill cb invoked %d times, want 2 (Shutdown drained idle, skipped claimed)", got)
	}
}

// TestAutoRenewFailureKeepsEntry: a renew failure logs but does NOT
// evict + kill the entry; admin sees the failure via the next read.
func TestAutoRenewFailureKeepsEntry(t *testing.T) {
	t.Parallel()
	var killCnt int32
	ff := &fakeFactory{killCnt: &killCnt}
	fr := newFakeRenewer()
	p, err := New(Options{
		Factory:               ff.Factory(),
		Renewer:               fr.Renewer(),
		AutoRenewScanInterval: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	created, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 2)
	id := created[0]
	fr.mu.Lock()
	fr.failOnce[id] = true
	fr.mu.Unlock()
	if err := p.SetAutoRenewThreshold(context.Background(), id, 5); err != nil {
		t.Fatalf("SetAutoRenewThreshold: %v", err)
	}

	// Wait through the failed auto-renew tick.
	if !waitForCondition(func() bool { return fr.callCountFor(id) >= 1 }, time.Second) {
		t.Fatalf("auto-renew never fired; calls=%d", fr.callCountFor(id))
	}
	// Entry must still be in the map (and idle for the next attempt).
	time.Sleep(20 * time.Millisecond)
	if s := p.Stats(); s.Total() != 1 || s.Idle != 1 {
		t.Errorf("post-renew-failure stats=%+v, want Total=1 Idle=1 (no evict)", s)
	}
	if atomic.LoadInt32(&killCnt) != 0 {
		t.Errorf("kill cb invoked %d times on renew failure, want 0 (no evict)", atomic.LoadInt32(&killCnt))
	}
}

// TestListEntriesReturnsSnapshot pins the ListEntries shape: every
// in-memory entry surfaces with its lifecycle fields, sorted
// newest-first. Used by the admin UI when in-memory is authoritative.
func TestListEntriesReturnsSnapshot(t *testing.T) {
	t.Parallel()
	ff := &fakeFactory{}
	p, err := New(Options{Factory: ff.Factory()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	first, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 600)
	time.Sleep(10 * time.Millisecond)
	second, _ := p.BatchSpawn(context.Background(), "ws-a", 1, 600)
	if len(first) != 1 || len(second) != 1 {
		t.Fatal("BatchSpawn missing")
	}
	entries := p.ListEntries()
	if len(entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(entries))
	}
	// Newest first.
	if entries[0].SandboxID != second[0] {
		t.Errorf("newest-first broken: first=%s want=%s", entries[0].SandboxID, second[0])
	}
	for _, e := range entries {
		if e.Status != EntryStatusIdle {
			t.Errorf("entry %s status=%q want %q", e.SandboxID, e.Status, EntryStatusIdle)
		}
		if e.TimeoutSeconds != 600 {
			t.Errorf("entry %s timeout=%d want 600", e.SandboxID, e.TimeoutSeconds)
		}
		if e.ExpiresAt.IsZero() {
			t.Errorf("entry %s expires_at zero", e.SandboxID)
		}
	}
}
