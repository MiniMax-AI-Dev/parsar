package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestSkillUploadCredentialScopeAndExpiry(t *testing.T) {
	s := NewSkillUploadSigner("test-master-key")
	const runID = "00000000-0000-0000-0000-000000001234"
	token, err := s.Token(runID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Verify(token); err != nil || got != runID {
		t.Fatalf("verify: run=%q, error=%v", got, err)
	}
	if _, err := NewSkillUploadSigner("other-key").Verify(token); err == nil {
		t.Fatal("accepted a different signing key")
	}
	for _, tc := range []struct {
		name     string
		audience string
		expires  *jwt.NumericDate
		method   jwt.SigningMethod
	}{
		{"expired", skillUploadAudience, jwt.NewNumericDate(time.Now().Add(-time.Second)), jwt.SigningMethodHS256},
		{"no expiry", skillUploadAudience, nil, jwt.SigningMethodHS256},
		{"wrong audience", "runtime", jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS256},
		{"wrong algorithm", skillUploadAudience, jwt.NewNumericDate(time.Now().Add(time.Hour)), jwt.SigningMethodHS384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad, err := jwt.NewWithClaims(tc.method, jwt.RegisteredClaims{Subject: runID, Audience: jwt.ClaimStrings{tc.audience}, ExpiresAt: tc.expires}).SignedString(s.key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Verify(bad); err == nil {
				t.Fatal("accepted invalid credential")
			}
		})
	}
	if NewSkillUploadSigner(" ") != nil {
		t.Fatal("empty master key enabled signing")
	}
}
