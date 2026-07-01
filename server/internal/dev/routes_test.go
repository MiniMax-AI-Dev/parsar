package dev

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	gatewaypkg "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

const (
	testConversationID = "00000000-0000-0000-0000-000000000012"
	testRunID          = "00000000-0000-0000-0000-000000000101"
)

func nonNilStubMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func testRouter() http.Handler {
	r := chi.NewRouter()
	RegisterRoutes(r)
	return r
}

func testRouterWithAuth(devAuth bool, sessions auth.SessionStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{}, WithAuthMiddleware(auth.NewMiddleware(sessions).WithDevAuth(devAuth)))
	return r
}

func withTestUser(req *http.Request) *http.Request {
	return req.WithContext(auth.WithUserID(req.Context(), store.DefaultDevFixtureIDs().UserID))
}

func registerRoutesWithRBACStore(roleStore RuntimeStore, devAuth bool) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, roleStore, WithAuthMiddleware(auth.NewMiddleware(newStubSessions()).WithDevAuth(devAuth)))
	return r
}

func newRequestWithDevUser(method, target, body, userID string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(auth.DevUserHeader, userID)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestSeedEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dev/seed", nil)
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "Demo Workspace") {
		t.Fatalf("expected seed response, got %s", res.Body.String())
	}
}

func TestDevAuthVerification(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"admin@example.com","code":"888888"}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/auth/verify", body)
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
}

func TestDevMeRequiresAuthCookie(t *testing.T) {
	r := testRouterWithAuth(false, newStubSessions())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without cookie, got %d: %s", res.Code, res.Body.String())
	}
}

func TestDevMeIgnoresDevHeaderWhenDevAuthDisabled(t *testing.T) {
	r := testRouterWithAuth(false, newStubSessions())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set(auth.DevUserHeader, store.DefaultDevFixtureIDs().UserID)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with dev header while dev auth disabled, got %d: %s", res.Code, res.Body.String())
	}
}

func TestDevMeAcceptsDevHeaderWhenDevAuthEnabled(t *testing.T) {
	r := testRouterWithAuth(true, newStubSessions())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.Header.Set(auth.DevUserHeader, store.DefaultDevFixtureIDs().UserID)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 with dev header while dev auth enabled, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"user_id":"00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("expected seed user profile, got %s", res.Body.String())
	}
}

// E2B smoke endpoint tests. Use testRouter() (no auth middleware) so the
// /dev group's require(r) closure is a no-op and the smoke endpoint stays
// reachable without a dev session cookie.

func TestE2BSmokeEndpointRequiresAPIKey(t *testing.T) {
	t.Setenv("E2B_API_KEY", "")
	req := httptest.NewRequest(http.MethodPost, "/dev/sandboxes/e2b/smoke", bytes.NewBufferString(`{}`))
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "api_key") {
		t.Fatalf("expected missing api key error, got %s", res.Body.String())
	}
}

func TestE2BSmokeEndpointRunsCommandAndKillsSandbox(t *testing.T) {
	api, envd, killed := fakeE2BSmokeServers(t, fakeE2BOptions{})
	defer api.Close()
	defer envd.Close()

	body := fmt.Sprintf(`{"api_key":"test-key","api_base_url":%q,"sandbox_base_url":%q,"template":"base","command":"echo hi"}`, api.URL, envd.URL)
	req := httptest.NewRequest(http.MethodPost, "/dev/sandboxes/e2b/smoke", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !*killed {
		t.Fatalf("expected smoke handler to kill sandbox when keep_alive=false")
	}
	bodyText := res.Body.String()
	if !strings.Contains(bodyText, `"sandbox_id":"sbx_123"`) || !strings.Contains(bodyText, `"stdout":"hi\n"`) || !strings.Contains(bodyText, `"killed":true`) {
		t.Fatalf("expected sandbox id/stdout/killed in response, got %s", bodyText)
	}
}

func TestE2BSmokeEndpointKeepAliveSkipsKill(t *testing.T) {
	api, envd, killed := fakeE2BSmokeServers(t, fakeE2BOptions{})
	defer api.Close()
	defer envd.Close()

	body := fmt.Sprintf(`{"api_key":"test-key","api_base_url":%q,"sandbox_base_url":%q,"template":"base","command":"echo hi","keep_alive":true}`, api.URL, envd.URL)
	req := httptest.NewRequest(http.MethodPost, "/dev/sandboxes/e2b/smoke", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if *killed {
		t.Fatalf("expected keep_alive=true to skip sandbox kill")
	}
	if !strings.Contains(res.Body.String(), `"killed":false`) {
		t.Fatalf("expected killed=false response, got %s", res.Body.String())
	}
}

func TestE2BSmokeEndpointRedactsAPIKeyFromUpstreamError(t *testing.T) {
	const apiKey = "sk_live_SECRET_123"
	api, envd, _ := fakeE2BSmokeServers(t, fakeE2BOptions{
		createStatus: http.StatusUnauthorized,
		createBody:   `{"message":"invalid api key sk_live_SECRET_123"}`,
	})
	defer api.Close()
	defer envd.Close()

	body := fmt.Sprintf(`{"api_key":%q,"api_base_url":%q,"sandbox_base_url":%q,"template":"base","command":"echo hi"}`, apiKey, api.URL, envd.URL)
	req := httptest.NewRequest(http.MethodPost, "/dev/sandboxes/e2b/smoke", bytes.NewBufferString(body))
	res := httptest.NewRecorder()
	testRouter().ServeHTTP(res, req)
	if res.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", res.Code, res.Body.String())
	}
	bodyText := res.Body.String()
	if strings.Contains(bodyText, apiKey) {
		t.Fatalf("response leaked api key: %s", bodyText)
	}
	if !strings.Contains(bodyText, "[REDACTED]") {
		t.Fatalf("expected redacted marker in response, got %s", bodyText)
	}
}

type fakeE2BOptions struct {
	createStatus int
	createBody   string
}

func fakeE2BSmokeServers(t *testing.T, opts fakeE2BOptions) (*httptest.Server, *httptest.Server, *bool) {
	t.Helper()
	killed := false
	envd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/process.Process/Start" {
			t.Fatalf("unexpected envd path %s", r.URL.Path)
		}
		var reqPayload struct {
			Process struct {
				Args []string `json:"args"`
			} `json:"process"`
		}
		if err := readDevConnectEnvelope(r.Body, &reqPayload); err != nil {
			t.Fatalf("read envd request: %v", err)
		}
		if len(reqPayload.Process.Args) != 3 || reqPayload.Process.Args[2] != "echo hi" {
			t.Fatalf("unexpected command args %#v", reqPayload.Process.Args)
		}
		stdout := base64.StdEncoding.EncodeToString([]byte("hi\n"))
		writeDevConnectEnvelope(t, w, map[string]any{"event": map[string]any{"start": map[string]any{"pid": 123}}})
		writeDevConnectEnvelope(t, w, map[string]any{"event": map[string]any{"data": map[string]any{"stdout": stdout}}})
		writeDevConnectEnvelope(t, w, map[string]any{"event": map[string]any{"end": map[string]any{"exited": true, "status": "exit status 0"}}})
	}))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			if opts.createStatus != 0 {
				w.WriteHeader(opts.createStatus)
				_, _ = w.Write([]byte(opts.createBody))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"templateID": "base", "sandboxID": "sbx_123", "envdVersion": "0.2.0", "envdAccessToken": "envd-token"})
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/sbx_123":
			killed = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected api request %s %s", r.Method, r.URL.Path)
		}
	}))
	return api, envd, &killed
}

func readDevConnectEnvelope(r io.Reader, out any) error {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[1:])
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, out)
}

func writeDevConnectEnvelope(t *testing.T, w http.ResponseWriter, msg any) {
	t.Helper()
	payload, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal connect payload: %v", err)
	}
	var header [5]byte
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	_, _ = w.Write(header[:])
	_, _ = w.Write(payload)
}

func TestSecretAPIEncryptsAndMasksSecret(t *testing.T) {
	// Pin the server-side env so the new master-key cross-validate gate
	// does not 401 us when this test runs in an environment that has
	// PARSAR_MASTER_KEY set to something else.
	t.Setenv("PARSAR_MASTER_KEY", "test-master-key")
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	body := bytes.NewBufferString(`{"name":"OpenAI","provider":"openai","auth_type":"api_key","payload":{"api_key":"sk-test-secret-value","base_url":"https://example.test/v1"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	bodyText := res.Body.String()
	if strings.Contains(bodyText, "sk-test-secret-value") || !strings.Contains(bodyText, "sk-tes...") {
		t.Fatalf("expected masked response without plaintext secret, got %s", bodyText)
	}
}

func TestSecretAPIRequiresServerMasterKey(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	body := bytes.NewBufferString(`{"name":"OpenAI","provider":"openai","auth_type":"api_key","payload":{"api_key":"sk-test-secret-value"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", res.Code, res.Body.String())
	}
}

func TestSecretAPIAllowsServerMasterKey(t *testing.T) {
	// Sanity: when server env has a master key, create succeeds without exposing it to the client.
	t.Setenv("PARSAR_MASTER_KEY", "shared-dev-key")
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	body := bytes.NewBufferString(`{"name":"OpenAI","provider":"openai","auth_type":"api_key","payload":{"api_key":"sk-ok"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCreateSecretRequiresWorkspaceOwnerOrAdmin(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-master-key")
	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		store.DefaultDevFixtureIDs().UserID:    "owner",
		"00000000-0000-0000-0000-0000000000aa": "viewer",
	}), true)
	body := `{"name":"OpenAI","provider":"openai","auth_type":"api_key","payload":{"api_key":"sk-ok"}}`

	req := newRequestWithDevUser(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body, store.DefaultDevFixtureIDs().UserID)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected owner 201, got %d: %s", res.Code, res.Body.String())
	}

	req = newRequestWithDevUser(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body, "00000000-0000-0000-0000-0000000000aa")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected viewer 403, got %d: %s", res.Code, res.Body.String())
	}

	req = newRequestWithDevUser(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", body, "00000000-0000-0000-0000-0000000000bb")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected non-member 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestListSecretAPIDoesNotReturnPlaintext(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "sk-test-secret-value") {
		t.Fatalf("list response leaked secret: %s", res.Body.String())
	}
}

func TestDisableModelAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702/disable", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"disabled"`) {
		t.Fatalf("expected disabled model response, got %s", res.Body.String())
	}
}

func TestDisableModelAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/not-a-uuid/disable", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000099999/disable", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestUpdateModelAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	// Send malicious provider_type / adapter / credential_mode in the body;
	// updateModelBody doesn't carry them so the server must drop them and
	// the response must keep the stable identifiers from the original row.
	// capabilities / limits fold into config (migration 028 lineage); the
	// response must surface them there.
	body := bytes.NewBufferString(`{
		"name":"GPT-5.5 Renamed",
		"model_key":"gpt-5.5",
		"capabilities":{"chat":true,"tool_call":true,"vision":true},
		"limits":{"context":200000,"output":8192},
		"config":{},
		"provider_type":"malicious-type",
		"adapter":"malicious-adapter",
		"credential_mode":"credential_ref"
	}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"name":"GPT-5.5 Renamed"`) {
		t.Fatalf("expected renamed model, got %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"vision":true`) {
		t.Fatalf("expected updated capabilities folded into config, got %s", res.Body.String())
	}
	// provider_type / adapter / credential_mode must NOT shift to the
	// malicious values sent in the body.
	if !strings.Contains(res.Body.String(), `"provider_type":"openai"`) {
		t.Fatalf("expected stable provider_type, got %s", res.Body.String())
	}
	if strings.Contains(res.Body.String(), `"malicious-type"`) {
		t.Fatalf("malicious provider_type leaked into response: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"credential_mode":"inline_secret"`) {
		t.Fatalf("expected stable credential_mode, got %s", res.Body.String())
	}
}

func TestUpdateModelAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	// 400 — missing model_key (mandatory because it drives the upstream API)
	body := bytes.NewBufferString(`{"name":"x","model_key":""}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing model_key, got %d: %s", res.Code, res.Body.String())
	}

	// 400 — invalid model id (mirror the provider bad-uuid coverage)
	body = bytes.NewBufferString(`{"name":"x","model_key":"y"}`)
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/not-a-uuid", body)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad uuid, got %d: %s", res.Code, res.Body.String())
	}

	// 404 — unknown model
	body = bytes.NewBufferString(`{"name":"x","model_key":"y"}`)
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000099999", body)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

// connectivityStubStore overrides ResolveModelRuntime + GetSecretPayload
// so the tests can point the connectivity check at a httptest.Server and
// inject a real encrypted secret.
type connectivityStubStore struct {
	stubRuntimeStore
	runtime store.ModelRuntime
	payload store.SecretPayload
}

func (c connectivityStubStore) ResolveModelRuntime(ctx context.Context, workspaceID, modelID string) (store.ModelRuntime, error) {
	return c.runtime, nil
}
func (c connectivityStubStore) ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (store.ModelRuntime, error) {
	return c.runtime, nil
}
func (c connectivityStubStore) GetSecretPayload(ctx context.Context, workspaceID, secretID string) (store.SecretPayload, error) {
	return c.payload, nil
}

type feishuSecretRouteStore struct {
	captureCreateMessageStore
	appID   string
	route   store.FeishuAgentRoute
	secrets map[string]store.SecretPayload
	userID  string
	member  bool
	// threadHistory drives HasFeishuThreadInboundHistory. Default false;
	// tests covering 话题续聊 set true to assert the gate lets follow-ups
	// through without an explicit @mention.
	threadHistory bool
}

func (s *feishuSecretRouteStore) GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error) {
	if appID != s.appID {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return s.route, nil
}

func (s *feishuSecretRouteStore) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	if s.userID == "" {
		return "", store.ErrUnknownPlatformUser
	}
	return s.userID, nil
}

func (s *feishuSecretRouteStore) HasFeishuThreadInboundHistory(_ context.Context, _, _ string) (bool, error) {
	return s.threadHistory, nil
}

func (s *feishuSecretRouteStore) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return s.member, nil
}

func (s *feishuSecretRouteStore) GetSecretPayload(ctx context.Context, workspaceID, secretID string) (store.SecretPayload, error) {
	if payload, ok := s.secrets[secretID]; ok {
		return payload, nil
	}
	return store.SecretPayload{}, store.ErrUnknownSecret
}

type fakeFeishuRegistrationClient struct {
	begin    gatewaypkg.FeishuAppRegistrationBeginResult
	beginErr error
	poll     gatewaypkg.FeishuAppRegistrationPollResult
	pollErr  error
}

func (f fakeFeishuRegistrationClient) Begin(ctx context.Context) (gatewaypkg.FeishuAppRegistrationBeginResult, error) {
	return f.begin, f.beginErr
}

func (f fakeFeishuRegistrationClient) Poll(ctx context.Context, deviceCode string, currentIntervalSec int, tenantBrand string) (gatewaypkg.FeishuAppRegistrationPollResult, error) {
	return f.poll, f.pollErr
}

type feishuProvisioningStore struct {
	stubRuntimeStore
	createdSecrets []store.CreateSecretInput
	lastConnector  store.UpdateAgentFeishuConnectorInput
	appIDInUseBy   string
}

func (s *feishuProvisioningStore) CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error) {
	s.createdSecrets = append(s.createdSecrets, input)
	return store.SecretRead{
		ID:         "00000000-0000-0000-0000-0000000006f1",
		Name:       input.Name,
		Kind:       input.Kind,
		Provider:   input.Provider,
		AuthType:   input.AuthType,
		KeyVersion: "v1",
		Status:     "active",
		Masked:     input.Masked,
		Metadata:   map[string]any{"masked": input.Masked},
		CreatedAt:  time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (s *feishuProvisioningStore) UpdateAgentFeishuConnector(ctx context.Context, input store.UpdateAgentFeishuConnectorInput, actorID string) (store.AgentFeishuConnectorChange, error) {
	s.lastConnector = input
	return store.AgentFeishuConnectorChange{
		AgentID:     input.AgentID,
		WorkspaceID: store.DefaultDevFixtureIDs().WorkspaceID,
		Name:        "Agent",
		Slug:        "agent",
		New: store.FeishuConnectorSnapshot{
			Enabled:      input.Enabled,
			AppID:        input.AppID,
			AppSecretRef: input.AppSecretRef,
			BotOpenID:    input.BotOpenID,
			EventMode:    input.EventMode,
		},
	}, nil
}

func (s *feishuProvisioningStore) GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error) {
	if s.appIDInUseBy != "" {
		return store.FeishuAgentRoute{AgentID: s.appIDInUseBy}, nil
	}
	return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
}

func feishuRouteConfigForTest(t *testing.T, appID, verificationRef, appSecretRef string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled":                true,
				"app_id":                 appID,
				"verification_token_ref": verificationRef,
				"app_secret_ref":         appSecretRef,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func encryptedSecretForTest(t *testing.T, masterKey string, payload map[string]any) store.SecretPayload {
	t.Helper()
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatal(err)
	}
	return store.SecretPayload{EncryptedPayload: enc}
}

func TestOpenAIChatCompletionsAdapterRecognition(t *testing.T) {
	cases := map[string]bool{
		"openai":                    true,
		"openai_compatible":         true,
		"openai-compatible":         true,
		"@ai-sdk/openai":            true,
		"@ai-sdk/openai-compatible": true,
		"@ai-sdk/anthropic":         false,
		"@ai-sdk/google":            false,
	}
	for adapter, want := range cases {
		if got := isOpenAIChatCompletionsAdapter(adapter); got != want {
			t.Fatalf("isOpenAIChatCompletionsAdapter(%q) = %v, want %v", adapter, got, want)
		}
	}
}

func TestAnthropicMessagesAdapterRecognition(t *testing.T) {
	cases := map[string]bool{
		"anthropic":                 true,
		"anthropic_compatible":      true,
		"anthropic-compatible":      true,
		"@ai-sdk/anthropic":         true,
		"@ai-sdk/openai":            false,
		"@ai-sdk/openai-compatible": false,
		"@ai-sdk/google":            false,
	}
	for adapter, want := range cases {
		if got := isAnthropicMessagesAdapter(adapter); got != want {
			t.Fatalf("isAnthropicMessagesAdapter(%q) = %v, want %v", adapter, got, want)
		}
	}
}

func TestModelConnectivityUnsupportedAdapter(t *testing.T) {
	store := connectivityStubStore{
		runtime: store.ModelRuntime{
			ModelID:  "00000000-0000-0000-0000-000000000702",
			ModelKey: "gemini-3.1-pro-preview",
			Adapter:  "@ai-sdk/google",
			BaseURL:  "https://generativelanguage.googleapis.com/v1beta",
			SecretID: "00000000-0000-0000-0000-000000000601",
		},
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702/test", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"supported":false`) {
		t.Fatalf("expected supported:false, got %s", body)
	}
}

func TestModelConnectivityAnthropicSuccess(t *testing.T) {
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-ant-test"})
	if err != nil {
		t.Fatal(err)
	}

	var gotPath, gotKey, gotVersion, gotSub string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotKey = req.Header.Get("x-api-key")
		gotVersion = req.Header.Get("anthropic-version")
		gotSub = req.Header.Get("X-Sub-Module")
		bodyBytes, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(bodyBytes, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"pong from anthropic"}]}`))
	}))
	defer upstream.Close()

	store := connectivityStubStore{
		runtime: store.ModelRuntime{
			ModelID:        "00000000-0000-0000-0000-000000000702",
			ModelKey:       "claude-opus-4-7",
			Adapter:        "@ai-sdk/anthropic",
			BaseURL:        upstream.URL + "/anthropic",
			SecretID:       "00000000-0000-0000-0000-000000000601",
			ProviderConfig: map[string]any{"headers": map[string]any{"X-Sub-Module": "claude-code-internal"}},
		},
		payload: store.SecretPayload{EncryptedPayload: enc},
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702/test", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"sample":"pong from anthropic"`) {
		t.Fatalf("expected success + anthropic sample, got %s", body)
	}
	if gotPath != "/anthropic/v1/messages" {
		t.Fatalf("upstream got path %q, want /anthropic/v1/messages", gotPath)
	}
	if gotKey != "sk-ant-test" {
		t.Fatalf("upstream got wrong x-api-key: %q", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Fatalf("upstream got wrong anthropic-version: %q", gotVersion)
	}
	if gotSub != "claude-code-internal" {
		t.Fatalf("upstream got wrong X-Sub-Module: %q", gotSub)
	}
	if gotBody["model"] != "claude-opus-4-7" || gotBody["max_tokens"].(float64) != 16 {
		t.Fatalf("upstream got wrong body: %+v", gotBody)
	}
}

