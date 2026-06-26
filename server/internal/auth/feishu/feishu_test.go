package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func envFromMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestNewClientFromEnvMissingRequiredReturnsErr(t *testing.T) {
	_, err := NewClientFromEnv(envFromMap(map[string]string{}))
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

func TestIsConfiguredFalseWhenMissing(t *testing.T) {
	if IsConfigured(envFromMap(map[string]string{})) {
		t.Fatal("IsConfigured(empty env) must be false")
	}
}

func TestIsConfiguredTrueWhenAllSet(t *testing.T) {
	env := envFromMap(map[string]string{
		EnvAppID: "cli_x", EnvAppSecret: "s", EnvRedirectURI: "https://parsar/cb",
	})
	if !IsConfigured(env) {
		t.Fatal("IsConfigured with the 3 required vars must be true")
	}
}

func TestMockClientShortCircuit(t *testing.T) {
	env := envFromMap(map[string]string{
		EnvMock:        "true",
		EnvRedirectURI: "https://parsar/cb",
	})
	c, err := NewClientFromEnv(env)
	if err != nil {
		t.Fatalf("NewClientFromEnv(mock): %v", err)
	}
	if !c.IsMock() {
		t.Fatal("expected mock client")
	}
	u := c.AuthorizeURL("csrf-1")
	if !strings.Contains(u, "https://parsar/cb") || !strings.Contains(u, "code=mock-code") || !strings.Contains(u, "state=csrf-1") {
		t.Fatalf("AuthorizeURL = %q, want it to embed redirect + mock-code + state", u)
	}
	prof, err := c.ExchangeCode(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if prof.Email != "admin@example.com" || prof.UnionID == "" {
		t.Fatalf("mock profile = %+v, want default fixture", prof)
	}
}

func TestMockClientHonoursEnvOverrides(t *testing.T) {
	env := envFromMap(map[string]string{
		EnvMock:        "1",
		EnvRedirectURI: "/api/v1/auth/feishu/callback",
		EnvMockEmail:   "alice@example.com",
		EnvMockName:    "Alice",
		EnvMockUnionID: "on_alice",
	})
	c, err := NewClientFromEnv(env)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := c.ExchangeCode(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Email != "alice@example.com" || prof.Name != "Alice" || prof.UnionID != "on_alice" {
		t.Fatalf("env overrides not applied: %+v", prof)
	}
}

// feishuStubServer fakes the three upstream endpoints with the exact
// contract Feishu enforces:
//
//   - app_access_token/internal: {app_id, app_secret} → flat envelope.
//   - oidc/access_token: REQUIRES Bearer header and rejects any of
//     {client_id, client_secret, redirect_uri} in the body — verifying
//     this here prevents regressing to a generic-OAuth body shape
//     that fails in prod with code=20014.
//   - user_info: requires Bearer user_access_token.
type feishuStubServer struct {
	appTokenCalls atomic.Int32
	tokenCalls    atomic.Int32
	appToken      string
	userToken     string
	userInfo      map[string]any
	tokenCode     int // non-zero overrides success and returns this code
	tokenMsg      string
	t             *testing.T
}

func newFeishuStubServer(t *testing.T) *feishuStubServer {
	t.Helper()
	return &feishuStubServer{
		t:         t,
		appToken:  "t-app-fixture",
		userToken: "uat-fixture",
		userInfo: map[string]any{
			"name": "Bob Lee", "open_id": "ou_x", "union_id": "on_x",
			"email": "bob@example.com", "avatar_url": "https://cdn/bob.png",
		},
	}
}

func (s *feishuStubServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(appTokenPath, func(w http.ResponseWriter, r *http.Request) {
		s.appTokenCalls.Add(1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["app_id"] == "" || body["app_secret"] == "" {
			s.t.Errorf("app_access_token body missing app_id/app_secret: %v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "ok",
			"app_access_token":    s.appToken,
			"tenant_access_token": s.appToken,
			"expire":              7200,
		})
	})
	mux.HandleFunc(tokenPath, func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls.Add(1)
		if got, want := r.Header.Get("Authorization"), "Bearer "+s.appToken; got != want {
			s.t.Errorf("oidc/access_token Authorization = %q, want %q", got, want)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["grant_type"] != "authorization_code" {
			s.t.Errorf("oidc/access_token grant_type = %q, want authorization_code", body["grant_type"])
		}
		if body["code"] == "" {
			s.t.Errorf("oidc/access_token missing code")
		}
		// Feishu's OIDC token endpoint does NOT accept these
		// generic OAuth 2.0 fields in the body; sending them
		// silently gets back code=20014.
		for _, forbidden := range []string{"client_id", "client_secret", "redirect_uri"} {
			if v, ok := body[forbidden]; ok && v != "" {
				s.t.Errorf("oidc/access_token body should not include %q (legacy contract leak), got %q", forbidden, v)
			}
		}
		if s.tokenCode != 0 {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": s.tokenCode, "msg": s.tokenMsg})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "success",
			"data": map[string]any{"access_token": s.userToken, "token_type": "Bearer", "expires_in": 7200},
		})
	})
	mux.HandleFunc(userInfoPath, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer "+s.userToken; got != want {
			s.t.Errorf("user_info Authorization = %q, want %q", got, want)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "success",
			"data": s.userInfo,
		})
	})
	return mux
}

