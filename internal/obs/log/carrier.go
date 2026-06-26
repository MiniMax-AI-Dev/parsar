package log

import (
	"errors"
	"strings"
)

// Carrier is the in-memory form of a W3C `traceparent` value
// (`00-{32hex trace_id}-{16hex span_id}-{2hex flags}`). We do not
// parse `tracestate`. Sampled tracks the low bit of the flags byte;
// auto-generated carriers default to true.
type Carrier struct {
	Trace   TraceID
	Span    SpanID
	Sampled bool
}

// HeaderName is the canonical HTTP header for W3C traceparent.
const HeaderName = "Traceparent"

// version is the only W3C version this package accepts on the wire.
const version = "00"

// NewCarrier returns a fresh Carrier with newly-minted trace + span IDs.
func NewCarrier() Carrier {
	return Carrier{
		Trace:   NewTraceID(),
		Span:    NewSpanID(),
		Sampled: true,
	}
}

// ChildSpan returns a Carrier sharing this Trace but with a fresh Span.
func (c Carrier) ChildSpan() Carrier {
	return Carrier{Trace: c.Trace, Span: NewSpanID(), Sampled: c.Sampled}
}

// String formats the Carrier as a W3C traceparent. Returns "" for a
// zero-trace Carrier so callers can use it as a presence check.
func (c Carrier) String() string {
	if c.Trace.IsZero() || c.Span.IsZero() {
		return ""
	}
	flags := "00"
	if c.Sampled {
		flags = "01"
	}
	var b strings.Builder
	b.Grow(55)
	b.WriteString(version)
	b.WriteByte('-')
	b.WriteString(c.Trace.String())
	b.WriteByte('-')
	b.WriteString(c.Span.String())
	b.WriteByte('-')
	b.WriteString(flags)
	return b.String()
}

// ParseTraceparent parses a W3C traceparent string. Any deviation
// (wrong length, bad hex, reserved sentinels) yields an error and
// callers should treat it as "no trace present".
func ParseTraceparent(s string) (Carrier, error) {
	s = strings.TrimSpace(s)
	if len(s) != 55 {
		return Carrier{}, errors.New("traceparent must be 55 chars")
	}
	parts := strings.Split(s, "-")
	if len(parts) != 4 {
		return Carrier{}, errors.New("traceparent must have 4 hyphen-separated fields")
	}
	if parts[0] != version {
		return Carrier{}, errors.New("unsupported traceparent version")
	}
	trace, err := ParseTraceID(parts[1])
	if err != nil {
		return Carrier{}, err
	}
	span, err := ParseSpanID(parts[2])
	if err != nil {
		return Carrier{}, err
	}
	if len(parts[3]) != 2 {
		return Carrier{}, errors.New("traceparent flags must be 2 hex chars")
	}
	sampled := parts[3] == "01" || parts[3] == "03"
	return Carrier{Trace: trace, Span: span, Sampled: sampled}, nil
}
