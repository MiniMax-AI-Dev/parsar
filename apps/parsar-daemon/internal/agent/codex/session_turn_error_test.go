package codex

import (
	"testing"
)

// TestTurnErrorMessage_UnwrapsGatewayJSON pins the case that bit us
// in production: codex's TurnError.Message is the raw HTTP body the
// gateway returned, which for OpenAI-Responses-style proxies is a
// JSON-encoded {"error":{"code","message","type"}} blob. Forwarding
// it verbatim ("escaped braces and all") leaves operators staring at
// what looks like JSON in their UI error text. Unwrapping one layer
// surfaces the actual code + message.
func TestTurnErrorMessage_UnwrapsGatewayJSON(t *testing.T) {
	te := &TurnError{
		Message:        `{"error":{"code":"submodule_not_allowed","message":"X-Sub-Module is not allowed for this API key","type":"invalid_request_error"}}`,
		CodexErrorInfo: "other",
	}
	got := turnErrorMessage(te)
	want := "submodule_not_allowed: X-Sub-Module is not allowed for this API key"
	if got != want {
		t.Fatalf("turnErrorMessage = %q\nwant %q", got, want)
	}
}

// TestTurnErrorMessage_KeepsPlainStringAsIs handles the path where
// codex itself constructed the message (rate-limit hints, context-window
// exceeded, etc.) — these aren't gateway-wrapped JSON; pass through.
func TestTurnErrorMessage_KeepsPlainStringAsIs(t *testing.T) {
	te := &TurnError{Message: "rate limit hit, retry after 30s"}
	if got := turnErrorMessage(te); got != te.Message {
		t.Fatalf("turnErrorMessage = %q, want %q", got, te.Message)
	}
}

// TestTurnErrorMessage_NilSafe — sessions that never saw a turn error
// must still drive emitTerminal without panicking.
func TestTurnErrorMessage_NilSafe(t *testing.T) {
	if got := turnErrorMessage(nil); got != "" {
		t.Fatalf("nil → %q, want empty string", got)
	}
}

// TestTurnErrorMessage_EmptyInnerMessageFallsBackToRaw — if the JSON
// peel succeeds but the inner message is empty (rare malformed bodies),
// don't drop the original raw text on the floor.
func TestTurnErrorMessage_EmptyInnerMessageFallsBackToRaw(t *testing.T) {
	te := &TurnError{Message: `{"error":{"code":"x","message":"","type":"y"}}`}
	got := turnErrorMessage(te)
	if got != te.Message {
		t.Fatalf("empty inner.message must fall back to raw, got %q", got)
	}
}

// TestAppendOnNewline pins the body-assembly behavior so the precedence
// order in onTurnCompleted stays predictable.
func TestAppendOnNewline(t *testing.T) {
	cases := []struct {
		base, extra, want string
	}{
		{"", "", ""},
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a\n\nb"},
	}
	for _, tc := range cases {
		got := appendOnNewline(tc.base, tc.extra)
		if got != tc.want {
			t.Errorf("appendOnNewline(%q,%q) = %q, want %q", tc.base, tc.extra, got, tc.want)
		}
	}
}
