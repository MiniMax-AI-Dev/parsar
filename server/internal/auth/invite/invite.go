package invite

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Wire format: <base64url(payload-json)>.<base64url(hmac-sha256(payload-json))>
// Same structure as the OTLP token signer, but with a distinct derived key
// and different claims. Tokens are URL-safe and compact enough for IM sharing.

type Claims struct {
	WorkspaceID string `json:"ws"`
	Email       string `json:"e"`
	Role        string `json:"r"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	Nonce       string `json:"n,omitempty"`
}

const MaxLifetime = 72 * time.Hour

var (
	ErrMalformed      = errors.New("invite: token is malformed")
	ErrSignatureBad   = errors.New("invite: token signature does not match")
	ErrExpired        = errors.New("invite: token has expired")
	ErrMissingClaim   = errors.New("invite: token is missing a required claim")
	ErrNotConfigured  = errors.New("invite: signer has no signing key configured")
	ErrLifetimeTooLong = errors.New("invite: requested lifetime exceeds max")
)

type Signer struct {
	key []byte
	now func() time.Time
}

func NewSigner(masterKey string) (*Signer, error) {
	if strings.TrimSpace(masterKey) == "" {
		return nil, ErrNotConfigured
	}
	// Derive a sub-key so invite tokens are domain-separated from OTLP tokens.
	mac := hmac.New(sha256.New, []byte(masterKey))
	mac.Write([]byte("parsar-invite-v1"))
	return &Signer{key: mac.Sum(nil), now: time.Now}, nil
}

func (s *Signer) Sign(workspaceID, email, role string, lifetime time.Duration) (string, error) {
	if s == nil {
		return "", ErrNotConfigured
	}
	if lifetime <= 0 || lifetime > MaxLifetime {
		return "", fmt.Errorf("%w: got %s, max %s", ErrLifetimeTooLong, lifetime, MaxLifetime)
	}
	if workspaceID == "" || email == "" || role == "" {
		return "", fmt.Errorf("%w: workspace_id, email, and role required", ErrMissingClaim)
	}

	nonce, err := randomNonce()
	if err != nil {
		return "", err
	}

	now := s.now()
	claims := Claims{
		WorkspaceID: workspaceID,
		Email:       email,
		Role:        role,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(lifetime).Unix(),
		Nonce:       nonce,
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("invite: marshal claims: %w", err)
	}

	sig := s.sign(payloadJSON)
	return b64(payloadJSON) + "." + b64(sig), nil
}

func (s *Signer) Verify(token string) (Claims, error) {
	if s == nil {
		return Claims{}, ErrNotConfigured
	}
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Claims{}, ErrMalformed
	}

	payloadBytes, err := unb64(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: payload: %v", ErrMalformed, err)
	}
	sigBytes, err := unb64(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: signature: %v", ErrMalformed, err)
	}

	expected := s.sign(payloadBytes)
	if !hmac.Equal(expected, sigBytes) {
		return Claims{}, ErrSignatureBad
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: json: %v", ErrMalformed, err)
	}
	if claims.WorkspaceID == "" || claims.Email == "" || claims.Role == "" || claims.ExpiresAt == 0 {
		return Claims{}, ErrMissingClaim
	}
	if s.now().Unix() >= claims.ExpiresAt {
		return Claims{}, ErrExpired
	}
	return claims, nil
}

// TokenHash returns SHA-256 of the raw token string for DB storage/lookup.
func TokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func (s *Signer) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

func b64(b []byte) string            { return base64.RawURLEncoding.EncodeToString(b) }
func unb64(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func randomNonce() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("invite: crypto/rand: %w", err)
	}
	return b64(buf[:]), nil
}
