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
	authoidc "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/oidc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// OAuthRuntimeStore is the narrow surface the OAuth callback uses, so
// dev/routes_test.go can fake it without satisfying RuntimeStore (50+ methods).
type OAuthRuntimeStore interface {
	UpsertOAuthUser(ctx context.Context, in store.UpsertOAuthUserInput) (store.UpsertOAuthUserResult, error)
}

// CookieStateName is the short-lived CSRF cookie name.
const CookieStateName = "parsar_oauth_state"

// CookieNonceName is the short-lived OIDC nonce cookie name.
const CookieNonceName = "parsar_oidc_nonce"

// stateCookieTTL is the wall time the state cookie is valid for —
// generous because the user may pause on the Feishu consent page.
const stateCookieTTL = 10 * time.Minute

// stateRandBytes drives the CSRF token length. 128-bit nonce.
const stateRandBytes = 16

// OAuthHandlerDeps wires the three deps the Feishu callback needs.
type OAuthHandlerDeps struct {
	Feishu   feishu.Client
	OIDC     map[string]authoidc.Client
	Sessions auth.SessionStore
	Store    OAuthRuntimeStore

	// CookieSecure drives the Secure attribute on issued cookies.
	CookieSecure bool

	// LoginRedirectURL is where the browser bounces after a successful
	// Feishu callback. Defaults to "/" (server-served SPA). In dev set
	// to the Vite origin so the post-login bounce lands on the dev frontend.
	LoginRedirectURL string
}

type oauthProfile struct {
	Provider string
	Subject  string
	Email    string
	Name     string
	Metadata map[string]any
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

// oidcStartHandler is GET /api/v1/auth/oidc/{providerID}/start. It mints
// CSRF state plus an OIDC nonce, then redirects to the configured provider.
//
//	@Summary		Start generic OIDC login
//	@Description	Mints CSRF and nonce cookies and redirects the browser to the configured OIDC provider authorize URL.
//	@Tags			auth
//	@ID				startOIDCOAuth
//	@Produce		json
//	@Param			providerID	path	string	true	"OIDC provider ID"
//	@Success		302	"Redirect to OIDC authorize URL"
//	@Failure		404	{object}	map[string]string	"OIDC provider is not configured"
//	@Failure		500	{object}	map[string]string	"State minting or discovery failed"
//	@Router			/api/v1/auth/oidc/{providerID}/start [get]
func oidcStartHandler(deps OAuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providerID := strings.TrimSpace(chi.URLParam(r, "providerID"))
		client := deps.OIDC[providerID]
		if client == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "OIDC provider not configured"})
			return
		}
		state, err := newOAuthState()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mint CSRF state"})
			return
		}
		nonce, err := newOAuthState()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mint OIDC nonce"})
			return
		}
		loginURL, err := client.AuthorizeURL(r.Context(), state, nonce)
		if err != nil {
			log.Bg().Warn("OIDC discovery failed", "provider", providerID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("OIDC discovery failed: %v", err)})
			return
		}
		cookiePath := oidcCookiePath(providerID)
		http.SetCookie(w, &http.Cookie{
			Name:     CookieStateName,
			Value:    state,
			Path:     cookiePath,
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(stateCookieTTL.Seconds()),
		})
		http.SetCookie(w, &http.Cookie{
			Name:     CookieNonceName,
			Value:    nonce,
			Path:     cookiePath,
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(stateCookieTTL.Seconds()),
		})
		http.Redirect(w, r, loginURL, http.StatusFound)
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

		loginProfile := oauthProfile{
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
		}
		completeOAuthLogin(w, r, deps, loginProfile)
	}
}