func TestModelConnectivitySuccess(t *testing.T) {
	// Stand up a fake upstream that mimics OpenAI's chat-completions
	// response. We assert the request reached it with the right
	// Authorization + custom header, and that the handler reports
	// success + sample content.
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-test-12345"})
	if err != nil {
		t.Fatal(err)
	}

	var gotAuth, gotSub string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotSub = req.Header.Get("X-Sub-Module")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"}}]}`))
	}))
	defer upstream.Close()

	store := connectivityStubStore{
		runtime: store.ModelRuntime{
			ModelID:        "00000000-0000-0000-0000-000000000702",
			ModelKey:       "gpt-5.5",
			Adapter:        "@ai-sdk/openai-compatible",
			BaseURL:        upstream.URL,
			SecretID:       "00000000-0000-0000-0000-000000000601",
			ProviderConfig: map[string]any{"headers": map[string]any{"X-Sub-Module": "claude-code-internal"}},
		},
		payload: store.SecretPayload{EncryptedPayload: enc},
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702/test", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"sample":"pong"`) {
		t.Fatalf("expected success+pong, got %s", body)
	}
	if gotAuth != "Bearer sk-test-12345" {
		t.Fatalf("upstream got wrong Authorization: %q", gotAuth)
	}
	if gotSub != "claude-code-internal" {
		t.Fatalf("upstream got wrong X-Sub-Module: %q", gotSub)
	}
}

func TestModelConnectivityUpstream401(t *testing.T) {
	// Upstream returns 401 with an OpenAI-shaped error body — handler
	// should surface success:false + the upstream error message.
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)
	svc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := svc.Encrypt(map[string]any{"api_key": "sk-bad"})
	if err != nil {
		t.Fatal(err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer upstream.Close()

	store := connectivityStubStore{
		runtime: store.ModelRuntime{
			ModelID:  "00000000-0000-0000-0000-000000000702",
			ModelKey: "gpt-5.5",
			Adapter:  "openai",
			BaseURL:  upstream.URL,
			SecretID: "00000000-0000-0000-0000-000000000601",
		},
		payload: store.SecretPayload{EncryptedPayload: enc},
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/models/00000000-0000-0000-0000-000000000702/test", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 (handler always returns 200, success flag carries the verdict), got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"success":false`) || !strings.Contains(body, "Invalid API key") {
		t.Fatalf("expected success:false + upstream message, got %s", body)
	}
}

func TestGatewayInboundReturnsScheduledRuns(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := bytes.NewBufferString(`{"gateway":"feishu","conversation":"demo-group","sender":"admin@example.com","text":"@产品Agent @后端Agent 看一下"}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/gateway/inbound", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	bodyText := res.Body.String()
	if !strings.Contains(bodyText, `"gateway":"feishu"`) || !strings.Contains(bodyText, "message_id") || !strings.Contains(bodyText, "run-1") {
		t.Fatalf("expected gateway scheduled run response, got %s", bodyText)
	}
}

func TestGatewayInboundPassesSourceAndGateway(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{"gateway":"feishu","conversation":"demo-group","sender":"admin@example.com","text":"@后端Agent 看一下"}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/gateway/inbound", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.Source != "gateway" || capturing.lastInput.Gateway != "feishu" {
		t.Fatalf("expected gateway source metadata, got %+v", capturing.lastInput)
	}
}

func TestGatewayInboundPassesExternalIDs(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{"gateway":"feishu","external_chat_id":"oc_demo","external_user_id":"ou_demo","external_thread_id":"om_thread","external_message_id":"om_message","sender":"admin@example.com","text":"@后端Agent 看一下"}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/gateway/inbound", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.ExternalChatID != "oc_demo" || capturing.lastInput.ExternalUserID != "ou_demo" || capturing.lastInput.ExternalThreadID != "om_thread" || capturing.lastInput.ExternalMessageID != "om_message" {
		t.Fatalf("expected external ids passed to store, got %+v", capturing.lastInput)
	}
}

func TestGatewayInboundNormalizesAdapterPayload(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{"gateway":"feishu","message":{"id":"om_message","text":"@后端Agent 看一下"},"actor":{"id":"ou_demo","email":"admin@example.com"},"conversation_ref":{"id":"oc_demo","title":"demo-group","thread_id":"om_thread"}}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/gateway/inbound", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.Text != "@后端Agent 看一下" || capturing.lastInput.ExternalMessageID != "om_message" || capturing.lastInput.ExternalChatID != "oc_demo" || capturing.lastInput.ExternalUserID != "ou_demo" {
		t.Fatalf("expected normalized adapter payload, got %+v", capturing.lastInput)
	}
}

func TestFeishuMessageEventNormalizesToGatewayInbound(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{
		"event": {
			"message": {
				"message_id": "om_message",
				"chat_id": "oc_demo",
				"chat_type": "group",
				"thread_id": "om_thread",
				"content": "{\"text\":\"@后端Agent 看一下 API\"}"
			},
			"sender": {
				"sender_id": {"open_id": "ou_demo"},
				"tenant_key": "tenant_demo"
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"gateway":"feishu"`) || !strings.Contains(res.Body.String(), "run-1") {
		t.Fatalf("expected feishu gateway response, got %s", res.Body.String())
	}
	input := capturing.lastInput
	if input.Source != "gateway" || input.Gateway != "feishu" {
		t.Fatalf("expected gateway/feishu source, got %+v", input)
	}
	if input.ExternalChatID != "oc_demo" || input.ExternalThreadID != "om_thread" || input.ExternalMessageID != "om_message" || input.ExternalUserID != "ou_demo" {
		t.Fatalf("expected Feishu external ids, got %+v", input)
	}
	if input.Text != "@后端Agent 看一下 API" || len(input.Mentions) != 1 || input.Mentions[0] != "@后端Agent" {
		t.Fatalf("expected content text JSON to become mention text, got %+v", input)
	}
}

func TestFeishuMessageEventUsesUserIDFallback(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{
		"event": {
			"message": {"message_id": "om_message", "chat_id": "oc_demo", "chat_type": "group", "content": "{\"text\":\"@后端Agent 看一下\"}"},
			"sender": {"sender_id": {"user_id": "user_demo"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.ExternalUserID != "user_demo" {
		t.Fatalf("expected user_id fallback, got %+v", capturing.lastInput)
	}
}

func TestFeishuWebhookRoutesByAppIDToTargetAgent(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_test_bot"},
		"event": {
			"message": {
				"message_id": "om_feishu_once",
				"chat_id": "oc_feishu_chat",
				"chat_type": "p2p",
				"content": "{\"text\":\"不用显式 @Parsar 内部 Agent 也应路由\"}"
			},
			"sender": {
				"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"},
				"tenant_key": "tenant_a"
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.Gateway != "feishu" || capturing.lastInput.TargetAgentID != "00000000-0000-0000-0000-000000000901" || capturing.lastInput.SourceAppID != "cli_test_bot" {
		t.Fatalf("expected app_id routed target fields, got %+v", capturing.lastInput)
	}
	if capturing.lastInput.ExternalChatID != "oc_feishu_chat" || capturing.lastInput.ExternalMessageID != "om_feishu_once" || capturing.lastInput.ExternalUserID != "on_test_user" {
		t.Fatalf("expected feishu external ids, got %+v", capturing.lastInput)
	}
	if capturing.lastInput.Text != "不用显式 @Parsar 内部 Agent 也应路由" {
		t.Fatalf("expected normalized text, got %q", capturing.lastInput.Text)
	}
}

func TestFeishuWebhookGroupWithoutBotMentionIgnoredWhenBotOpenIDKnown(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:  "cli_group_bot",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot","bot_open_id":"ou_bot_self"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_no_mention",
				"chat_id": "oc_group",
				"chat_type": "group",
				"content": "{\"text\":\"群里普通消息不该触发\"}"
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected ignored event to return 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"reason":"group_without_bot_mention"`) {
		t.Fatalf("expected group_without_bot_mention response, got %s", res.Body.String())
	}
	if fs.lastInput.Gateway != "" {
		t.Fatalf("ignored group message should not create inbound input, got %+v", fs.lastInput)
	}
}

func TestFeishuWebhookGroupMentioningBotTriggersWhenBotOpenIDKnown(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:  "cli_group_bot",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot","bot_open_id":"ou_bot_self"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_with_mention",
				"chat_id": "oc_group",
				"chat_type": "group",
				"content": "{\"text\":\"@Bot 请处理\"}",
				"mentions": [{"id": {"open_id": "ou_bot_self"}}]
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected mentioned group event to create inbound, got %d: %s", res.Code, res.Body.String())
	}
	if fs.lastInput.TargetAgentID != fs.route.AgentID || fs.lastInput.ExternalMessageID != "om_with_mention" {
		t.Fatalf("expected routed mentioned group input, got %+v", fs.lastInput)
	}
}

// Group chat without bot_open_id configured: the gate cannot prove the
// bot was @mentioned, so it errs on the side of silence — matches the
// "在群聊里，只有单独 @ 这个机器人的消息，机器人才会进行回复" UX.
// Without this guard, the bot would respond to every group message and
// spam the channel.
func TestFeishuWebhookGroupWithoutBotMentionIgnoredWhenBotOpenIDMissing(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:  "cli_group_bot",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		// No bot_open_id in connector config.
		Config: []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_no_bot_open_id",
				"chat_id": "oc_group",
				"chat_type": "group",
				"content": "{\"text\":\"群里普通消息\"}"
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected ignored event to return 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"reason":"group_without_bot_mention"`) {
		t.Fatalf("expected group_without_bot_mention response, got %s", res.Body.String())
	}
	if fs.lastInput.Gateway != "" {
		t.Fatalf("ignored group message should not create inbound input, got %+v", fs.lastInput)
	}
}

// Group message mentioning a different user (not the bot): also dropped,
// so the bot doesn't barge into a conversation aimed at someone else.
func TestFeishuWebhookGroupMentioningOtherUserIgnored(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:  "cli_group_bot",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot","bot_open_id":"ou_bot_self"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_mention_other",
				"chat_id": "oc_group",
				"chat_type": "group",
				"content": "{\"text\":\"@Alice 一起看下\"}",
				"mentions": [{"id": {"open_id": "ou_alice"}}]
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected ignored event to return 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"reason":"group_without_bot_mention"`) {
		t.Fatalf("expected group_without_bot_mention response, got %s", res.Body.String())
	}
	if fs.lastInput.Gateway != "" {
		t.Fatalf("mention-other group message should not create inbound, got %+v", fs.lastInput)
	}
}

// Thread follow-up: a group message inside a 话题 where the bot has
// previously stored an inbound flows through without requiring an
// explicit @mention. Matches the "如果已经开启一个飞书话题,在同一个
// 话题下,用户不进行艾特,也能够进行回复" UX.
func TestFeishuWebhookGroupThreadFollowupPassesWithoutMention(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:         "cli_group_bot",
		userID:        store.DefaultDevFixtureIDs().UserID,
		member:        true,
		threadHistory: true, // simulate prior inbound on this thread
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot","bot_open_id":"ou_bot_self"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_thread_followup",
				"chat_id": "oc_group",
				"chat_type": "group",
				"thread_id": "omt_thread_1",
				"content": "{\"text\":\"你还在吗\"}"
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected thread follow-up to create inbound, got %d: %s", res.Code, res.Body.String())
	}
	if fs.lastInput.TargetAgentID != fs.route.AgentID || fs.lastInput.ExternalMessageID != "om_thread_followup" {
		t.Fatalf("expected routed thread follow-up input, got %+v", fs.lastInput)
	}
}

// First message in a 话题 with no prior bot participation and no
// @mention: dropped. The thread continuation rule only kicks in once
// the bot has actually responded once.
func TestFeishuWebhookGroupThreadWithoutHistoryAndMentionIgnored(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:         "cli_group_bot",
		userID:        store.DefaultDevFixtureIDs().UserID,
		member:        true,
		threadHistory: false,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot","bot_open_id":"ou_bot_self"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_thread_first",
				"chat_id": "oc_group",
				"chat_type": "group",
				"thread_id": "omt_thread_new",
				"content": "{\"text\":\"新话题首条不带 @\"}"
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected ignored event to return 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"reason":"group_without_bot_mention"`) {
		t.Fatalf("expected group_without_bot_mention response, got %s", res.Body.String())
	}
}

// P2P direct message: always flows through regardless of mention or
// bot_open_id configuration — matches the "1 对 1 私聊时,无需艾特" UX.
func TestFeishuWebhookP2PAlwaysPassesWithoutMention(t *testing.T) {
	fs := &feishuSecretRouteStore{
		appID:  "cli_group_bot",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		// bot_open_id intentionally omitted — p2p path must not depend on it.
		Config: []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_group_bot"}}}`),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_group_bot"},
		"event": {
			"message": {
				"message_id": "om_dm",
				"chat_id": "oc_p2p",
				"chat_type": "p2p",
				"content": "{\"text\":\"帮我查询上海天气\"}"
			},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected p2p event to create inbound, got %d: %s", res.Code, res.Body.String())
	}
	if fs.lastInput.TargetAgentID != fs.route.AgentID || fs.lastInput.ExternalMessageID != "om_dm" {
		t.Fatalf("expected routed p2p input, got %+v", fs.lastInput)
	}
}

func TestFeishuWebhookFallsBackToPerAgentVerificationToken(t *testing.T) {
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)

	fs := &feishuSecretRouteStore{
		appID:  "cli_agent_token",
		userID: store.DefaultDevFixtureIDs().UserID,
		member: true,
		secrets: map[string]store.SecretPayload{
			"verify-secret": encryptedSecretForTest(t, masterKey, map[string]any{"verification_token": "agent-token"}),
		},
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        feishuRouteConfigForTest(t, fs.appID, "verify-secret", ""),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs, WithFeishuWebhookSecurity(false, "global-token", ""))

	body := bytes.NewBufferString(`{
		"schema": "2.0",
		"header": {"app_id": "cli_agent_token", "token": "agent-token"},
		"event": {
			"message": {"message_id": "om_agent_token", "chat_id": "oc_agent_token", "chat_type": "p2p", "content": "{\"text\":\"hello\"}"},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected per-agent token to pass after global mismatch, got %d: %s", res.Code, res.Body.String())
	}
	if fs.lastInput.SourceAppID != "cli_agent_token" || fs.lastInput.TargetAgentID != fs.route.AgentID {
		t.Fatalf("expected routed input after per-agent verify, got %+v", fs.lastInput)
	}
}

func TestFeishuWebhookRejectionSendsReplyHint(t *testing.T) {
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)

	var sentMessage bool
	var sendBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_ = json.NewDecoder(r.Body).Decode(&sendBody)
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","tenant_access_token":"tenant-token","expire":7200}`))
		case strings.HasSuffix(r.URL.Path, "/im/v1/messages/om_reject/reply"):
			sentMessage = true
			sendBody = map[string]any{}
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q, want Bearer tenant-token", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&sendBody); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"message_id":"om_rejection_reply"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	t.Setenv("PARSAR_FEISHU_OPENAPI_BASE_URL", upstream.URL)

	fs := &feishuSecretRouteStore{
		appID:   "cli_reject_bot",
		userID:  store.DefaultDevFixtureIDs().UserID,
		member:  false,
		secrets: map[string]store.SecretPayload{"app-secret": encryptedSecretForTest(t, masterKey, map[string]any{"app_secret": "secret-for-reject"})},
	}
	fs.route = store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        feishuRouteConfigForTest(t, fs.appID, "", "app-secret"),
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs)

	body := bytes.NewBufferString(`{
		"header": {"app_id": "cli_reject_bot"},
		"event": {
			"message": {"message_id": "om_reject", "chat_id": "oc_reject", "chat_type": "p2p", "content": "{\"text\":\"hello\"}"},
			"sender": {"sender_id": {"open_id": "ou_test_user", "union_id": "on_test_user"}}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected rejected event to return 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"replied":true`) || !sentMessage {
		t.Fatalf("expected immediate rejection reply, response=%s sent=%v", res.Body.String(), sentMessage)
	}
	if sendBody["receive_id"] != nil || sendBody["msg_type"] != "interactive" || sendBody["reply_in_thread"] != true {
		t.Fatalf("unexpected reply body: %+v", sendBody)
	}
}

func TestFeishuConnectorDiagnosticsAPIAllowsWorkspaceMember(t *testing.T) {
	ids := store.DefaultDevFixtureIDs()
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, newRoleStubStore(map[string]string{ids.UserID: "member"}))

	req := withTestUser(httptest.NewRequest(http.MethodGet, "/api/v1/agents/00000000-0000-0000-0000-000000000901/connector/feishu/diagnostics", nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected member diagnostics read 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	for _, want := range []string{
		`"configured":true`,
		`"event_mode":"websocket"`,
		`"pending_outbound_count":1`,
		`"delivered_outbound_count":1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagnostics response missing %s: %s", want, body)
		}
	}
}

func TestFeishuProvisioningBeginReturnsQRCodeMaterial(t *testing.T) {
	fs := &feishuProvisioningStore{}
	reg := fakeFeishuRegistrationClient{begin: gatewaypkg.FeishuAppRegistrationBeginResult{
		DeviceCode:              "dc_begin",
		UserCode:                "UC-1",
		VerificationURI:         "https://accounts.feishu.cn/cli",
		VerificationURIComplete: "https://open.feishu.cn/page/cli?user_code=UC-1",
		ExpiresIn:               300,
		Interval:                5,
	}}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs, WithFeishuAppRegistration(reg, ""))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000901/connector/feishu/provision/begin", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"device_code":"dc_begin"`) || !strings.Contains(res.Body.String(), `"next_interval_sec":5`) {
		t.Fatalf("unexpected begin response: %s", res.Body.String())
	}
	if len(fs.createdSecrets) != 0 || fs.lastConnector.AgentID != "" {
		t.Fatalf("begin should not mutate store; secrets=%+v connector=%+v", fs.createdSecrets, fs.lastConnector)
	}
}

func TestFeishuProvisioningPollPendingDoesNotWriteSecrets(t *testing.T) {
	fs := &feishuProvisioningStore{}
	reg := fakeFeishuRegistrationClient{poll: gatewaypkg.FeishuAppRegistrationPollResult{
		Kind:            gatewaypkg.FeishuAppRegistrationPollPending,
		NextIntervalSec: 10,
	}}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs, WithFeishuAppRegistration(reg, ""))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000901/connector/feishu/provision/poll", strings.NewReader(`{"device_code":"dc","interval_sec":5}`))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"pending"`) || !strings.Contains(res.Body.String(), `"next_interval_sec":10`) {
		t.Fatalf("unexpected pending response: %s", res.Body.String())
	}
	if len(fs.createdSecrets) != 0 || fs.lastConnector.AgentID != "" {
		t.Fatalf("pending should not mutate store; secrets=%+v connector=%+v", fs.createdSecrets, fs.lastConnector)
	}
}

