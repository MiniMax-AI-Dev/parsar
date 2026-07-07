package inflight

import (
	"strings"
	"testing"
	"time"
)

// TestBuildPingText_WithOpenID locks in the at-mention shape that
// Feishu's msg_type=text body parses for desktop / mobile push.
func TestBuildPingText_WithOpenID(t *testing.T) {
	got := buildPingText("ou_bob", "Task complete ✓ took 17s")
	want := `<at user_id="ou_bob">user</at> Task complete ✓ took 17s`
	if got != want {
		t.Errorf("buildPingText = %q, want %q", got, want)
	}
}

// TestBuildPingText_NoOpenID locks in the documented degradation: a
// missing sender_open_id (legacy rows, system-initiated runs) must
// fall back to the bare message rather than emitting an invalid
// `<at user_id="">` tag — Feishu would reject the malformed mention.
func TestBuildPingText_NoOpenID(t *testing.T) {
	cases := []string{"", "   ", "\t\n"}
	for _, openID := range cases {
		got := buildPingText(openID, "Task complete ✓ took 17s")
		want := "Task complete ✓ took 17s"
		if got != want {
			t.Errorf("buildPingText(openID=%q) = %q, want %q", openID, got, want)
		}
		if strings.Contains(got, "<at ") {
			t.Errorf("buildPingText(openID=%q) leaked an <at> tag: %q", openID, got)
		}
	}
}

// TestBuildPingText_TrimsInputs verifies the helper normalises stray
// whitespace on both arguments so the rendered ping is always tidy.
func TestBuildPingText_TrimsInputs(t *testing.T) {
	got := buildPingText("  ou_bob  ", "  Task complete ✓ took 17s  ")
	want := `<at user_id="ou_bob">user</at> Task complete ✓ took 17s`
	if got != want {
		t.Errorf("buildPingText (trimmed) = %q, want %q", got, want)
	}
}

// TestTerminalPingMessage_FormatsDuration sanity-checks the
// "Task complete ✓ took 17s" template against several elapsed values so
// callers don't need to think about FormatElapsed's units.
func TestTerminalPingMessage_FormatsDuration(t *testing.T) {
	cases := map[time.Duration]string{
		3 * time.Second:               "Task complete ✓ took 3s",
		17 * time.Second:              "Task complete ✓ took 17s",
		60 * time.Second:              "Task complete ✓ took 1m",
		3*time.Minute + 5*time.Second: "Task complete ✓ took 3m5s",
		0:                             "Task complete ✓ took 0s",
	}
	for d, want := range cases {
		if got := terminalPingMessage(d); got != want {
			t.Errorf("terminalPingMessage(%v) = %q, want %q", d, got, want)
		}
	}
}
