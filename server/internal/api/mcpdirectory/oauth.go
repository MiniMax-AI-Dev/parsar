package mcpdirectory

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/mcpoauth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/mcpcatalog"
)

const (
	oauthCookieName = "parsar_mcp_oauth"
	oauthCookieTTL  = 10 * time.Minute
)

type oauthCookie struct {
	WorkspaceID string               `json:"workspace_id"`
	CatalogID   string               `json:"catalog_id"`
	UserID      string               `json:"user_id"`
	Transaction mcpoauth.Transaction `json:"transaction"`
}

// oauthStart godoc
//
//	@Summary Start an MCP connector OAuth flow
//	@Tags mcp-directory
//	@Param workspaceID path string true "workspace id"
//	@Param catalogID path string true "catalog item id"
//	@Success 302 "Redirect to the provider authorization page"
//	@Failure 400 {object} errorResponse
//	@Failure 404 {object} errorResponse
//	@Failure 503 {object} errorResponse
//	@Router /api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/start [get]
func (h *handler) oauthStart(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeRoles(w, r, "owner", "admin", "member")
	if !ok {
		return
	}
	item, ok := h.oauthItem(w, r)
	if !ok {
		return
	}
	if h.deps.OAuth == nil || h.deps.Secrets == nil || h.deps.WorkspaceCredentials == nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_unavailable")
		return
	}
	baseURL, err := h.oauthBaseURL(r)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_public_url_invalid")
		return
	}
	callbackURL, err := publicURLFor(baseURL, oauthCookiePath(workspaceID, item.ID)+"/callback")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_public_url_invalid")
		return
	}
	transaction, authorizeURL, err := h.deps.OAuth.Begin(r.Context(), item.Server.URL, callbackURL)
	if err != nil {
		log.Warn(r.Context(), "mcp oauth start failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "connector_oauth_discovery_failed")
		return
	}
	cookieValue, err := h.encryptOAuthCookie(oauthCookie{
		WorkspaceID: workspaceID,
		CatalogID:   item.ID,
		UserID:      auth.UserIDFromContext(r.Context()),
		Transaction: transaction,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connector_oauth_state_failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    cookieValue,
		Path:     oauthCookiePath(workspaceID, item.ID),
		HttpOnly: true,
		Secure:   h.deps.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthCookieTTL.Seconds()),
	})
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// oauthCallback godoc
//
//	@Summary Complete an MCP connector OAuth flow
//	@Tags mcp-directory
//	@Param workspaceID path string true "workspace id"
//	@Param catalogID path string true "catalog item id"
//	@Param state query string true "OAuth state"
//	@Param code query string true "OAuth authorization code"
//	@Success 200 "Closes the OAuth popup"
//	@Failure 400 {object} errorResponse
//	@Failure 502 {object} errorResponse
//	@Router /api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/callback [get]
func (h *handler) oauthCallback(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorizeRoles(w, r, "owner", "admin", "member")
	if !ok {
		return
	}
	item, ok := h.oauthItem(w, r)
	if !ok {
		return
	}
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		h.writeOAuthCompletion(w, item.ID, providerError)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "connector_oauth_missing_code_or_state")
		return
	}
	cookie, err := r.Cookie(oauthCookieName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "connector_oauth_state_missing")
		return
	}
	oauthState, err := h.decryptOAuthCookie(cookie.Value)
	if err != nil ||
		oauthState.WorkspaceID != workspaceID ||
		oauthState.CatalogID != item.ID ||
		oauthState.UserID != auth.UserIDFromContext(r.Context()) ||
		oauthState.Transaction.State != state {
		writeError(w, http.StatusBadRequest, "connector_oauth_state_mismatch")
		return
	}
	if time.Since(time.Unix(oauthState.Transaction.IssuedAt, 0)) > oauthCookieTTL {
		writeError(w, http.StatusBadRequest, "connector_oauth_state_expired")
		return
	}
	if h.deps.OAuth == nil || h.deps.Secrets == nil || h.deps.WorkspaceCredentials == nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_unavailable")
		return
	}
	h.clearOAuthCookie(w, workspaceID, item.ID)
	credential, err := h.deps.OAuth.Exchange(r.Context(), oauthState.Transaction, code)
	if err != nil {
		log.Warn(r.Context(), "mcp oauth token exchange failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "connector_oauth_exchange_failed")
		return
	}
	if err := h.saveWorkspaceOAuthCredential(r.Context(), workspaceID, item, credential, auth.UserIDFromContext(r.Context())); err != nil {
		log.Error(r.Context(), "mcp oauth credential persist failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "connector_oauth_persist_failed")
		return
	}
	h.writeOAuthCompletion(w, item.ID, "")
}

func (h *handler) oauthItem(w http.ResponseWriter, r *http.Request) (mcpcatalog.Item, bool) {
	catalog, err := h.deps.Catalog.Load()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mcp_catalog_unavailable")
		return mcpcatalog.Item{}, false
	}
	item, found := catalog.Find(chi.URLParam(r, "catalogID"))
	if !found {
		writeError(w, http.StatusNotFound, "connector_not_found")
		return mcpcatalog.Item{}, false
	}
	if item.Authentication.EffectiveType() != "oauth2" {
		writeError(w, http.StatusBadRequest, "connector_does_not_use_oauth")
		return mcpcatalog.Item{}, false
	}
	return item, true
}