func TestFeishuProvisioningPollSuccessStoresSecretAndBindsWebSocketConnector(t *testing.T) {
	const masterKey = "test-master-key"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)

	var tokenBody map[string]string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			if err := json.NewDecoder(r.Body).Decode(&tokenBody); err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`)
		case strings.HasSuffix(r.URL.Path, "/bot/v3/info/"):
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("Authorization = %q, want Bearer tenant-token", got)
			}
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","bot":{"app_name":"Agent Bot","open_id":"ou_bot_self"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	fs := &feishuProvisioningStore{}
	reg := fakeFeishuRegistrationClient{poll: gatewaypkg.FeishuAppRegistrationPollResult{
		Kind:         gatewaypkg.FeishuAppRegistrationPollSuccess,
		ClientID:     "cli_qr_bound",
		ClientSecret: "qr-secret",
	}}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, fs, WithFeishuAppRegistration(reg, upstream.URL))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000901/connector/feishu/provision/poll", strings.NewReader(`{"device_code":"dc","interval_sec":5}`))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if tokenBody["app_id"] != "cli_qr_bound" || tokenBody["app_secret"] != "qr-secret" {
		t.Fatalf("unexpected token exchange body: %+v", tokenBody)
	}
	if len(fs.createdSecrets) != 1 {
		t.Fatalf("expected one app secret, got %+v", fs.createdSecrets)
	}
	secret := fs.createdSecrets[0]
	if secret.Kind != "feishu_app_secret" || secret.Provider != "feishu" || secret.AuthType != "app_secret" {
		t.Fatalf("unexpected secret input: %+v", secret)
	}
	if fs.lastConnector.AgentID != "00000000-0000-0000-0000-000000000901" || !fs.lastConnector.Enabled || fs.lastConnector.AppID != "cli_qr_bound" {
		t.Fatalf("unexpected connector input: %+v", fs.lastConnector)
	}
	if fs.lastConnector.AppSecretRef == "" || fs.lastConnector.VerificationTokenRef != "" || fs.lastConnector.EventMode != "websocket" || fs.lastConnector.BotOpenID != "ou_bot_self" {
		t.Fatalf("expected websocket connector with app secret ref and bot id, got %+v", fs.lastConnector)
	}
	if !strings.Contains(res.Body.String(), `"status":"success"`) || !strings.Contains(res.Body.String(), `"bot_name":"Agent Bot"`) {
		t.Fatalf("unexpected success response: %s", res.Body.String())
	}
}

func TestFeishuMessageEventMapsInvalidJSON(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", bytes.NewBufferString(`{`))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestFeishuMessageEventMapsUnknownStoreErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "conversation", err: store.ErrUnknownConversation},
		{name: "sender", err: store.ErrUnknownSender},
		{name: "mention", err: store.ErrUnknownMention},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, errorCreateMessageStore{err: tc.err})

			body := bytes.NewBufferString(`{"event":{"message":{"message_id":"om_message","chat_id":"oc_demo","chat_type":"group","content":"{\"text\":\"@后端Agent 看一下\"}"},"sender":{"sender_id":{"open_id":"ou_demo"}}}}`)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestFeishuMessageEventMapsInternalStoreError(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, errorCreateMessageStore{err: context.Canceled})

	body := bytes.NewBufferString(`{"event":{"message":{"message_id":"om_message","chat_id":"oc_demo","chat_type":"group","content":"{\"text\":\"@后端Agent 看一下\"}"},"sender":{"sender_id":{"open_id":"ou_demo"}}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", res.Code, res.Body.String())
	}
}

func TestFeishuMessageEventVerifiesToken(t *testing.T) {
	capturing := &captureCreateMessageStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capturing, WithFeishuWebhookSecurity(false, "test-token", ""))

	body := bytes.NewBufferString(`{"token":"test-token","event":{"message":{"message_id":"om_message","chat_id":"oc_demo","chat_type":"group","content":"{\"text\":\"@后端Agent 看一下\"}"},"sender":{"sender_id":{"open_id":"ou_demo"}}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if capturing.lastInput.ExternalMessageID != "om_message" {
		t.Fatalf("expected normal flow after token verification, got %+v", capturing.lastInput)
	}
}

func TestFeishuMessageEventRejectsBadToken(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{}, WithFeishuWebhookSecurity(false, "test-token", ""))

	body := bytes.NewBufferString(`{"event":{"message":{"message_id":"om_message"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", res.Code, res.Body.String())
	}
}

func TestFeishuMessageEventChallengeEcho(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{}, WithFeishuWebhookSecurity(false, "test-token", ""))

	body := bytes.NewBufferString(`{"type":"url_verification","challenge":"challenge-code","token":"test-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"challenge":"challenge-code"`) {
		t.Fatalf("expected challenge echo, got %d: %s", res.Code, res.Body.String())
	}
}

func TestFeishuMessageEventChallengeRejectsBadToken(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{}, WithFeishuWebhookSecurity(false, "test-token", ""))

	body := bytes.NewBufferString(`{"type":"url_verification","challenge":"challenge-code","token":"bad"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/feishu/events/message", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", res.Code, res.Body.String())
	}
}

func TestConfigureConversationExternalRefRoute(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := bytes.NewBufferString(`{"gateway":"feishu","external_chat_id":"oc_demo","external_thread_id":"om_thread"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+testConversationID+"/external-ref", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	bodyText := res.Body.String()
	if !strings.Contains(bodyText, `"platform":"feishu"`) || !strings.Contains(bodyText, `"external_id":"oc_demo"`) {
		t.Fatalf("expected external conversation mapping, got %s", bodyText)
	}
}

func TestGatewayInboundValidatesRequiredFields(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := bytes.NewBufferString(`{"gateway":"feishu","conversation":"demo-group","sender":"admin@example.com"}`)
	req := httptest.NewRequest(http.MethodPost, "/dev/gateway/inbound", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestWorkspaceAgentsRouteReturnsEnabledAgents(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agents", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "产品Agent") || !strings.Contains(res.Body.String(), "后端Agent") || !strings.Contains(res.Body.String(), "测试Agent") {
		t.Fatalf("expected three enabled agents, got %s", res.Body.String())
	}
}

func TestConfigureAgentConnectorRoute(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := bytes.NewBufferString(`{"connector_type":"http","endpoint":"http://127.0.0.1:19090/agent"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000010/connector", body)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	bodyText := res.Body.String()
	if !strings.Contains(bodyText, `"connector_type":"http"`) || !strings.Contains(bodyText, `"endpoint":"http://127.0.0.1:19090/agent"`) {
		t.Fatalf("expected configured http connector response, got %s", bodyText)
	}
}

func TestConfigureAgentConnectorRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name       string
		agentID    string
		body       string
		wantStatus int
	}{
		{name: "malformed uuid", agentID: "not-a-uuid", body: `{"connector_type":"http","endpoint":"http://127.0.0.1:19090/agent"}`, wantStatus: http.StatusBadRequest},
		{name: "missing endpoint", agentID: "00000000-0000-0000-0000-000000000010", body: `{"connector_type":"http"}`, wantStatus: http.StatusBadRequest},
		{name: "invalid connector", agentID: "00000000-0000-0000-0000-000000000010", body: `{"connector_type":"bogus"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown", agentID: "00000000-0000-0000-0000-000000099999", body: `{"connector_type":"http","endpoint":"http://127.0.0.1:19090/agent"}`, wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, stubRuntimeStore{})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+tc.agentID+"/connector", bytes.NewBufferString(tc.body))
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, res.Code, res.Body.String())
			}
		})
	}
}

func TestConversationTimelineRouteReturnsMessagesAndRuns(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/"+testConversationID+"/timeline", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, "user message") || !strings.Contains(body, "agent output") || !strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("expected timeline messages and run status, got %s", body)
	}
}

func TestAgentRunRouteReturnsDetail(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runs/"+testRunID, nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"status":"completed"`) || !strings.Contains(body, `"output_message"`) || !strings.Contains(body, "agent output") {
		t.Fatalf("expected completed run detail with output message, got %s", body)
	}
}

func TestGatewayOutboundRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	listReq := httptest.NewRequest(http.MethodGet, "/dev/gateway/outbound?gateway=feishu", nil)
	listRes := httptest.NewRecorder()
	r.ServeHTTP(listRes, listReq)
	if listRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", listRes.Code, listRes.Body.String())
	}
	// The endpoint returns the inflight slot view rather than a P1
	// outbound queue.
	if !strings.Contains(listRes.Body.String(), `"external_chat_id":"oc_demo"`) ||
		!strings.Contains(listRes.Body.String(), `"inflight"`) ||
		!strings.Contains(listRes.Body.String(), `"working_msg_id":"om_stub"`) {
		t.Fatalf("expected inflight conversation response, got %s", listRes.Body.String())
	}

	body := bytes.NewBufferString(`{"delivery_id":"im_delivered_1"}`)
	deliverReq := httptest.NewRequest(http.MethodPost, "/dev/gateway/outbound/00000000-0000-0000-0000-000000000202/delivered", body)
	deliverRes := httptest.NewRecorder()
	r.ServeHTTP(deliverRes, deliverReq)
	if deliverRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", deliverRes.Code, deliverRes.Body.String())
	}
	// Only gateway_delivered_at is persisted; delivery_id /
	// delivery_status fields were dropped with the P1 worker.
	if !strings.Contains(deliverRes.Body.String(), `"gateway_delivered_at"`) {
		t.Fatalf("expected delivered_at stamp in response, got %s", deliverRes.Body.String())
	}
}

func TestWorkspaceAgentRunsRoutePassesStatusFilter(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agent-runs?status=queued", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"status":"queued"`) || strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("expected queued-only run response, got %s", body)
	}
}

// The admin "进行中" tab unions running+queued in one round-trip via
// comma-separated status.
func TestWorkspaceAgentRunsRouteAcceptsMultipleStatuses(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agent-runs?status=running,queued", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	// Stub only has queued+completed; queued must survive, completed must not.
	if !strings.Contains(body, `"status":"queued"`) || strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("expected union filter to keep queued and drop completed, got %s", body)
	}
	// Response shape must carry the echoed statuses list for the UI.
	if !strings.Contains(body, `"statuses":["running","queued"]`) {
		t.Fatalf("expected echoed statuses list, got %s", body)
	}
}

