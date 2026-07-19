package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	authoidc "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/oidc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// stubSessionStore tracks Create / Resolve / Revoke calls in memory.
type stubSessionStore struct {
	mu       sync.Mutex
	sessions map[string]string // session id → user id
	revoked  map[string]bool
	calls    int
}

func newStubSessions() *stubSessionStore {
	return &stubSessionStore{sessions: map[string]string{}, revoked: map[string]bool{}}
}

func (s *stubSessionStore) Create(_ context.Context, in auth.CreateSessionInput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	id := "session-" + in.UserID + "-" + strings.TrimSpace(in.IP)
	s.sessions[id] = in.UserID
	return id, nil
}

func (s *stubSessionStore) Resolve(_ context.Context, id string, _ time.Time) (auth.SessionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked[id] {
		return auth.SessionInfo{}, auth.ErrInvalidSession
	}
	uid, ok := s.sessions[id]
	if !ok {
		return auth.SessionInfo{}, auth.ErrInvalidSession
	}
	return auth.SessionInfo{ID: id, UserID: uid}, nil
}

func (s *stubSessionStore) Revoke(_ context.Context, id string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[id] = true
	return nil
}

// stubOAuthStore records UpsertOAuthUser inputs so the test asserts
// the callback passed the right (provider, subject, email, metadata).
type stubOAuthStore struct {
	mu       sync.Mutex
	lastIn   store.UpsertOAuthUserInput
	userID   string
	created  bool
	upsertEr error
}

func (s *stubOAuthStore) UpsertOAuthUser(_ context.Context, in store.UpsertOAuthUserInput) (store.UpsertOAuthUserResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastIn = in
	if s.upsertEr != nil {
		return store.UpsertOAuthUserResult{}, s.upsertEr
	}
	uid := s.userID
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000001"
	}
	return store.UpsertOAuthUserResult{
		UserID: uid, Email: in.Email, Name: in.Name, Created: s.created,
	}, nil
}

func buildOAuthRouter(t *testing.T, deps OAuthHandlerDeps) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/auth/feishu/start", feishuStartHandler(deps))
		r.Get("/auth/feishu/callback", feishuCallbackHandler(deps))
		r.Get("/auth/oidc/{providerID}/start", oidcStartHandler(deps))
		r.Get("/auth/oidc/{providerID}/callback", oidcCallbackHandler(deps))
		r.Post("/auth/logout", authLogoutHandler(deps))
	})
	return r
}

type stubOIDCClient struct {
	authURL string
	profile authoidc.UserProfile
	seen    struct {
		state string
		nonce string
		code  string
	}
}

func (c *stubOIDCClient) ID() string          { return c.profile.ProviderID }
func (c *stubOIDCClient) Label() string       { return "Stub OIDC" }
func (c *stubOIDCClient) RedirectURI() string { return "/api/v1/auth/oidc/stub/callback" }
func (c *stubOIDCClient) AuthorizeURL(_ context.Context, state, nonce string) (string, error) {
	c.seen.state = state
	c.seen.nonce = nonce
	return c.authURL + "?state=" + state + "&nonce=" + nonce, nil
}
func (c *stubOIDCClient) ExchangeCode(_ context.Context, code, nonce string) (authoidc.UserProfile, error) {
	c.seen.code = code
	c.seen.nonce = nonce
	return c.profile, nil
}

