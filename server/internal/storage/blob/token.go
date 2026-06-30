package blob

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

// Proxy-token contract for the PG blob backend. A token authorizes one
// HTTP op (PUT or GET) on one ref for one workspace until it expires.
// Stateless HMAC-SHA256 — validation is pure HMAC + JSON unmarshal, no DB
// round-trip. Wire form mirrors otlp.TokenSigner:
//
//	<base64url(payload-json)>.<base64url(hmac-sha256(payload-json))>

// MaxProxyTokenLifetime caps how far in the future a token may sit.
const MaxProxyTokenLifetime = 24 * time.Hour

var (
	ErrTokenMalformed      = errors.New("blob: proxy token is malformed")
	ErrTokenSignatureBad   = errors.New("blob: proxy token signature does not match")
	ErrTokenExpired        = errors.New("blob: proxy token has expired")
	ErrTokenMissingClaim   = errors.New("blob: proxy token is missing a required claim")
	ErrSignerNotConfigured = errors.New("blob: proxy signer has no signing key configured")
)

// ProxyClaims is the payload carried in a proxy token. Method pins the
// token to a single verb so a GET token can't be replayed as a PUT.
type ProxyClaims struct {
	Ref         string `json:"ref"`
	WorkspaceID string `json:"ws"`
	Method      string `json:"m"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	Nonce       string `json:"n,omitempty"`
}

// ProxySigner mints and verifies proxy tokens. The key is derived from
// the process master key via a domain separator so it is distinct from
// any other HMAC use of the same secret. An empty master key yields a
// disabled signer (Enabled()==false) that refuses to sign.
type ProxySigner struct {
	key []byte
	now func() time.Time
}

// NewProxySigner derives the signing key from masterKey. Empty masterKey
// produces a disabled signer rather than an error so construction is
// infallible at wiring time; Sign/Verify then return ErrSignerNotConfigured.
func NewProxySigner(masterKey string) *ProxySigner {
	var key []byte
	if strings.TrimSpace(masterKey) != "" {
		mac := hmac.New(sha256.New, []byte(masterKey))
		mac.Write([]byte("parsar-blob-proxy-v1"))
		key = mac.Sum(nil)
	}
	return &ProxySigner{key: key, now: time.Now}
}

// Enabled reports whether the signer has a key (master key was set).
func (s *ProxySigner) Enabled() bool { return s != nil && len(s.key) > 0 }

func (s *ProxySigner) Sign(claims ProxyClaims, lifetime time.Duration) (string, error) {
	if !s.Enabled() {
		return "", ErrSignerNotConfigured
	}
	if lifetime <= 0 || lifetime > MaxProxyTokenLifetime {
		return "", fmt.Errorf("blob: token lifetime %s out of range (max %s)", lifetime, MaxProxyTokenLifetime)
	}
	if strings.TrimSpace(claims.Ref) == "" || strings.TrimSpace(claims.WorkspaceID) == "" || strings.TrimSpace(claims.Method) == "" {
		return "", ErrTokenMissingClaim
	}
	now := s.now()
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(lifetime).Unix()
	if claims.Nonce == "" {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("blob: generate nonce: %w", err)
		}
		claims.Nonce = base64.RawURLEncoding.EncodeToString(buf[:])
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("blob: marshal claims: %w", err)
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *ProxySigner) Verify(token string) (ProxyClaims, error) {
	if !s.Enabled() {
		return ProxyClaims{}, ErrSignerNotConfigured
	}
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ProxyClaims{}, ErrTokenMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ProxyClaims{}, fmt.Errorf("%w: payload base64: %v", ErrTokenMalformed, err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ProxyClaims{}, fmt.Errorf("%w: signature base64: %v", ErrTokenMalformed, err)
	}
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return ProxyClaims{}, ErrTokenSignatureBad
	}
	var claims ProxyClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ProxyClaims{}, fmt.Errorf("%w: payload json: %v", ErrTokenMalformed, err)
	}
	if claims.Ref == "" || claims.WorkspaceID == "" || claims.Method == "" || claims.ExpiresAt == 0 {
		return ProxyClaims{}, ErrTokenMissingClaim
	}
	if s.now().Unix() >= claims.ExpiresAt {
		return ProxyClaims{}, ErrTokenExpired
	}
	return claims, nil
}
