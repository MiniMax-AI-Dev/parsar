package otlp

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

// Bearer-token contract for the embedded OTLP receiver.
//
// Tokens are stateless HMAC-SHA256 over a small JSON payload: validation
// is pure HMAC + JSON unmarshal with no DB round-trip on the hot path
// of audit ingestion. Revoke is by `exp` (24h cap); the per-run token
// never outlives the agent_run it was minted for.
//
// Wire format:
//
//	<base64url(payload-json)>.<base64url(hmac-sha256(payload-json))>
//
// Padding-stripped base64url so the token rides in an Authorization
// header without escaping. Adding a payload field is a wire-compat
// decision and must coincide with a Verifier update.

// TokenClaims is the payload carried inside every signed token. Field
// names are short to keep the encoded token compact.
//
//   - WorkspaceID is enforced at the receiver: any `parsar.workspace_id`
//     attribute on the OTLP payload is overridden by this value so a
//     compromised tool cannot cross-workspace forge.
//   - AgentRunID anchors every emitted audit.Event to a specific run.
//   - SandboxID is optional — non-sandbox runs (e.g. host-local
//     opencode) still ship lifecycle events without a sandbox dimension.
//   - IssuedAt / ExpiresAt are unix seconds. ExpiresAt is required;
//     missing or zero is rejected so a malformed signer cannot mint
//     a forever-valid token.
type TokenClaims struct {
	WorkspaceID string `json:"ws"`
	AgentRunID  string `json:"run"`
	SandboxID   string `json:"sb,omitempty"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
	// Nonce randomizes the encoded token even for identical claims,
	// avoiding collision-based fingerprinting in proxy access logs.
	// Not validated semantically; only its presence changes the
	// signature.
	Nonce string `json:"n,omitempty"`
}

// MaxTokenLifetime caps how far in the future ExpiresAt may sit.
// 24h matches the expected upper bound of an agent_run; longer-lived
// runs should re-mint.
const MaxTokenLifetime = 24 * time.Hour

var (
	ErrTokenMalformed       = errors.New("otlp: token is malformed")
	ErrTokenSignatureBad    = errors.New("otlp: token signature does not match")
	ErrTokenExpired         = errors.New("otlp: token has expired")
	ErrTokenMissingClaim    = errors.New("otlp: token is missing a required claim")
	ErrTokenLifetimeTooLong = errors.New("otlp: requested token lifetime exceeds max")
	ErrSignerNotConfigured  = errors.New("otlp: signer has no signing key configured")
)

// TokenSigner mints + verifies bearer tokens. The signing key is a
// process-level secret. Constructor refuses an empty key so the
// embedded receiver never accidentally accepts every request.
type TokenSigner struct {
	key []byte
	now func() time.Time
}

// SignerOptions configures a TokenSigner. Now is injectable for
// deterministic tests; production wires time.Now.
type SignerOptions struct {
	Now func() time.Time
}

// NewSigner returns a TokenSigner backed by the supplied key. Empty
// key is rejected with ErrSignerNotConfigured.
func NewSigner(signingKey string, opts SignerOptions) (*TokenSigner, error) {
	if strings.TrimSpace(signingKey) == "" {
		return nil, ErrSignerNotConfigured
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &TokenSigner{
		key: []byte(signingKey),
		now: now,
	}, nil
}

// Sign produces a bearer token for the supplied claims. The signer is
// the single source of "now", so lifetime is supplied separately and
// the MaxTokenLifetime cap is enforced uniformly.
func (s *TokenSigner) Sign(claims TokenClaims, lifetime time.Duration) (string, error) {
	if s == nil {
		return "", ErrSignerNotConfigured
	}
	if lifetime <= 0 || lifetime > MaxTokenLifetime {
		return "", fmt.Errorf("%w: got %s, max %s",
			ErrTokenLifetimeTooLong, lifetime, MaxTokenLifetime)
	}
	if strings.TrimSpace(claims.WorkspaceID) == "" || strings.TrimSpace(claims.AgentRunID) == "" {
		return "", fmt.Errorf("%w: workspace_id and agent_run_id required",
			ErrTokenMissingClaim)
	}

	now := s.now()
	claims.IssuedAt = now.Unix()
	claims.ExpiresAt = now.Add(lifetime).Unix()
	if claims.Nonce == "" {
		nonce, err := randomNonce()
		if err != nil {
			return "", fmt.Errorf("otlp: generate nonce: %w", err)
		}
		claims.Nonce = nonce
	}

	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("otlp: marshal claims: %w", err)
	}

	mac := hmac.New(sha256.New, s.key)
	mac.Write(payloadJSON)
	sig := mac.Sum(nil)

	return base64URL(payloadJSON) + "." + base64URL(sig), nil
}

// Verify decodes a bearer token, validates the signature with
// hmac.Equal (constant-time), and checks expiry. The signature MUST
// be checked before the JSON unmarshal output is trusted.
func (s *TokenSigner) Verify(token string) (TokenClaims, error) {
	if s == nil {
		return TokenClaims{}, ErrSignerNotConfigured
	}
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TokenClaims{}, ErrTokenMalformed
	}

	payloadBytes, err := decodeURL(parts[0])
	if err != nil {
		return TokenClaims{}, fmt.Errorf("%w: payload base64: %v",
			ErrTokenMalformed, err)
	}
	sigBytes, err := decodeURL(parts[1])
	if err != nil {
		return TokenClaims{}, fmt.Errorf("%w: signature base64: %v",
			ErrTokenMalformed, err)
	}

	mac := hmac.New(sha256.New, s.key)
	mac.Write(payloadBytes)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, sigBytes) {
		return TokenClaims{}, ErrTokenSignatureBad
	}

	var claims TokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return TokenClaims{}, fmt.Errorf("%w: payload json: %v",
			ErrTokenMalformed, err)
	}

	if claims.WorkspaceID == "" || claims.AgentRunID == "" || claims.ExpiresAt == 0 {
		return TokenClaims{}, ErrTokenMissingClaim
	}
	if s.now().Unix() >= claims.ExpiresAt {
		return TokenClaims{}, ErrTokenExpired
	}
	return claims, nil
}

// base64URL is the padding-stripped URL-safe base64 encoding the
// token format uses.
func base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeURL(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// randomNonce returns 8 bytes of crypto/rand encoded as base64url. A
// crypto/rand failure surfaces as an error rather than silently
// emitting all-zero bytes — an all-zero nonce would defeat the
// fingerprint-prevention purpose of the field.
func randomNonce() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return base64URL(buf[:]), nil
}
