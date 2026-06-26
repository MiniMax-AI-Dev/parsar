package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	authgithub "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/github"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const githubTestUserID = "00000000-0000-0000-0000-0000000000aa"

func TestGitHubConnectionStartRedirectsWithStateCookie(t *testing.T) {
	store := &githubCredentialStore{}
	client := fakeGitHubClient{authorizeURL: "https://github.test/login/oauth/authorize?state=fake"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/github/start", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), githubTestUserID))
	rec := httptest.NewRecorder()

	githubConnectionStartHandler(store, GitHubConnectionDeps{Client: client})(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != client.authorizeURL {
		t.Fatalf("location = %q", got)
	}
	if cookie := rec.Result().Cookies()[0]; cookie.Name != githubOAuthStateCookieName || cookie.Value == "" || !cookie.HttpOnly {
		t.Fatalf("bad state cookie: %+v", cookie)
	}
}

func TestGitHubConnectionCallbackCreatesGitHubCredential(t *testing.T) {
	masterKey := "test-master-key-test-master-key-"
	t.Setenv("PARSAR_MASTER_KEY", masterKey)
	store := &githubCredentialStore{}
	client := fakeGitHubClient{credential: authgithub.Credential{
		AccessToken: "gho_secret",
		TokenType:   "bearer",
		Scope:       "repo read:user",
		Login:       "octocat",
		UserID:      42,
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/github/callback?code=ok&state=s123", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), githubTestUserID))
	req.AddCookie(&http.Cookie{Name: githubOAuthStateCookieName, Value: "s123"})
	rec := httptest.NewRecorder()

	githubConnectionCallbackHandler(store, GitHubConnectionDeps{Client: client, RedirectURL: "http://127.0.0.1:5173/"})(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "http://127.0.0.1:5173/?admin=connections&connected=github" {
		t.Fatalf("location = %q", got)
	}
	if store.created.Kind != "github_pat" || store.created.DisplayName != "GitHub @octocat" || store.created.UserID != githubTestUserID {
		t.Fatalf("unexpected created credential: %+v", store.created)
	}
	secretSvc, err := secrets.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := secretSvc.Decrypt(store.created.EncryptedValue)
	if err != nil {
		t.Fatal(err)
	}
	if payload["value"] != "gho_secret" || payload["provider"] != "github" || payload["login"] != "octocat" {
		t.Fatalf("unexpected encrypted payload: %+v", payload)
	}
}

func TestGitHubConnectionCallbackRotatesExistingGitHubCredential(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-master-key-test-master-key-")
	store := &githubCredentialStore{existing: []store.UserCredentialRead{{ID: "00000000-0000-0000-0000-000000000123", Kind: "github_pat"}}}
	client := fakeGitHubClient{credential: authgithub.Credential{AccessToken: "gho_new", Login: "octocat"}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/connections/github/callback?code=ok&state=s123", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), githubTestUserID))
	req.AddCookie(&http.Cookie{Name: githubOAuthStateCookieName, Value: "s123"})
	rec := httptest.NewRecorder()

	githubConnectionCallbackHandler(store, GitHubConnectionDeps{Client: client})(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if store.updated.CredentialID != "00000000-0000-0000-0000-000000000123" || store.updated.DisplayName == nil || *store.updated.DisplayName != "GitHub @octocat" {
		t.Fatalf("unexpected update: %+v", store.updated)
	}
}

type fakeGitHubClient struct {
	authorizeURL string
	credential   authgithub.Credential
	err          error
}

func (f fakeGitHubClient) AuthorizeURL(state string) string { return f.authorizeURL }
func (f fakeGitHubClient) ExchangeCode(ctx context.Context, code string) (authgithub.Credential, error) {
	return f.credential, f.err
}

type githubCredentialStore struct {
	stubRuntimeStore
	existing []store.UserCredentialRead
	created  store.CreateUserCredentialInput
	updated  store.UpdateUserCredentialInput
}

func (s *githubCredentialStore) ListUserCredentials(ctx context.Context, userID string) ([]store.UserCredentialRead, error) {
	return s.existing, nil
}

func (s *githubCredentialStore) CreateUserCredential(ctx context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error) {
	s.created = input
	return store.UserCredentialRead{ID: "00000000-0000-0000-0000-000000000c03", UserID: input.UserID, Kind: input.Kind, DisplayName: input.DisplayName, Ciphertext: input.EncryptedValue}, nil
}

func (s *githubCredentialStore) UpdateUserCredential(ctx context.Context, input store.UpdateUserCredentialInput) (store.UserCredentialRead, error) {
	s.updated = input
	displayName := ""
	if input.DisplayName != nil {
		displayName = *input.DisplayName
	}
	return store.UserCredentialRead{ID: input.CredentialID, UserID: githubTestUserID, Kind: "github_pat", DisplayName: displayName, Ciphertext: input.EncryptedValue}, nil
}
