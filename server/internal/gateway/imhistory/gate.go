// Package imhistory adds the cross-cutting reliability guarantees the
// on-demand chat-history tool needs on top of any channel.HistoryFetcher.
//
// The tool's hard contract is a 100% success rate: an agent asking for recent
// context must never see a failed tool call, even when the platform throttles.
// Partial history is acceptable; a failure is not. Gate turns a transient
// rate-limit into a slightly slower answer via three mechanisms:
//
//   - block-and-retry: a *channel.RateLimitedError is not surfaced; Gate sleeps
//     the platform-suggested Retry-After (capped) and retries.
//   - per-chat serialization: concurrent requests for the same (platform, chat)
//     run one at a time, so we never self-inflict a burst that trips a 429.
//   - short-TTL cache: identical requests within the TTL window replay the last
//     page instead of hitting the platform again.
//
// Gate holds only the cross-cutting state (locks + cache); the resolved
// HistoryFetcher is passed per call because the Feishu adapter is constructed
// per conversation (its token cache is keyed by the source app_id).
package imhistory

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// Defaults chosen for the 100%-success contract without unbounded blocking: a
// handful of retries covers a normal throttle window, the backoff ceiling keeps
// any single wait sane, and a few seconds of cache dedupes an agent's rapid
// repeat calls without serving stale history.
const (
	defaultMaxRetries = 4
	defaultMaxBackoff = 30 * time.Second
	defaultCacheTTL   = 3 * time.Second
)

// Options tunes Gate. The zero value is valid and falls back to the package
// defaults; set CacheTTL to a negative duration to disable caching.
type Options struct {
	MaxRetries int           // block-retry attempts on a rate-limit error
	MaxBackoff time.Duration // ceiling on any single Retry-After sleep
	CacheTTL   time.Duration // <0 disables the cache; 0 uses the default
}

func (o Options) withDefaults() Options {
	if o.MaxRetries <= 0 {
		o.MaxRetries = defaultMaxRetries
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = defaultMaxBackoff
	}
	if o.CacheTTL == 0 {
		o.CacheTTL = defaultCacheTTL
	}
	return o
}

// Gate serializes, retries, and caches history fetches. It is safe for
// concurrent use and meant to be long-lived (one per process), so its cache and
// per-chat locks outlive the per-conversation HistoryFetcher instances.
type Gate struct {
	opts Options

	// log is the timing/diagnostic sink. nil means silent — the production
	// wiring in cmd/server sets it so we can attribute slow requests to
	// sandbox startup vs Feishu rate-limit retry without changing tests.
	log *slog.Logger

	// now/sleep are injected so tests drive cache expiry and retry backoff
	// without wall-clock waits.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	mu     sync.Mutex
	chLock map[string]*sync.Mutex
	cache  map[string]cacheEntry
}

type cacheEntry struct {
	res     channel.FetchHistoryResult
	expires time.Time
}

// New builds a Gate with the given options (zero value is fine).
func New(opts Options) *Gate {
	return &Gate{
		opts:   opts.withDefaults(),
		now:    time.Now,
		sleep:  sleepCtx,
		chLock: make(map[string]*sync.Mutex),
		cache:  make(map[string]cacheEntry),
	}
}

// SetLogger attaches a structured logger used for per-request timing and
// retry diagnostics. nil disables logging; safe to call once during boot.
func (g *Gate) SetLogger(l *slog.Logger) { g.log = l }

