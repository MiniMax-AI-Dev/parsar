package otlp

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// freezeTime returns a now() func anchored at the supplied unix
// second so token expiry / clock-skew behavior is deterministic.
func freezeTime(unixSec int64) func() time.Time {
	return func() time.Time { return time.Unix(unixSec, 0).UTC() }
}

func newTestSigner(t *testing.T, key string, nowSec int64) *TokenSigner {
	t.Helper()
	s, err := NewSigner(key, SignerOptions{Now: freezeTime(nowSec)})
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

// TestSignerRoundTrip: sign known claims, verify, claims survive.
func TestSignerRoundTrip(t *testing.T) {
	s := newTestSigner(t, "dev-otlp-signing-key", 1_700_000_000)
	in := TokenClaims{
		WorkspaceID: "11111111-1111-1111-1111-111111111111",
		AgentRunID:  "22222222-2222-2222-2222-222222222222",
		SandboxID:   "sb_abc",
	}
	tok, err := s.Sign(in, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if tok == "" || !strings.Contains(tok, ".") {
		t.Fatalf("token shape: %q", tok)
	}

	out, err := s.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if out.WorkspaceID != in.WorkspaceID || out.AgentRunID != in.AgentRunID ||
		out.SandboxID != in.SandboxID {
		t.Errorf("claims lost: got %+v want %+v", out, in)
	}
	if out.IssuedAt != 1_700_000_000 {
		t.Errorf("iat: got %d", out.IssuedAt)
	}
	if out.ExpiresAt != 1_700_003_600 {
		t.Errorf("exp: got %d (want iat+3600)", out.ExpiresAt)
	}
	if out.Nonce == "" {
		t.Errorf("nonce should be auto-populated")
	}
}

// TestSignerRejectsEmptyKey: a zero-byte key would implicitly
// authenticate every request, which is strictly worse than refusing
// to start.
func TestSignerRejectsEmptyKey(t *testing.T) {
	for _, k := range []string{"", "  ", "\t\n"} {
		if _, err := NewSigner(k, SignerOptions{}); !errors.Is(err, ErrSignerNotConfigured) {
			t.Errorf("NewSigner(%q): want ErrSignerNotConfigured, got %v", k, err)
		}
	}
}

// TestSignerRequiresClaims asserts Sign refuses to mint a token with
// missing identifiers — the receiver pins these dimensions to
// override OTLP-payload spoofing.
func TestSignerRequiresClaims(t *testing.T) {
	s := newTestSigner(t, "k", 1)
	cases := []struct {
		name   string
		claims TokenClaims
	}{
		{"missing workspace", TokenClaims{AgentRunID: "r"}},
		{"missing run", TokenClaims{WorkspaceID: "w"}},
		{"both blank", TokenClaims{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Sign(tc.claims, time.Hour); !errors.Is(err, ErrTokenMissingClaim) {
				t.Errorf("want ErrTokenMissingClaim, got %v", err)
			}
		})
	}
}

// TestSignerLifetimeCap enforces the 24h MaxTokenLifetime so a
// leaked key cannot accidentally widen the breach window.
func TestSignerLifetimeCap(t *testing.T) {
	s := newTestSigner(t, "k", 1)
	claims := TokenClaims{WorkspaceID: "w", AgentRunID: "r"}
	for _, d := range []time.Duration{0, -time.Second, MaxTokenLifetime + time.Second} {
		if _, err := s.Sign(claims, d); !errors.Is(err, ErrTokenLifetimeTooLong) {
			t.Errorf("Sign(%s): want ErrTokenLifetimeTooLong, got %v", d, err)
		}
	}
	if _, err := s.Sign(claims, MaxTokenLifetime); err != nil {
		t.Errorf("Sign(MaxTokenLifetime): unexpected error %v", err)
	}
}

// TestVerifyRejectsExpired anchors the verifier past exp and confirms
// ErrTokenExpired.
func TestVerifyRejectsExpired(t *testing.T) {
	signer := newTestSigner(t, "k", 1_700_000_000)
	tok, err := signer.Sign(TokenClaims{WorkspaceID: "w", AgentRunID: "r"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	verifier := newTestSigner(t, "k", 1_700_003_601) // 1s past exp
	if _, err := verifier.Verify(tok); !errors.Is(err, ErrTokenExpired) {
		t.Errorf("want ErrTokenExpired, got %v", err)
	}
}

// TestVerifyRejectsWrongKey: a token minted with key A must NOT
// verify under key B.
func TestVerifyRejectsWrongKey(t *testing.T) {
	good := newTestSigner(t, "key-A", 1)
	bad := newTestSigner(t, "key-B", 1)
	tok, err := good.Sign(TokenClaims{WorkspaceID: "w", AgentRunID: "r"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := bad.Verify(tok); !errors.Is(err, ErrTokenSignatureBad) {
		t.Errorf("want ErrTokenSignatureBad, got %v", err)
	}
}

// TestVerifyRejectsTamperedPayload mutates the payload and confirms
// the signature check trips — guards against a regression where
// Verify decodes the payload before validating the signature.
func TestVerifyRejectsTamperedPayload(t *testing.T) {
	s := newTestSigner(t, "k", 1)
	tok, err := s.Sign(TokenClaims{WorkspaceID: "w", AgentRunID: "r"}, time.Hour)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parts := strings.SplitN(tok, ".", 2)
	tampered := "A" + parts[0][1:] + "." + parts[1]
	if _, err := s.Verify(tampered); !errors.Is(err, ErrTokenSignatureBad) {
		t.Errorf("want ErrTokenSignatureBad, got %v", err)
	}
}

// TestVerifyRejectsMalformed covers parser defensive branches.
func TestVerifyRejectsMalformed(t *testing.T) {
	s := newTestSigner(t, "k", 1)
	for _, tok := range []string{"", "no-dot-token", "only.", ".only", "bad-b64!@#.bad-b64"} {
		if _, err := s.Verify(tok); !errors.Is(err, ErrTokenMalformed) {
			t.Errorf("Verify(%q): want ErrTokenMalformed, got %v", tok, err)
		}
	}
}

// TestSignerNonceVaries asserts identical claims encode to different
// tokens — fingerprinting protection if tokens surface in access logs.
func TestSignerNonceVaries(t *testing.T) {
	s := newTestSigner(t, "k", 1_700_000_000)
	claims := TokenClaims{WorkspaceID: "w", AgentRunID: "r"}
	a, err := s.Sign(claims, time.Hour)
	if err != nil {
		t.Fatalf("Sign a: %v", err)
	}
	b, err := s.Sign(claims, time.Hour)
	if err != nil {
		t.Fatalf("Sign b: %v", err)
	}
	if a == b {
		t.Errorf("identical claims produced identical tokens; nonce not varying")
	}
}

// TestNilSignerVerifyError guards the nil-receiver path so a wiring
// bug surfaces as a clear error instead of a nil panic.
func TestNilSignerVerifyError(t *testing.T) {
	var s *TokenSigner
	if _, err := s.Verify("anything"); !errors.Is(err, ErrSignerNotConfigured) {
		t.Errorf("nil Verify: want ErrSignerNotConfigured, got %v", err)
	}
	if _, err := s.Sign(TokenClaims{WorkspaceID: "w", AgentRunID: "r"}, time.Hour); !errors.Is(err, ErrSignerNotConfigured) {
		t.Errorf("nil Sign: want ErrSignerNotConfigured, got %v", err)
	}
}
