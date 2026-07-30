package oidc

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestLoadProviderStatusesMultipleProviders(t *testing.T) {
	env := map[string]string{
		EnvProviders:                              "google,company-sso",
		"PARSAR_AUTH_OIDC_GOOGLE_LABEL":           "Google",
		"PARSAR_AUTH_OIDC_GOOGLE_ISSUER_URL":      "https://accounts.google.com",
		"PARSAR_AUTH_OIDC_GOOGLE_CLIENT_ID":       "google-client",
		"PARSAR_AUTH_OIDC_GOOGLE_CLIENT_SECRET":   "google-secret",
		"PARSAR_AUTH_OIDC_COMPANY_SSO_LABEL":      "Company SSO",
		"PARSAR_AUTH_OIDC_COMPANY_SSO_ISSUER_URL": "https://idp.example.com",
	}
	statuses, err := LoadProviderStatuses(func(k string) string { return env[k] }, "https://parsar.example")
	if err != nil {
		t.Fatalf("LoadProviderStatuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("status count = %d, want 2", len(statuses))
	}
	if statuses[0].Config.ID != "google" || statuses[0].Config.RedirectURI != "https://parsar.example/api/v1/auth/oidc/google/callback" {
		t.Fatalf("google status = %+v", statuses[0])
	}
	if len(statuses[1].MissingEnv) == 0 {
		t.Fatalf("company status should report missing client env: %+v", statuses[1])
	}
}

func TestClientExchangeUsesOIDCLibraryAndBuildsProfile(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(t, w, map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                issuer + "/authorize",
				"token_endpoint":                        issuer + "/token",
				"userinfo_endpoint":                     issuer + "/userinfo",
				"jwks_uri":                              issuer + "/jwks",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/jwks":
			writeTestJSON(t, w, map[string]any{
				"keys": []map[string]any{rsaJWK("test-kid", &key.PublicKey)},
			})
		case "/token":
			user, pass, _ := r.BasicAuth()
			if user != "client-1" || pass != "secret-1" {
				t.Fatalf("basic auth = %q/%q, want client credentials", user, pass)
			}
			writeTestJSON(t, w, map[string]any{
				"access_token": "access-1",
				"id_token":     signTestIDToken(t, key, issuer, "client-1", "nonce-1"),
				"token_type":   "Bearer",
			})
		case "/userinfo":
			writeTestJSON(t, w, map[string]any{
				"sub":     "sub-1",
				"picture": "https://idp.example/avatar.png",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	issuer = srv.URL

	client, err := NewClient(ProviderConfig{
		ID:                   "company",
		Label:                "Company",
		IssuerURL:            issuer,
		ClientID:             "client-1",
		ClientSecret:         "secret-1",
		RedirectURI:          "https://parsar.example/api/v1/auth/oidc/company/callback",
		Scopes:               []string{"openid", "email", "profile"},
		AllowedDomains:       []string{"example.com"},
		RequireVerifiedEmail: true,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	authURL, err := client.AuthorizeURL(t.Context(), "state-1", "nonce-1")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	if !strings.HasPrefix(authURL, issuer+"/authorize?") {
		t.Fatalf("authorize URL = %q, want discovered endpoint", authURL)
	}
	profile, err := client.ExchangeCode(t.Context(), "code-1", "nonce-1")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if profile.Provider != "oidc:company" || profile.Subject != "sub-1" || profile.Email != "admin@example.com" {
		t.Fatalf("profile = %+v", profile)
	}
	if profile.AvatarURL != "https://idp.example/avatar.png" {
		t.Fatalf("avatar = %q", profile.AvatarURL)
	}
}

func TestClientExchangeRejectsDomainPolicy(t *testing.T) {
	err := validateProfile(ProviderConfig{
		AllowedDomains:       []string{"example.com"},
		RequireVerifiedEmail: true,
	}, UserProfile{
		Subject: "sub-1",
		Email:   "admin@other.com",
		Claims:  map[string]any{"email_verified": true},
	})
	if err == nil || !strings.Contains(err.Error(), "email domain") {
		t.Fatalf("validateProfile err = %v, want domain rejection", err)
	}
}

func signTestIDToken(t *testing.T, key *rsa.PrivateKey, issuer, audience, nonce string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":            issuer,
		"aud":            audience,
		"sub":            "sub-1",
		"email":          "admin@example.com",
		"email_verified": true,
		"name":           "Admin User",
		"nonce":          nonce,
		"exp":            time.Now().Add(time.Hour).Unix(),
		"iat":            time.Now().Add(-time.Minute).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-kid"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func rsaJWK(kid string, key *rsa.PublicKey) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("Encode: %v", err)
	}
}
