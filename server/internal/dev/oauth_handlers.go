package dev

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// OAuthRuntimeStore is the narrow surface the OAuth callback uses, so
// dev/routes_test.go can fake it without satisfying RuntimeStore (50+ methods).
type OAuthRuntimeStore interface {
	UpsertOAuthUser(ctx context.Context, in store.UpsertOAuthUserInput) (store.UpsertOAuthUserResult, error)
}

// CookieStateName is the short-lived CSRF cookie name.
const CookieStateName = "parsar_oauth_state"

// stateCookieTTL is the wall time the state cookie is valid for —
// generous because the user may pause on the Feishu consent page.
const stateCookieTTL = 10 * time.Minute

// stateRandBytes drives the CSRF token length. 128-bit nonce.
const stateRandBytes = 16

// OAuthHandlerDeps wires the three deps the Feishu callback needs.
type OAuthHandlerDeps struct {
	Feishu   feishu.Client
	Sessions auth.SessionStore
	Store    OAuthRuntimeStore

	// CookieSecure drives the Secure attribute on issued cookies.
	CookieSecure bool

	// LoginRedirectURL is where the browser bounces after a successful
	// Feishu callback. Defaults to "/" (server-served SPA). In dev set
	// to the Vite origin so the post-login bounce lands on the dev frontend.
	LoginRedirectURL string
}

// loginRedirectURL returns the configured success redirect, falling back
// to "/" when unset.
func (d OAuthHandlerDeps) loginRedirectURL() string {
	if d.LoginRedirectURL == "" {
		return "/"
	}
	return d.LoginRedirectURL
}

// feishuStartHandler is GET /api/v1/auth/feishu/start. Mints a CSRF state,
// drops a short-lived state cookie, and 302-redirects to Feishu's
// authorize URL. In mock mode the redirect lands on the callback URL with
// code=mock-code so dev can drive the flow without real credentials.
//
//	@Summary		Start Feishu OIDC login
//	@Description	Mints a CSRF state cookie and 302-redirects the browser to Feishu's authorize URL. Returns 503 when Feishu OIDC is not configured.
//	@Tags			auth
//	@ID				startDevFeishuOAuth
//	@Produce		json
//	@Success		302	"Redirect to Feishu authorize URL"
//	@Failure		500	{object}	map[string]string	"CSRF state minting failed"
//	@Failure		503	{object}	map[string]string	"Feishu OIDC not configured"
//	@Router			/api/v1/auth/feishu/start [get]
func feishuStartHandler(deps OAuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Feishu == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "feishu OIDC not configured — set PARSAR_FEISHU_APP_ID / APP_SECRET / REDIRECT_URI, or PARSAR_FEISHU_MOCK=true for dev",
			})
			return
		}
		state, err := newOAuthState()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mint CSRF state"})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     CookieStateName,
			Value:    state,
			Path:     "/api/v1/auth/feishu",
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(stateCookieTTL.Seconds()),
		})
		http.Redirect(w, r, deps.Feishu.AuthorizeURL(state), http.StatusFound)
	}
}

