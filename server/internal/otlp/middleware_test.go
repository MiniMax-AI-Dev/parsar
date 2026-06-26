package otlp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
)

// TestExtractBearer tables the RFC 7235 §2.1 happy + unhappy shapes
// so a regression that re-permits `Bearer<token>` (no separator) or
// scheme typos surfaces immediately.
func TestExtractBearer(t *testing.T) {
	cases := []struct {
		name      string
		header    string
		wantToken string
		wantErr   bool
	}{
		{"canonical single space", "Bearer abc", "abc", false},
		{"multiple spaces between scheme and token", "Bearer    abc", "abc", false},
		{"tab separator", "Bearer\tabc", "abc", false},
		{"lowercase scheme", "bearer abc", "abc", false},
		{"mixed-case scheme", "BeArEr abc", "abc", false},
		{"leading whitespace trimmed", "   Bearer abc", "abc", false},

		// RFC 7235 requires a separator after the scheme.
		{"no separator after scheme", "Bearerabc", "", true},
		{"scheme starts with bearer but is longer", "Bearersomething abc", "", true},

		{"empty header", "", "", true},
		{"empty token after separator", "Bearer ", "", true},
		{"only whitespace after separator", "Bearer   ", "", true},
		{"wrong scheme basic", "Basic abc", "", true},
		{"wrong scheme token", "Token abc", "", true},
		{"scheme only", "Bearer", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractBearer(tc.header)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got token %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantToken {
				t.Errorf("token = %q, want %q", got, tc.wantToken)
			}
		})
	}
}

// TestClassifyVerifyErrorIsUniform pins the security invariant: every
// signer error class maps to the same external string. The unification
// of the wire string is what matters; precise reasons stay in logs.
func TestClassifyVerifyErrorIsUniform(t *testing.T) {
	for _, err := range []error{
		ErrTokenMalformed,
		ErrTokenExpired,
		ErrTokenSignatureBad,
		ErrTokenMissingClaim,
		ErrSignerNotConfigured,
		errors.New("anything else"),
	} {
		if got := classifyVerifyError(err); got != "token rejected" {
			t.Errorf("classifyVerifyError(%v) = %q, want %q",
				err, got, "token rejected")
		}
	}
}

// TestMiddlewareReturnsUniform401Body exercises the chain through real
// signer + real middleware and confirms every verifier-failure path
// yields an identical user-facing 401 body — no expired/malformed leak.
func TestMiddlewareReturnsUniform401Body(t *testing.T) {
	signer, err := NewSigner("k", SignerOptions{
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	validClaims := TokenClaims{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		AgentRunID:  "22222222-2222-2222-2222-222222222222",
	}
	validToken, err := signer.Sign(validClaims, time.Hour)
	if err != nil {
		t.Fatalf("Sign valid: %v", err)
	}
	expiredVerifier, err := NewSigner("k", SignerOptions{
		Now: func() time.Time { return time.Unix(1_700_003_601, 0) },
	})
	if err != nil {
		t.Fatalf("NewSigner expired: %v", err)
	}

	wrongKeyVerifier, err := NewSigner("WRONG", SignerOptions{
		Now: func() time.Time { return time.Unix(1_700_000_001, 0) },
	})
	if err != nil {
		t.Fatalf("NewSigner wrong: %v", err)
	}

	cases := []struct {
		name     string
		verifier *TokenSigner
		header   string
	}{
		{"missing header", signer, ""},
		{"wrong scheme", signer, "Basic xx"},
		{"no separator", signer, "Bearer" + validToken},
		{"empty bearer", signer, "Bearer "},
		{"tampered token", signer, "Bearer " + "A" + validToken[1:]},
		{"expired token", expiredVerifier, "Bearer " + validToken},
		{"wrong signing key", wrongKeyVerifier, "Bearer " + validToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := authMiddleware(tc.verifier, nil)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					t.Fatalf("handler should not be reached on auth failure")
				}),
			)
			req, _ := http.NewRequest(http.MethodPost, "/v1/traces", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := &recordingResponseWriter{header: make(http.Header)}
			handler.ServeHTTP(rec, req)
			if rec.status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.status)
			}
			body := strings.TrimSpace(rec.body.String())
			// Header parser failures surface their own descriptive
			// errors; token-verifier failures MUST all collapse to
			// "token rejected" so an attacker cannot distinguish
			// expired from wrong-key.
			if tc.name == "tampered token" ||
				tc.name == "expired token" ||
				tc.name == "wrong signing key" {
				if body != "token rejected" {
					t.Errorf("verifier-failure body = %q, want %q",
						body, "token rejected")
				}
			}
		})
	}
}

