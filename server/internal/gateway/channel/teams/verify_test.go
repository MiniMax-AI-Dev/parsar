package teams

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeVerifier is a TokenVerifier seam: it returns its canned error (nil =
// accept) and records the header it saw.
type fakeVerifier struct {
	err     error
	sawAuth string
}

func (f *fakeVerifier) Verify(_ context.Context, authorizationHeader string) error {
	f.sawAuth = authorizationHeader
	return f.err
}

// TestVerify_NoVerifierPassthrough: with no verifier wired (the Emulator / local
// path) Verify returns the body unchanged and never a challenge.
func TestVerify_NoVerifierPassthrough(t *testing.T) {
	c := New(Config{}) // empty AppID → no verifier
	body := []byte(`{"type":"message"}`)
	got, challenge, err := c.Verify(httptest.NewRequest(http.MethodPost, "/", nil), body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if challenge != "" {
		t.Errorf("Teams has no url_verification handshake; challenge = %q", challenge)
	}
	if string(got) != string(body) {
		t.Errorf("body altered: %q", got)
	}
}

func TestVerify_AcceptsGoodToken(t *testing.T) {
	fv := &fakeVerifier{err: nil}
	c := New(Config{AppID: "app-123"}, WithTokenVerifier(fv))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer good.jwt")
	body := []byte(`{"type":"message"}`)
	got, _, err := c.Verify(req, body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body altered: %q", got)
	}
	if fv.sawAuth != "Bearer good.jwt" {
		t.Errorf("verifier saw %q, want the raw Authorization header", fv.sawAuth)
	}
}

func TestVerify_RejectsBadToken(t *testing.T) {
	c := New(Config{AppID: "app-123"}, WithTokenVerifier(&fakeVerifier{err: errors.New("bad sig")}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer bad.jwt")
	if _, _, err := c.Verify(req, []byte(`{}`)); err == nil {
		t.Fatal("Verify must reject a token the verifier rejects")
	}
}

// TestNewJWKSVerifier_RejectsEmptyHeader guards the production verifier's cheap
// front-door checks without any network I/O (a missing header fails before the
// JWKS fetch).
func TestNewJWKSVerifier_RejectsEmptyHeader(t *testing.T) {
	v := NewJWKSVerifier("app-123")
	if err := v.Verify(context.Background(), ""); err == nil {
		t.Fatal("empty Authorization header must error")
	}
	if err := v.Verify(context.Background(), "Bearer "); err == nil {
		t.Fatal("empty bearer token must error")
	}
}

// TestAudienceAccepted checks the multi-tenant audience predicate helper: any
// non-empty aud that passes the allow closure accepts; empty entries and a
// wholly-unknown set reject.
func TestAudienceAccepted(t *testing.T) {
	allow := func(a string) bool { return a == "app-a" || a == "app-b" }
	cases := []struct {
		name string
		auds []string
		want bool
	}{
		{"hit-single", []string{"app-a"}, true},
		{"hit-among-many", []string{"nope", "app-b"}, true},
		{"miss", []string{"nope", "other"}, false},
		{"empty-entries-skipped", []string{"", "  "}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := audienceAccepted(tc.auds, allow); got != tc.want {
				t.Fatalf("audienceAccepted(%v) = %v, want %v", tc.auds, got, tc.want)
			}
		})
	}
}

// TestMultiTenantJWKSVerifier_AudienceGate stands up fixture OpenID/JWKS servers
// serving one RSA key, signs a Bot Framework-shaped token with a chosen audience,
// and asserts the multi-tenant verifier accepts the token only when its own aud
// claim passes the allow closure — signature/issuer being identical either way.
func TestMultiTenantJWKSVerifier_AudienceGate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	const kid = "test-kid-1"
	const issuer = "https://fixture.issuer/"

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"keys": []any{jwkFromPublic(key, kid)}})
	}))
	defer jwks.Close()

	openid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"jwks_uri": jwks.URL})
	}))
	defer openid.Close()

	sign := func(aud string) string {
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"aud": aud,
			"iss": issuer,
			"exp": time.Now().Add(time.Hour).Unix(),
			"iat": time.Now().Add(-time.Minute).Unix(),
		})
		tok.Header["kid"] = kid
		s, err := tok.SignedString(key)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return s
	}

	allowed := func(a string) bool { return a == "registered-app" }
	v := NewMultiTenantJWKSVerifier(allowed,
		WithOpenIDConfigURL(openid.URL),
		WithExpectedIssuer(issuer),
		WithHTTPClient(jwks.Client()),
	)

	if err := v.Verify(context.Background(), "Bearer "+sign("registered-app")); err != nil {
		t.Fatalf("registered audience must verify: %v", err)
	}
	if err := v.Verify(context.Background(), "Bearer "+sign("stranger-app")); err == nil {
		t.Fatal("unregistered audience must be rejected")
	}
}

// writeJSON is a fixture-server helper that fails the test on an encode error.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encode fixture json: %v", err)
	}
}

// jwkFromPublic renders an RSA public key as a JWK map (base64url n/e) for the
// fixture JWKS endpoint.
func jwkFromPublic(key *rsa.PrivateKey, kid string) map[string]any {
	eBytes := big.NewInt(int64(key.E)).Bytes()
	return map[string]any{
		"kty": "RSA",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(eBytes),
	}
}