// Handler returns total + limit + offset so the pager can render
// "showing X-Y of N".
func TestWorkspaceAgentRunsRouteReturnsPaginationEnvelope(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agent-runs?limit=1&offset=1", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	// Stub returns 2 runs total (newest first: completed @ 0:01, then
	// queued @ 0:00). offset=1+limit=1 → second item = queued.
	if !strings.Contains(body, `"total":2`) {
		t.Fatalf("expected total=2 in response, got %s", body)
	}
	if !strings.Contains(body, `"limit":1`) || !strings.Contains(body, `"offset":1`) {
		t.Fatalf("expected limit/offset echo, got %s", body)
	}
	if !strings.Contains(body, `"status":"queued"`) || strings.Contains(body, `"status":"completed"`) {
		t.Fatalf("expected offset to skip newest (completed) row and keep older queued, got %s", body)
	}
}

// Agent-detail "近 N 天表现" panel: handler should return the metrics
// snapshot as-is and accept a `?days=` override. Stub returns a fixed
// shape so the route test pins the wire contract.
func TestWorkspaceAgentMetricsRouteReturnsSnapshot(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agents/00000000-0000-0000-0000-000000000009/metrics", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"window_days":30`) {
		t.Fatalf("expected default window_days=30, got %s", body)
	}
	if !strings.Contains(body, `"completed_count":12`) || !strings.Contains(body, `"failed_count":1`) {
		t.Fatalf("expected stub counters in response, got %s", body)
	}
}

func TestWorkspaceAgentMetricsRouteHonorsDaysParam(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agents/00000000-0000-0000-0000-000000000009/metrics?days=7", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"window_days":7`) {
		t.Fatalf("expected window_days=7 in response, got %s", res.Body.String())
	}
}

func TestAgentRunEventsRouteReturnsEventsWithCursor(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agent-runs/"+testRunID+"/events?after_sequence=1", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"events"`) || !strings.Contains(body, `"sequence":2`) || !strings.Contains(body, `"event_kind":"run.completed"`) || strings.Contains(body, `"sequence":1`) {
		t.Fatalf("expected cursor-filtered events response, got %s", body)
	}
}

func TestAgentRunEventsRouteRejectsWorkspaceMismatch(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000009999/agent-runs/"+testRunID+"/events", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestWorkspaceAuditRecordsRouteFiltersBySource(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/audit-records?source=runtime&limit=10", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	// runtime row stays, admin row gets filtered out
	if !strings.Contains(body, `"audit_records"`) || !strings.Contains(body, `"source":"runtime"`) || strings.Contains(body, `"source":"admin"`) {
		t.Fatalf("expected runtime-only audit records, got %s", body)
	}
}

func TestWorkspaceAuditRecordsRouteRejectsBadWorkspaceID(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/not-a-uuid/audit-records", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

func TestWorkspaceUsageRouteReturnsUsageAndFiltersRun(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/usage?agent_run_id="+testRunID+"&limit=1", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"usage_logs"`) || !strings.Contains(body, `"agent_run_id":"`+testRunID+`"`) || !strings.Contains(body, `"input_tokens":12`) || strings.Contains(body, `"agent_run_id":"00000000-0000-0000-0000-000000000102"`) {
		t.Fatalf("expected filtered usage logs, got %s", body)
	}
}

func TestReadRoutesRejectMalformedUUIDs(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{name: "workspace agents", path: "/api/v1/workspaces/not-a-uuid/agents"},
		{name: "workspace agent runs", path: "/api/v1/workspaces/not-a-uuid/agent-runs"},
		{name: "workspace audit records", path: "/api/v1/workspaces/not-a-uuid/audit-records"},
		{name: "workspace usage", path: "/api/v1/workspaces/not-a-uuid/usage"},
		{name: "conversation timeline", path: "/api/v1/conversations/not-a-uuid/timeline"},
		{name: "agent run detail", path: "/api/v1/agent-runs/not-a-uuid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := chi.NewRouter()
			RegisterRoutesWithStore(r, stubRuntimeStore{})

			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for malformed uuid, got %d: %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), "valid uuid") {
				t.Fatalf("expected uuid validation error, got %s", res.Body.String())
			}
		})
	}
}

func TestWorkspaceUsageRouteRejectsMalformedAgentRunID(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/usage?agent_run_id=not-a-uuid", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

type stubRuntimeStore struct {
	httpInvocationConnectorType string
	claimRunID                  string
	httpEndpoint                string
}

type roleStubStore struct {
	stubRuntimeStore
	roles map[string]string
}

func newRoleStubStore(roles map[string]string) roleStubStore {
	return roleStubStore{roles: roles}
}

func (s roleStubStore) GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error) {
	return s.roleForUser(userID)
}

func (s roleStubStore) roleForUser(userID string) (string, error) {
	role, ok := s.roles[userID]
	if !ok {
		return "", store.ErrNotMember
	}
	return role, nil
}

func (stubRuntimeStore) ListScheduledTasksByAgent(ctx context.Context, agentID string) ([]store.ScheduledTaskRead, error) {
	return nil, nil
}

func (stubRuntimeStore) ListScheduledTasksByWorkspace(ctx context.Context, workspaceID string, limit, offset int32) (store.ListScheduledTasksByWorkspaceResult, error) {
	return store.ListScheduledTasksByWorkspaceResult{}, nil
}

func (stubRuntimeStore) CreateScheduledTask(ctx context.Context, in store.CreateScheduledTaskInput) (store.ScheduledTaskRead, error) {
	return store.ScheduledTaskRead{}, nil
}

func (stubRuntimeStore) GetScheduledTask(ctx context.Context, taskID string) (store.ScheduledTaskRead, error) {
	return store.ScheduledTaskRead{}, nil
}

func (stubRuntimeStore) GetScheduledTaskScope(ctx context.Context, taskID string) (store.ScheduledTaskScope, error) {
	return store.ScheduledTaskScope{}, nil
}

func (stubRuntimeStore) UpdateScheduledTask(ctx context.Context, in store.UpdateScheduledTaskInput) (store.ScheduledTaskRead, error) {
	return store.ScheduledTaskRead{}, nil
}

func (stubRuntimeStore) SoftDeleteScheduledTask(ctx context.Context, taskID string) error {
	return nil
}

func (stubRuntimeStore) RunScheduledTaskNow(ctx context.Context, taskID string) (string, error) {
	return "", nil
}

func (stubRuntimeStore) ListAgentRunsByScheduledTask(ctx context.Context, taskID string, limit int32) ([]store.ScheduledTaskRunRead, error) {
	return nil, nil
}

func (stubRuntimeStore) GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error) {
	return "owner", nil
}

func (stubRuntimeStore) GetWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error) {
	return store.WorkspaceSettingsRead{WorkspaceID: workspaceID}, nil
}

func (stubRuntimeStore) GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (store.WorkspaceRuntimeSettingsRead, error) {
	return store.WorkspaceRuntimeSettingsRead{WorkspaceID: workspaceID}, nil
}

func (stubRuntimeStore) SetWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, secretID string, now time.Time) error {
	return nil
}

func (stubRuntimeStore) ClearWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, name, kind string, now time.Time) error {
	return nil
}

func (stubRuntimeStore) RegisterWorkspaceRuntimeCredential(ctx context.Context, input store.RegisterWorkspaceRuntimeCredentialInput) (store.SecretRead, error) {
	return store.SecretRead{
		ID:         "00000000-0000-0000-0000-000000000601",
		Name:       input.Name,
		Kind:       input.Kind,
		Provider:   input.Provider,
		AuthType:   input.AuthType,
		KeyVersion: "v1",
		Status:     "active",
		Masked:     input.Masked,
		Metadata:   map[string]any{"masked": input.Masked},
		CreatedAt:  input.Now,
		UpdatedAt:  input.Now,
	}, nil
}

func (stubRuntimeStore) PatchWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error) {
	return store.WorkspaceSettingsRead{WorkspaceID: workspaceID}, nil
}

func (stubRuntimeStore) ListCapabilities(ctx context.Context, workspaceID string, filter store.ListCapabilityFilter) ([]store.CapabilityRead, error) {
	return []store.CapabilityRead{}, nil
}

func (stubRuntimeStore) ListMarketplaceCapabilities(ctx context.Context, targetWorkspaceID string) ([]store.MarketplaceCapabilityRead, error) {
	return []store.MarketplaceCapabilityRead{}, nil
}

func (stubRuntimeStore) ListWorkspaceMarketplaceInstalls(ctx context.Context, targetWorkspaceID string) ([]store.MarketplaceInstallRead, error) {
	return []store.MarketplaceInstallRead{}, nil
}

func (stubRuntimeStore) CountInstalls(ctx context.Context, sourceCapabilityID string) (int64, error) {
	return 0, nil
}

func (stubRuntimeStore) ListEnabledAgents(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) ([]store.EnabledMarketplaceAgentRead, error) {
	return []store.EnabledMarketplaceAgentRead{}, nil
}

func (stubRuntimeStore) CreateCapability(ctx context.Context, input store.CreateCapabilityInput) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: "00000000-0000-0000-0000-000000000c01", WorkspaceID: input.WorkspaceID, Type: input.Type, Name: input.Name, Visibility: "workspace", Status: "active"}, nil
}

func (stubRuntimeStore) GetCapability(ctx context.Context, capabilityID string) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Type: "mcp", Name: "GitHub", Visibility: "workspace", Status: "active"}, nil
}

func (stubRuntimeStore) UpdateCapability(ctx context.Context, input store.UpdateCapabilityInput) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: input.CapabilityID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Type: "mcp", Name: "GitHub", Visibility: "workspace", Status: "active"}, nil
}

func (stubRuntimeStore) SoftDeleteCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: workspaceID, Type: "mcp", Name: "GitHub", Visibility: "workspace", Status: "active"}, nil
}

func (stubRuntimeStore) PublishCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: workspaceID, Visibility: "public", Status: "active"}, nil
}

func (stubRuntimeStore) UnpublishCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: workspaceID, Visibility: "workspace", Status: "active"}, nil
}

func (stubRuntimeStore) DeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error) {
	now := time.Now().UTC()
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: workspaceID, Visibility: "public", Status: "active", DeprecatedAt: &now}, nil
}

func (stubRuntimeStore) UndeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error) {
	return store.CapabilityRead{ID: capabilityID, WorkspaceID: workspaceID, Visibility: "public", Status: "active"}, nil
}

func (stubRuntimeStore) ListCapabilityVersions(ctx context.Context, capabilityID string) ([]store.CapabilityVersionRead, error) {
	return []store.CapabilityVersionRead{}, nil
}

func (stubRuntimeStore) CreateCapabilityVersion(ctx context.Context, input store.CreateCapabilityVersionInput) (store.CapabilityVersionRead, error) {
	return store.CapabilityVersionRead{ID: "00000000-0000-0000-0000-000000000c02", CapabilityID: input.CapabilityID, Version: input.Version}, nil
}

func (stubRuntimeStore) GetCapabilityVersion(ctx context.Context, capabilityVersionID string) (store.CapabilityVersionRead, error) {
	return store.CapabilityVersionRead{ID: capabilityVersionID, CapabilityID: "00000000-0000-0000-0000-000000000c01", Version: "v1.0.0"}, nil
}

// Stub implementations for the capability import / credential_kinds methods
// added in plan M3. These tests don't exercise the import flow, so returning
// trivial values keeps the existing assertions green.
func (stubRuntimeStore) ImportCapability(ctx context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error) {
	return store.ImportCapabilityResult{}, nil
}

func (stubRuntimeStore) ImportCapabilityVersion(ctx context.Context, input store.ImportCapabilityVersionInput) (store.ImportCapabilityResult, error) {
	return store.ImportCapabilityResult{}, nil
}

func (stubRuntimeStore) ListCredentialKinds(ctx context.Context) ([]store.CredentialKindRead, error) {
	return []store.CredentialKindRead{}, nil
}

func (stubRuntimeStore) GetCredentialKindByCode(ctx context.Context, code string) (store.CredentialKindRead, error) {
	return store.CredentialKindRead{}, nil
}

func (stubRuntimeStore) CreateCredentialKind(ctx context.Context, input store.CreateCredentialKindInput) (store.CredentialKindRead, error) {
	return store.CredentialKindRead{}, nil
}

func (stubRuntimeStore) ListUserCredentials(ctx context.Context, userID string) ([]store.UserCredentialRead, error) {
	return []store.UserCredentialRead{}, nil
}

func (stubRuntimeStore) CreateUserCredential(ctx context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error) {
	return store.UserCredentialRead{ID: "00000000-0000-0000-0000-000000000c03", UserID: input.UserID, Kind: input.Kind, DisplayName: input.DisplayName}, nil
}

func (stubRuntimeStore) GetUserCredential(ctx context.Context, credentialID string) (store.UserCredentialRead, error) {
	return store.UserCredentialRead{ID: credentialID, UserID: "00000000-0000-0000-0000-0000000000aa", Kind: "github_pat", DisplayName: "GitHub"}, nil
}

func (stubRuntimeStore) UpdateUserCredential(ctx context.Context, input store.UpdateUserCredentialInput) (store.UserCredentialRead, error) {
	return store.UserCredentialRead{ID: input.CredentialID, UserID: "00000000-0000-0000-0000-0000000000aa", Kind: "github_pat", DisplayName: "GitHub"}, nil
}

func (stubRuntimeStore) SoftDeleteUserCredential(ctx context.Context, credentialID string) (store.UserCredentialRead, error) {
	return store.UserCredentialRead{ID: credentialID, UserID: "00000000-0000-0000-0000-0000000000aa", Kind: "github_pat", DisplayName: "GitHub"}, nil
}

func (stubRuntimeStore) ListAgentCapabilities(ctx context.Context, agentID string) ([]store.AgentCapabilityRead, error) {
	return []store.AgentCapabilityRead{}, nil
}

func (stubRuntimeStore) GetEnabledMarketplaceCapabilitiesForAgent(ctx context.Context, agentID string) ([]store.EnabledCapabilityRead, error) {
	return []store.EnabledCapabilityRead{}, nil
}

func (stubRuntimeStore) EnableAgentCapability(ctx context.Context, agentID string, versionID string, configuration map[string]any, pinningMode string) (store.AgentCapabilityRead, error) {
	return store.AgentCapabilityRead{ID: "00000000-0000-0000-0000-000000000c04", AgentID: agentID, CapabilityID: "00000000-0000-0000-0000-000000000c01", CapabilityVersionID: versionID, Enabled: true, PinningMode: pinningMode}, nil
}

func (stubRuntimeStore) UpgradeAgentCapability(ctx context.Context, agentID string, capabilityID string, newVersionID string, pinningMode string) (store.AgentCapabilityRead, error) {
	return store.AgentCapabilityRead{ID: "00000000-0000-0000-0000-000000000c04", AgentID: agentID, CapabilityID: capabilityID, CapabilityVersionID: newVersionID, Enabled: true, PinningMode: pinningMode}, nil
}

func (stubRuntimeStore) UninstallWorkspaceMarketplaceCapability(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) (int64, error) {
	return 0, nil
}

func (stubRuntimeStore) DeleteAgentCapability(ctx context.Context, agentID string, capabilityVersionID string) error {
	return nil
}

func (stubRuntimeStore) IsBuiltinCapabilityEnabled(ctx context.Context, agentID, key string) (bool, error) {
	return true, nil
}

func (stubRuntimeStore) SetBuiltinCapabilityEnabled(ctx context.Context, agentID, key string, enabled bool) error {
	return nil
}

func (stubRuntimeStore) CreateAgent(ctx context.Context, input store.CreateAgentInput) (store.CreateAgentResult, error) {
	agentConfig := map[string]any{"capabilities": input.Capabilities}
	for k, v := range input.AgentConfig {
		agentConfig[k] = v
	}
	if rt := strings.TrimSpace(input.Runtime); rt != "" {
		agentConfig["runtime"] = rt
	}
	result := store.CreateAgentResult{Agent: store.AgentSummary{ID: "00000000-0000-0000-0000-000000000901", WorkspaceID: input.WorkspaceID, Name: input.Name, Slug: "new-agent", ConnectorType: input.ConnectorType, Status: "active", Capabilities: input.Capabilities, Config: agentConfig}}
	for _, capability := range input.InitialCapabilities {
		result.InitialCapabilities = append(result.InitialCapabilities, store.AgentCapabilityRead{ID: "00000000-0000-0000-0000-000000000c04", AgentID: result.Agent.ID, CapabilityID: "00000000-0000-0000-0000-000000000c01", CapabilityVersionID: capability.CapabilityVersionID, Enabled: true, Configuration: nonNilStubMap(capability.Configuration)})
	}
	return result, nil
}

func (stubRuntimeStore) GetAgent(ctx context.Context, agentID string) (store.AgentSummary, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentSummary{}, store.ErrUnknownAgent
	}
	return store.AgentSummary{ID: agentID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Name: "Agent", Slug: "agent", ConnectorType: "agent_daemon", Status: "active"}, nil
}