func TestFeishuStartRedirectsAndSetsStateCookie(t *testing.T) {
	client := feishu.NewMockClient(func(k string) string {
		if k == feishu.EnvRedirectURI {
			return "/api/v1/auth/feishu/callback"
		}
		return ""
	})
	r := buildOAuthRouter(t, OAuthHandlerDeps{Feishu: client, Sessions: newStubSessions(), Store: &stubOAuthStore{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/start", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "code=mock-code") || !strings.Contains(loc, "state=") {
		t.Fatalf("Location = %q, want mock redirect with code+state", loc)
	}
	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.HasPrefix(setCookie, CookieStateName+"=") || !strings.Contains(setCookie, "HttpOnly") {
		t.Fatalf("state cookie missing or unsafe: %q", setCookie)
	}
}

func TestOIDCStartRedirectsAndSetsStateAndNonceCookies(t *testing.T) {
	client := &stubOIDCClient{
		authURL: "https://idp.example/authorize",
		profile: authoidc.UserProfile{
			ProviderID: "company",
		},
	}
	r := buildOAuthRouter(t, OAuthHandlerDeps{
		OIDC:     map[string]authoidc.Client{"company": client},
		Sessions: newStubSessions(),
		Store:    &stubOAuthStore{},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/company/start", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://idp.example/authorize?") {
		t.Fatalf("Location = %q, want OIDC authorize URL", loc)
	}
	cookies := rec.Header().Values("Set-Cookie")
	if len(cookies) != 2 {
		t.Fatalf("cookies = %v, want state and nonce cookies", cookies)
	}
	if client.seen.state == "" || client.seen.nonce == "" {
		t.Fatalf("state/nonce not passed to client: %+v", client.seen)
	}
}

func TestFeishuStart503WhenClientNil(t *testing.T) {
	r := buildOAuthRouter(t, OAuthHandlerDeps{Sessions: newStubSessions(), Store: &stubOAuthStore{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/start", nil)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestFeishuCallbackHappyPath(t *testing.T) {
	feishuClient := feishu.NewMockClient(func(k string) string {
		if k == feishu.EnvRedirectURI {
			return "/api/v1/auth/feishu/callback"
		}
		return ""
	})
	sessions := newStubSessions()
	st := &stubOAuthStore{userID: "00000000-0000-0000-0000-0000deadbeef"}
	r := buildOAuthRouter(t, OAuthHandlerDeps{Feishu: feishuClient, Sessions: sessions, Store: st})

	// Drive start to capture the state cookie.
	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/start", nil))
	stateCookie := rec1.Header().Get("Set-Cookie")
	state := strings.SplitN(strings.TrimPrefix(stateCookie, CookieStateName+"="), ";", 2)[0]
	if state == "" {
		t.Fatal("could not extract state cookie")
	}

	// Now callback with the state echoed in the URL + the cookie attached.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/callback?code=mock&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: state})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect to /", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}
	cookies := rec.Header().Values("Set-Cookie")
	var foundSession bool
	for _, c := range cookies {
		if strings.HasPrefix(c, auth.CookieName+"=") && !strings.Contains(c, "Max-Age=0") && !strings.Contains(c, "Max-Age=-1") {
			foundSession = true
		}
	}
	if !foundSession {
		t.Fatalf("session cookie not set; got cookies: %v", cookies)
	}
	if sessions.calls != 1 {
		t.Fatalf("session.Create called %d times, want 1", sessions.calls)
	}
	if st.lastIn.Provider != "feishu" || st.lastIn.Email != "admin@example.com" {
		t.Fatalf("upsert input wrong: %+v", st.lastIn)
	}
}

func TestOIDCCallbackHappyPath(t *testing.T) {
	client := &stubOIDCClient{
		authURL: "https://idp.example/authorize",
		profile: authoidc.UserProfile{
			Provider:   "oidc:company",
			ProviderID: "company",
			Subject:    "sub-123",
			Email:      "admin@example.com",
			Name:       "Admin User",
			AvatarURL:  "https://idp.example/avatar.png",
			Issuer:     "https://idp.example",
			Claims: map[string]any{
				"sub":            "sub-123",
				"email":          "admin@example.com",
				"email_verified": true,
			},
		},
	}
	sessions := newStubSessions()
	st := &stubOAuthStore{userID: "00000000-0000-0000-0000-0000deadbeef"}
	r := buildOAuthRouter(t, OAuthHandlerDeps{
		OIDC:     map[string]authoidc.Client{"company": client},
		Sessions: sessions,
		Store:    st,
	})

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/company/start", nil))
	var state, nonce string
	for _, rawCookie := range rec1.Header().Values("Set-Cookie") {
		if strings.HasPrefix(rawCookie, CookieStateName+"=") {
			state = strings.SplitN(strings.TrimPrefix(rawCookie, CookieStateName+"="), ";", 2)[0]
		}
		if strings.HasPrefix(rawCookie, CookieNonceName+"=") {
			nonce = strings.SplitN(strings.TrimPrefix(rawCookie, CookieNonceName+"="), ";", 2)[0]
		}
	}
	if state == "" || nonce == "" {
		t.Fatalf("missing start cookies: %v", rec1.Header().Values("Set-Cookie"))
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oidc/company/callback?code=oidc-code&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: state})
	req.AddCookie(&http.Cookie{Name: CookieNonceName, Value: nonce})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 redirect to /", rec.Code)
	}
	if client.seen.code != "oidc-code" || client.seen.nonce != nonce {
		t.Fatalf("exchange input = %+v, want code and nonce", client.seen)
	}
	if st.lastIn.Provider != "oidc:company" || st.lastIn.Subject != "sub-123" || st.lastIn.Email != "admin@example.com" {
		t.Fatalf("upsert input wrong: %+v", st.lastIn)
	}
	if got := st.lastIn.Metadata["issuer"]; got != "https://idp.example" {
		t.Fatalf("metadata issuer = %#v", got)
	}
}

// TestFeishuCallbackDoesNotProvisionWorkspace pins down the post-D.6
// behavior: the callback no longer auto-provisions a workspace for
// brand-new OAuth users. The admin shell's `/onboarding` flow owns
// that step now (driven off an empty `/api/v1/me/workspaces`). This
// test guards against silently re-adding the auto-bootstrap call.
func TestFeishuCallbackDoesNotProvisionWorkspace(t *testing.T) {
	feishuClient := feishu.NewMockClient(func(k string) string {
		if k == feishu.EnvRedirectURI {
			return "/api/v1/auth/feishu/callback"
		}
		return ""
	})
	// `created: true` would have triggered the old BootstrapUserWorkspace
	// branch — assert the callback still 302s cleanly and only invokes
	// the OAuth upsert (single store interaction).
	st := &stubOAuthStore{userID: "00000000-0000-0000-0000-0000deadbeef", created: true}
	r := buildOAuthRouter(t, OAuthHandlerDeps{Feishu: feishuClient, Sessions: newStubSessions(), Store: st})

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/start", nil))
	state := strings.SplitN(strings.TrimPrefix(rec1.Header().Get("Set-Cookie"), CookieStateName+"="), ";", 2)[0]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/callback?code=mock&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: state})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	// stubOAuthStore has no BootstrapUserWorkspace method anymore;
	// compile-time guarantees no callback path can invoke it. The
	// runtime upsert still happens — confirm only that.
	if st.lastIn.Provider != "feishu" {
		t.Fatalf("OAuth upsert not called or wrong provider: %+v", st.lastIn)
	}
}

// TestFeishuCallbackHonorsLoginRedirectURL covers the dev-mode bounce
// target. With LoginRedirectURL set (e.g. Vite origin in `make dev`)
// the callback should 302 there instead of the default "/".
func TestFeishuCallbackHonorsLoginRedirectURL(t *testing.T) {
	feishuClient := feishu.NewMockClient(func(k string) string {
		if k == feishu.EnvRedirectURI {
			return "/api/v1/auth/feishu/callback"
		}
		return ""
	})
	sessions := newStubSessions()
	st := &stubOAuthStore{userID: "00000000-0000-0000-0000-0000deadbeef"}
	r := buildOAuthRouter(t, OAuthHandlerDeps{
		Feishu:           feishuClient,
		Sessions:         sessions,
		Store:            st,
		LoginRedirectURL: "http://127.0.0.1:5173/",
	})

	rec1 := httptest.NewRecorder()
	r.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/start", nil))
	stateCookie := rec1.Header().Get("Set-Cookie")
	state := strings.SplitN(strings.TrimPrefix(stateCookie, CookieStateName+"="), ";", 2)[0]

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/callback?code=mock&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: state})
	r.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); loc != "http://127.0.0.1:5173/" {
		t.Fatalf("Location = %q, want vite override", loc)
	}
}

