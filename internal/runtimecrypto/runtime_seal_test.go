package runtimecrypto

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	pub, priv, err := GenerateRuntimeKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	pubArr, err := DecodeKey(pub)
	if err != nil {
		t.Fatalf("decode pub: %v", err)
	}
	privArr, err := DecodeKey(priv)
	if err != nil {
		t.Fatalf("decode priv: %v", err)
	}

	plain := []byte(`{"api_key":"sk-secret","provider":"anthropic"}`)
	cipher, err := SealForRuntime(plain, pub)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if cipher == "" {
		t.Fatal("empty cipher")
	}
	if bytes.Contains([]byte(cipher), []byte("sk-secret")) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := OpenSeal(cipher, pubArr, privArr)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("decrypt mismatch:\n  got  %q\n  want %q", got, plain)
	}
}

// Each call uses a fresh ephemeral keypair so the same plaintext
// yields different ciphertexts.
func TestSealNonDeterministic(t *testing.T) {
	pub, _, err := GenerateRuntimeKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	c1, _ := SealForRuntime([]byte("hello"), pub)
	c2, _ := SealForRuntime([]byte("hello"), pub)
	if c1 == c2 {
		t.Errorf("two seals of same plaintext should differ; got identical")
	}
}

// Tampered ciphertext must fail open (Poly1305 authenticator).
func TestOpenTamperedFails(t *testing.T) {
	pub, priv, _ := GenerateRuntimeKeypair()
	pubArr, _ := DecodeKey(pub)
	privArr, _ := DecodeKey(priv)
	cipher, _ := SealForRuntime([]byte("payload"), pub)

	raw, _ := base64.StdEncoding.DecodeString(cipher)
	raw[len(raw)/2] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := OpenSeal(tampered, pubArr, privArr); err != ErrDecryptFailed {
		t.Errorf("tampered ciphertext: got err=%v, want ErrDecryptFailed", err)
	}
}

// Wrong recipient private key must fail open.
func TestOpenWrongKeyFails(t *testing.T) {
	pub, _, _ := GenerateRuntimeKeypair()
	cipher, _ := SealForRuntime([]byte("payload"), pub)

	otherPub, otherPriv, _ := GenerateRuntimeKeypair()
	otherPubArr, _ := DecodeKey(otherPub)
	otherPrivArr, _ := DecodeKey(otherPriv)
	if _, err := OpenSeal(cipher, otherPubArr, otherPrivArr); err != ErrDecryptFailed {
		t.Errorf("wrong key: got err=%v, want ErrDecryptFailed", err)
	}
}

// Bad public key (wrong length / non-base64) must error, not panic.
func TestSealRejectsBadPublicKey(t *testing.T) {
	cases := []string{
		"",
		"not-base64-!!!",
		base64.StdEncoding.EncodeToString([]byte("short")),
	}
	for _, k := range cases {
		if _, err := SealForRuntime([]byte("x"), k); err != ErrInvalidPublicKey {
			t.Errorf("SealForRuntime(%q): got err=%v, want ErrInvalidPublicKey", k, err)
		}
	}
}

func TestKeypairUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		pub, priv, _ := GenerateRuntimeKeypair()
		if seen[pub] {
			t.Fatalf("duplicate pub at i=%d", i)
		}
		if seen[priv] {
			t.Fatalf("duplicate priv at i=%d", i)
		}
		seen[pub] = true
		seen[priv] = true
	}
}

// Guard against a zero/empty-ciphertext regression if crypto/rand is
// not wired into the build.
func TestSealNotEmptyEvenForEmptyPlaintext(t *testing.T) {
	pub, _, _ := GenerateRuntimeKeypair()
	cipher, err := SealForRuntime([]byte{}, pub)
	if err != nil {
		t.Fatalf("seal empty: %v", err)
	}
	// nacl sealed-box overhead = 32 (ephemeral pub) + 16 (poly1305) = 48
	raw, _ := base64.StdEncoding.DecodeString(cipher)
	if len(raw) < 48 {
		t.Errorf("ciphertext too short: %d bytes, want >= 48", len(raw))
	}
}

func TestRandomReaderAvailable(t *testing.T) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("crypto/rand unavailable: %v", err)
	}
}
