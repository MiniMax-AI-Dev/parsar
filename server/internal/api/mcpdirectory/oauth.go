package mcpdirectory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	oauthCookieName   = "parsar_mcp_oauth"
	oauthCookieTTL    = 10 * time.Minute
	oauthIntentImport = "import"
)

type oauthCookie struct {
	WorkspaceID string               `json:"workspace_id"`
	CatalogID   string               `json:"catalog_id"`
	UserID      string               `json:"user_id"`
	BaseURL     string               `json:"base_url"`
	Intent      string               `json:"intent,omitempty"`
	Transaction mcpoauth.Transaction `json:"transaction"`
}

// oauthStart godoc
//
//	@Summary Start an MCP connector OAuth flow
//	@Tags mcp-directory
//	@Produce json
//	@Param workspaceID path string true "workspace id"
//	@Param catalogID path string true "catalog item id"
//	@Param intent query string false "post-authorization action; currently import"
//	@Success 302 "Redirect to the provider authorization page"
//	@Failure 400 {object} map[string]string
//	@Failure 404 {object} map[string]string
//	@Failure 503 {object} map[string]string
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
	intent := strings.TrimSpace(r.URL.Query().Get("intent"))
	if intent != "" && intent != oauthIntentImport {
		writeError(w, http.StatusBadRequest, "connector_oauth_intent_unsupported")
		return
	}
	if h.deps.OAuth == nil || h.deps.Secrets == nil || h.deps.WorkspaceCredentials == nil || strings.TrimSpace(h.deps.PublicURL) == "" {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_unavailable")
		return
	}
	baseURL, err := h.oauthBaseURL(r)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_public_url_invalid")
		return
	}
	callbackURL, err := h.callbackURL(baseURL, workspaceID, item.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_public_url_invalid")
		return
	}
	transaction, authorizeURL, err := h.deps.OAuth.Begin(r.Context(), item.Server.URL, callbackURL)
	if err != nil {
		log.Bg().Warn("mcp oauth start failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "connector_oauth_discovery_failed")
		return
	}
	cookieValue, err := h.encryptOAuthCookie(oauthCookie{
		WorkspaceID: workspaceID,
		CatalogID:   item.ID,
		UserID:      auth.UserIDFromContext(r.Context()),
		BaseURL:     baseURL,
		Intent:      intent,
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
	log.Bg().Info("mcp oauth authorization started", "catalog_id", item.ID, "callback_origin", baseURL)
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// oauthCallback godoc
//
//	@Summary Complete an MCP connector OAuth flow
//	@Tags mcp-directory
//	@Produce json
//	@Param workspaceID path string true "workspace id"
//	@Param catalogID path string true "catalog item id"
//	@Param state query string true "OAuth state"
//	@Param code query string true "OAuth authorization code"
//	@Success 302 "Redirect to the connector detail page"
//	@Failure 400 {object} map[string]string
//	@Failure 502 {object} map[string]string
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
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": providerError})
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
	context, err := h.decryptOAuthCookie(cookie.Value)
	if err != nil || context.WorkspaceID != workspaceID || context.CatalogID != item.ID || context.UserID != auth.UserIDFromContext(r.Context()) || context.Transaction.State != state {
		writeError(w, http.StatusBadRequest, "connector_oauth_state_mismatch")
		return
	}
	if time.Since(time.Unix(context.Transaction.IssuedAt, 0)) > oauthCookieTTL {
		writeError(w, http.StatusBadRequest, "connector_oauth_state_expired")
		return
	}
	if h.deps.OAuth == nil || h.deps.Secrets == nil || h.deps.WorkspaceCredentials == nil {
		writeError(w, http.StatusServiceUnavailable, "connector_oauth_unavailable")
		return
	}
	h.clearOAuthCookie(w, workspaceID, item.ID)
	credential, err := h.deps.OAuth.Exchange(r.Context(), context.Transaction, code)
	if err != nil {
		log.Bg().Warn("mcp oauth token exchange failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusBadGateway, "connector_oauth_exchange_failed")
		return
	}
	stored, saveErr := h.saveWorkspaceOAuthCredential(r.Context(), workspaceID, item, credential, auth.UserIDFromContext(r.Context()))
	if saveErr != nil {
		log.Bg().Error("mcp workspace oauth credential persist failed", "catalog_id", item.ID, "error", saveErr)
		writeError(w, http.StatusInternalServerError, "connector_oauth_persist_failed")
		return
	}
	if _, verifyErr := h.verifyStoredWorkspaceOAuthCredential(r.Context(), workspaceID, item, stored); verifyErr != nil {
		log.Bg().Warn("mcp workspace oauth post-authorization verification failed", "catalog_id", item.ID, "error", verifyErr)
	}
	returnBaseURL := context.BaseURL
	if strings.TrimSpace(returnBaseURL) == "" {
		returnBaseURL = h.deps.PublicURL
	}
	redirectURL, err := h.directoryRedirectURL(returnBaseURL, workspaceID, item.ID, context.Intent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connector_oauth_redirect_failed")
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

type oauthConnectionResponse struct {
	Authorized      bool       `json:"authorized"`
	Verified        bool       `json:"verified"`
	Status          string     `json:"status"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ProtocolVersion string     `json:"protocol_version,omitempty"`
	ServerName      string     `json:"server_name,omitempty"`
	ServerVersion   string     `json:"server_version,omitempty"`
	ToolCount       *int       `json:"tool_count,omitempty"`
}

// oauthTest godoc
//
//	@Summary Verify an authorized OAuth MCP connector
//	@Description Refreshes the token when necessary, then completes MCP initialize and tools/list without executing a tool.
//	@Tags mcp-directory
//	@Produce json
//	@Param workspaceID path string true "workspace id"
//	@Param catalogID path string true "catalog item id"
//	@Success 200 {object} oauthConnectionResponse
//	@Failure 404 {object} map[string]string
//	@Failure 503 {object} map[string]string
//	@Router /api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/test [post]
func (h *handler) oauthTest(w http.ResponseWriter, r *http.Request) {
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
	stored, found, err := h.workspaceOAuthCredential(r.Context(), workspaceID, item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connector_oauth_credential_load_failed")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "connector_oauth_not_connected")
		return
	}
	result, err := h.verifyStoredWorkspaceOAuthCredential(r.Context(), workspaceID, item, stored)
	if err != nil {
		log.Bg().Error("mcp workspace oauth verification failed", "catalog_id", item.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "connector_oauth_test_failed")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) verifyOAuthPayload(
	ctx context.Context,
	item mcpcatalog.Item,
	payload map[string]any,
) (map[string]any, mcpoauth.Verification, error) {
	credential, isOAuth, err := mcpoauth.CredentialFromPayload(payload)
	if err != nil {
		return nil, mcpoauth.Verification{}, err
	}
	if !isOAuth {
		return nil, mcpoauth.Verification{}, errors.New("credential is not an MCP OAuth credential")
	}

	checkedAt := time.Now().UTC()
	if credential.NeedsRefresh(checkedAt) {
		refreshed, refreshErr := h.deps.OAuth.Refresh(ctx, credential)
		if refreshErr != nil {
			return payload, mcpoauth.Verification{
				Status:    mcpoauth.VerificationReconnectRequired,
				CheckedAt: checkedAt,
				ErrorCode: "connector_oauth_refresh_failed",
			}, nil
		}
		refreshedPayload := refreshed.Payload()
		mcpoauth.PreserveMetadata(payload, refreshedPayload)
		payload = refreshedPayload
		credential = refreshed
	}

	probe, probeErr := h.deps.OAuth.Probe(ctx, item.Server.URL, credential.AccessToken)
	if probeErr != nil {
		status := mcpoauth.VerificationUnavailable
		errorCode := "connector_oauth_connection_unavailable"
		if errors.Is(probeErr, mcpoauth.ErrUnauthorized) {
			status = mcpoauth.VerificationReconnectRequired
			errorCode = "connector_oauth_reconnect_required"
		}
		log.Bg().Warn("mcp oauth connection probe failed", "catalog_id", item.ID, "error", probeErr)
		return payload, mcpoauth.Verification{
			Status:    status,
			CheckedAt: checkedAt,
			ErrorCode: errorCode,
		}, nil
	}
	return payload, mcpoauth.Verification{
		Status:          mcpoauth.VerificationVerified,
		CheckedAt:       checkedAt,
		ProtocolVersion: probe.ProtocolVersion,
		ServerName:      probe.ServerName,
		ServerVersion:   probe.ServerVersion,
		ToolCount:       probe.ToolCount,
	}, nil
}

func oauthConnectionResult(verification mcpoauth.Verification) oauthConnectionResponse {
	result := oauthConnectionResponse{
		Authorized:      true,
		Verified:        verification.Status == mcpoauth.VerificationVerified,
		Status:          verification.Status,
		CheckedAt:       &verification.CheckedAt,
		ErrorCode:       verification.ErrorCode,
		ProtocolVersion: verification.ProtocolVersion,
		ServerName:      verification.ServerName,
		ServerVersion:   verification.ServerVersion,
	}
	toolCount := verification.ToolCount
	result.ToolCount = &toolCount
	return result
}

func (h *handler) oauthItem(w http.ResponseWriter, r *http.Request) (mcpcatalog.Item, bool) {
	snapshot, err := h.deps.Catalog.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mcp_catalog_unavailable")
		return mcpcatalog.Item{}, false
	}
	item, found := snapshot.Find(chi.URLParam(r, "catalogID"))
	if !found {
		writeError(w, http.StatusNotFound, "connector_not_found")
		return mcpcatalog.Item{}, false
	}
	if item.Authentication.EffectiveType() != "oauth2" {
		writeError(w, http.StatusBadRequest, "connector_does_not_use_oauth")
		return mcpcatalog.Item{}, false
	}
	if !item.Authentication.ConnectionSupported() {
		writeError(w, http.StatusConflict, "connector_oauth_approved_client_required")
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
		"base_url":     value.BaseURL,
		"intent":       value.Intent,
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
		WorkspaceID: stringField(payload, "workspace_id"),
		CatalogID:   stringField(payload, "catalog_id"),
		UserID:      stringField(payload, "user_id"),
		BaseURL:     stringField(payload, "base_url"),
		Intent:      stringField(payload, "intent"),
	}
	if err := json.Unmarshal([]byte(stringField(payload, "transaction")), &result.Transaction); err != nil {
		return oauthCookie{}, err
	}
	return result, nil
}

func (h *handler) callbackURL(baseURL, workspaceID, catalogID string) (string, error) {
	return publicURLFor(baseURL, oauthCookiePath(workspaceID, catalogID)+"/callback")
}

func (h *handler) directoryRedirectURL(baseURL, workspaceID, catalogID, intent string) (string, error) {
	redirectURL, err := publicURLFor(baseURL, "/")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(redirectURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("admin", "capabilities")
	query.Set("tab", "marketplace")
	query.Set("ws", workspaceID)
	query.Set("item", "mcp:"+catalogID)
	query.Set("connected", catalogID)
	if intent == oauthIntentImport {
		query.Set("import", catalogID)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
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

// oauthBaseURL keeps production callbacks pinned to PARSAR_PUBLIC_URL. In
// loopback development only, it follows the host the browser actually used
// (localhost, 127.0.0.1, or ::1) so OAuth cookies are not split across hosts.
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

func stringField(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}
