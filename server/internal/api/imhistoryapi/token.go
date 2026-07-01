package imhistoryapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrEmptySecret guards against a misconfigured signer that would mint tokens
// anyone could forge (an empty HMAC key). Construction fails loudly instead.
var ErrEmptySecret = errors.New("imhistory: signing secret is empty")

// Signer mints and verifies per-conversation access tokens. A token is
// hex(HMAC-SHA256(conversation_id, secret)); verification is constant-time.
// The zero value is unusable — build one with NewSigner.
type Signer struct {
	secret []byte
}

// NewSigner returns a Signer over secret. An empty secret is rejected so a
// deployment can never fall back to unauthenticated access silently.
func NewSigner(secret string) (*Signer, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrEmptySecret
	}
	return &Signer{secret: []byte(secret)}, nil
}

// Token returns the access token for a conversation id.
func (s *Signer) Token(conversationID string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(strings.TrimSpace(conversationID)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether token authorizes access to conversationID. It is
// constant-time and tolerates malformed hex without leaking which check failed.
func (s *Signer) Verify(conversationID, token string) bool {
	want, err := hex.DecodeString(strings.TrimSpace(token))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(strings.TrimSpace(conversationID)))
	return hmac.Equal(want, mac.Sum(nil))
}
