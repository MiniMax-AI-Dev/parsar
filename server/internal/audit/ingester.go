package audit

import (
	"context"
	"errors"
	"log/slog"

	"sync"
	"sync/atomic"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// DefaultBufferCapacity sizes the buffer to absorb ~5s of burst at
// realistic emit rates (a few hundred events/sec).
const DefaultBufferCapacity = 2048

// DefaultWriteTimeout caps Sink.Write; a stuck Write blocks the entire
// ingester worker and causes Dropped to climb upstream.
const DefaultWriteTimeout = 5 * time.Second

// Options configures an Ingester.
type Options struct {
	// BufferCapacity bounds the pending-event queue. Emit() never blocks;
	// when full it returns ErrDropped.
	BufferCapacity int
	WriteTimeout   time.Duration
	Logger         *slog.Logger
	// Now is injectable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// ErrDropped is returned by Emit when the buffer is full. Observability-only;
// callers MUST NOT retry — audit emit is best-effort by design.
var ErrDropped = errors.New("audit: ingester buffer full; event dropped")

var ErrClosed = errors.New("audit: ingester has been stopped")

// Ingester is an asynchronous pipeline that decouples audit emit from
// audit persistence. A single worker goroutine drains a bounded buffer
// into the configured Sink.
//
// Emit is safe to call from many goroutines; Start and Stop must each
// be called exactly once.
type Ingester struct {
	sink         Sink
	buffer       chan Event
	writeTimeout time.Duration
	logger       *slog.Logger
	now          func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stopped   chan struct{} // closed when worker exits
	closing   atomic.Bool

	emitted      atomic.Int64
	handled      atomic.Int64
	dropped      atomic.Int64
	sinkErrors   atomic.Int64
	lastLagNanos atomic.Int64
}

// NewIngester constructs an Ingester. Call Start before Emit.
func NewIngester(sink Sink, opts Options) *Ingester {
	if sink == nil {
		panic("audit.NewIngester: sink is nil")
	}
	capacity := opts.BufferCapacity
	if capacity <= 0 {
		capacity = DefaultBufferCapacity
	}
	timeout := opts.WriteTimeout
	if timeout <= 0 {
		timeout = DefaultWriteTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = obslog.Bg()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Ingester{
		sink:         sink,
		buffer:       make(chan Event, capacity),
		writeTimeout: timeout,
		logger:       logger,
		now:          now,
		stopped:      make(chan struct{}),
	}
}

// Start spawns the worker goroutine. Cancelling ctx triggers the same
// drain path as Stop. Calling Start more than once is a no-op.
func (i *Ingester) Start(ctx context.Context) {
	i.startOnce.Do(func() {
		go i.run(ctx)
	})
}

// Stop closes the ingester to new Emits, drains the buffer, and waits
// for the worker to exit. If ctx expires while events are in-flight,
// Stop returns ctx.Err() and the worker continues until its own context
// dies.
func (i *Ingester) Stop(ctx context.Context) error {
	i.stopOnce.Do(func() {
		i.closing.Store(true)
		close(i.buffer)
	})
	select {
	case <-i.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Emit hands an event to the ingester. Never blocks. Returns ErrDropped
// when the buffer is full and ErrClosed after Stop. The error is
// observability-only; callers must not let audit failures fail business
// operations.
func (i *Ingester) Emit(ev Event) error {
	if i.closing.Load() {
		return ErrClosed
	}
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = i.now()
	}
	select {
	case i.buffer <- ev:
		i.emitted.Add(1)
		return nil
	default:
		i.dropped.Add(1)
		return ErrDropped
	}
}

// Stats returns a snapshot of the pipeline's observable health.
func (i *Ingester) Stats() Stats {
	return Stats{
		Emitted:      i.emitted.Load(),
		Dropped:      i.dropped.Load(),
		SinkErrors:   i.sinkErrors.Load(),
		LastLagNanos: i.lastLagNanos.Load(),
		BufferLen:    len(i.buffer),
		BufferCap:    cap(i.buffer),
	}
}

// Flush blocks until the pending buffer is empty AND the worker has
// finished its current Sink.Write, or ctx expires. For tests only —
// production code uses Stop. Returns ErrClosed if Stop has been called.
func (i *Ingester) Flush(ctx context.Context) error {
	if i.closing.Load() {
		return ErrClosed
	}
	target := i.emitted.Load()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if i.handled.Load() >= target {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (i *Ingester) run(ctx context.Context) {
	defer close(i.stopped)
	for {
		select {
		case <-ctx.Done():
			// Drain remaining events best-effort then return.
			i.drain(context.Background())
			return
		case ev, ok := <-i.buffer:
			if !ok {
				return
			}
			i.handle(ctx, ev)
			i.handled.Add(1)
		}
	}
}

// drain processes events still sitting in the buffer after the worker
// context is cancelled, to avoid losing events Emit'd just before Stop.
func (i *Ingester) drain(ctx context.Context) {
	for {
		select {
		case ev, ok := <-i.buffer:
			if !ok {
				return
			}
			i.handle(ctx, ev)
			i.handled.Add(1)
		default:
			return
		}
	}
}

func (i *Ingester) handle(ctx context.Context, ev Event) {
	lag := i.now().Sub(ev.OccurredAt)
	if lag < 0 {
		lag = 0
	}
	i.lastLagNanos.Store(lag.Nanoseconds())

	writeCtx, cancel := context.WithTimeout(ctx, i.writeTimeout)
	defer cancel()
	if err := i.sink.Write(writeCtx, ev); err != nil {
		i.sinkErrors.Add(1)
		i.logger.Warn("audit sink write failed",
			"source", ev.Source,
			"event_type", ev.EventType,
			"error", err,
		)
	}
}
