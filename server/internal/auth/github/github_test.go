package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestAuthorizeURLIncludesOAuthParams(t *testing.T) {
	client, err := NewClientFromEnv(mapEnv(map[string]string{
		EnvClientID:      "client-id",
		EnvClientSecret:  "client-secret",
		EnvRedirectURI:   "https://parsar.test/api/v1/connections/github/callback",
		EnvAuthorizeBase: "https://github.test",
	}).get)
	if err != nil {
		t.Fatal(err)
	}
	got := client.AuthorizeURL("state-123")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "github.test" || parsed.Path != authorizePath {
		t.Fatalf("unexpected authorize URL: %s", got)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"client_id":    "client-id",
		"redirect_uri": "https://parsar.test/api/v1/connections/github/callback",
		"scope":        DefaultScope,
		"state":        "state-123",
	} {
		if got := q.Get(k); got != want {
			t.Fatalf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestExchangeCodeFetchesUserProfile(t *testing.T) {
	var tokenForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenPath:
			if r.Method != http.MethodPost {
				t.Fatalf("token method = %s", r.Method)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Fatalf("token accept = %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			tokenForm = r.PostForm
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "gho_token",
				"token_type":   "bearer",
				"scope":        "repo read:user",
			})
		case userPath:
			if got := r.Header.Get("Authorization"); got != "Bearer gho_token" {
				t.Fatalf("user auth = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"login":      "octocat",
				"id":         42,
				"name":       "The Octocat",
				"avatar_url": "https://github.test/avatar.png",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClientFromEnv(mapEnv(map[string]string{
		EnvClientID:      "client-id",
		EnvClientSecret:  "client-secret",
		EnvRedirectURI:   "https://parsar.test/api/v1/connections/github/callback",
		EnvAuthorizeBase: server.URL,
		EnvAPIBase:       server.URL,
	}).get)
	if err != nil {
		t.Fatal(err)
	}
	cred, err := client.ExchangeCode(context.Background(), "code-123")
	if err != nil {
		t.Fatal(err)
	}
	if tokenForm.Get("client_id") != "client-id" || tokenForm.Get("client_secret") != "client-secret" || tokenForm.Get("code") != "code-123" {
		t.Fatalf("unexpected token form: %s", tokenForm.Encode())
	}
	if cred.AccessToken != "gho_token" || cred.Login != "octocat" || cred.UserID != 42 || cred.Scope != "repo read:user" {
		t.Fatalf("unexpected credential: %+v", cred)
	}
}

func TestExchangeCodeRejectsOAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad_verification_code", "error_description": "bad code"})
	}))
	defer server.Close()
	client, err := NewClientFromEnv(mapEnv(map[string]string{
		EnvClientID:      "client-id",
		EnvClientSecret:  "client-secret",
		EnvRedirectURI:   "https://parsar.test/callback",
		EnvAuthorizeBase: server.URL,
		EnvAPIBase:       server.URL,
	}).get)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ExchangeCode(context.Background(), "bad")
	if err == nil || !strings.Contains(err.Error(), "bad code") {
		t.Fatalf("expected oauth error, got %v", err)
	}
}

type mapEnv map[string]string

func (e mapEnv) get(key string) string { return e[key] }
