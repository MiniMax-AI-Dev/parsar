package invite

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestNewTokenIsCompactAndURLSafe(t *testing.T) {
	tokenA, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenB, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if tokenA == tokenB {
		t.Fatal("two generated tokens must differ")
	}
	if len(tokenA) != 32 {
		t.Fatalf("token length = %d, want 32", len(tokenA))
	}
	decoded, err := base64.RawURLEncoding.DecodeString(tokenA)
	if err != nil {
		t.Fatalf("token is not raw base64url: %v", err)
	}
	if len(decoded) != tokenEntropyBytes {
		t.Fatalf("decoded token length = %d, want %d", len(decoded), tokenEntropyBytes)
	}
}

func TestTokenHashIsStableAndDoesNotExposeToken(t *testing.T) {
	token := "opaque-invitation-token"
	hashA := TokenHash(token)
	hashB := TokenHash(token)
	if !bytes.Equal(hashA, hashB) {
		t.Fatal("same token produced different hashes")
	}
	if len(hashA) != sha256.Size {
		t.Fatalf("hash length = %d, want %d", len(hashA), sha256.Size)
	}
	if bytes.Equal(hashA, []byte(token)) {
		t.Fatal("hash equals the plaintext token")
	}
}
