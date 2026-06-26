package log

import (
	"strings"
	"testing"
)

func TestCarrierRoundTrip(t *testing.T) {
	c := NewCarrier()
	encoded := c.String()
	if len(encoded) != 55 {
		t.Fatalf("traceparent length: want 55, got %d (%q)", len(encoded), encoded)
	}
	if !strings.HasPrefix(encoded, "00-") || !strings.HasSuffix(encoded, "-01") {
		t.Fatalf("traceparent shape: %q", encoded)
	}
	got, err := ParseTraceparent(encoded)
	if err != nil {
		t.Fatalf("ParseTraceparent on our own output: %v", err)
	}
	if got.Trace != c.Trace || got.Span != c.Span {
		t.Fatalf("round-trip mismatch: out=%+v in=%+v", got, c)
	}
	if !got.Sampled {
		t.Fatalf("sampled flag should round-trip true; got false")
	}
}

func TestParseTraceparentRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"too-short", "00-1234"},
		{"too-long", "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-01-extra"},
		{"wrong-version", "ff-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"},
		{"all-zero-trace", "00-" + strings.Repeat("0", 32) + "-b7ad6b7169203331-01"},
		{"all-zero-span", "00-0af7651916cd43dd8448eb211c80319c-" + strings.Repeat("0", 16) + "-01"},
		{"bad-hex-trace", "00-zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz-b7ad6b7169203331-01"},
		{"bad-hex-span", "00-0af7651916cd43dd8448eb211c80319c-zzzzzzzzzzzzzzzz-01"},
		{"three-fields", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ParseTraceparent(c.in); err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
		})
	}
}

// TestParseTraceparentSampledBit: 00 = not sampled, 01 = sampled.
// "03" (sampled + random bit) also accepted as sampled per W3C
// "ignore unknown flag bits".
func TestParseTraceparentSampledBit(t *testing.T) {
	cases := []struct {
		flags   string
		sampled bool
	}{
		{"00", false},
		{"01", true},
		{"03", true},
	}
	const trace = "0af7651916cd43dd8448eb211c80319c"
	const span = "b7ad6b7169203331"
	for _, c := range cases {
		t.Run(c.flags, func(t *testing.T) {
			got, err := ParseTraceparent("00-" + trace + "-" + span + "-" + c.flags)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			if got.Sampled != c.sampled {
				t.Fatalf("sampled: want %v got %v", c.sampled, got.Sampled)
			}
		})
	}
}

// TestCarrierZeroValueString: zero formats as "" so callers can use
// the formatted string as a "did we have a carrier" boolean.
func TestCarrierZeroValueString(t *testing.T) {
	var zero Carrier
	if got := zero.String(); got != "" {
		t.Fatalf("zero carrier should format as empty; got %q", got)
	}
}
