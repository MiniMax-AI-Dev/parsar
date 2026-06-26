package auth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

type Middleware struct {
	store SessionStore

	// devAuthEnabled is snapshot at construction so later env
	// mutation cannot flip behaviour mid-process.
	devAuthEnabled bool

	nowFn func() time.Time
}

func NewMiddleware(store SessionStore) *Middleware {
	return &Middleware{
		store:          store,
		devAuthEnabled: parseDevAuth(os.Getenv(DevAuthEnv)),
		nowFn:          func() time.Time { return time.Now().UTC() },
	}
}

// WithDevAuth forces the dev-auth toggle regardless of env.
func (m *Middleware) WithDevAuth(enabled bool) *Middleware {
	m.devAuthEnabled = enabled
	return m
}

func (m *Middleware) WithClock(now func() time.Time) *Middleware {
	if now == nil {
		return m
	}
	m.nowFn = now
	return m
}

// Require returns 401 when no user resolves. Resolution order:
//  1. parsar_session cookie → SessionStore.Resolve.
//  2. If (1) fails AND PARSAR_DEV_AUTH=true, accept
//     X-Parsar-Dev-User-ID as a verbatim user_id (not verified
//     against users table).
//  3. Otherwise 401.
func (m *Middleware) Require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := m.resolve(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := WithUserID(r.Context(), userID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Optional resolves a user when possible but does NOT reject
// anonymous requests. Use for OAuth start/callback where the user
// is mid-flight.
func (m *Middleware) Optional(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := m.resolve(r)
		if err != nil {
			h.ServeHTTP(w, r)
			return
		}
		ctx := WithUserID(r.Context(), userID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) resolve(r *http.Request) (string, error) {
	if cookie, err := r.Cookie(CookieName); err == nil && cookie.Value != "" {
		info, resolveErr := m.store.Resolve(r.Context(), cookie.Value, m.nowFn())
		if resolveErr == nil {
			return info.UserID, nil
		}
		// In production a stale cookie is unauthenticated; only the
		// dev shim path may fall through to the header.
		if !m.devAuthEnabled {
			return "", resolveErr
		}
	}
	if m.devAuthEnabled {
		if h := strings.TrimSpace(r.Header.Get(DevUserHeader)); h != "" {
			return h, nil
		}
	}
	return "", ErrInvalidSession
}

// IssueCookie writes a fresh session cookie. `secure` MUST be true
// behind HTTPS; the helper does not sniff the request scheme because
// proxy headers complicate that — the caller knows its deployment.
func IssueCookie(w http.ResponseWriter, sessionID string, ttl time.Duration, secure bool) {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearCookie removes the session cookie. The user_sessions row is
// revoked separately by the caller.
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func parseDevAuth(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

var _ SessionStore = (*PostgresSessionStore)(nil)

var _ = errors.Is
var _ context.Context
