package audit

import (
	"context"
	"errors"
)

// MultiSink fans out a single Event to N downstream sinks. The first sink
// is canonical (its error is the returned error); errors from additional
// sinks are logged but swallowed — a flaky fan-out target MUST NOT cause
// the canonical sink's success to be reported as a failure.
//
// The logger is a func to avoid pulling slog into the public signature;
// callers pass a closure that logs at WARN.
type MultiSink struct {
	sinks []Sink
	log   func(format string, args ...any)
}

// NewMultiSink composes Sinks. The first is canonical; remaining are
// best-effort fan-out. At least one sink is required. logger may be nil
// to suppress fan-out error notifications.
func NewMultiSink(logger func(format string, args ...any), sinks ...Sink) (*MultiSink, error) {
	if len(sinks) == 0 {
		return nil, errors.New("audit.NewMultiSink: at least one sink required")
	}
	for i, s := range sinks {
		if s == nil {
			return nil, errorAt("nil sink at position", i)
		}
	}
	noop := func(string, ...any) {}
	if logger == nil {
		logger = noop
	}
	return &MultiSink{sinks: sinks, log: logger}, nil
}

// Write delivers ev to every sink. The first sink's result is returned
// verbatim; additional sinks' errors are logged but swallowed.
func (m *MultiSink) Write(ctx context.Context, ev Event) error {
	canonicalErr := m.sinks[0].Write(ctx, ev)
	for i := 1; i < len(m.sinks); i++ {
		if err := m.sinks[i].Write(ctx, ev); err != nil {
			m.log("audit multi-sink: fan-out sink %d write failed source=%s event_type=%s err=%v",
				i, ev.Source, ev.EventType, err)
		}
	}
	return canonicalErr
}

func errorAt(prefix string, idx int) error {
	switch idx {
	case 0:
		return errors.New(prefix + " 0")
	case 1:
		return errors.New(prefix + " 1")
	case 2:
		return errors.New(prefix + " 2")
	default:
		return errors.New(prefix + " (>2)")
	}
}

var _ Sink = (*MultiSink)(nil)
