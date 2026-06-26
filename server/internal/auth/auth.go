// Package auth provides session resolution, cookies, and HTTP
// middleware for authenticating requests.
//
// Session tokens are opaque 256-bit random values stored verbatim as
// the user_sessions PK; hashing is deferred — add via migration if
// DB-confidentiality becomes a requirement.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
)

const CookieName = "parsar_session"

// DevAuthEnv opts into the X-Parsar-Dev-User-ID dev shim. Must be
// unset in production so anonymous requests are rejected.
const DevAuthEnv = "PARSAR_DEV_AUTH"

// DevUserHeader value MUST be a UUID; the middleware does not verify
// it against the users table.
const DevUserHeader = "X-Parsar-Dev-User-ID"

// SessionIDByteLen yields a 64-char hex token (256 bits of entropy).
const SessionIDByteLen = 32

var ErrInvalidSession = errors.New("auth: invalid or expired session")

type userIDCtxKey struct{}

func WithUserID(parent context.Context, userID string) context.Context {
	return context.WithValue(parent, userIDCtxKey{}, userID)
}

func UserIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(userIDCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// NewSessionID returns a fresh 64-char hex session token. Callers
// persist this verbatim as the user_sessions row id AND set it as
// the cookie value.
func NewSessionID() (string, error) {
	b := make([]byte, SessionIDByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: session id rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
