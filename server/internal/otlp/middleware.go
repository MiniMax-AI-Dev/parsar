package otlp

import (
	"context"
	"errors"
	"log/slog"

	"net/http"
	"strings"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// claimsContextKey is unexported so external packages cannot collide
// on the context key.
type claimsContextKey struct{}

// ClaimsFromContext returns the TokenClaims attached to ctx by
// authMiddleware, or false when no claims are present. Absence is a
// programming error — the middleware should have rejected the request
// already; handlers MUST 500 rather than dispatch unattributed events.
func ClaimsFromContext(ctx context.Context) (TokenClaims, bool) {
	c, ok := ctx.Value(claimsContextKey{}).(TokenClaims)
	return c, ok
}

// authMiddleware verifies the bearer token from Authorization and
// attaches the parsed TokenClaims to the request context. Missing /
// malformed / expired / wrong-signature tokens all return HTTP 401
// with an opaque body; nothing about the signing-key state leaks back.
//
// verifier MUST be non-nil — passing nil panics to avoid an
// "open by default" path. nil logger falls back to obslog.Bg.
func authMiddleware(verifier *TokenSigner, logger *slog.Logger) func(http.Handler) http.Handler {
	if verifier == nil {
		panic("otlp.authMiddleware: verifier is nil; receiver constructor should refuse this")
	}
	if logger == nil {
		logger = obslog.Bg()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearer(r.Header.Get("Authorization"))
			if err != nil {
				logger.Warn("otlp auth rejected: bad Authorization header",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"reason", err.Error())
				writeAuthError(w, http.StatusUnauthorized, err.Error())
				return
			}
			claims, err := verifier.Verify(token)
			if err != nil {
				// Wire response stays opaque; detailed reason only
				// reaches the operator log so an external probe
				// cannot distinguish wrong-key / expired / tampered.
				logger.Warn("otlp auth rejected: token verify failed",
					"path", r.URL.Path,
					"remote_addr", r.RemoteAddr,
					"reason", err.Error())
				writeAuthError(w, http.StatusUnauthorized,
					classifyVerifyError(err))
				return
			}
			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractBearer pulls the bearer token from an Authorization header
// value. Per RFC 7235 §2.1 the scheme MUST be followed by whitespace
// before the credential; `Bearer<token>` is rejected.
func extractBearer(header string) (string, error) {
	if header == "" {
		return "", errors.New("missing Authorization header")
	}
	const prefix = "bearer"
	trimmed := strings.TrimSpace(header)
	if len(trimmed) <= len(prefix)+1 ||
		!strings.EqualFold(trimmed[:len(prefix)], prefix) {
		return "", errors.New("Authorization scheme must be Bearer")
	}
	sep := trimmed[len(prefix)]
	if sep != ' ' && sep != '\t' {
		return "", errors.New("Authorization scheme must be Bearer")
	}
	token := strings.TrimLeft(trimmed[len(prefix)+1:], " \t")
	if token == "" {
		return "", errors.New("Bearer token is empty")
	}
	return token, nil
}

// classifyVerifyError collapses every signer error to a single opaque
// "token rejected" string. Distinguishing "expired" from "wrong key"
// at the wire would confirm the signature was valid under the current
// key — a small but real signal a probe could exploit. The switch is
// retained so future signer errors don't fall through to the default
// empty string by accident.
func classifyVerifyError(err error) string {
	switch {
	case errors.Is(err, ErrTokenMalformed),
		errors.Is(err, ErrTokenExpired),
		errors.Is(err, ErrTokenSignatureBad),
		errors.Is(err, ErrTokenMissingClaim),
		errors.Is(err, ErrSignerNotConfigured):
		return "token rejected"
	default:
		return "token rejected"
	}
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}