func (stubRuntimeStore) UpdateAgent(ctx context.Context, input store.UpdateAgentInput) (store.AgentSummary, []string, error) {
	name := "Agent"
	if input.Name != nil {
		name = *input.Name
	}
	return store.AgentSummary{ID: input.AgentID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Name: name, Slug: "agent", ConnectorType: "agent_daemon", Status: "active", Capabilities: input.Capabilities}, []string{"name"}, nil
}

func (stubRuntimeStore) UpdateAgentVisibility(ctx context.Context, agentID, newVisibility, actorID string) (store.AgentVisibilityChange, error) {
	switch newVisibility {
	case "workspace", "tenant", "public":
		return store.AgentVisibilityChange{
			AgentID:       agentID,
			WorkspaceID:   "00000000-0000-0000-0000-000000000002",
			Name:          "Agent",
			Slug:          "agent",
			OldVisibility: "workspace",
			NewVisibility: newVisibility,
			Noop:          newVisibility == "workspace",
		}, nil
	default:
		return store.AgentVisibilityChange{}, store.ErrInvalidAgentVisibility
	}
}

func (stubRuntimeStore) GetFeishuConnectorDiagnostics(ctx context.Context, agentID string) (store.FeishuConnectorDiagnosticsRead, error) {
	return store.FeishuConnectorDiagnosticsRead{
		AgentID:                agentID,
		WorkspaceID:            "00000000-0000-0000-0000-000000000002",
		Configured:             true,
		Enabled:                true,
		EventMode:              "websocket",
		AppIDSet:               true,
		AppSecretSet:           true,
		BotOpenIDSet:           true,
		ConversationCount:      1,
		InboundMessageCount:    2,
		OutboundMessageCount:   2,
		PendingOutboundCount:   1,
		DeliveredOutboundCount: 1,
	}, nil
}

func (stubRuntimeStore) UpdateAgentFeishuConnector(ctx context.Context, input store.UpdateAgentFeishuConnectorInput, actorID string) (store.AgentFeishuConnectorChange, error) {
	// Minimal stub: validate the same incomplete/uniqueness gates the real
	// store enforces, so handler-level tests can exercise the error paths.
	if input.Enabled && (input.AppID == "" || input.AppSecretRef == "" || input.VerificationTokenRef == "") {
		return store.AgentFeishuConnectorChange{}, store.ErrFeishuConnectorIncomplete
	}
	return store.AgentFeishuConnectorChange{
		AgentID:     input.AgentID,
		WorkspaceID: "00000000-0000-0000-0000-000000000002",
		Name:        "Agent",
		Slug:        "agent",
		New: store.FeishuConnectorSnapshot{
			Enabled:              input.Enabled,
			AppID:                input.AppID,
			AppSecretRef:         input.AppSecretRef,
			VerificationTokenRef: input.VerificationTokenRef,
			EncryptKeyRef:        input.EncryptKeyRef,
			BotOpenID:            input.BotOpenID,
			EventMode:            input.EventMode,
			RoutingMode:          input.RoutingMode,
		},
	}, nil
}

func (stubRuntimeStore) GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error) {
	if appID != "cli_test_bot" {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return store.FeishuAgentRoute{
		AgentID:       "00000000-0000-0000-0000-000000000901",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		AgentName:     "Agent",
		AgentSlug:     "agent",
		Visibility:    "workspace",
		Config:        []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_test_bot"}}}`),
	}, nil
}

func (stubRuntimeStore) GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error) {
	switch agentID {
	case "00000000-0000-0000-0000-000000000901":
		return store.FeishuAgentRoute{
			AgentID:       "00000000-0000-0000-0000-000000000901",
			WorkspaceID:   "00000000-0000-0000-0000-000000000002",
			WorkspaceName: "Default Workspace",
			AgentName:     "Agent",
			AgentSlug:     "agent",
			Visibility:    "workspace",
			Config:        []byte(`{}`),
		}, nil
	case "00000000-0000-0000-0000-000000000902":
		return store.FeishuAgentRoute{
			AgentID:       "00000000-0000-0000-0000-000000000902",
			WorkspaceID:   "00000000-0000-0000-0000-000000000002",
			WorkspaceName: "Default Workspace",
			AgentName:     "Backend Agent",
			AgentSlug:     "backend",
			Visibility:    "workspace",
			Config:        []byte(`{}`),
		}, nil
	default:
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
}

func (stubRuntimeStore) ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error) {
	return []store.FeishuSharedBotAgent{{
		AgentID:       "00000000-0000-0000-0000-000000000902",
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		WorkspaceName: "Default Workspace",
		WorkspaceSlug: "default",
		AgentName:     "Backend Agent",
		AgentSlug:     "backend",
		Visibility:    "workspace",
	}}, nil
}

func (stubRuntimeStore) UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error {
	return nil
}

func (stubRuntimeStore) GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error) {
	return "", store.ErrUnknownGatewaySessionSelection
}

func (stubRuntimeStore) ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error {
	return nil
}

func (stubRuntimeStore) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	if subject == "on_test_user" || subject == "ou_feishu_admin" {
		return store.DefaultDevFixtureIDs().UserID, nil
	}
	return "", store.ErrUnknownPlatformUser
}

func (stubRuntimeStore) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return userID == store.DefaultDevFixtureIDs().UserID, nil
}

func (stubRuntimeStore) GetWorkspaceVisibility(context.Context, string) (string, error) {
	return "private", nil
}

func (stubRuntimeStore) ListActiveWorkspaceOwnerNames(context.Context, string, int32) ([]string, error) {
	return nil, nil
}

func (stubRuntimeStore) FindConversationByExternalRef(context.Context, string, string, string) (string, error) {
	return "", store.ErrUnknownConversation
}

func (stubRuntimeStore) CancelAllInflightForConversation(context.Context, string, string) ([]store.SupersededRun, error) {
	return nil, nil
}

func (stubRuntimeStore) HasFeishuThreadInboundHistory(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}

func (stubRuntimeStore) HasThreadInboundHistory(_ context.Context, _, _, _ string) (bool, error) {
	return false, nil
}

func (stubRuntimeStore) DeleteAgent(ctx context.Context, agentID string, actorID string) (store.DeleteAgentResult, int64, error) {
	if agentID == "00000000-0000-0000-0000-000000000999" {
		return store.DeleteAgentResult{}, 2, store.ErrInFlightAgentRuns
	}
	return store.DeleteAgentResult{Agent: store.AgentSummary{ID: agentID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Name: "Agent", Slug: "agent"}}, 0, nil
}

type captureCreateMessageStore struct {
	stubRuntimeStore
	lastInput store.CreateInboundIMMessageInput
}

type errorCreateMessageStore struct {
	stubRuntimeStore
	err error
}

func (s *captureCreateMessageStore) CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error) {
	s.lastInput = input
	return s.stubRuntimeStore.CreateInboundIMMessage(ctx, input)
}

func (s errorCreateMessageStore) CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error) {
	return store.CreateInboundIMMessageResult{}, s.err
}

func (stubRuntimeStore) CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error) {
	return store.CreateInboundIMMessageResult{
		MessageID: "message-1",
		RunIDs:    []string{"run-1", "run-2"},
		Mentions:  input.Mentions,
		CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (stubRuntimeStore) CompleteAgentRun(ctx context.Context, input store.CompleteAgentRunInput) (store.CompleteAgentRunResult, error) {
	if input.RunID == "00000000-0000-0000-0000-000000099999" {
		return store.CompleteAgentRunResult{}, store.ErrUnknownAgentRun
	}
	usage := store.UsageLogRead{
		ID:           "00000000-0000-0000-0000-000000000501",
		WorkspaceID:  "00000000-0000-0000-0000-000000000002",
		AgentRunID:   input.RunID,
		Provider:     input.Usage.Provider,
		Model:        input.Usage.Model,
		InputTokens:  input.Usage.InputTokens,
		OutputTokens: input.Usage.OutputTokens,
		CostUSD:      input.Usage.CostUSD,
		Raw:          input.Usage.Raw,
		CreatedAt:    time.Date(2026, 5, 22, 0, 1, 0, 0, time.UTC),
	}
	return store.CompleteAgentRunResult{
		RunID:           input.RunID,
		MessageID:       "output-message-1",
		Status:          "completed",
		ChildRunIDs:     []string{"child-run-1"},
		SkippedMentions: []store.SkippedAgentMention{{Mention: "@后端Agent", AgentID: "agent-2", Reason: "self_trigger"}},
		StartedAt:       time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
		FinishedAt:      time.Date(2026, 5, 22, 0, 1, 0, 0, time.UTC),
		Usage:           usage,
	}, nil
}

func (s stubRuntimeStore) GetHTTPAgentRunInvocation(ctx context.Context, runID string) (store.HTTPAgentRunInvocation, error) {
	if runID == "00000000-0000-0000-0000-000000099999" {
		return store.HTTPAgentRunInvocation{}, store.ErrUnknownAgentRun
	}
	connectorType := s.httpInvocationConnectorType
	if connectorType == "" {
		connectorType = "http"
	}
	if connectorType != "http" {
		return store.HTTPAgentRunInvocation{}, store.ErrInvalidHTTPConnector
	}
	return store.HTTPAgentRunInvocation{
		RunID:                 runID,
		WorkspaceID:           "00000000-0000-0000-0000-000000000002",
		ConversationID:        testConversationID,
		AgentID:               "00000000-0000-0000-0000-000000000007",
		AgentName:             "后端Agent",
		AgentSlug:             "backend-agent",
		ConnectorType:         "http",
		Status:                "queued",
		TriggerMessageContent: "@后端Agent 看一下 API",
		AgentConfig:           map[string]any{"profile": map[string]any{"skills": []any{"go"}}, "endpoint": s.httpEndpoint},
	}, nil
}

func (s stubRuntimeStore) ClaimNextQueuedHTTPAgentRun(ctx context.Context) (store.ClaimHTTPAgentRunResult, error) {
	if s.claimRunID == "none" {
		return store.ClaimHTTPAgentRunResult{Claimed: false}, nil
	}
	runID := s.claimRunID
	if runID == "" {
		runID = testRunID
	}
	return store.ClaimHTTPAgentRunResult{RunID: runID, Claimed: true}, nil
}

func (stubRuntimeStore) FailAgentRun(ctx context.Context, input store.FailAgentRunInput) error {
	return nil
}

func (stubRuntimeStore) CancelAgentRun(ctx context.Context, runID, reason string) (bool, error) {
	return true, nil
}

func (stubRuntimeStore) GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error) {
	return store.SecretPayload{}, store.ErrUnknownSecret
}

func (stubRuntimeStore) RequeueFailedAgentRun(ctx context.Context, input store.RequeueAgentRunInput) (store.RequeueAgentRunResult, error) {
	if input.RunID == "00000000-0000-0000-0000-000000099999" {
		return store.RequeueAgentRunResult{}, store.ErrUnknownAgentRun
	}
	return store.RequeueAgentRunResult{
		RunID:          input.RunID,
		WorkspaceID:    "00000000-0000-0000-0000-000000000002",
		ConversationID: testConversationID,
		Status:         "queued",
	}, nil
}

func (stubRuntimeStore) ConfigureDevConversationExternalRef(ctx context.Context, input store.ConfigureDevConversationExternalRefInput) (store.ConfigureDevConversationExternalRefResult, error) {
	if input.ConversationID == "00000000-0000-0000-0000-000000099999" {
		return store.ConfigureDevConversationExternalRefResult{}, store.ErrUnknownConversation
	}
	return store.ConfigureDevConversationExternalRefResult{
		ConversationID:   input.ConversationID,
		WorkspaceID:      "00000000-0000-0000-0000-000000000002",
		Platform:         input.Gateway,
		ExternalID:       input.ExternalChatID,
		ExternalThreadID: input.ExternalThreadID,
	}, nil
}

func (stubRuntimeStore) ConfigureDevAgentConnector(ctx context.Context, input store.ConfigureDevAgentConnectorInput) (store.ConfigureDevAgentConnectorResult, error) {
	if input.AgentID == "00000000-0000-0000-0000-000000099999" {
		return store.ConfigureDevAgentConnectorResult{}, store.ErrUnknownAgent
	}
	if input.ConnectorType != "http" {
		return store.ConfigureDevAgentConnectorResult{}, store.ErrInvalidConnectorType
	}
	config := map[string]any{}
	if input.Endpoint != "" {
		config["endpoint"] = input.Endpoint
	}
	return store.ConfigureDevAgentConnectorResult{
		AgentID:       input.AgentID,
		Name:          "后端Agent",
		Slug:          "backend-agent",
		ConnectorType: input.ConnectorType,
		AgentConfig:   config,
	}, nil
}

func (stubRuntimeStore) ConfigureAgentProfile(ctx context.Context, input store.ConfigureAgentProfileInput) (store.ConfigureDevAgentConnectorResult, error) {
	if input.AgentID == "00000000-0000-0000-0000-000000099999" {
		return store.ConfigureDevAgentConnectorResult{}, store.ErrUnknownAgent
	}
	if input.ModelID == "00000000-0000-0000-0000-000000000901" {
		return store.ConfigureDevAgentConnectorResult{}, store.ErrModelDisabled
	}
	if input.ModelID == "00000000-0000-0000-0000-000000000902" {
		return store.ConfigureDevAgentConnectorResult{}, store.ErrUnknownModel
	}
	return store.ConfigureDevAgentConnectorResult{
		AgentID:       input.AgentID,
		Name:          "后端Agent",
		Slug:          "backend-agent",
		ConnectorType: "agent_daemon",
		AgentConfig:   map[string]any{"model_id": input.ModelID},
	}, nil
}

func (stubRuntimeStore) GetAgentDetail(ctx context.Context, agentID string) (store.AgentStatusRead, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentStatusRead{}, fmt.Errorf("%w: %s", store.ErrUnknownAgent, agentID)
	}
	return store.AgentStatusRead{
		AgentID:       agentID,
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		AgentName:     "后端Agent",
		AgentSlug:     "backend-agent",
		ConnectorType: "agent_daemon",
		Status:        "active",
		Config:        map[string]any{},
		CreatedBy:     store.DefaultDevFixtureIDs().UserID,
	}, nil
}

func (stubRuntimeStore) GetAgentRuntimeBinding(ctx context.Context, workspaceID, agentID string) (store.AgentRuntimeBinding, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentRuntimeBinding{}, fmt.Errorf("%w: %s", store.ErrUnknownAgent, agentID)
	}
	return store.AgentRuntimeBinding{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		RuntimeID:   "",
	}, nil
}

func (stubRuntimeStore) SetAgentRuntime(ctx context.Context, input store.SetAgentRuntimeInput) (store.AgentRuntimeBinding, error) {
	if input.AgentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentRuntimeBinding{}, fmt.Errorf("%w: %s", store.ErrUnknownAgent, input.AgentID)
	}
	return store.AgentRuntimeBinding{
		AgentID:     input.AgentID,
		WorkspaceID: input.WorkspaceID,
		RuntimeID:   input.RuntimeID,
	}, nil
}

func (s stubRuntimeStore) ListWorkspaceAgentsForAdmin(ctx context.Context, workspaceID string) ([]store.AgentRead, error) {
	enabled, err := s.ListWorkspaceEnabledAgents(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	return append(enabled, store.AgentRead{AgentID: "agent-99", Name: "停用Agent", Slug: "disabled-agent", ConnectorType: "agent_daemon", Status: "disabled"}), nil
}

func (stubRuntimeStore) DisableAgent(ctx context.Context, agentID string) (store.AgentStatusRead, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentStatusRead{}, fmt.Errorf("%w: %s", store.ErrUnknownAgent, agentID)
	}
	return store.AgentStatusRead{
		AgentID:       agentID,
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		AgentName:     "后端Agent",
		AgentSlug:     "backend-agent",
		ConnectorType: "agent_daemon",
		Status:        "disabled",
		Config:        map[string]any{},
	}, nil
}

func (stubRuntimeStore) EnableAgent(ctx context.Context, agentID string) (store.AgentStatusRead, error) {
	if agentID == "00000000-0000-0000-0000-000000099999" {
		return store.AgentStatusRead{}, fmt.Errorf("%w: %s", store.ErrUnknownAgent, agentID)
	}
	return store.AgentStatusRead{
		AgentID:       agentID,
		WorkspaceID:   "00000000-0000-0000-0000-000000000002",
		AgentName:     "后端Agent",
		AgentSlug:     "backend-agent",
		ConnectorType: "agent_daemon",
		Status:        "active",
		Config:        map[string]any{},
	}, nil
}