// TestMiddlewareAttachesClaims confirms the happy path: a valid bearer
// reaches the handler with TokenClaims on the request context.
func TestMiddlewareAttachesClaims(t *testing.T) {
	signer, _ := NewSigner("k", SignerOptions{})
	wantClaims := TokenClaims{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		AgentRunID:  "22222222-2222-2222-2222-222222222222",
		SandboxID:   "sb_xyz",
	}
	token, err := signer.Sign(wantClaims, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var gotClaims TokenClaims
	handler := authMiddleware(signer, nil)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := ClaimsFromContext(r.Context())
			if !ok {
				t.Fatalf("ClaimsFromContext should return claims")
			}
			gotClaims = c
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	req, _ := http.NewRequest(http.MethodPost, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := &recordingResponseWriter{header: make(http.Header)}
	handler.ServeHTTP(rec, req)

	if rec.status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.status)
	}
	if gotClaims.WorkspaceID != wantClaims.WorkspaceID ||
		gotClaims.AgentRunID != wantClaims.AgentRunID ||
		gotClaims.SandboxID != wantClaims.SandboxID {
		t.Errorf("claims mismatch: got %+v want %+v", gotClaims, wantClaims)
	}
}

// TestMiddlewarePanicsOnNilSigner asserts the fail-closed wiring
// check: nil signer at construction is a programming error.
func TestMiddlewarePanicsOnNilSigner(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("authMiddleware(nil) should panic")
		}
	}()
	authMiddleware(nil, nil)
}

// TestMiddlewareLogsPreciseReason confirms the operator log records
// the precise verifier-error class on a 401, while the wire response
// remains uniform.
func TestMiddlewareLogsPreciseReason(t *testing.T) {
	signer, _ := NewSigner("k", SignerOptions{
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	expiredVerifier, _ := NewSigner("k", SignerOptions{
		Now: func() time.Time { return time.Unix(1_700_003_601, 0) },
	})
	token, err := signer.Sign(TokenClaims{
		WorkspaceID: "ws-x", AgentRunID: "run-y",
	}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	handler := authMiddleware(expiredVerifier, logger)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Fatalf("handler should not be reached on auth failure")
		}),
	)
	req, _ := http.NewRequest(http.MethodPost, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := &recordingResponseWriter{header: make(http.Header)}
	handler.ServeHTTP(rec, req)

	if rec.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.status)
	}
	if !strings.Contains(rec.body.String(), "token rejected") {
		t.Errorf("wire body should still be uniform; got %q", rec.body.String())
	}
	logged := buf.String()
	if !strings.Contains(logged, "token verify failed") {
		t.Errorf("logger should record verify-failed event; got %q", logged)
	}
	if !strings.Contains(logged, "expired") {
		t.Errorf("logger should record precise reason (expired); got %q", logged)
	}
	if !strings.Contains(logged, "/v1/traces") {
		t.Errorf("logger should include request path; got %q", logged)
	}
}

// TestMiddlewareLogsHeaderParserFailures confirms parser-level
// failures also produce an operator-visible log entry.
func TestMiddlewareLogsHeaderParserFailures(t *testing.T) {
	signer, _ := NewSigner("k", SignerOptions{})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	handler := authMiddleware(signer, logger)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Fatalf("handler should not be reached on auth failure")
		}),
	)
	req, _ := http.NewRequest(http.MethodPost, "/v1/traces", nil)
	// no Authorization header at all
	rec := &recordingResponseWriter{header: make(http.Header)}
	handler.ServeHTTP(rec, req)

	if rec.status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.status)
	}
	logged := buf.String()
	if !strings.Contains(logged, "bad Authorization header") {
		t.Errorf("logger should record header-parse failure; got %q", logged)
	}
}

// recordingResponseWriter captures status + body so tests can assert
// the 401 payload without standing up an httptest server.
type recordingResponseWriter struct {
	status int
	header http.Header
	body   bytes.Buffer
}

func (r *recordingResponseWriter) Header() http.Header { return r.header }
func (r *recordingResponseWriter) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(b)
}
func (r *recordingResponseWriter) WriteHeader(status int) {
	r.status = status
}

var _ http.ResponseWriter = (*recordingResponseWriter)(nil)

func dummyMiddlewareLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func _testCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Second)
}

func _ensureIngesterAlive() audit.Ingester { return audit.Ingester{} }

var (
	_ = dummyMiddlewareLog
	_ = _testCtx
	_ = _ensureIngesterAlive
)
