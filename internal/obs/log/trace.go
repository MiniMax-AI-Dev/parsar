// Package log wraps log/slog with a context-aware handler that injects
// W3C trace IDs from ctx so a single request — HTTP, WS envelope,
// sweeper tick — can be grepped end-to-end by `trace_id`.
package log

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// TraceID is the W3C trace-id: 16 random bytes, rendered as 32
// lowercase hex chars. All-zero is reserved and rejected by parsers.
type TraceID [16]byte

// SpanID is the W3C span-id: 8 random bytes, 16 lowercase hex chars.
// All-zero is reserved.
type SpanID [8]byte

// NewTraceID returns a fresh random TraceID. On the vanishingly rare
// crypto/rand failure the result is the zero ID; the handler then
// skips trace-attr injection rather than emitting all-zero strings.
func NewTraceID() TraceID {
	var id TraceID
	_, _ = rand.Read(id[:])
	return id
}

func NewSpanID() SpanID {
	var id SpanID
	_, _ = rand.Read(id[:])
	return id
}

func (t TraceID) String() string { return hex.EncodeToString(t[:]) }

func (s SpanID) String() string { return hex.EncodeToString(s[:]) }

func (t TraceID) IsZero() bool {
	for _, b := range t {
		if b != 0 {
			return false
		}
	}
	return true
}

func (s SpanID) IsZero() bool {
	for _, b := range s {
		if b != 0 {
			return false
		}
	}
	return true
}

// ParseTraceID decodes a 32-char lowercase hex string. Rejects
// all-zero per W3C reserved-sentinel rule.
func ParseTraceID(s string) (TraceID, error) {
	var out TraceID
	if len(s) != 32 {
		return out, errors.New("trace id must be 32 hex chars")
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, errors.New("trace id is not hex")
	}
	if out.IsZero() {
		return out, errors.New("trace id is all-zero")
	}
	return out, nil
}

// ParseSpanID decodes a 16-char lowercase hex string. Rejects all-zero.
func ParseSpanID(s string) (SpanID, error) {
	var out SpanID
	if len(s) != 16 {
		return out, errors.New("span id must be 16 hex chars")
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return out, errors.New("span id is not hex")
	}
	if out.IsZero() {
		return out, errors.New("span id is all-zero")
	}
	return out, nil
}
