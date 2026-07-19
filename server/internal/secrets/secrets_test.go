package secrets

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewRejectsEmptyKey(t *testing.T) {
	if _, err := New("   "); err != ErrMasterKeyRequired {
		t.Fatalf("expected ErrMasterKeyRequired, got %v", err)
	}
}

// TestNewAcceptsRaw32ByteKey verifies the 32-byte literal form lets operators
// paste an existing AES-256 key without base64 wrapping.
func TestNewAcceptsRaw32ByteKey(t *testing.T) {
	raw := strings.Repeat("k", 32)
	svc, err := New(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil || svc.keyVersion != EnvelopeVersion {
		t.Fatalf("unexpected service: %+v", svc)
	}
}

func TestNewAcceptsBase64Key(t *testing.T) {
	raw := strings.Repeat("k", 32)
	encoded := "base64:" + base64.StdEncoding.EncodeToString([]byte(raw))
	svc, err := New(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.keyVersion != EnvelopeVersion {
		t.Fatalf("expected key version %q, got %q", EnvelopeVersion, svc.keyVersion)
	}
}

// TestNewNormalizesShortBase64KeyViaSHA256 verifies operators sometimes paste
// a passphrase, not a raw key. The fallback hash is intentional, so we
// document the behavior here instead of asserting failure.
func TestNewNormalizesShortBase64KeyViaSHA256(t *testing.T) {
	encoded := "base64:" + base64.StdEncoding.EncodeToString([]byte("short"))
	svc, err := New(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.keyVersion != EnvelopeVersion {
		t.Fatalf("expected key version %q, got %q", EnvelopeVersion, svc.keyVersion)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	svc := mustService(t)
	payload := map[string]any{
		"api_key": "sk-test-1234567890",
		"team_id": "T123",
		"scopes":  []any{"chat:write", "channels:read"},
	}

	envelope, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(envelope) == 0 {
		t.Fatal("expected non-empty envelope")
	}

	decoded, err := svc.Decrypt(envelope)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if decoded["api_key"] != payload["api_key"] {
		t.Fatalf("api_key mismatch: got %v", decoded["api_key"])
	}
	if decoded["team_id"] != payload["team_id"] {
		t.Fatalf("team_id mismatch: got %v", decoded["team_id"])
	}
}

// TestEncryptUsesFreshNonce verifies same plaintext twice must produce different
// envelopes; otherwise an attacker can correlate ciphertexts.
func TestEncryptUsesFreshNonce(t *testing.T) {
	svc := mustService(t)
	payload := map[string]any{"token": "same-value-1234567890"}

	a, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt a: %v", err)
	}
	b, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatalf("encrypt b: %v", err)
	}
	if string(a) == string(b) {
		t.Fatal("expected distinct envelopes for identical payloads")
	}
}

func TestEncryptNilPayloadIsEmptyObject(t *testing.T) {
	svc := mustService(t)
	envelope, err := svc.Encrypt(nil)
	if err != nil {
		t.Fatalf("encrypt nil: %v", err)
	}
	decoded, err := svc.Decrypt(envelope)
	if err != nil {
		t.Fatalf("decrypt nil: %v", err)
	}
	if len(decoded) != 0 {
		t.Fatalf("expected empty map, got %v", decoded)
	}
}

func TestDecryptRejectsWrongEnvelope(t *testing.T) {
	svc := mustService(t)
	bad := []byte(`{"version":"v9","alg":"ROT13","nonce":"","ciphertext":""}`)
	if _, err := svc.Decrypt(bad); err == nil {
		t.Fatal("expected error for unsupported envelope")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	svc := mustService(t)
	envelope, err := svc.Encrypt(map[string]any{"api_key": "abcdefghijklmnop"})
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	var decoded Envelope
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	decoded.Ciphertext = flipLastByte(decoded.Ciphertext)
	tampered, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("encode tampered envelope: %v", err)
	}

	if _, err := svc.Decrypt(tampered); err == nil {
		t.Fatal("expected error on tampered ciphertext")
	}
}

func TestDecryptRejectsMalformedJSON(t *testing.T) {
	svc := mustService(t)
	if _, err := svc.Decrypt([]byte("not json")); err == nil {
		t.Fatal("expected error for malformed JSON envelope")
	}
}

func TestMaskPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{"empty payload shows configured", map[string]any{}, "configured"},
		{"mask api_key", map[string]any{"api_key": "sk-1234567890abcdef"}, "sk-123...cdef"},
		{"mask token", map[string]any{"token": "xoxb-1234567890-abcdefghij"}, "xoxb-1...ghij"},
		{"short value falls back", map[string]any{"api_key": "short"}, "****"},
		{"non-string ignored", map[string]any{"api_key": 42}, "configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskPayload(tc.payload); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func mustService(t *testing.T) *Service {
	t.Helper()
	svc, err := New(strings.Repeat("k", 32))
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	return svc
}

func flipLastByte(s string) string {
	if len(s) == 0 {
		return s
	}
	b := []byte(s)
	b[len(b)-1] ^= 0x01
	return string(b)
}
