package dev

import (
	"context"
	"fmt"

	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	authgithub "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/github"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const githubOAuthStateCookieName = "parsar_github_oauth_state"

// GitHubConnectionDeps wires the personal GitHub OAuth connection flow.
// It stores the resulting access token as the current user's github_pat
// credential so existing Agent capability credential injection can reuse it.
type GitHubConnectionDeps struct {
	Client       authgithub.Client
	RedirectURL  string
	CookieSecure bool
}

func (d GitHubConnectionDeps) redirectURL() string {
	return adminRedirectURL(d.RedirectURL, "connections", "github")
}

// githubConnectionStartHandler is GET /api/v1/connections/github/start.
// Mints a CSRF state cookie and 302-redirects the browser to GitHub's OAuth
// authorize URL so the current user can grant access to their account.
//
//	@Summary		Start GitHub personal OAuth connection
//	@Description	Mints a CSRF state cookie and 302-redirects to GitHub's authorize URL. The resulting access token is later stored as the caller's github_pat credential.
//	@Tags			connections
//	@ID				startDevGitHubConnection
//	@Produce		json
//	@Success		302	"Redirect to GitHub authorize URL"
//	@Failure		401	{object}	map[string]string	"Not authenticated"
//	@Failure		500	{object}	map[string]string	"CSRF state minting failed"
//	@Failure		503	{object}	map[string]string	"GitHub OAuth not configured"
//	@Router			/api/v1/connections/github/start [get]
func githubConnectionStartHandler(runtimeStore RuntimeStore, deps GitHubConnectionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireAuthenticatedUser(w, r, runtimeStore); !ok {
			return
		}
		if deps.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "github OAuth not configured — set PARSAR_GITHUB_CLIENT_ID / CLIENT_SECRET / REDIRECT_URI",
			})
			return
		}
		state, err := newOAuthState()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mint CSRF state"})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     githubOAuthStateCookieName,
			Value:    state,
			Path:     "/api/v1/connections/github",
			HttpOnly: true,
			Secure:   deps.CookieSecure,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(stateCookieTTL.Seconds()),
		})
		http.Redirect(w, r, deps.Client.AuthorizeURL(state), http.StatusFound)
	}
}

// githubConnectionCallbackHandler is GET /api/v1/connections/github/callback.
// Verifies CSRF state, exchanges the code for an access token, persists it
// as the current user's github_pat credential, then redirects back to the
// admin Connections view.
//
//	@Summary		Handle GitHub OAuth callback
//	@Description	Verifies CSRF state, exchanges the code for a GitHub access token, persists it as the caller's github_pat credential, and 302-redirects to the admin Connections view.
//	@Tags			connections
//	@ID				callbackDevGitHubConnection
//	@Produce		json
//	@Param			state	query		string	true	"CSRF state minted at /start"
//	@Param			code	query		string	true	"OAuth authorization code returned by GitHub"
//	@Success		302		"Redirect to admin Connections view"
//	@Failure		400		{object}	map[string]string	"Missing state/code or CSRF mismatch"
//	@Failure		401		{object}	map[string]string	"Not authenticated"
//	@Failure		502		{object}	map[string]string	"GitHub OAuth exchange failed"
//	@Failure		503		{object}	map[string]string	"GitHub OAuth not configured"
//	@Router			/api/v1/connections/github/callback [get]
func githubConnectionCallbackHandler(runtimeStore RuntimeStore, deps GitHubConnectionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireAuthenticatedUser(w, r, runtimeStore)
		if !ok {
			return
		}
		if deps.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "github OAuth not configured — set PARSAR_GITHUB_CLIENT_ID / CLIENT_SECRET / REDIRECT_URI",
			})
			return
		}
		urlState := strings.TrimSpace(r.URL.Query().Get("state"))
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		if urlState == "" || code == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing state or code"})
			return
		}
		cookie, err := r.Cookie(githubOAuthStateCookieName)
		if err != nil || cookie.Value != urlState {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "CSRF state mismatch — re-start the GitHub connection flow"})
			return
		}
		clearGitHubOAuthStateCookie(w, deps.CookieSecure)

		credential, err := deps.Client.ExchangeCode(r.Context(), code)
		if err != nil {
			log.Bg().Warn("github OAuth exchange failed", "error", err, "user_id", userID)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("github OAuth exchange failed: %v", err)})
			return
		}
		created, err := upsertGitHubUserCredential(r.Context(), runtimeStore, userID, credential)
		if err != nil {
			log.Bg().Error("github OAuth credential persist failed", "error", err, "user_id", userID, "github_login", credential.Login)
			writeCapabilityError(w, err, "failed to persist github credential")
			return
		}
		log.Bg().Info("github OAuth credential saved", "user_id", userID, "credential_id", created.ID, "github_login", credential.Login)
		http.Redirect(w, r, deps.redirectURL(), http.StatusFound)
	}
}

func upsertGitHubUserCredential(ctx context.Context, runtimeStore RuntimeStore, userID string, credential authgithub.Credential) (store.UserCredentialRead, error) {
	encrypted, err := encryptGitHubToken(credential)
	if err != nil {
		return store.UserCredentialRead{}, err
	}
	displayName := githubCredentialDisplayName(credential)
	credentials, err := runtimeStore.ListUserCredentials(ctx, userID)
	if err != nil {
		return store.UserCredentialRead{}, err
	}
	for _, existing := range credentials {
		if existing.Kind == "github_pat" {
			return runtimeStore.UpdateUserCredential(ctx, store.UpdateUserCredentialInput{
				CredentialID:   existing.ID,
				DisplayName:    &displayName,
				EncryptedValue: encrypted,
				KeyVersion:     secrets.EnvelopeVersion,
			})
		}
	}
	return runtimeStore.CreateUserCredential(ctx, store.CreateUserCredentialInput{
		UserID:         userID,
		Kind:           "github_pat",
		DisplayName:    displayName,
		EncryptedValue: encrypted,
		KeyVersion:     secrets.EnvelopeVersion,
	})
}

func encryptGitHubToken(credential authgithub.Credential) ([]byte, error) {
	secretService, err := secrets.New(strings.TrimSpace(os.Getenv("PARSAR_MASTER_KEY")))
	if err != nil {
		return nil, fmt.Errorf("secrets service unavailable: %w", err)
	}
	payload := map[string]any{
		"value":      strings.TrimSpace(credential.AccessToken),
		"provider":   "github",
		"credential": "oauth_access_token",
		"token_type": credential.TokenType,
		"scope":      credential.Scope,
		"login":      credential.Login,
		"github_id":  credential.UserID,
		"name":       credential.Name,
		"avatar_url": credential.AvatarURL,
		"git_hint":   "use as HTTPS password or API Bearer token",
	}
	return secretService.Encrypt(payload)
}

func githubCredentialDisplayName(credential authgithub.Credential) string {
	login := strings.TrimSpace(credential.Login)
	if login == "" {
		return "GitHub OAuth"
	}
	return "GitHub @" + login
}

func clearGitHubOAuthStateCookie(w http.ResponseWriter, cookieSecure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     githubOAuthStateCookieName,
		Value:    "",
		Path:     "/api/v1/connections/github",
		HttpOnly: true,
		Secure:   cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func adminRedirectURL(base string, adminView string, connected string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "/"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "/?admin=" + url.QueryEscape(adminView)
	}
	q := u.Query()
	q.Set("admin", adminView)
	if connected != "" {
		q.Set("connected", connected)
	}
	u.RawQuery = q.Encode()
	return u.String()
}