// Fetch runs req through f under the Gate's guarantees. platform scopes the
// cache + serialization key so two platforms with the same chat id never
// collide. The returned result is the adapter's own (already oldest-first,
// clamped, cursor-carrying) page.
func (g *Gate) Fetch(ctx context.Context, platform channel.Platform, f channel.HistoryFetcher, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	key := cacheKey(platform, req)

	if res, ok := g.cacheGet(key); ok {
		if g.log != nil {
			g.log.Debug("imhistory: cache hit", "platform", platform, "chat", req.ExternalChatID)
		}
		return res, nil
	}

	start := g.now()

	// Serialize same-chat requests: hold the per-chat lock across the fetch so
	// concurrent callers queue instead of bursting the platform.
	lock := g.lockFor(chatKey(platform, req))
	lock.Lock()
	defer lock.Unlock()

	// Double-check: a request we queued behind may have just populated the
	// cache while we waited for the lock.
	if res, ok := g.cacheGet(key); ok {
		if g.log != nil {
			g.log.Debug("imhistory: cache hit after lock wait", "platform", platform, "chat", req.ExternalChatID, "lock_wait", g.now().Sub(start))
		}
		return res, nil
	}

	res, retries, totalSleep, err := g.fetchWithRetry(ctx, f, req)
	if g.log != nil {
		attrs := []any{
			"platform", platform,
			"chat", req.ExternalChatID,
			"thread", req.ExternalThreadID,
			"elapsed", g.now().Sub(start),
			"retries", retries,
			"total_sleep", totalSleep,
		}
		if err != nil {
			attrs = append(attrs, "err", err)
			g.log.Warn("imhistory: fetch failed", attrs...)
		} else {
			g.log.Info("imhistory: fetch ok", attrs...)
		}
	}
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}
	g.cachePut(key, res)
	return res, nil
}

// fetchWithRetry calls f, absorbing rate-limit errors by sleeping the suggested
// Retry-After (capped by MaxBackoff) and retrying up to MaxRetries times. Any
// non-rate-limit error is returned immediately; ctx cancellation aborts the
// wait. Returns (result, retries, totalSleep, err) so callers can attribute
// slow requests to rate-limit backoff.
func (g *Gate) fetchWithRetry(ctx context.Context, f channel.HistoryFetcher, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, int, time.Duration, error) {
	var (
		lastErr    error
		retries    int
		totalSleep time.Duration
	)
	for attempt := 0; attempt <= g.opts.MaxRetries; attempt++ {
		res, err := f.FetchHistory(ctx, req)
		if err == nil {
			return res, retries, totalSleep, nil
		}
		var rl *channel.RateLimitedError
		if !errors.As(err, &rl) {
			return channel.FetchHistoryResult{}, retries, totalSleep, err
		}
		lastErr = err
		if attempt == g.opts.MaxRetries {
			break
		}
		wait := capBackoff(rl.RetryAfter, g.opts.MaxBackoff)
		if g.log != nil {
			g.log.Debug("imhistory: 429 retry", "platform", rl.Platform, "retry_after", rl.RetryAfter, "capped", wait, "attempt", attempt+1, "max", g.opts.MaxRetries)
		}
		if werr := g.sleep(ctx, wait); werr != nil {
			return channel.FetchHistoryResult{}, retries, totalSleep, werr
		}
		retries++
		totalSleep += wait
	}
	return channel.FetchHistoryResult{}, retries, totalSleep, lastErr
}

func (g *Gate) lockFor(key string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.chLock[key]
	if !ok {
		l = &sync.Mutex{}
		g.chLock[key] = l
	}
	return l
}

func (g *Gate) cacheGet(key string) (channel.FetchHistoryResult, bool) {
	if g.opts.CacheTTL < 0 {
		return channel.FetchHistoryResult{}, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.cache[key]
	if !ok || !g.now().Before(e.expires) {
		if ok {
			delete(g.cache, key)
		}
		return channel.FetchHistoryResult{}, false
	}
	return e.res, true
}

func (g *Gate) cachePut(key string, res channel.FetchHistoryResult) {
	if g.opts.CacheTTL < 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cache[key] = cacheEntry{res: res, expires: g.now().Add(g.opts.CacheTTL)}
}

// capBackoff floors any wait at a small positive value (a rate-limit error with
// no Retry-After still deserves a brief pause) and ceilings it at max.
func capBackoff(d, max time.Duration) time.Duration {
	if d <= 0 {
		d = time.Second
	}
	if d > max {
		d = max
	}
	return d
}

// sleepCtx waits d or returns ctx.Err() if the context is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