func (h *handler) encryptOAuthCookie(value oauthCookie) (string, error) {
	transactionJSON, err := json.Marshal(value.Transaction)
	if err != nil {
		return "", err
	}
	encrypted, err := h.deps.Secrets.Encrypt(map[string]any{
		"workspace_id": value.WorkspaceID,
		"catalog_id":   value.CatalogID,
		"user_id":      value.UserID,
		"transaction":  string(transactionJSON),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encrypted), nil
}

func (h *handler) decryptOAuthCookie(encoded string) (oauthCookie, error) {
	encrypted, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return oauthCookie{}, err
	}
	payload, err := h.deps.Secrets.Decrypt(encrypted)
	if err != nil {
		return oauthCookie{}, err
	}
	result := oauthCookie{
		WorkspaceID: metadataString(payload, "workspace_id"),
		CatalogID:   metadataString(payload, "catalog_id"),
		UserID:      metadataString(payload, "user_id"),
	}
	if err := json.Unmarshal([]byte(metadataString(payload, "transaction")), &result.Transaction); err != nil {
		return oauthCookie{}, err
	}
	return result, nil
}

func (h *handler) writeOAuthCompletion(w http.ResponseWriter, catalogID, errorCode string) {
	payload, _ := json.Marshal(map[string]string{
		"type":       "parsar:mcp-oauth",
		"catalog_id": catalogID,
		"error":      errorCode,
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Parsar OAuth</title><script>if(window.opener){window.opener.postMessage(%s,window.location.origin)}window.close()</script>`, payload)
}

func publicURLFor(baseURL, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid public url")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func (h *handler) oauthBaseURL(r *http.Request) (string, error) {
	configured, err := url.Parse(strings.TrimSpace(h.deps.PublicURL))
	if err != nil || configured.Host == "" || (configured.Scheme != "http" && configured.Scheme != "https") {
		return "", fmt.Errorf("invalid public url")
	}
	if !isLoopbackHost(configured.Hostname()) {
		return strings.TrimRight(configured.String(), "/"), nil
	}
	requestURL, err := url.Parse("http://" + strings.TrimSpace(r.Host))
	if err != nil || requestURL.Host == "" || !isLoopbackHost(requestURL.Hostname()) {
		return strings.TrimRight(configured.String(), "/"), nil
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	return (&url.URL{Scheme: scheme, Host: requestURL.Host, Path: strings.TrimRight(configured.Path, "/")}).String(), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func (h *handler) clearOAuthCookie(w http.ResponseWriter, workspaceID, catalogID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    "",
		Path:     oauthCookiePath(workspaceID, catalogID),
		HttpOnly: true,
		Secure:   h.deps.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func oauthCookiePath(workspaceID, catalogID string) string {
	return "/api/v1/workspaces/" + url.PathEscape(workspaceID) + "/mcp-directory/" + url.PathEscape(catalogID) + "/oauth"
}
