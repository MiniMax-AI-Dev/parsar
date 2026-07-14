package invite

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
)

const MaxLifetime = 72 * time.Hour

const tokenEntropyBytes = 24

// NewToken returns a 192-bit opaque token whose invitation data remains server-side.
func NewToken() (string, error) {
	var buf [tokenEntropyBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("invite: crypto/rand: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

// TokenHash returns SHA-256 of the raw token string for DB storage/lookup.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