// feishuCallbackHandler is GET /api/v1/auth/feishu/callback. Verifies the
// CSRF state, exchanges the code for a profile, upserts user + identity,
// creates a session, sets the cookie, then redirects to the admin shell.
//
//	@Summary		Handle Feishu OIDC callback
//	@Description	Verifies CSRF state, exchanges the code for a Feishu profile, upserts the user, creates a session cookie, and 302-redirects to the configured login-success URL.
//	@Tags			auth
//	@ID				callbackDevFeishuOAuth
//	@Produce		json
//	@Param			state	query		string	true	"CSRF state minted at /start"
//	@Param			code	query		string	true	"OAuth authorization code returned by Feishu"
//	@Success		302		"Redirect to login-success URL"
//	@Failure		400		{object}	map[string]string	"Missing state/code or CSRF mismatch"
//	@Failure		500		{object}	map[string]string	"User upsert or session creation failed"
//	@Failure		502		{object}	map[string]string	"Feishu OIDC exchange failed"
//	@Failure		503		{object}	map[string]string	"Feishu OIDC not configured"
//	@Router			/api/v1/auth/feishu/callback [get]
func feishuCallbackHandler(deps OAuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Feishu == nil || deps.Sessions == nil || deps.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "feishu OIDC not configured (missing client / session store / user store dep)",
			})
			return
		}
		urlState := strings.TrimSpace(r.URL.Query().Get("state"))
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if urlState == "" || code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state or code"})
			return
		}
		cookie, err := r.Cookie(CookieStateName)
		if err != nil || cookie.Value != urlState {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CSRF state mismatch — re-start the login flow"})
			return
		}
		// Clear the state cookie regardless of outcome.
		http.SetCookie(w, &http.Cookie{
			Name:     CookieStateName,
			Value:    "",
			Path:     "/api/v1/auth/feishu",
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})

		profile, err := deps.Feishu.ExchangeCode(r.Context(), code)
		if err != nil {
			log.Bg().Warn("feishu OIDC exchange failed", "error", err, "mock", deps.Feishu.IsMock())
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("feishu OIDC exchange failed: %v", err),
			})
			return
		}

		now := time.Now().UTC()
		upsert, err := deps.Store.UpsertOAuthUser(r.Context(), store.UpsertOAuthUserInput{
			Provider: "feishu",
			Subject:  profile.UnionID,
			Email:    profile.Email,
			Name:     profile.Name,
			Metadata: map[string]any{
				"name":       profile.Name,
				"open_id":    profile.OpenID,
				"avatar_url": profile.AvatarURL,
				"mock":       deps.Feishu.IsMock(),
			},
			Now: now,
		})
		if err != nil {
			log.Bg().Error("feishu OIDC user upsert failed", "error", err, "email", profile.Email)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist user identity"})
			return
		}

		sessionID, err := deps.Sessions.Create(r.Context(), auth.CreateSessionInput{
			UserID:    upsert.UserID,
			UserAgent: r.UserAgent(),
			IP:        clientIP(r),
		})
		if err != nil {
			log.Bg().Error("session create failed after OIDC upsert", "error", err, "user_id", upsert.UserID)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
			return
		}
		auth.IssueCookie(w, sessionID, 0, deps.CookieSecure)
		log.Bg().Info("feishu OIDC login success",
			"user_id", upsert.UserID, "email", upsert.Email,
			"created", upsert.Created, "mock", deps.Feishu.IsMock())

		http.Redirect(w, r, deps.loginRedirectURL(), http.StatusFound)
	}
}

// authLogoutHandler is POST /api/v1/auth/logout. Revokes the session and
// clears the cookie. Always 204 — logout is idempotent.
//
//	@Summary		Log out the current session
//	@Description	Revokes the current session cookie server-side and clears it on the client. Idempotent — returns 204 whether or not a session was present.
//	@Tags			auth
//	@ID				logoutDevSession
//	@Produce		json
//	@Success		204	"Session cleared"
//	@Failure		503	{object}	map[string]string	"Session store not wired"
//	@Router			/api/v1/auth/logout [post]
func authLogoutHandler(deps OAuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Sessions == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session store not wired"})
			return
		}
		if cookie, err := r.Cookie(auth.CookieName); err == nil && cookie.Value != "" {
			_ = deps.Sessions.Revoke(r.Context(), cookie.Value, time.Now().UTC())
		}
		auth.ClearCookie(w, deps.CookieSecure)
		w.WriteHeader(http.StatusNoContent)
	}
}

func newOAuthState() (string, error) {
	b := make([]byte, stateRandBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func clientIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		if i := strings.Index(xf, ","); i >= 0 {
			return strings.TrimSpace(xf[:i])
		}
		return strings.TrimSpace(xf)
	}
	if r.RemoteAddr != "" {
		if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
			return r.RemoteAddr[:i]
		}
		return r.RemoteAddr
	}
	return ""
}