func (stubRuntimeStore) CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error) {
	return store.SecretRead{
		ID:         "00000000-0000-0000-0000-000000000601",
		Name:       input.Name,
		Kind:       "model_provider",
		Provider:   input.Provider,
		AuthType:   input.AuthType,
		KeyVersion: "v1",
		Status:     "active",
		Masked:     input.Masked,
		Metadata:   map[string]any{"masked": input.Masked},
		CreatedAt:  time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (stubRuntimeStore) ListSecrets(ctx context.Context, workspaceID string, limit int32) ([]store.SecretRead, error) {
	return []store.SecretRead{{
		ID:         "00000000-0000-0000-0000-000000000601",
		Name:       "OpenAI",
		Kind:       "model_provider",
		Provider:   "openai",
		AuthType:   "api_key",
		KeyVersion: "v1",
		Status:     "active",
		Masked:     "sk-tes...alue",
		Metadata:   map[string]any{"masked": "sk-tes...alue"},
		CreatedAt:  time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func (stubRuntimeStore) DisableSecret(ctx context.Context, workspaceID string, secretID string) (store.SecretRead, error) {
	if secretID == "00000000-0000-0000-0000-000000099999" {
		return store.SecretRead{}, store.ErrUnknownSecret
	}
	return store.SecretRead{
		ID:         secretID,
		Name:       "OpenAI",
		Kind:       "model_provider",
		Provider:   "openai",
		AuthType:   "api_key",
		KeyVersion: "v1",
		Status:     "disabled",
		Masked:     "sk-tes...alue",
		Metadata:   map[string]any{"masked": "sk-tes...alue"},
		CreatedAt:  time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (stubRuntimeStore) ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (store.ModelRuntime, error) {
	return store.ModelRuntime{ModelID: modelID, ModelName: "GPT 5.4", ModelKey: "gpt-5.4", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://platform-api.example.com/v1", CredentialMode: "inline_secret", SecretID: "00000000-0000-0000-0000-000000000601"}, nil
}

func (stubRuntimeStore) ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (store.ModelRuntime, error) {
	return store.ModelRuntime{ModelID: modelID, ModelName: "GPT 5.4", ModelKey: "gpt-5.4", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://platform-api.example.com/v1", CredentialMode: "credential_ref", CredentialKindCode: "openai_api_key"}, nil
}

func (stubRuntimeStore) ListModels(ctx context.Context, workspaceID string, limit int32) ([]store.ModelRead, error) {
	return []store.ModelRead{{ID: "00000000-0000-0000-0000-000000000702", Slug: "model-test", Name: "GPT 5.4", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://platform-api.example.com/v1", ModelKey: "gpt-5.4", CredentialMode: "inline_secret", SecretID: "00000000-0000-0000-0000-000000000601", Status: "active", Config: map[string]any{"capabilities": map[string]any{"tool_call": true}, "limits": map[string]any{"context": 1000}}, CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)}}, nil
}

func (stubRuntimeStore) CreateModel(ctx context.Context, input store.CreateModelInput) (store.ModelRead, error) {
	return store.ModelRead{ID: "00000000-0000-0000-0000-000000000702", Slug: "model-test", Name: input.Name, ProviderType: input.ProviderType, Adapter: input.Adapter, BaseURL: input.BaseURL, ModelKey: input.ModelKey, CredentialMode: input.CredentialMode, SecretID: input.SecretID, CredentialKindCode: input.CredentialKindCode, Status: "active", Config: input.Config, CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)}, nil
}

func (stubRuntimeStore) DisableModel(ctx context.Context, workspaceID string, modelID string) (store.ModelRead, error) {
	if modelID == "00000000-0000-0000-0000-000000099999" {
		return store.ModelRead{}, store.ErrUnknownModel
	}
	return store.ModelRead{ID: modelID, Slug: "model-test", Name: "GPT 5.4", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://platform-api.example.com/v1", ModelKey: "gpt-5.4", CredentialMode: "inline_secret", SecretID: "00000000-0000-0000-0000-000000000601", Status: "disabled", Config: map[string]any{"capabilities": map[string]any{"tool_call": true}, "limits": map[string]any{"context": 1000}}, CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)}, nil
}

func (stubRuntimeStore) UpdateModel(ctx context.Context, input store.UpdateModelInput) (store.ModelRead, error) {
	if input.ModelID == "00000000-0000-0000-0000-000000099999" {
		return store.ModelRead{}, store.ErrUnknownModel
	}
	return store.ModelRead{ID: input.ModelID, Slug: "model-test", Name: input.Name, ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: input.BaseURL, ModelKey: input.ModelKey, CredentialMode: "inline_secret", SecretID: input.SecretID, CredentialKindCode: input.CredentialKindCode, Status: "active", Config: input.Config, CreatedAt: time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)}, nil
}

func (stubRuntimeStore) ListWorkspaceEnabledAgents(ctx context.Context, workspaceID string) ([]store.AgentRead, error) {
	return []store.AgentRead{
		{AgentID: "agent-1", Name: "产品Agent", Slug: "product-agent", ConnectorType: "agent_daemon", Status: "active"},
		{AgentID: "agent-2", Name: "后端Agent", Slug: "backend-agent", ConnectorType: "agent_daemon", Status: "active"},
		{AgentID: "agent-3", Name: "测试Agent", Slug: "test-agent", ConnectorType: "agent_daemon", Status: "active"},
	}, nil
}

func (stubRuntimeStore) CreateWorkspaceConversation(ctx context.Context, input store.CreateWorkspaceConversationInput) (store.ConversationRead, error) {
	switch input.WorkspaceID {
	case "00000000-0000-0000-0000-000000000404":
		return store.ConversationRead{}, fmt.Errorf("%w: %s", store.ErrUnknownWorkspace, input.WorkspaceID)
	case "00000000-0000-0000-0000-000000000400":
		return store.ConversationRead{}, fmt.Errorf("invalid conversation surface: %s", input.Surface)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "未命名会话"
	}
	surface := strings.TrimSpace(input.Surface)
	if surface == "" {
		surface = "web"
	}
	form := strings.TrimSpace(input.Form)
	if form == "" {
		form = "thread"
	}
	return store.ConversationRead{
		ID:          "00000000-0000-0000-0000-000000000c11",
		WorkspaceID: input.WorkspaceID,
		Surface:     surface,
		Form:        form,
		Title:       title,
		Status:      "active",
		Metadata:    nonNilStubMap(input.Metadata),
	}, nil
}

func (stubRuntimeStore) ListWorkspaceConversations(ctx context.Context, workspaceID string, agentID string, limit int32) ([]store.ConversationListItem, error) {
	if workspaceID == "00000000-0000-0000-0000-000000000404" {
		return nil, fmt.Errorf("%w: %s", store.ErrUnknownWorkspace, workspaceID)
	}
	if agentID == "bad-uuid" {
		return nil, fmt.Errorf("%w: agent_id", store.ErrInvalidWorkspaceInput)
	}
	return []store.ConversationListItem{{
		ConversationRead: store.ConversationRead{
			ID: "00000000-0000-0000-0000-000000000c10", WorkspaceID: workspaceID,
			Surface: "web", Form: "thread", Title: "Demo Group", Status: "active",
		},
		MessageCount:          2,
		LastMessagePreview:    "ping",
		LastMessageSenderType: "user",
	}}, nil
}

func (stubRuntimeStore) GetConversation(ctx context.Context, conversationID string) (store.ConversationRead, error) {
	if conversationID == "00000000-0000-0000-0000-000000000404" {
		return store.ConversationRead{}, fmt.Errorf("%w: %s", store.ErrUnknownConversation, conversationID)
	}
	return store.ConversationRead{
		ID: conversationID, WorkspaceID: "00000000-0000-0000-0000-0000000000aa",
		Surface: "web", Form: "thread", Title: "Demo Group", Status: "active",
	}, nil
}

func (stubRuntimeStore) UpdateConversationTitle(ctx context.Context, conversationID string, title string) error {
	if conversationID == "00000000-0000-0000-0000-000000000404" {
		return fmt.Errorf("%w: %s", store.ErrUnknownConversation, conversationID)
	}
	return nil
}

func (stubRuntimeStore) SoftDeleteConversation(ctx context.Context, conversationID string) error {
	if conversationID == "00000000-0000-0000-0000-000000000404" {
		return fmt.Errorf("%w: %s", store.ErrUnknownConversation, conversationID)
	}
	return nil
}

func (stubRuntimeStore) SendUserMessageToConversation(ctx context.Context, input store.SendUserMessageToConversationInput) (store.SendUserMessageToConversationResult, error) {
	return store.SendUserMessageToConversationResult{Message: store.MessageRead{ID: "message-1", ConversationID: input.ConversationID, SenderType: "user", SenderID: input.UserID, Kind: "message", ContentFormat: "text", Content: input.Content, Metadata: map[string]any{}, CreatedAt: time.Now().UTC()}}, nil
}

func (stubRuntimeStore) GetConversationTimeline(ctx context.Context, conversationID string, limit int32) (store.ConversationTimelineRead, error) {
	createdAt := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	completedAt := createdAt.Add(time.Minute)
	run := store.AgentRunBriefRead{
		ID:               testRunID,
		ConversationID:   conversationID,
		TriggerMessageID: "00000000-0000-0000-0000-000000000201",
		OutputMessageID:  "00000000-0000-0000-0000-000000000202",
		AgentID:          "00000000-0000-0000-0000-000000000007",
		AgentName:        "后端Agent",
		AgentSlug:        "backend-agent",
		ConnectorType:    "agent_daemon",
		Status:           "completed",
		CreatedAt:        createdAt,
		FinishedAt:       &completedAt,
	}
	childRun := run
	childRun.ID = "00000000-0000-0000-0000-000000000103"
	childRun.TriggerMessageID = "00000000-0000-0000-0000-000000000202"
	childRun.OutputMessageID = ""
	childRun.AgentID = "00000000-0000-0000-0000-000000000008"
	childRun.AgentName = "测试Agent"
	childRun.AgentSlug = "test-agent"
	childRun.Status = "queued"
	childRun.FinishedAt = nil
	return store.ConversationTimelineRead{
		ConversationID: conversationID,
		Messages: []store.MessageRead{
			{ID: "00000000-0000-0000-0000-000000000201", ConversationID: conversationID, SenderType: "user", SenderID: "00000000-0000-0000-0000-000000000001", Kind: "message", ContentFormat: "text", Content: "user message", CreatedAt: createdAt, Runs: []store.AgentRunBriefRead{run}},
			{ID: "00000000-0000-0000-0000-000000000202", ConversationID: conversationID, SenderType: "agent", SenderID: "00000000-0000-0000-0000-000000000007", Kind: "message", ContentFormat: "text", Content: "agent output", CreatedAt: completedAt, Runs: []store.AgentRunBriefRead{childRun}},
		},
		AgentRuns: []store.AgentRunBriefRead{run, childRun},
	}, nil
}

func (stubRuntimeStore) GetAgentRun(ctx context.Context, runID string) (store.AgentRunDetailRead, error) {
	if runID == "missing-run" {
		return store.AgentRunDetailRead{}, store.ErrUnknownAgentRun
	}
	createdAt := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	finishedAt := createdAt.Add(time.Minute)
	return store.AgentRunDetailRead{
		AgentRunBriefRead: store.AgentRunBriefRead{
			ID:               runID,
			WorkspaceID:      testWorkspaceID,
			ConversationID:   testConversationID,
			TriggerMessageID: "00000000-0000-0000-0000-000000000201",
			OutputMessageID:  "00000000-0000-0000-0000-000000000202",
			AgentID:          "00000000-0000-0000-0000-000000000007",
			AgentName:        "后端Agent",
			AgentSlug:        "backend-agent",
			ConnectorType:    "agent_daemon",
			Status:           "completed",
			CreatedAt:        createdAt,
			FinishedAt:       &finishedAt,
		},
		RequestedByType: "user",
		RequestedByID:   "00000000-0000-0000-0000-000000000001",
		UpdatedAt:       finishedAt,
		OutputMessage:   &store.MessageRead{ID: "00000000-0000-0000-0000-000000000202", ConversationID: testConversationID, SenderType: "agent", SenderID: "00000000-0000-0000-0000-000000000007", Kind: "message", ContentFormat: "text", Content: "agent output", CreatedAt: finishedAt},
		Artifacts:       []store.ArtifactRead{},
		Usage:           []store.UsageLogRead{{ID: "00000000-0000-0000-0000-000000000501", WorkspaceID: "00000000-0000-0000-0000-000000000002", AgentRunID: runID, Provider: "fake", Model: "parsar-test-model", InputTokens: 42, OutputTokens: 18, CostUSD: 0.000123, Raw: map[string]any{"source": "runtime"}, CreatedAt: finishedAt}},
	}, nil
}

func (stubRuntimeStore) ListAgentRunEvents(ctx context.Context, runID string, afterSequence int64) ([]store.AgentRunEventRead, error) {
	if runID == "00000000-0000-0000-0000-000000099999" {
		return nil, store.ErrUnknownAgentRun
	}
	createdAt := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	events := []store.AgentRunEventRead{
		{ID: "00000000-0000-0000-0000-000000000701", AgentRunID: runID, Sequence: 1, EventKind: "message.delta", Payload: map[string]any{"delta": "hello"}, OccurredAt: createdAt, CreatedAt: createdAt},
		{ID: "00000000-0000-0000-0000-000000000702", AgentRunID: runID, Sequence: 2, EventKind: "run.completed", Payload: map[string]any{"sequence": 2}, OccurredAt: createdAt.Add(time.Second), CreatedAt: createdAt.Add(time.Second)},
	}
	out := make([]store.AgentRunEventRead, 0, len(events))
	for _, ev := range events {
		if ev.Sequence > afterSequence {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (stubRuntimeStore) ListActiveFeishuInflightConversations(ctx context.Context, cutoff time.Time, limit int32) ([]store.FeishuInflightConversation, error) {
	return []store.FeishuInflightConversation{{
		ConversationID:   testConversationID,
		WorkspaceID:      testWorkspaceID,
		ExternalChatID:   "oc_demo",
		SourceAppID:      "cli_stub",
		AgentRunID:       "00000000-0000-0000-0000-000000000007",
		RunStatus:        "running",
		MaxEventSequence: 3,
		ConversationMetadata: map[string]any{
			"gateway_inflight": map[string]any{
				"working": map[string]any{
					"external_msg_id": "om_stub",
					"seq_emitted":     float64(2),
				},
			},
		},
	}}, nil
}

func (stubRuntimeStore) MarkGatewayOutboundDelivered(ctx context.Context, input store.MarkGatewayOutboundDeliveredInput) (store.MarkGatewayOutboundDeliveredResult, error) {
	if input.MessageID == "00000000-0000-0000-0000-000000099999" {
		return store.MarkGatewayOutboundDeliveredResult{}, store.ErrUnknownMessage
	}
	// Real SQL only stamps gateway_delivered_at (no delivery_id /
	// delivery_status).
	return store.MarkGatewayOutboundDeliveredResult{MessageID: input.MessageID, Metadata: map[string]any{"gateway_delivered_at": "2026-06-12T18:00:00Z"}}, nil
}

func (stubRuntimeStore) ListWorkspaceAgentRuns(ctx context.Context, workspaceID string, statuses []string, limit, offset int32) (store.ListWorkspaceAgentRunsResult, error) {
	// Stub mirrors the real SQL ordering (created_at DESC, id DESC)
	// so route tests pin "newest first" pagination semantics.
	runs := []store.AgentRunBriefRead{
		{ID: "00000000-0000-0000-0000-000000000102", WorkspaceID: workspaceID, ConversationID: testConversationID, AgentID: "00000000-0000-0000-0000-000000000007", AgentName: "后端Agent", AgentSlug: "backend-agent", ConnectorType: "agent_daemon", Status: "completed", CreatedAt: time.Date(2026, 5, 22, 0, 1, 0, 0, time.UTC)},
		{ID: testRunID, WorkspaceID: workspaceID, ConversationID: testConversationID, AgentID: "00000000-0000-0000-0000-000000000007", AgentName: "后端Agent", AgentSlug: "backend-agent", ConnectorType: "agent_daemon", Status: "queued", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
	}
	filtered := runs
	if len(statuses) > 0 {
		want := make(map[string]struct{}, len(statuses))
		for _, s := range statuses {
			want[s] = struct{}{}
		}
		filtered = filtered[:0]
		for _, run := range runs {
			if _, ok := want[run.Status]; ok {
				filtered = append(filtered, run)
			}
		}
	}
	total := int64(len(filtered))
	if offset >= int32(len(filtered)) {
		return store.ListWorkspaceAgentRunsResult{Runs: []store.AgentRunBriefRead{}, Total: total}, nil
	}
	end := min(offset+limit, int32(len(filtered)))
	return store.ListWorkspaceAgentRunsResult{Runs: filtered[offset:end], Total: total}, nil
}

// GetAgentMetrics returns a fixed metrics snapshot so the
// agent-detail "近 N 天表现" route test can assert the JSON shape
// without spinning up a real DB.
func (stubRuntimeStore) GetAgentMetrics(ctx context.Context, agentID string, windowDays int32) (store.AgentMetricsRead, error) {
	if windowDays <= 0 {
		windowDays = 30
	}
	return store.AgentMetricsRead{
		WindowDays:     windowDays,
		CompletedCount: 12,
		FailedCount:    1,
		SuccessRate:    12.0 / 13.0,
		AvgDurationMs:  48000,
	}, nil
}

// ListAuditRecords returns a fixed set of admin / runtime rows the
// route test below can assert against; honors source / event_type /
// target_type / target_id / actor_id filters.
func (stubRuntimeStore) ListAuditRecords(ctx context.Context, filter store.ListAuditRecordsFilter, limit int32) ([]store.AuditRecordRead, error) {
	rows := []store.AuditRecordRead{
		{ID: 1002, OccurredAt: time.Date(2026, 5, 22, 0, 1, 0, 0, time.UTC), Source: "runtime", EventType: "agent_run.completed", ActorType: "agent", ActorID: "00000000-0000-0000-0000-000000000007", TargetType: "agent_run", TargetID: testRunID, WorkspaceID: "00000000-0000-0000-0000-000000000002", Payload: map[string]any{"source": "runtime"}},
		{ID: 1001, OccurredAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), Source: "admin", EventType: "im.message.created", ActorType: "user", ActorID: "00000000-0000-0000-0000-000000000001", TargetType: "message", TargetID: "00000000-0000-0000-0000-000000000201", WorkspaceID: "00000000-0000-0000-0000-000000000002", Payload: map[string]any{"source": "im"}},
	}
	out := make([]store.AuditRecordRead, 0, len(rows))
	for _, row := range rows {
		if filter.Source != "" && row.Source != filter.Source {
			continue
		}
		if filter.EventType != "" && row.EventType != filter.EventType {
			continue
		}
		if filter.TargetType != "" && row.TargetType != filter.TargetType {
			continue
		}
		if filter.TargetID != "" && row.TargetID != filter.TargetID {
			continue
		}
		if filter.ActorID != "" && row.ActorID != filter.ActorID {
			continue
		}
		out = append(out, row)
	}
	if limit > 0 && int(limit) < len(out) {
		return out[:limit], nil
	}
	return out, nil
}

func (stubRuntimeStore) ListWorkspaceUsageLogs(ctx context.Context, workspaceID string, agentRunID string, limit int32) ([]store.UsageLogRead, error) {
	logs := []store.UsageLogRead{
		{ID: "00000000-0000-0000-0000-000000000501", WorkspaceID: workspaceID, AgentRunID: testRunID, Provider: "fake", Model: "parsar-test-model", InputTokens: 12, OutputTokens: 8, CostUSD: 0.000321, Raw: map[string]any{"source": "runtime"}, CreatedAt: time.Date(2026, 5, 22, 0, 1, 0, 0, time.UTC)},
		{ID: "00000000-0000-0000-0000-000000000502", WorkspaceID: workspaceID, AgentRunID: "00000000-0000-0000-0000-000000000102", Provider: "fake", Model: "parsar-test-model", InputTokens: 42, OutputTokens: 18, CostUSD: 0.000123, Raw: map[string]any{"source": "runtime"}, CreatedAt: time.Date(2026, 5, 22, 0, 2, 0, 0, time.UTC)},
	}
	if agentRunID == "" {
		return logs, nil
	}
	filtered := make([]store.UsageLogRead, 0, len(logs))
	for _, log := range logs {
		if log.AgentRunID == agentRunID {
			filtered = append(filtered, log)
		}
	}
	if limit > 0 && int(limit) < len(filtered) {
		return filtered[:limit], nil
	}
	return filtered, nil
}

func (stubRuntimeStore) ListWorkspaceMembers(ctx context.Context, workspaceID string, limit int32) ([]store.WorkspaceMemberRead, error) {
	return []store.WorkspaceMemberRead{
		{ID: "00000000-0000-0000-0000-000000000003", WorkspaceID: workspaceID, UserID: "00000000-0000-0000-0000-000000000001", Role: "owner", UserEmail: "owner@example.com", UserName: "Owner", UserStatus: "active", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
	}, nil
}

func (stubRuntimeStore) GetUserByID(ctx context.Context, userID string) (store.UserRead, error) {
	if userID == "00000000-0000-0000-0000-000000000001" {
		return store.UserRead{ID: userID, Email: "admin@example.com", Name: "Dev Admin", Status: "active"}, nil
	}
	return store.UserRead{ID: userID, Email: "bob@example.com", Name: "Bob", Status: "active", AvatarURL: "https://cdn.example.test/bob.png"}, nil
}

func (stubRuntimeStore) ListUserWorkspaces(ctx context.Context, userID string, limit int32) ([]store.UserWorkspaceRead, error) {
	return []store.UserWorkspaceRead{
		{ID: "00000000-0000-0000-0000-000000000002", Name: "Demo Workspace", Slug: "demo", Role: "owner", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
		{ID: "00000000-0000-0000-0000-000000000020", Name: "Second Workspace", Slug: "second", Role: "admin", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
	}, nil
}

func (stubRuntimeStore) ListAllActiveWorkspaces(ctx context.Context, limit int32) ([]store.UserWorkspaceRead, error) {
	return []store.UserWorkspaceRead{
		{ID: "00000000-0000-0000-0000-000000000002", Name: "Demo Workspace", Slug: "demo", Role: "owner", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
		{ID: "00000000-0000-0000-0000-000000000020", Name: "Second Workspace", Slug: "second", Role: "owner", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
		{ID: "00000000-0000-0000-0000-000000000077", Name: "Other Workspace", Slug: "other", Role: "owner", CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)},
	}, nil
}

func (stubRuntimeStore) CreateWorkspace(ctx context.Context, input store.CreateWorkspaceInput) (store.CreateWorkspaceResult, error) {
	if strings.TrimSpace(input.Name) == "" {
		return store.CreateWorkspaceResult{}, store.ErrInvalidWorkspaceInput
	}
	// Slug is system-generated; tests can opt into the duplicate path
	// by naming the workspace "Dup".
	if strings.TrimSpace(input.Name) == "Dup" {
		return store.CreateWorkspaceResult{}, store.ErrDuplicateWorkspaceSlug
	}
	return store.CreateWorkspaceResult{
		Workspace: store.UserWorkspaceRead{
			ID:        "00000000-0000-0000-0000-0000000000cc",
			Name:      input.Name,
			Slug:      "workspace-deadbeef",
			Role:      "owner",
			CreatedAt: input.Now,
			UpdatedAt: input.Now,
		},
		Member: store.WorkspaceMemberRead{
			ID:          "00000000-0000-0000-0000-0000000000cd",
			WorkspaceID: "00000000-0000-0000-0000-0000000000cc",
			UserID:      input.CreatedBy,
			Role:        "owner",
			UserEmail:   "owner@example.com",
			UserName:    "Owner",
			UserStatus:  "active",
			CreatedAt:   input.Now,
			UpdatedAt:   input.Now,
		},
	}, nil
}

func (stubRuntimeStore) UpdateWorkspace(ctx context.Context, input store.UpdateWorkspaceInput) (store.UserWorkspaceRead, error) {
	if input.WorkspaceID == "00000000-0000-0000-0000-000000099999" {
		return store.UserWorkspaceRead{}, store.ErrUnknownWorkspace
	}
	if input.Name == nil {
		return store.UserWorkspaceRead{}, store.ErrInvalidWorkspaceInput
	}
	return store.UserWorkspaceRead{
		ID:        input.WorkspaceID,
		Name:      *input.Name,
		Slug:      "workspace-deadbeef",
		CreatedAt: input.Now,
		UpdatedAt: input.Now,
	}, nil
}

func (stubRuntimeStore) ArchiveWorkspace(ctx context.Context, input store.ArchiveWorkspaceInput) (store.UserWorkspaceRead, error) {
	if input.WorkspaceID == "00000000-0000-0000-0000-000000099999" {
		return store.UserWorkspaceRead{}, store.ErrUnknownWorkspace
	}
	return store.UserWorkspaceRead{
		ID:        input.WorkspaceID,
		Name:      "Demo Workspace",
		Slug:      "workspace-deadbeef",
		CreatedAt: input.Now,
		UpdatedAt: input.Now,
	}, nil
}

func (stubRuntimeStore) AddWorkspaceMember(ctx context.Context, input store.AddWorkspaceMemberInput) (store.AddWorkspaceMemberResult, error) {
	return store.AddWorkspaceMemberResult{
		Member: store.WorkspaceMemberRead{
			ID:          "00000000-0000-0000-0000-000000000aaa",
			WorkspaceID: input.WorkspaceID,
			UserID:      "00000000-0000-0000-0000-000000000bbb",
			Role:        input.Role,
			UserEmail:   input.Email,
			UserName:    input.Name,
			UserStatus:  "active",
			CreatedAt:   input.Now,
			UpdatedAt:   input.Now,
		},
		UserCreated: true,
	}, nil
}

func (stubRuntimeStore) UpdateWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string, role string, now time.Time) (store.WorkspaceMemberRead, error) {
	return store.WorkspaceMemberRead{
		ID:          "00000000-0000-0000-0000-000000000003",
		WorkspaceID: workspaceID,
		UserID:      userID,
		Role:        role,
		UserEmail:   "owner@example.com",
		UserName:    "Owner",
		UserStatus:  "active",
		CreatedAt:   time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   now,
	}, nil
}

func (stubRuntimeStore) RemoveWorkspaceMember(ctx context.Context, workspaceID string, userID string, now time.Time) (store.RemoveWorkspaceMemberResult, error) {
	return store.RemoveWorkspaceMemberResult{
		Member: store.WorkspaceMemberRead{
			ID:          "00000000-0000-0000-0000-000000000003",
			WorkspaceID: workspaceID,
			UserID:      userID,
			Role:        "member",
			UserEmail:   "owner@example.com",
			UserName:    "Owner",
			UserStatus:  "active",
			CreatedAt:   time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
			UpdatedAt:   now,
		},
	}, nil
}

func (stubRuntimeStore) SearchUsers(ctx context.Context, input store.SearchUsersInput) ([]store.SearchUsersResultItem, error) {
	return nil, nil
}

func TestConfigureAgentProfileAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	// active model -> 200
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/profile",
		strings.NewReader(`{"model_id":"00000000-0000-0000-0000-000000000702"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for active model, got %d: %s", res.Code, res.Body.String())
	}

	// disabled model -> 400
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/profile",
		strings.NewReader(`{"model_id":"00000000-0000-0000-0000-000000000901"}`))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for disabled model, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "disabled") {
		t.Fatalf("expected error to mention disabled, got %s", res.Body.String())
	}

	// unknown model -> 404
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/profile",
		strings.NewReader(`{"model_id":"00000000-0000-0000-0000-000000000902"}`))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown model, got %d: %s", res.Code, res.Body.String())
	}
}

func TestConfigureAgentProfileRequiresWorkspaceOwnerOrAdmin(t *testing.T) {
	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		store.DefaultDevFixtureIDs().UserID:    "admin",
		"00000000-0000-0000-0000-0000000000aa": "viewer",
	}), true)
	body := `{"model_id":"00000000-0000-0000-0000-000000000702"}`

	req := newRequestWithDevUser(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/profile", body, store.DefaultDevFixtureIDs().UserID)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected admin 200, got %d: %s", res.Code, res.Body.String())
	}

	req = newRequestWithDevUser(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/profile", body, "00000000-0000-0000-0000-0000000000aa")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected viewer 403, got %d: %s", res.Code, res.Body.String())
	}
}

func TestListWorkspaceConversationsAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000004/conversations", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"conversations"`) || !strings.Contains(body, `"Demo Group"`) || !strings.Contains(body, `"message_count":2`) {
		t.Fatalf("expected conversation list payload, got %s", body)
	}
}

func TestListWorkspaceConversationsAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/not-a-uuid/conversations", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad workspace uuid, got %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000404/conversations", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown workspace, got %d: %s", res.Code, res.Body.String())
	}
}

func TestListMyWorkspacesAPIDefaultUser(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/workspaces", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), store.DefaultDevFixtureIDs().UserID))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	// Default user is the dev seed UserID — header fallback path.
	if !strings.Contains(body, `"user_id":"00000000-0000-0000-0000-000000000001"`) {
		t.Fatalf("expected default seed user_id in response, got %s", body)
	}
	if !strings.Contains(body, `"workspaces"`) || !strings.Contains(body, `"Demo Workspace"`) || !strings.Contains(body, `"Second Workspace"`) {
		t.Fatalf("expected both stubbed workspaces in response, got %s", body)
	}
}

func TestListMyWorkspacesAPICustomUser(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/workspaces", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), "00000000-0000-0000-0000-0000000000aa"))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"user_id":"00000000-0000-0000-0000-0000000000aa"`) {
		t.Fatalf("expected custom user_id echoed back, got %s", res.Body.String())
	}
}

func TestListMyWorkspacesAPIBadUser(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/workspaces", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), "not-a-uuid"))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad user uuid, got %d: %s", res.Code, res.Body.String())
	}
}

func TestListMyWorkspacesAPIPlatformAdminListsAll(t *testing.T) {
	const adminID = "00000000-0000-0000-0000-0000000000ad"
	auth.SetPlatformAdminIDs([]string{adminID})
	t.Cleanup(func() { auth.SetPlatformAdminIDs(nil) })

	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/workspaces", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), adminID))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"Other Workspace"`) {
		t.Fatalf("platform admin should see workspaces they're not a member of; got %s", body)
	}
}

func TestCreateWorkspaceAPIHappyPath(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := strings.NewReader(`{"name":"New Workspace"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", body)
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"workspace"`) || !strings.Contains(res.Body.String(), `"member"`) {
		t.Fatalf("expected workspace + member envelope, got %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"slug":"workspace-`) {
		t.Fatalf("expected system-generated slug starting with workspace-, got %s", res.Body.String())
	}
}

func TestCreateWorkspaceAPIDuplicateSlug(t *testing.T) {
	// Stub returns ErrDuplicateWorkspaceSlug when name == "Dup" — exercises
	// the auto-slug retry-exhaustion path.
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := strings.NewReader(`{"name":"Dup"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", body)
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate slug, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCreateWorkspaceAPIInvalidBody(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := strings.NewReader(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", body)
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d: %s", res.Code, res.Body.String())
	}
}

func TestUpdateWorkspaceAPIHappyPath(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := strings.NewReader(`{"name":"Renamed"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002", body)
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("expected new name echoed, got %s", res.Body.String())
	}
}

func TestUpdateWorkspaceAPIUnknown(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	body := strings.NewReader(`{"name":"X"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000099999", body)
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown workspace, got %d: %s", res.Code, res.Body.String())
	}
}

func TestArchiveWorkspaceAPIHappyPath(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/archive", nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
}

func TestArchiveWorkspaceAPIUnknown(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := withTestUser(httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000099999/archive", nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown workspace, got %d: %s", res.Code, res.Body.String())
	}
}

func TestWorkspaceSettingsAPIHappyPathAndForbidden(t *testing.T) {
	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}), true)
	req := newRequestWithDevUser(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/settings", "", store.DefaultDevFixtureIDs().UserID)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"workspace_id"`) {
		t.Fatalf("expected 200 settings, got %d: %s", res.Code, res.Body.String())
	}

	req = newRequestWithDevUser(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/settings", `{}`, store.DefaultDevFixtureIDs().UserID)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"workspace_id"`) {
		t.Fatalf("expected 200 settings patch, got %d: %s", res.Code, res.Body.String())
	}

	r = registerRoutesWithRBACStore(newRoleStubStore(map[string]string{store.DefaultDevFixtureIDs().UserID: "member"}), true)
	req = newRequestWithDevUser(http.MethodPatch, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/settings", `{}`, store.DefaultDevFixtureIDs().UserID)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for member settings patch, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCreateAgentAPIHappyPathAndNameConflict(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents", strings.NewReader(`{"name":"New Agent","connector_type":"agent_daemon","capabilities":["shell"],"initial_capabilities":[{"capability_version_id":"00000000-0000-0000-0000-000000000c02","configuration":{"mode":"create"}}],"config":{"daemon_mode":"sandbox","agent_kind":"opencode"}}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"agent"`) || !strings.Contains(res.Body.String(), `"initial_capabilities"`) || !strings.Contains(res.Body.String(), `"capability_version_id":"00000000-0000-0000-0000-000000000c02"`) || strings.Contains(res.Body.String(), `"runtime"`) {
		t.Fatalf("expected 201 create agent without runtime and with initial capabilities, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents", strings.NewReader(`{"name":"Bad Runtime","connector_type":"agent_daemon","runtime":"bad"}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 invalid create runtime, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents", strings.NewReader(`{"name":"Daemon Agent","connector_type":"agent_daemon","config":{"device_id":"00000000-0000-0000-0000-00000000d001","agent_kind":"claude_code"}}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"device_id":"00000000-0000-0000-0000-00000000d001"`) || strings.Contains(res.Body.String(), `"runtime"`) {
		t.Fatalf("expected 201 agent_daemon without runtime and with config, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents", strings.NewReader(`{"name":"Daemon Bad Runtime","connector_type":"agent_daemon","runtime":"sandbox"}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 agent_daemon runtime, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents", strings.NewReader(`{"name":"Dup","connector_type":"agent_daemon","config":{"daemon_mode":"sandbox","agent_kind":"opencode"}}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	// Agent names are no longer unique within a workspace — "Dup" is allowed.
	// The admin list disambiguates collisions via the creator column.
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201 for duplicate agent name (no longer rejected), got %d: %s", res.Code, res.Body.String())
	}
}

func TestUpdateAgentAPIHappyPathAndImmutableSlug(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(`{"name":"Renamed"}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"name":"Renamed"`) {
		t.Fatalf("expected 200 update agent, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(`{"slug":"new-slug"}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "slug is immutable") {
		t.Fatalf("expected 422 immutable slug, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901", strings.NewReader(`{"runtime":"sandbox"}`))
	req = withTestUser(req)
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	// v5: runtime is immutable post-create. The handler rejects any
	// body that includes a "runtime" field with the new immutability
	// message.
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "runtime is immutable post-create") {
		t.Fatalf("expected 422 immutable runtime rejection, got %d: %s", res.Code, res.Body.String())
	}
}

// TestUpdateAgentVisibilityAPI exercises the B4 visibility PATCH:
// happy path with each enum value, idempotent noop, invalid enum 422,
// bogus UUID 400, unknown agent 404.
func TestUpdateAgentVisibilityAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	// Happy path — set to tenant.
	req := withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901/visibility", strings.NewReader(`{"visibility":"tenant"}`)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("happy path expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"new_visibility":"tenant"`) {
		t.Errorf("response missing new_visibility=tenant: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"old_visibility":"workspace"`) {
		t.Errorf("response missing old_visibility=workspace: %s", res.Body.String())
	}

	// Happy path — public.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901/visibility", strings.NewReader(`{"visibility":"public"}`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"new_visibility":"public"`) {
		t.Fatalf("public path expected 200, got %d: %s", res.Code, res.Body.String())
	}

	// Noop — same value, still 200, response carries noop=true.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901/visibility", strings.NewReader(`{"visibility":"workspace"}`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("noop expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"noop":true`) {
		t.Errorf("noop case missing noop flag: %s", res.Body.String())
	}

	// Invalid enum — 422.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901/visibility", strings.NewReader(`{"visibility":"everyone"}`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid enum expected 422, got %d: %s", res.Code, res.Body.String())
	}

	// Bogus UUID — 400.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/not-a-uuid/visibility", strings.NewReader(`{"visibility":"tenant"}`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Errorf("bogus uuid expected 400, got %d", res.Code)
	}

	// Unknown agent (stub returns ErrUnknownAgent for the 99999 id) — 404.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000099999/visibility", strings.NewReader(`{"visibility":"tenant"}`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Errorf("unknown agent expected 404, got %d: %s", res.Code, res.Body.String())
	}

	// Malformed JSON — 400.
	req = withTestUser(httptest.NewRequest(http.MethodPatch, "/api/v1/agents/00000000-0000-0000-0000-000000000901/visibility", strings.NewReader(`{`)))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Errorf("malformed json expected 400, got %d", res.Code)
	}
}

func TestDeleteAgentAPIHappyPathAndInFlightRuns(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})
	req := withTestUser(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/00000000-0000-0000-0000-000000000901", nil))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"agent"`) {
		t.Fatalf("expected 200 delete agent, got %d: %s", res.Code, res.Body.String())
	}

	req = withTestUser(httptest.NewRequest(http.MethodDelete, "/api/v1/agents/00000000-0000-0000-0000-000000000999", nil))
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "in_flight_runs") {
		t.Fatalf("expected 409 in-flight runs, got %d: %s", res.Code, res.Body.String())
	}
}

// TestAgentCRUDForbiddenForNonAdmin covers the AGENTS.md mandatory pre-merge
// permission gate: every Agent CRUD mutation endpoint must reject members
// (non-admin) with 403. We hit each endpoint with a member-role user and
// expect a 403 + "forbidden" error envelope so the RBAC wiring on
// requireWorkspaceOwnerOrAdmin can't silently
// regress to a no-op.
func TestAgentCRUDForbiddenForNonAdmin(t *testing.T) {
	memberID := store.DefaultDevFixtureIDs().UserID
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "create agent",
			method: http.MethodPost,
			path:   "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents",
			body:   `{"name":"NoPerm","connector_type":"agent_daemon"}`,
		},
		{
			name:   "update agent",
			method: http.MethodPatch,
			path:   "/api/v1/agents/00000000-0000-0000-0000-000000000901",
			body:   `{"name":"NoPerm"}`,
		},
		{
			name:   "delete agent",
			method: http.MethodDelete,
			path:   "/api/v1/agents/00000000-0000-0000-0000-000000000901",
			body:   "",
		},
		{
			name:   "delete merged agent",
			method: http.MethodDelete,
			path:   "/api/v1/agents/00000000-0000-0000-0000-000000000007",
			body:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{memberID: "member"}), true)
			req := newRequestWithDevUser(tc.method, tc.path, tc.body, memberID)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Fatalf("%s: expected 403 for member, got %d: %s", tc.name, res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), "forbidden") {
				t.Fatalf("%s: expected forbidden error envelope, got: %s", tc.name, res.Body.String())
			}
		})
	}
}

func TestCreateConversationAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/conversations",
		strings.NewReader(`{"title":"排查 API","surface":"web","form":"thread","metadata":{"source":"dev"}}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, `"title":"排查 API"`) || !strings.Contains(body, `"surface":"web"`) || !strings.Contains(body, `"form":"thread"`) {
		t.Fatalf("expected created conversation echoed, got %s", body)
	}
}

func TestCreateConversationAPIDefaults(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/conversations", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201 with default body, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"title":"未命名会话"`) {
		t.Fatalf("expected default title, got %s", res.Body.String())
	}
}

func TestCreateConversationAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/not-a-uuid/conversations", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad workspace uuid, got %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/conversations",
		strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000400/conversations",
		strings.NewReader(`{"type":"bogus"}`))
	req.Header.Set("Content-Type", "application/json")
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid surface, got %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000404/conversations", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown workspace, got %d: %s", res.Code, res.Body.String())
	}
}

func TestGetConversationAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/conversations/00000000-0000-0000-0000-000000000c10", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"Demo Group"`) {
		t.Fatalf("expected Demo Group payload, got %s", res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/conversations/00000000-0000-0000-0000-000000000404", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/conversations/not-a-uuid", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad conversation uuid, got %d", res.Code)
	}
}

func TestDisableAgentAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/disable", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"disabled"`) {
		t.Fatalf("expected disabled status, got %s", res.Body.String())
	}
}

