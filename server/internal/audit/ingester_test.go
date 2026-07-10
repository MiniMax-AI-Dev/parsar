package audit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSink struct {
	mu      sync.Mutex
	events  []Event
	errOnce error
	delay   time.Duration
}

func (s *fakeSink) Write(ctx context.Context, ev Event) error {
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.delay):
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	if s.errOnce != nil {
		err := s.errOnce
		s.errOnce = nil
		return err
	}
	return nil
}

func (s *fakeSink) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

func TestIngesterEmitsToSink(t *testing.T) {
	sink := &fakeSink{}
	ing := NewIngester(sink, Options{BufferCapacity: 8})
	ing.Start(context.Background())
	defer func() { _ = ing.Stop(context.Background()) }()

	for _, src := range []string{SourceIdentity, SourceAdmin, SourceRuntime, SourceApproval, SourceData} {
		if err := ing.Emit(Event{
			Source:    src,
			EventType: "test.smoke",
			ActorType: ActorTypeSystem,
		}); err != nil {
			t.Fatalf("emit %s: unexpected error: %v", src, err)
		}
	}

	waitForCount(t, sink, 5, time.Second)

	got := sink.Events()
	if len(got) != 5 {
		t.Fatalf("expected 5 events, got %d", len(got))
	}
	for _, ev := range got {
		if ev.OccurredAt.IsZero() {
			t.Errorf("expected OccurredAt to be defaulted, got zero")
		}
	}

	stats := ing.Stats()
	if stats.Emitted != 5 || stats.Dropped != 0 || stats.SinkErrors != 0 {
		t.Errorf("unexpected stats: %+v", stats)
	}
}

func TestIngesterDropsWhenBufferFull(t *testing.T) {
	// Block the worker with a slow sink so events pile up in the buffer.
	sink := &fakeSink{delay: 200 * time.Millisecond}
	ing := NewIngester(sink, Options{BufferCapacity: 2, WriteTimeout: time.Second})
	ing.Start(context.Background())
	defer func() { _ = ing.Stop(context.Background()) }()

	var dropped int
	for k := 0; k < 50; k++ {
		err := ing.Emit(Event{Source: SourceAdmin, EventType: "test.flood", ActorType: ActorTypeSystem})
		if errors.Is(err, ErrDropped) {
			dropped++
		}
	}
	if dropped == 0 {
		t.Fatalf("expected at least one ErrDropped under buffer pressure")
	}
	if got := ing.Stats().Dropped; got != int64(dropped) {
		t.Errorf("Stats.Dropped = %d, want %d", got, dropped)
	}
}

func TestIngesterStopWaitsForWorker(t *testing.T) {
	sink := &fakeSink{}
	ing := NewIngester(sink, Options{BufferCapacity: 8})
	ing.Start(context.Background())

	for k := 0; k < 4; k++ {
		if err := ing.Emit(Event{Source: SourceAdmin, EventType: "test.drain", ActorType: ActorTypeSystem}); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	if err := ing.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := len(sink.Events()); got != 4 {
		t.Errorf("expected all 4 buffered events to drain on stop, got %d", got)
	}
	// Emit after Stop is rejected.
	if err := ing.Emit(Event{Source: SourceAdmin, EventType: "test.after_stop", ActorType: ActorTypeSystem}); !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed after Stop, got %v", err)
	}
}

func TestIngesterFlushWaitsForInFlightWrite(t *testing.T) {
	sink := &fakeSink{delay: 75 * time.Millisecond}
	ing := NewIngester(sink, Options{BufferCapacity: 4, WriteTimeout: time.Second})
	ing.Start(context.Background())
	defer func() { _ = ing.Stop(context.Background()) }()

	if err := ing.Emit(Event{Source: SourceAdmin, EventType: "test.flush", ActorType: ActorTypeSystem}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := ing.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(sink.Events()); got != 1 {
		t.Fatalf("Flush returned before in-flight sink write completed; got %d events", got)
	}
}

func TestIngesterCountsSinkErrors(t *testing.T) {
	sink := &fakeSink{errOnce: errors.New("boom")}
	ing := NewIngester(sink, Options{BufferCapacity: 4})
	ing.Start(context.Background())
	defer func() { _ = ing.Stop(context.Background()) }()

	for k := 0; k < 3; k++ {
		if err := ing.Emit(Event{Source: SourceRuntime, EventType: "test.err", ActorType: ActorTypeSystem}); err != nil {
			t.Fatalf("emit: %v", err)
		}
	}
	waitForCount(t, sink, 3, time.Second)
	if got := ing.Stats().SinkErrors; got != 1 {
		t.Errorf("expected exactly 1 SinkError, got %d", got)
	}
}

func TestIngesterConcurrentEmitIsSafe(t *testing.T) {
	sink := &fakeSink{}
	ing := NewIngester(sink, Options{BufferCapacity: 256})
	ing.Start(context.Background())
	defer func() { _ = ing.Stop(context.Background()) }()

	const goroutines = 16
	const each = 32
	var wg sync.WaitGroup
	var ok atomic.Int64
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < each; k++ {
				if err := ing.Emit(Event{Source: SourceAdmin, EventType: "test.concurrent", ActorType: ActorTypeSystem}); err == nil {
					ok.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	waitForCount(t, sink, int(ok.Load()), 2*time.Second)
	stats := ing.Stats()
	total := stats.Emitted + stats.Dropped
	if total != int64(goroutines*each) {
		t.Errorf("Emitted+Dropped = %d, want %d", total, goroutines*each)
	}
}

func waitForCount(t *testing.T, sink *fakeSink, want int, max time.Duration) {
	t.Helper()
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		if len(sink.Events()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %d events; got %d", want, len(sink.Events()))
}