// oidcCallbackHandler is GET /api/v1/auth/oidc/{providerID}/callback. It
// verifies state and nonce, exchanges the code, upserts the identity, and
// issues a Parsar session cookie.
//
//	@Summary		Handle generic OIDC callback
//	@Description	Verifies CSRF state and OIDC nonce, exchanges the authorization code, upserts the user identity, creates a session cookie, and redirects to the login-success URL.
//	@Tags			auth
//	@ID				callbackOIDCOAuth
//	@Produce		json
//	@Param			providerID	path	string	true	"OIDC provider ID"
//	@Param			state		query	string	true	"CSRF state minted at /start"
//	@Param			code		query	string	true	"OAuth authorization code returned by the OIDC provider"
//	@Success		302		"Redirect to login-success URL"
//	@Failure		400		{object}	map[string]string	"Missing state/code or state/nonce mismatch"
//	@Failure		404		{object}	map[string]string	"OIDC provider is not configured"
//	@Failure		500		{object}	map[string]string	"User upsert or session creation failed"
//	@Failure		502		{object}	map[string]string	"OIDC exchange failed"
//	@Router			/api/v1/auth/oidc/{providerID}/callback [get]
func oidcCallbackHandler(deps OAuthHandlerDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Sessions == nil || deps.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "OIDC login not configured (missing session store / user store dep)",
			})
			return
		}
		providerID := strings.TrimSpace(chi.URLParam(r, "providerID"))
		client := deps.OIDC[providerID]
		if client == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "OIDC provider not configured"})
			return
		}
		urlState := strings.TrimSpace(r.URL.Query().Get("state"))
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if urlState == "" || code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state or code"})
			return
		}
		cookiePath := oidcCookiePath(providerID)
		stateCookie, err := r.Cookie(CookieStateName)
		if err != nil || stateCookie.Value != urlState {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CSRF state mismatch — re-start the login flow"})
			return
		}
		nonceCookie, err := r.Cookie(CookieNonceName)
		if err != nil || strings.TrimSpace(nonceCookie.Value) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OIDC nonce missing — re-start the login flow"})
			return
		}
		clearOAuthCookie(w, CookieStateName, cookiePath, deps.CookieSecure)
		clearOAuthCookie(w, CookieNonceName, cookiePath, deps.CookieSecure)

		profile, err := client.ExchangeCode(r.Context(), code, nonceCookie.Value)
		if err != nil {
			log.Bg().Warn("OIDC exchange failed", "provider", providerID, "error", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": fmt.Sprintf("OIDC exchange failed: %v", err),
			})
			return
		}
		completeOAuthLogin(w, r, deps, oauthProfile{
			Provider: profile.Provider,
			Subject:  profile.Subject,
			Email:    profile.Email,
			Name:     profile.Name,
			Metadata: map[string]any{
				"issuer":      profile.Issuer,
				"name":        profile.Name,
				"avatar_url":  profile.AvatarURL,
				"provider_id": profile.ProviderID,
				"claims":      profile.Claims,
				"userinfo":    profile.UserInfo,
			},
		})
	}
}

func completeOAuthLogin(w http.ResponseWriter, r *http.Request, deps OAuthHandlerDeps, profile oauthProfile) {
	now := time.Now().UTC()
	upsert, err := deps.Store.UpsertOAuthUser(r.Context(), store.UpsertOAuthUserInput{
		Provider: profile.Provider,
		Subject:  profile.Subject,
		Email:    profile.Email,
		Name:     profile.Name,
		Metadata: profile.Metadata,
		Now:      now,
	})
	if err != nil {
		log.Bg().Error("OAuth user upsert failed", "error", err, "provider", profile.Provider, "email", profile.Email)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist user identity"})
		return
	}
	sessionID, err := deps.Sessions.Create(r.Context(), auth.CreateSessionInput{
		UserID:    upsert.UserID,
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
	})
	if err != nil {
		log.Bg().Error("session create failed after OAuth upsert", "error", err, "user_id", upsert.UserID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
		return
	}
	auth.IssueCookie(w, sessionID, 0, deps.CookieSecure)
	log.Bg().Info("OAuth login success",
		"provider", profile.Provider, "user_id", upsert.UserID, "email", upsert.Email, "created", upsert.Created)
	http.Redirect(w, r, deps.loginRedirectURL(), http.StatusFound)
}

func clearOAuthCookie(w http.ResponseWriter, name, path string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func oidcCookiePath(providerID string) string {
	return "/api/v1/auth/oidc/" + strings.TrimSpace(providerID)
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