func TestEnableAgentAPI(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000000007/enable", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"status":"active"`) {
		t.Fatalf("expected active status, got %s", res.Body.String())
	}
}

func TestAgentLifecycleAPIErrors(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/not-a-uuid/disable", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad agent_id, got %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000099999/disable", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent, got %d: %s", res.Code, res.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/00000000-0000-0000-0000-000000099999/enable", nil)
	res = httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown agent enable, got %d: %s", res.Code, res.Body.String())
	}
}

// TestReadRBACGatesNonMemberAcrossEndpoints exercises a representative
// sample of newly-gated GET endpoints — direct workspace-scoped, and
// indirect ones that load the resource first to discover the parent
// workspace — and asserts:
//
//  1. an active member gets through (200), and
//  2. a non-member gets 404 (ErrNotMember → "not found" to avoid
//     leaking existence).
func TestReadRBACGatesNonMemberAcrossEndpoints(t *testing.T) {
	const memberUser = "00000000-0000-0000-0000-0000000000aa"
	const outsiderUser = "00000000-0000-0000-0000-0000000000bb"

	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		memberUser: "member",
		// outsiderUser intentionally absent → ErrNotMember → 404
	}), true)

	cases := []struct {
		name string
		path string
	}{
		// Direct workspace-scoped reads
		{"list_secrets", "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/secrets"},
		{"list_workspace_conversations", "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/conversations"},
		// Indirect: keyed by conversation_id / run_id, must
		// reverse-lookup the parent workspace before gating.
		{"get_conversation", "/api/v1/conversations/00000000-0000-0000-0000-000000000123"},
		{"get_conversation_timeline", "/api/v1/conversations/00000000-0000-0000-0000-000000000123/timeline"},
		{"get_agent_run", "/api/v1/agent-runs/00000000-0000-0000-0000-000000000777"},
	}

	for _, tc := range cases {
		t.Run(tc.name+"_member_passes_gate", func(t *testing.T) {
			req := newRequestWithDevUser(http.MethodGet, tc.path, "", memberUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			// Stub backends return real (or empty) payloads, so we
			// just assert "got past the RBAC layer" — anything in
			// {200, 503} (when a sub-store is missing) is fine; the
			// thing that MUST NOT happen is 404 not-a-member.
			if res.Code == http.StatusNotFound && strings.Contains(res.Body.String(), "not a member") {
				t.Fatalf("member should have passed RBAC gate, got 404 not-a-member: %s", res.Body.String())
			}
		})
		t.Run(tc.name+"_non_member_blocked", func(t *testing.T) {
			req := newRequestWithDevUser(http.MethodGet, tc.path, "", outsiderUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("non-member must get 404, got %d: %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), "not a member") {
				t.Fatalf("non-member 404 must come from RBAC layer (\"not a member\"), got: %s", res.Body.String())
			}
		})
	}
}

// TestSandboxAdminRBAC closes the gap the earlier read-RBAC PR
// flagged as out-of-scope: the 4 sandbox admin handlers
// (listSandboxes / getSandboxStatus / killSandbox / rebuildSandbox)
// live in sandbox_admin.go with their own deps struct and never
// saw a role check before this commit. We don't wrap them by
// extending sandboxAdminDeps; instead the RegisterRoutesWithStore
// router wraps each sandbox route in gateWorkspaceMember /
// gateWorkspaceOwnerOrAdmin at registration time. This test runs
// the full router (not the minimal newSandboxTestRouter used in
// sandbox_admin_test.go) so the wrappers are actually exercised.
//
// Expectation for non-member (outsider) on each of the 4 routes:
// 404 "not a member" from the RBAC layer — NOT 503 "sandbox
// lifecycle store not wired" which is what the handler itself
// would return if the gate were bypassed.
func TestSandboxAdminRBAC(t *testing.T) {
	const memberUser = "00000000-0000-0000-0000-0000000000aa"
	const adminUser = "00000000-0000-0000-0000-0000000000ad"
	const outsiderUser = "00000000-0000-0000-0000-0000000000bb"

	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		memberUser: "member",
		adminUser:  "admin",
		// outsiderUser absent → ErrNotMember → 404
	}), true)

	type tc struct {
		name   string
		method string
		path   string
	}
	readRoutes := []tc{
		{"list_sandboxes", http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes"},
		{"get_sandbox_status", http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000099/sandbox"},
	}
	writeRoutes := []tc{
		{"kill_sandbox", http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000099/sandbox/kill"},
		{"rebuild_sandbox", http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000099/sandbox/rebuild"},
	}

	// Outsider hits read + write — all must return 404 "not a
	// member" from the RBAC layer, NOT pass through to the
	// sandbox handler's own 503.
	for _, c := range append(readRoutes, writeRoutes...) {
		t.Run(c.name+"_outsider_blocked", func(t *testing.T) {
			req := newRequestWithDevUser(c.method, c.path, "", outsiderUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound {
				t.Fatalf("outsider must get 404, got %d: %s", res.Code, res.Body.String())
			}
			if !strings.Contains(res.Body.String(), "not a member") {
				t.Fatalf("outsider 404 must come from RBAC layer (\"not a member\"), got: %s", res.Body.String())
			}
		})
	}

	// Member of the workspace passes the read gate. The sandbox
	// handler then 503s because no sandboxAdminDeps were wired
	// in this test — that 503 means "RBAC layer let me through".
	for _, c := range readRoutes {
		t.Run(c.name+"_member_passes_gate", func(t *testing.T) {
			req := newRequestWithDevUser(c.method, c.path, "", memberUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code == http.StatusNotFound && strings.Contains(res.Body.String(), "not a member") {
				t.Fatalf("member should have passed RBAC gate, got 404 not-a-member: %s", res.Body.String())
			}
			// Acceptable: 503 (not wired in test), 200 (if wired).
			// Forbidden: 404 not-a-member.
		})
	}

	// kill / rebuild — bare member (role=member) MUST be rejected
	// with 403; only owner/admin should pass through. Confirms
	// gateWorkspaceOwnerOrAdmin holds the same destructive-write
	// bar D.5 uses for other workspace mutations.
	for _, c := range writeRoutes {
		t.Run(c.name+"_bare_member_forbidden", func(t *testing.T) {
			req := newRequestWithDevUser(c.method, c.path, "", memberUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Fatalf("bare member calling destructive sandbox endpoint must get 403, got %d: %s", res.Code, res.Body.String())
			}
		})
		t.Run(c.name+"_admin_passes_gate", func(t *testing.T) {
			req := newRequestWithDevUser(c.method, c.path, "", adminUser)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code == http.StatusForbidden {
				t.Fatalf("admin should pass owner/admin gate, got 403: %s", res.Body.String())
			}
			if res.Code == http.StatusNotFound && strings.Contains(res.Body.String(), "not a member") {
				t.Fatalf("admin should pass owner/admin gate, got 404 not-a-member: %s", res.Body.String())
			}
			// Acceptable: 503 (not wired), 200/204 (if wired).
		})
	}
}

// Self-service join request noop stubs (handler behaviour verified at
// the integration layer).

func (stubRuntimeStore) ListDiscoverableWorkspaces(ctx context.Context, input store.ListDiscoverableWorkspacesInput) (store.ListDiscoverableWorkspacesResult, error) {
	return store.ListDiscoverableWorkspacesResult{}, nil
}

func (stubRuntimeStore) ListPendingJoinRequests(ctx context.Context, workspaceID string) ([]store.PendingJoinRequestRead, error) {
	return nil, nil
}

func (stubRuntimeStore) CountPendingJoinRequests(ctx context.Context, workspaceID string) (int64, error) {
	return 0, nil
}

func (stubRuntimeStore) RequestJoinWorkspace(ctx context.Context, input store.RequestJoinWorkspaceInput) (store.RequestJoinWorkspaceResult, error) {
	return store.RequestJoinWorkspaceResult{}, nil
}

func (stubRuntimeStore) ApproveJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error) {
	return store.WorkspaceMemberRead{}, nil
}

func (stubRuntimeStore) RejectJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error) {
	return store.WorkspaceMemberRead{}, nil
}

func (stubRuntimeStore) WithdrawOwnJoinRequest(ctx context.Context, workspaceID, userID string, now time.Time) error {
	return nil
}

func (stubRuntimeStore) GetWorkspaceIMConnectors(ctx context.Context, workspaceID string) ([]store.WorkspaceConnectorRead, error) {
	return nil, nil
}

func (stubRuntimeStore) UpsertWorkspaceSlackConnector(ctx context.Context, input store.UpsertWorkspaceSlackConnectorInput, actorID string) (store.WorkspaceConnectorChange, error) {
	return store.WorkspaceConnectorChange{}, nil
}

func (stubRuntimeStore) UpsertWorkspaceDiscordConnector(ctx context.Context, input store.UpsertWorkspaceDiscordConnectorInput, actorID string) (store.WorkspaceConnectorChange, error) {
	return store.WorkspaceConnectorChange{}, nil
}

func (stubRuntimeStore) UpsertWorkspaceFeishuConnector(ctx context.Context, input store.UpsertWorkspaceFeishuConnectorInput, actorID string) (store.WorkspaceConnectorChange, error) {
	return store.WorkspaceConnectorChange{}, nil
}
