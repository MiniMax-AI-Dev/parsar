package runtimecrypto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// wireFixture matches the JSON shape emitted by cmd/emit-fixture and
// committed to testdata/wire_v1.json. Explicit fields so a future
// schema bump (wire_format != nacl_sealed_box_v1) breaks decoding
// instead of silently passing.
type wireFixture struct {
	Description      string `json:"description"`
	WireFormat       string `json:"wire_format"`
	RecipientPubB64  string `json:"recipient_public_key_b64"`
	RecipientPrivB64 string `json:"recipient_private_key_b64"`
	CipherB64        string `json:"cipher_b64"`
	PlaintextUTF8    string `json:"plaintext_utf8"`
}

// TestOpenStaticWireFixture decrypts the committed wire vector via
// the production OpenSeal path. Canonical regression guard for
// cross-language protocol drift — if either side's algorithm
// (Go nacl/box, Node tweetnacl + @noble/hashes) changes, this fails
// on the first commit after the drift.
func TestOpenStaticWireFixture(t *testing.T) {
	path := filepath.Join("testdata", "wire_v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f wireFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if f.WireFormat != "nacl_sealed_box_v1" {
		t.Fatalf("wire_format=%q, want nacl_sealed_box_v1 (test is locked to v1)", f.WireFormat)
	}
	pub, err := DecodeKey(f.RecipientPubB64)
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	priv, err := DecodeKey(f.RecipientPrivB64)
	if err != nil {
		t.Fatalf("decode priv: %v", err)
	}
	got, err := OpenSeal(f.CipherB64, pub, priv)
	if err != nil {
		t.Fatalf("open static fixture: %v", err)
	}
	if string(got) != f.PlaintextUTF8 {
		t.Errorf("plaintext mismatch:\n  got  %q\n  want %q", got, f.PlaintextUTF8)
	}
}
