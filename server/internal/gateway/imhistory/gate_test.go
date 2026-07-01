package imhistory

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// stubFetcher is a channel.HistoryFetcher whose behavior each test scripts. It
// counts calls so tests assert retry and cache-dedup behavior.
type stubFetcher struct {
	mu    sync.Mutex
	calls int32
	fn    func(call int32, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error)
}

func (s *stubFetcher) FetchHistory(_ context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	n := atomic.AddInt32(&s.calls, 1)
	s.mu.Lock()
	fn := s.fn
	s.mu.Unlock()
	return fn(n, req)
}

// newTestGate builds a Gate with a fake clock and a recording sleep so retries
// and cache expiry advance deterministically instead of blocking on wall time.
func newTestGate(opts Options) (*Gate, *fakeClock, *[]time.Duration) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	var slept []time.Duration
	g := New(opts)
	g.now = clk.now
	g.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		clk.advance(d)
		return nil
	}
	return g, clk, &slept
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func req(chat string) channel.FetchHistoryRequest {
	return channel.FetchHistoryRequest{ExternalChatID: chat}
}

// TestGate_RetriesThenSucceeds: a rate-limit error is absorbed (block-retry on
// Retry-After) and the eventual success is returned — the 100%-success core.
func TestGate_RetriesThenSucceeds(t *testing.T) {
	g, _, slept := newTestGate(Options{})
	f := &stubFetcher{fn: func(call int32, _ channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		if call < 3 {
			return channel.FetchHistoryResult{}, &channel.RateLimitedError{Platform: channel.PlatformFeishu, RetryAfter: 2 * time.Second, Err: errors.New("429")}
		}
		return channel.FetchHistoryResult{NextCursor: "done"}, nil
	}}

	res, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1"))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.NextCursor != "done" {
		t.Fatalf("NextCursor = %q, want done", res.NextCursor)
	}
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3", f.calls)
	}
	if len(*slept) != 2 {
		t.Fatalf("slept %d times, want 2", len(*slept))
	}
	for _, d := range *slept {
		if d != 2*time.Second {
			t.Fatalf("backoff = %v, want 2s (the Retry-After)", d)
		}
	}
}

// TestGate_ExhaustsRetries: after MaxRetries the last rate-limit error surfaces
// (bounded, not infinite, blocking).
func TestGate_ExhaustsRetries(t *testing.T) {
	g, _, slept := newTestGate(Options{MaxRetries: 2})
	f := &stubFetcher{fn: func(int32, channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		return channel.FetchHistoryResult{}, &channel.RateLimitedError{Platform: channel.PlatformFeishu, RetryAfter: time.Second, Err: errors.New("429")}
	}}

	_, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1"))
	var rl *channel.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a RateLimitedError after exhausting retries", err)
	}
	if f.calls != 3 { // initial + 2 retries
		t.Fatalf("calls = %d, want 3", f.calls)
	}
	if len(*slept) != 2 {
		t.Fatalf("slept %d times, want 2 (one per retry)", len(*slept))
	}
}

// TestGate_NonRateLimitErrorNotRetried: a plain error is returned immediately,
// no retry, no sleep.
func TestGate_NonRateLimitErrorNotRetried(t *testing.T) {
	g, _, slept := newTestGate(Options{})
	boom := errors.New("boom")
	f := &stubFetcher{fn: func(int32, channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		return channel.FetchHistoryResult{}, boom
	}}

	_, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on a non-rate-limit error)", f.calls)
	}
	if len(*slept) != 0 {
		t.Fatal("must not sleep on a non-rate-limit error")
	}
}

// TestGate_CacheServesRepeat: a second identical request within the TTL replays
// the cached page without touching the fetcher.
func TestGate_CacheServesRepeat(t *testing.T) {
	g, clk, _ := newTestGate(Options{CacheTTL: 5 * time.Second})
	f := &stubFetcher{fn: func(call int32, _ channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		return channel.FetchHistoryResult{NextCursor: "c"}, nil
	}}

	if _, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1")); err != nil {
		t.Fatalf("Fetch#1: %v", err)
	}
	if _, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1")); err != nil {
		t.Fatalf("Fetch#2: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("calls = %d, want 1 (second served from cache)", f.calls)
	}

	// After the TTL lapses the cache misses and the fetcher runs again.
	clk.advance(6 * time.Second)
	if _, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1")); err != nil {
		t.Fatalf("Fetch#3: %v", err)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2 (cache expired)", f.calls)
	}
}

// TestGate_CacheDisabled: a negative TTL bypasses the cache entirely.
func TestGate_CacheDisabled(t *testing.T) {
	g, _, _ := newTestGate(Options{CacheTTL: -1})
	f := &stubFetcher{fn: func(int32, channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		return channel.FetchHistoryResult{}, nil
	}}
	for i := 0; i < 3; i++ {
		if _, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1")); err != nil {
			t.Fatalf("Fetch#%d: %v", i, err)
		}
	}
	if f.calls != 3 {
		t.Fatalf("calls = %d, want 3 (cache disabled)", f.calls)
	}
}

// TestGate_SerializesSameChat: concurrent requests for one chat never overlap
// inside the fetcher — the per-chat lock queues them.
func TestGate_SerializesSameChat(t *testing.T) {
	g, _, _ := newTestGate(Options{CacheTTL: -1}) // disable cache so every call runs the fetcher
	var inFlight, maxInFlight int32
	f := &stubFetcher{fn: func(int32, channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		n := atomic.AddInt32(&inFlight, 1)
		for {
			m := atomic.LoadInt32(&maxInFlight)
			if n <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return channel.FetchHistoryResult{}, nil
	}}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_same"))
		}()
	}
	wg.Wait()
	if maxInFlight != 1 {
		t.Fatalf("max concurrent fetches = %d, want 1 (same-chat requests must serialize)", maxInFlight)
	}
}

// TestGate_ContextCancelDuringBackoff: a cancelled context aborts the retry
// wait with the context error rather than hanging.
func TestGate_ContextCancelDuringBackoff(t *testing.T) {
	g := New(Options{})
	g.sleep = func(ctx context.Context, _ time.Duration) error { return context.Canceled }
	f := &stubFetcher{fn: func(int32, channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
		return channel.FetchHistoryResult{}, &channel.RateLimitedError{Platform: channel.PlatformFeishu, RetryAfter: time.Hour, Err: errors.New("429")}
	}}
	_, err := g.Fetch(context.Background(), channel.PlatformFeishu, f, req("oc_1"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