func TestHTTPClientExchangeRoundTrip(t *testing.T) {
	stub := newFeishuStubServer(t)
	upstream := httptest.NewServer(stub.handler())
	defer upstream.Close()

	env := envFromMap(map[string]string{
		EnvAppID:       "cli_test",
		EnvAppSecret:   "sec_test",
		EnvRedirectURI: "https://parsar/cb",
		EnvAPIBase:     upstream.URL,
	})
	c, err := NewClientFromEnv(env)
	if err != nil {
		t.Fatal(err)
	}
	prof, err := c.ExchangeCode(context.Background(), "real-code")
	if err != nil {
		t.Fatal(err)
	}
	if prof.Email != "bob@example.com" || prof.UnionID != "on_x" || prof.Name != "Bob Lee" {
		t.Fatalf("profile = %+v", prof)
	}
	if got := stub.appTokenCalls.Load(); got != 1 {
		t.Errorf("expected 1 app_access_token call, got %d", got)
	}
	if got := stub.tokenCalls.Load(); got != 1 {
		t.Errorf("expected 1 oidc/access_token call, got %d", got)
	}
}

// TestHTTPClientReusesAppAccessTokenAcrossExchanges: concurrent
// callbacks must share one app_access_token fetch.
func TestHTTPClientReusesAppAccessTokenAcrossExchanges(t *testing.T) {
	stub := newFeishuStubServer(t)
	upstream := httptest.NewServer(stub.handler())
	defer upstream.Close()

	c, err := NewClientFromEnv(envFromMap(map[string]string{
		EnvAppID: "cli", EnvAppSecret: "s", EnvRedirectURI: "/cb", EnvAPIBase: upstream.URL,
	}))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	const concurrent = 8
	errs := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ExchangeCode(context.Background(), "code-x"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ExchangeCode: %v", err)
	}
	if got := stub.appTokenCalls.Load(); got != 1 {
		t.Errorf("expected 1 app_access_token call shared across %d exchanges, got %d", concurrent, got)
	}
	if got := stub.tokenCalls.Load(); got != int32(concurrent) {
		t.Errorf("expected %d oidc/access_token calls, got %d", concurrent, got)
	}
}

func TestHTTPClientPropagatesAppAccessTokenFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(appTokenPath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 10003, "msg": "invalid app secret",
		})
	})
	mux.HandleFunc(tokenPath, func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("oidc/access_token must not be called when app_access_token failed")
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()
	c, _ := NewClientFromEnv(envFromMap(map[string]string{
		EnvAppID: "cli", EnvAppSecret: "wrong", EnvRedirectURI: "/cb", EnvAPIBase: upstream.URL,
	}))
	_, err := c.ExchangeCode(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "10003") {
		t.Fatalf("expected upstream code=10003 in error, got %v", err)
	}
}

func TestHTTPClientRejectsEmptyEmail(t *testing.T) {
	stub := newFeishuStubServer(t)
	stub.userInfo = map[string]any{"name": "Noemail", "open_id": "o", "union_id": "u", "email": ""}
	upstream := httptest.NewServer(stub.handler())
	defer upstream.Close()
	env := envFromMap(map[string]string{
		EnvAppID: "cli", EnvAppSecret: "s", EnvRedirectURI: "/cb", EnvAPIBase: upstream.URL,
	})
	c, _ := NewClientFromEnv(env)
	_, err := c.ExchangeCode(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("expected error mentioning email scope, got %v", err)
	}
}

func TestHTTPClientPropagatesUpstreamNonZeroCode(t *testing.T) {
	stub := newFeishuStubServer(t)
	stub.tokenCode = 99991663
	stub.tokenMsg = "code expired"
	upstream := httptest.NewServer(stub.handler())
	defer upstream.Close()
	env := envFromMap(map[string]string{
		EnvAppID: "cli", EnvAppSecret: "s", EnvRedirectURI: "/cb", EnvAPIBase: upstream.URL,
	})
	c, _ := NewClientFromEnv(env)
	_, err := c.ExchangeCode(context.Background(), "expired")
	if err == nil || !strings.Contains(err.Error(), "99991663") {
		t.Fatalf("expected upstream code in error, got %v", err)
	}
}

func TestHTTPClientAuthorizeURLShape(t *testing.T) {
	env := envFromMap(map[string]string{
		EnvAppID: "cli_abc", EnvAppSecret: "sec", EnvRedirectURI: "https://parsar/cb",
		EnvAuthorizeBase: "https://accounts.example",
	})
	c, _ := NewClientFromEnv(env)
	u := c.AuthorizeURL("csrf-state-1")
	if !strings.HasPrefix(u, "https://accounts.example/open-apis/authen/v1/authorize?") {
		t.Fatalf("AuthorizeURL prefix wrong: %q", u)
	}
	if !strings.Contains(u, "app_id=cli_abc") || !strings.Contains(u, "state=csrf-state-1") || !strings.Contains(u, "response_type=code") {
		t.Fatalf("AuthorizeURL query missing required params: %q", u)
	}
	if !strings.Contains(u, "scope=contact") {
		t.Fatalf("AuthorizeURL missing default scope: %q", u)
	}
}
