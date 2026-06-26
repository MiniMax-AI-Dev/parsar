package store

import (
	"strings"
	"testing"
)

// Mint output must round-trip through Hash or the pair handshake can't find the row.
func TestRuntimePairingTokenHashRoundTrip(t *testing.T) {
	plaintext, hash, err := MintRuntimePairingToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(plaintext, RuntimePairingTokenPrefix) {
		t.Fatalf("plaintext missing prefix: %q", plaintext)
	}
	if got, want := len(plaintext), len(RuntimePairingTokenPrefix)+64; got != want {
		t.Fatalf("plaintext length %d, want %d", got, want)
	}
	if got := HashRuntimePairingToken(plaintext); got != hash {
		t.Fatalf("hash mismatch: minted %q, recomputed %q", hash, got)
	}
}

// Operators paste tokens via chat; Hash must trim or the row lookup silently fails.
func TestRuntimePairingTokenHashTrimsWhitespace(t *testing.T) {
	plaintext, hash, err := MintRuntimePairingToken()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	cases := []string{
		plaintext + "\n",
		" " + plaintext,
		"\t" + plaintext + " \n",
	}
	for _, c := range cases {
		if got := HashRuntimePairingToken(c); got != hash {
			t.Errorf("hash(%q) = %q, want %q", c, got, hash)
		}
	}
}

// Catches regressions where someone seeds rand with a constant.
func TestRuntimePairingTokenUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 16; i++ {
		plaintext, hash, err := MintRuntimePairingToken()
		if err != nil {
			t.Fatalf("mint %d: %v", i, err)
		}
		if seen[plaintext] {
			t.Fatalf("duplicate plaintext at i=%d: %q", i, plaintext)
		}
		if seen[hash] {
			t.Fatalf("duplicate hash at i=%d: %q", i, hash)
		}
		seen[plaintext] = true
		seen[hash] = true
	}
}

// Runner credential prefix must differ from pairing token's so leaks classify.
func TestRuntimeCredentialMintHashRoundTrip(t *testing.T) {
	plaintext, hash, err := MintRuntimeCredential()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(plaintext, RuntimeCredentialPrefix) {
		t.Fatalf("credential missing prefix: %q", plaintext)
	}
	if got, want := len(plaintext), len(RuntimeCredentialPrefix)+64; got != want {
		t.Fatalf("credential length %d, want %d", got, want)
	}
	if got := HashRuntimeCredential(plaintext); got != hash {
		t.Fatalf("hash mismatch: minted %q, recomputed %q", hash, got)
	}
	if RuntimeCredentialPrefix == RuntimePairingTokenPrefix {
		t.Fatal("credential and pairing-token prefixes must differ for leak classification")
	}
}
