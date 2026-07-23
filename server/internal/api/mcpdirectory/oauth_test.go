package mcpdirectory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/mcpoauth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/mcpcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestNotionOAuthFlowSharesCredentialWithWorkspace(t *testing.T) {
	provider := newOAuthProvider(t)
	defer provider.Close()
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeDirectoryStore{role: "member"}
	snapshot := mcpcatalog.Snapshot{Source: mcpcatalog.SourceBuiltin, Catalog: mcpcatalog.Catalog{
		SchemaVersion: 1,
		UpdatedAt:     "2026-07-22T00:00:00Z",
		Items: []mcpcatalog.Item{{
			ID: "notion", Name: "Notion", Description: "Use Notion.",
			Publisher: mcpcatalog.Publisher{Name: "Notion", URL: "https://www.notion.so"},
			Verified:  true, Categories: []string{"Productivity"}, FeaturedRank: 1,
			Version: "1.0.0", Transport: "streamable-http",
			Authentication: mcpcatalog.Authentication{
				Type: "oauth2", CredentialKind: "notion_mcp_oauth",
			},
			Server: mcpcatalog.Server{Name: "notion", URL: provider.URL + "/mcp"},
		}},
	}}
	router := testOAuthRouter(t, fs, snapshot, mcpoauth.New(provider.Client()), secretService)

	start := httptest.NewRecorder()
	router.ServeHTTP(start, authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/start?intent=import"))
	if start.Code != http.StatusFound {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	authorizeURL, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	if state == "" || authorizeURL.Query().Get("code_challenge") == "" {
		t.Fatalf("authorize url = %s", authorizeURL)
	}
	cookies := start.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != oauthCookieName || !cookies[0].HttpOnly {
		t.Fatalf("cookies = %+v", cookies)
	}
	decoded, err := (&handler{deps: Deps{Secrets: secretService}}).decryptOAuthCookie(cookies[0].Value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Intent != oauthIntentImport {
		t.Fatalf("intent=%q", decoded.Intent)
	}

	callbackRequest := authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/callback?code=code-1&state="+url.QueryEscape(state))
	callbackRequest.AddCookie(cookies[0])
	callback := httptest.NewRecorder()
	router.ServeHTTP(callback, callbackRequest)
	if callback.Code != http.StatusFound {
		t.Fatalf("callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	redirectURL, err := url.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if redirectURL.Query().Get("ws") != testWorkspaceID || redirectURL.Query().Get("item") != "mcp:notion" || redirectURL.Query().Get("connected") != "notion" || redirectURL.Query().Get("import") != "notion" {
		t.Fatalf("callback redirect=%s", redirectURL)
	}
	if fs.createdCredential != nil || fs.createdSecret == nil {
		t.Fatalf("user credential=%+v workspace secret=%+v", fs.createdCredential, fs.createdSecret)
	}
	if fs.createdSecret.WorkspaceID != testWorkspaceID || fs.createdSecret.Kind != "capability_inline" || fs.createdSecret.CredentialKindCode != "notion_mcp_oauth" {
		t.Fatalf("created secret=%+v", fs.createdSecret)
	}
	if len(fs.workspaceSecrets) != 1 {
		t.Fatalf("workspace secrets=%+v", fs.workspaceSecrets)
	}
	payload, err := secretService.Decrypt(fs.workspaceSecrets[0].EncryptedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if payload["access_token"] != "notion-access" || payload["refresh_token"] != "notion-refresh" || payload["provider"] != mcpoauth.CredentialProvider || payload["connection_status"] != mcpoauth.VerificationVerified {
		t.Fatalf("payload = %+v", payload)
	}
	if _, exists := payload["credential_scope"]; exists {
		t.Fatalf("payload must not persist a credential scope: %+v", payload)
	}

	detail := httptest.NewRecorder()
	router.ServeHTTP(detail, authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion"))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	var item itemResponse
	decodeResponse(t, detail, &item)
	if item.Authentication != "oauth2" || !item.Connected || item.CredentialKind != "notion_mcp_oauth" || item.ConnectionStatus != mcpoauth.VerificationVerified || item.ConnectionToolCount == nil || *item.ConnectionToolCount != 2 {
		t.Fatalf("item = %+v", item)
	}
}

func TestOAuthStartRejectsWorkspaceViewer(t *testing.T) {
	provider := newOAuthProvider(t)
	defer provider.Close()
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	router := testOAuthRouter(
		t,
		&fakeDirectoryStore{role: "viewer"},
		notionSnapshot(provider.URL+"/mcp"),
		mcpoauth.New(provider.Client()),
		secretService,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/start"))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOAuthStartRejectsApprovedClientConnector(t *testing.T) {
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	router := testOAuthRouter(
		t,
		&fakeDirectoryStore{role: "member"},
		approvedClientSnapshot(),
		mcpoauth.New(http.DefaultClient),
		secretService,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/approved-connector/oauth/start"))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOAuthStartRejectsUnsupportedIntent(t *testing.T) {
	provider := newOAuthProvider(t)
	defer provider.Close()
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	router := testOAuthRouter(
		t,
		&fakeDirectoryStore{role: "member"},
		notionSnapshot(provider.URL+"/mcp"),
		mcpoauth.New(provider.Client()),
		secretService,
	)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/start?intent=delete"))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOAuthStartKeepsLoopbackCallbackOnBrowserHost(t *testing.T) {
	provider := newOAuthProvider(t)
	defer provider.Close()
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeDirectoryStore{role: "member"}
	router := testOAuthRouter(t, fs, notionSnapshot(provider.URL+"/mcp"), mcpoauth.New(provider.Client()), secretService)
	request := httptest.NewRequest(
		http.MethodGet,
		"http://localhost:18080/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/start",
		nil,
	).WithContext(auth.WithUserID(context.Background(), testUserID))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusFound {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	authorizeURL, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	want := "http://localhost:18080/api/v1/workspaces/" + testWorkspaceID + "/mcp-directory/notion/oauth/callback"
	if got := authorizeURL.Query().Get("redirect_uri"); got != want {
		t.Fatalf("redirect_uri=%q want=%q", got, want)
	}
	contextCookie, err := routerOAuthCookie(recorder)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (&handler{deps: Deps{Secrets: secretService}}).decryptOAuthCookie(contextCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.BaseURL != "http://localhost:18080" {
		t.Fatalf("base_url=%q", decoded.BaseURL)
	}
}

func TestOAuthConnectionTestReportsReconnectRequired(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer provider.Close()
	secretService, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	credential := mcpoauth.Credential{
		AccessToken:             "revoked",
		RefreshToken:            "refresh",
		ClientID:                "client-1",
		TokenEndpointAuthMethod: "none",
		TokenEndpoint:           provider.URL + "/token",
		Resource:                provider.URL,
	}
	payload := credential.Payload()
	payload["catalog_id"] = "notion"
	encrypted, err := secretService.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeDirectoryStore{role: "member", workspaceSecrets: []store.SecretPayload{{
		SecretRead: store.SecretRead{
			ID:       "00000000-0000-0000-0000-000000000055",
			Kind:     "capability_inline",
			AuthType: "oauth2",
			Status:   "active",
			Metadata: map[string]any{
				"workspace_id":         testWorkspaceID,
				"catalog_id":           "notion",
				"credential_kind_code": "notion_mcp_oauth",
			},
		},
		EncryptedPayload: encrypted,
	}}}
	snapshot := notionSnapshot(provider.URL)
	router := testOAuthRouter(t, fs, snapshot, mcpoauth.New(provider.Client()), secretService)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/oauth/test"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result oauthConnectionResponse
	decodeResponse(t, recorder, &result)
	if result.Verified || result.Status != mcpoauth.VerificationReconnectRequired || result.ErrorCode != "connector_oauth_reconnect_required" {
		t.Fatalf("result = %+v", result)
	}
}

func (f *fakeDirectoryStore) ListUserCredentials(context.Context, string) ([]store.UserCredentialRead, error) {
	return append([]store.UserCredentialRead(nil), f.credentials...), nil
}

func (f *fakeDirectoryStore) GetUserCredentialByUserKind(_ context.Context, userID, kind string) (store.UserCredentialRead, bool, error) {
	for _, credential := range f.credentials {
		if credential.UserID == userID && credential.Kind == kind {
			return credential, true, nil
		}
	}
	return store.UserCredentialRead{}, false, nil
}

func (f *fakeDirectoryStore) CreateUserCredential(_ context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error) {
	f.createdCredential = &input
	credential := store.UserCredentialRead{
		ID:          "00000000-0000-0000-0000-000000000044",
		UserID:      input.UserID,
		Kind:        input.Kind,
		DisplayName: input.DisplayName,
		Ciphertext:  input.EncryptedValue,
	}
	f.credentials = []store.UserCredentialRead{credential}
	return credential, nil
}

func (f *fakeDirectoryStore) UpdateUserCredential(_ context.Context, input store.UpdateUserCredentialInput) (store.UserCredentialRead, error) {
	f.updatedCredential = &input
	credential := store.UserCredentialRead{
		ID:         input.CredentialID,
		UserID:     testUserID,
		Kind:       "notion_mcp_oauth",
		Ciphertext: input.EncryptedValue,
	}
	f.credentials = []store.UserCredentialRead{credential}
	return credential, nil
}

func (f *fakeDirectoryStore) ListSecrets(context.Context, string, int32) ([]store.SecretRead, error) {
	result := make([]store.SecretRead, 0, len(f.workspaceSecrets))
	for _, secret := range f.workspaceSecrets {
		result = append(result, secret.SecretRead)
	}
	return result, nil
}

func (f *fakeDirectoryStore) CreateSecret(_ context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error) {
	f.createdSecret = &input
	f.createdSecretCount++
	metadata := map[string]any{"workspace_id": input.WorkspaceID, "credential_kind_code": input.CredentialKindCode}
	for key, value := range input.Metadata {
		metadata[key] = value
	}
	created := store.SecretRead{
		ID: "00000000-0000-0000-0000-000000000055", Name: input.Name, Kind: input.Kind,
		Provider: input.Provider, AuthType: input.AuthType, Status: "active", Metadata: metadata,
	}
	f.workspaceSecrets = []store.SecretPayload{{SecretRead: created, EncryptedPayload: encryptedPayload}}
	return created, nil
}

func (f *fakeDirectoryStore) GetSecretPayload(_ context.Context, _, secretID string) (store.SecretPayload, error) {
	for _, secret := range f.workspaceSecrets {
		if secret.ID == secretID {
			return secret, nil
		}
	}
	return store.SecretPayload{}, store.ErrUnknownSecret
}

func (f *fakeDirectoryStore) UpdateSecretPayload(_ context.Context, _, secretID string, encryptedPayload []byte) (store.SecretPayload, error) {
	f.updatedSecretID = secretID
	for index, secret := range f.workspaceSecrets {
		if secret.ID == secretID {
			secret.Status = "active"
			secret.EncryptedPayload = encryptedPayload
			f.workspaceSecrets[index] = secret
			return secret, nil
		}
	}
	return store.SecretPayload{}, store.ErrUnknownSecret
}

func testOAuthRouter(t *testing.T, fs *fakeDirectoryStore, snapshot mcpcatalog.Snapshot, oauthClient *mcpoauth.Client, secretService *secrets.Service) http.Handler {
	t.Helper()
	router := chi.NewRouter()
	RegisterRoutes(router, Deps{
		Catalog:              fakeCatalog{snapshot: snapshot},
		Store:                fs,
		WorkspaceCredentials: fs,
		OAuth:                oauthClient,
		Secrets:              secretService,
		PublicURL:            "http://127.0.0.1:18080",
		CookieSecure:         false,
	})
	return router
}

func authenticatedRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	return request.WithContext(auth.WithUserID(request.Context(), testUserID))
}

func routerOAuthCookie(recorder *httptest.ResponseRecorder) (*http.Cookie, error) {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == oauthCookieName {
			return cookie, nil
		}
	}
	return nil, fmt.Errorf("%s cookie missing", oauthCookieName)
}

func newOAuthProvider(t *testing.T) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		writeOAuthJSON(t, w, http.StatusOK, map[string]any{
			"resource":              server.URL + "/mcp",
			"authorization_servers": []string{server.URL},
		})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeOAuthJSON(t, w, http.StatusOK, map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/authorize",
			"token_endpoint":                   server.URL + "/token",
			"registration_endpoint":            server.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		writeOAuthJSON(t, w, http.StatusCreated, map[string]any{
			"client_id":                  "client-1",
			"token_endpoint_auth_method": "none",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("code") != "code-1" || r.Form.Get("code_verifier") == "" {
			t.Fatalf("token form = %v", r.Form)
		}
		writeOAuthJSON(t, w, http.StatusOK, map[string]any{
			"access_token":  "notion-access",
			"refresh_token": "notion-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer notion-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeOAuthJSON(t, w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]string{"name": "Notion", "version": "1.0.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeOAuthJSON(t, w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0", "id": 2,
				"result": map[string]any{"tools": []map[string]string{{"name": "notion-search"}, {"name": "notion-fetch"}}},
			})
		default:
			t.Fatalf("unexpected MCP method %q", request.Method)
		}
	})
	server = httptest.NewTLSServer(mux)
	return server
}

func notionSnapshot(serverURL string) mcpcatalog.Snapshot {
	return mcpcatalog.Snapshot{Source: mcpcatalog.SourceBuiltin, Catalog: mcpcatalog.Catalog{
		SchemaVersion: 1,
		UpdatedAt:     "2026-07-22T00:00:00Z",
		Items: []mcpcatalog.Item{{
			ID: "notion", Name: "Notion", Description: "Use Notion.",
			Publisher: mcpcatalog.Publisher{Name: "Notion", URL: "https://www.notion.so"},
			Verified:  true, Categories: []string{"Productivity"}, FeaturedRank: 1,
			Version: "1.0.0", Transport: "streamable-http",
			Authentication: mcpcatalog.Authentication{
				Type: "oauth2", CredentialKind: "notion_mcp_oauth",
			},
			Server: mcpcatalog.Server{Name: "notion", URL: serverURL},
		}},
	}}
}

func writeOAuthJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