func TestFeishuCallbackRejectsCSRFMismatch(t *testing.T) {
	feishuClient := feishu.NewMockClient(func(k string) string { return "" })
	r := buildOAuthRouter(t, OAuthHandlerDeps{Feishu: feishuClient, Sessions: newStubSessions(), Store: &stubOAuthStore{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/callback?code=mock&state=url-state", nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: "cookie-state-different"})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on CSRF mismatch", rec.Code)
	}
}

func TestFeishuCallbackRejectsMissingCode(t *testing.T) {
	feishuClient := feishu.NewMockClient(func(k string) string { return "" })
	r := buildOAuthRouter(t, OAuthHandlerDeps{Feishu: feishuClient, Sessions: newStubSessions(), Store: &stubOAuthStore{}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/feishu/callback?state=x", nil)
	req.AddCookie(&http.Cookie{Name: CookieStateName, Value: "x"})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 on missing code", rec.Code)
	}
}

func TestLogoutClearsCookieAndRevokes(t *testing.T) {
	sessions := newStubSessions()
	// pre-populate a session so revoke has something to do
	id, _ := sessions.Create(context.Background(), auth.CreateSessionInput{UserID: "u-1", IP: "1.2.3.4"})
	r := buildOAuthRouter(t, OAuthHandlerDeps{Sessions: sessions})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: id})
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if !sessions.revoked[id] {
		t.Fatalf("session %q should be revoked", id)
	}
	clear := rec.Header().Get("Set-Cookie")
	if !strings.Contains(clear, auth.CookieName+"=") || (!strings.Contains(clear, "Max-Age=0") && !strings.Contains(clear, "Max-Age=-1")) {
		t.Fatalf("ClearCookie expected, got: %q", clear)
	}
}

func TestLogoutIdempotentWithoutCookie(t *testing.T) {
	r := buildOAuthRouter(t, OAuthHandlerDeps{Sessions: newStubSessions()})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (idempotent)", rec.Code)
	}
}
