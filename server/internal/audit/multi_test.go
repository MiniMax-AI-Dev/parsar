package audit

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

type captureSink struct {
	mu     atomic.Int64
	events []Event
	err    error
}

func (c *captureSink) Write(_ context.Context, ev Event) error {
	c.mu.Add(1)
	c.events = append(c.events, ev)
	return c.err
}

// TestMultiSinkRequiresAtLeastOne — an empty MultiSink would silently
// swallow every audit event, the worst possible failure mode.
func TestMultiSinkRequiresAtLeastOne(t *testing.T) {
	if _, err := NewMultiSink(nil); err == nil {
		t.Errorf("expected error when no sinks supplied")
	}
}

// TestMultiSinkRejectsNilSink — a Write call would otherwise panic with
// a useless nil-pointer trace.
func TestMultiSinkRejectsNilSink(t *testing.T) {
	good := &captureSink{}
	if _, err := NewMultiSink(nil, good, nil); err == nil {
		t.Errorf("expected error when slice contains nil")
	}
}

func TestMultiSinkFansOutToAll(t *testing.T) {
	a, b, c := &captureSink{}, &captureSink{}, &captureSink{}
	m, err := NewMultiSink(nil, a, b, c)
	if err != nil {
		t.Fatalf("NewMultiSink: %v", err)
	}
	want := Event{Source: SourceAdmin, EventType: "model.created"}
	if err := m.Write(context.Background(), want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for label, sink := range map[string]*captureSink{"a": a, "b": b, "c": c} {
		if len(sink.events) != 1 || sink.events[0].EventType != want.EventType {
			t.Errorf("sink %s missed event: got %+v", label, sink.events)
		}
	}
}

// TestMultiSinkReturnsCanonicalError — the FIRST sink's error is what
// MultiSink returns, regardless of what later sinks do.
func TestMultiSinkReturnsCanonicalError(t *testing.T) {
	a := &captureSink{err: errors.New("primary down")}
	b := &captureSink{}
	m, err := NewMultiSink(nil, a, b)
	if err != nil {
		t.Fatalf("NewMultiSink: %v", err)
	}
	err = m.Write(context.Background(), Event{Source: SourceAdmin})
	if err == nil || !strings.Contains(err.Error(), "primary down") {
		t.Errorf("want canonical error 'primary down', got %v", err)
	}
}

// TestMultiSinkSwallowsFanoutErrors: if a fan-out target is unreachable,
// the canonical sink's success MUST still be reported as success so
// audit_records stays the source of truth.
func TestMultiSinkSwallowsFanoutErrors(t *testing.T) {
	a := &captureSink{}                                  // canonical OK
	b := &captureSink{err: errors.New("collector down")} // fan-out fails
	logged := 0
	logger := func(string, ...any) { logged++ }
	m, err := NewMultiSink(logger, a, b)
	if err != nil {
		t.Fatalf("NewMultiSink: %v", err)
	}
	if err := m.Write(context.Background(), Event{Source: SourceAdmin, EventType: "test"}); err != nil {
		t.Errorf("canonical success must surface as nil; got %v", err)
	}
	if logged != 1 {
		t.Errorf("fan-out error should log exactly once; got %d", logged)
	}
}

func TestMultiSinkNilLoggerWorks(t *testing.T) {
	bad := &captureSink{err: errors.New("nope")}
	m, _ := NewMultiSink(nil, &captureSink{}, bad)
	if err := m.Write(context.Background(), Event{Source: SourceAdmin}); err != nil {
		t.Errorf("nil-logger MultiSink should not panic / propagate: got %v", err)
	}
}
