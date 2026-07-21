package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	in := DeltaPayload{Delta: "hello", Sequence: 42}
	env, err := NewEnvelope(TypeDelta, "run-123", in)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if env.Type != TypeDelta {
		t.Fatalf("Type = %q, want %q", env.Type, TypeDelta)
	}
	if env.ID != "run-123" {
		t.Fatalf("ID = %q, want %q", env.ID, "run-123")
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}
	var got Envelope
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal envelope: %v", err)
	}
	var out DeltaPayload
	if err := got.DecodePayload(&out); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if out.Delta != "hello" || out.Sequence != 42 {
		t.Fatalf("payload round-trip lost data: %+v", out)
	}
}

func TestEnvelopeOmitsEmptyPayload(t *testing.T) {
	// prompt_cancel carries no body — the wire form must drop the
	// payload field entirely so receivers can route on Type alone
	// without hitting `decode: unexpected end` on an empty Payload.
	env, err := NewEnvelope(TypePromptCancel, "run-456", nil)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), `"payload"`) {
		t.Fatalf("expected payload field omitted, got %s", raw)
	}
}

func TestDecodePayloadEmptyIsNoop(t *testing.T) {
	env := Envelope{Type: TypePromptCancel, ID: "run-789"}
	var out PromptCancelPayload
	if err := env.DecodePayload(&out); err != nil {
		t.Fatalf("DecodePayload on empty payload: %v", err)
	}
}

func TestUsagePayloadEmbedsUsage(t *testing.T) {
	// The gateway hands UsagePayload straight to the connector boundary,
	// which translates Usage → store.UsageInput. If we accidentally
	// renamed the embedded field or stopped embedding, the persistence
	// path would silently lose all usage fields. Catch that here.
	in := UsagePayload{Usage: Usage{Provider: "anthropic", Model: "claude-opus", InputTokens: 100}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"provider":"anthropic"`) {
		t.Fatalf("expected provider field at top level, got %s", raw)
	}
	var out UsagePayload
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Provider != "anthropic" || out.InputTokens != 100 {
		t.Fatalf("usage round-trip lost data: %+v", out)
	}
}

func TestVersionCompatible(t *testing.T) {
	cases := []struct {
		client string
		ok     bool
	}{
		{Version, true},    // exact match
		{"0.2.99", true},   // patch drift OK
		{"0.1.99", false},  // minor drift NOT OK
		{"1.0.0", false},   // major drift NOT OK
		{"", false},        // missing
		{"garbage", false}, // unparseable
		{"0.1", false},     // truncated
	}
	for _, tc := range cases {
		if got := VersionCompatible(tc.client); got != tc.ok {
			t.Errorf("VersionCompatible(%q) = %v, want %v", tc.client, got, tc.ok)
		}
	}
}
