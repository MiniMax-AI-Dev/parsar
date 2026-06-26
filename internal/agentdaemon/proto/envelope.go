// Package proto defines the JSON wire format spoken by parsar-daemon over
// the reverse WebSocket tunnel to the Parsar server. Both ends import
// this package; adding an event means editing one file here and both
// sides at once.
//
// Topology: daemon dials OUT (firewall-friendly). Every frame is one
// JSON Envelope; the receiver routes by Type without partial-decoding
// Payload. Downstream = server → daemon (outbound.go), upstream =
// daemon → server (inbound.go). Event names match
// connector.PromptEvent.Type 1:1 so the gateway can translate without
// a lookup table.
//
// Envelope.ID correlation:
//   - prompt_request / prompt_cancel: ID = RunID.
//   - delta / tool_call / usage / error / done: ID = originating RunID.
//   - permission_request: ID = daemon-minted "perm_<8hex>"; the matching
//     downstream permission_decision echoes it back.
//   - heartbeats carry no ID.
package proto

import (
	"encoding/json"
	"fmt"
)

// Envelope is the outer JSON frame. Payload is held as raw JSON so the
// routing layer can dispatch by Type before paying a per-event decode.
type Envelope struct {
	// Type tags the payload shape. Must be one of the constants in
	// inbound.go (upstream) or outbound.go (downstream).
	Type string `json:"type"`

	// ID correlates frames to a logical work unit; meaning depends on
	// Type (see per-event comments). Omitted when empty (heartbeats).
	ID string `json:"id,omitempty"`

	// Payload is the type-specific body, marshalled separately so the
	// gateway can route on Type without double-decoding.
	Payload json.RawMessage `json:"payload,omitempty"`

	// Trace is the W3C `traceparent` value
	// ("00-{32hex trace_id}-{16hex span_id}-{2hex flags}") for the
	// logical request. Server-issued envelopes carry the gateway's
	// ctx carrier; daemon-issued envelopes carry the daemon's. Omitted
	// when no trace is in scope (heartbeats, legacy clients).
	// Receivers MUST tolerate missing/unparseable values — both mean
	// "mint a fresh trace locally", never reject.
	Trace string `json:"trace,omitempty"`
}

// NewEnvelope marshals payload into an Envelope. A nil payload yields
// an Envelope with no Payload field (for bodyless types like
// prompt_cancel).
func NewEnvelope(typ string, id string, payload any) (Envelope, error) {
	env := Envelope{Type: typ, ID: id}
	if payload == nil {
		return env, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("proto: marshal payload for type %q: %w", typ, err)
	}
	env.Payload = raw
	return env, nil
}

// NewEnvelopeWithTrace stamps the given W3C traceparent string into
// Envelope.Trace. Pass "" to skip. String (not typed Carrier) so this
// package stays free of an import on internal/obs/log.
func NewEnvelopeWithTrace(typ string, id string, payload any, traceparent string) (Envelope, error) {
	env, err := NewEnvelope(typ, id, payload)
	if err != nil {
		return Envelope{}, err
	}
	env.Trace = traceparent
	return env, nil
}

// DecodePayload unpacks Envelope.Payload into out. An empty Payload is
// a non-error so bodyless types (prompt_cancel, permission_cancel)
// decode cleanly.
func (e Envelope) DecodePayload(out any) error {
	if len(e.Payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(e.Payload, out); err != nil {
		return fmt.Errorf("proto: decode payload for type %q: %w", e.Type, err)
	}
	return nil
}
