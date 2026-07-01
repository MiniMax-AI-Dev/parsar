package teams

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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
