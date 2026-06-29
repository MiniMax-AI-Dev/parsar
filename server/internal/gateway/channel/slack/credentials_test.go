package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSecretLookup stands in for the store: it returns a canned secret for one
// team and a not-found error for everything else, recording the team it was
// asked about so the test can assert the resolver forwarded the right key.
type fakeSecretLookup struct {
	byTeam map[string]SlackBotSecret
	asked  string
}

func (f *fakeSecretLookup) ResolveSlackBotSecretByTeam(_ context.Context, teamID string) (SlackBotSecret, error) {
	f.asked = teamID
	if s, ok := f.byTeam[teamID]; ok {
		return s, nil
	}
	return SlackBotSecret{}, errors.New("no slack_bot secret for team " + teamID)
}

// fakeDecrypter maps an opaque envelope back to a cleartext payload by string
// key, so the resolver's payload→token extraction is exercised without real
// AES-GCM. A decryptErr forces the decrypt-failure branch.
type fakeDecrypter struct {
	payloads   map[string]map[string]any
	decryptErr error
}

func (f *fakeDecrypter) Decrypt(envelope []byte) (map[string]any, error) {
	if f.decryptErr != nil {
		return nil, f.decryptErr
	}
	if p, ok := f.payloads[string(envelope)]; ok {
		return p, nil
	}
	return map[string]any{}, nil
}

func TestDBCredentialResolver_ResolvesPerTeamToken(t *testing.T) {
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{
		"T_ACME": {AppID: "A_ACME", EncryptedPayload: []byte("env-acme")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-acme": {"token": "xoxb-acme-123"},
	}}
	r := NewDBCredentialResolver(lookup, dec, nil)

	cred, err := r.Resolve(context.Background(), "T_ACME")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if lookup.asked != "T_ACME" {
		t.Errorf("lookup asked for team %q, want T_ACME", lookup.asked)
	}
	if cred.AppSecret != "xoxb-acme-123" {
		t.Errorf("token = %q, want xoxb-acme-123", cred.AppSecret)
	}
	if cred.AppID != "A_ACME" {
		t.Errorf("app id = %q, want A_ACME (from secret metadata)", cred.AppID)
	}
}

func TestDBCredentialResolver_AppIDFallsBackToTeam(t *testing.T) {
	// A secret authored without an app_id in metadata still resolves; the
	// team id stands in as the neutral bot id.
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{
		"T_NOAPP": {EncryptedPayload: []byte("env-noapp")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-noapp": {"access_token": "xoxb-noapp"},
	}}
	cred, err := NewDBCredentialResolver(lookup, dec, nil).Resolve(context.Background(), "T_NOAPP")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppID != "T_NOAPP" {
		t.Errorf("app id = %q, want the team id as fallback", cred.AppID)
	}
	if cred.AppSecret != "xoxb-noapp" {
		t.Errorf("token = %q, want xoxb-noapp (access_token key)", cred.AppSecret)
	}
}

func TestDBCredentialResolver_UnknownTeamFallsBackToStatic(t *testing.T) {
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{}} // every team misses
	dec := &fakeDecrypter{}
	fallback := NewStaticCredentialResolver("A_ENV", "xoxb-env-default")
	r := NewDBCredentialResolver(lookup, dec, fallback)

	cred, err := r.Resolve(context.Background(), "T_UNKNOWN")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppSecret != "xoxb-env-default" {
		t.Errorf("token = %q, want the static env fallback", cred.AppSecret)
	}
}

func TestDBCredentialResolver_EmptyTeamUsesFallback(t *testing.T) {
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{}}
	r := NewDBCredentialResolver(lookup, &fakeDecrypter{}, NewStaticCredentialResolver("A_ENV", "xoxb-env"))

	cred, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppSecret != "xoxb-env" {
		t.Errorf("token = %q, want fallback for empty team", cred.AppSecret)
	}
	if lookup.asked != "" {
		t.Errorf("empty team must not hit the store; asked %q", lookup.asked)
	}
}

func TestDBCredentialResolver_NoFallbackNoTeamErrors(t *testing.T) {
	// DB-only deployment with no env token: an empty team has nothing to
	// resolve, so the missing-token error surfaces rather than a silent send.
	r := NewDBCredentialResolver(&fakeSecretLookup{byTeam: map[string]SlackBotSecret{}}, &fakeDecrypter{}, nil)
	if _, err := r.Resolve(context.Background(), ""); !errors.Is(err, errNoBotToken) {
		t.Fatalf("Resolve(empty, nil fallback) err = %v, want errNoBotToken", err)
	}
}

func TestDBCredentialResolver_DecryptFailureSurfaces(t *testing.T) {
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{
		"T_BAD": {EncryptedPayload: []byte("corrupt")},
	}}
	dec := &fakeDecrypter{decryptErr: errors.New("gcm: message authentication failed")}
	// A decrypt failure is a real misconfiguration (wrong master key) — it must
	// NOT silently fall back to the static token, so we pass a fallback and
	// assert it is not used.
	r := NewDBCredentialResolver(lookup, dec, NewStaticCredentialResolver("A", "xoxb-should-not-be-used"))
	if _, err := r.Resolve(context.Background(), "T_BAD"); err == nil {
		t.Fatal("Resolve must surface a decrypt failure, not fall back")
	}
}

func TestDBCredentialResolver_EmptyTokenValueErrors(t *testing.T) {
	lookup := &fakeSecretLookup{byTeam: map[string]SlackBotSecret{
		"T_EMPTY": {EncryptedPayload: []byte("env-empty")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-empty": {"unrelated": "value"}, // no token-shaped key
	}}
	r := NewDBCredentialResolver(lookup, dec, nil)
	if _, err := r.Resolve(context.Background(), "T_EMPTY"); err == nil {
		t.Fatal("a slack_bot secret with no token value must error")
	}
}

// compile-time guard: the static fallback resolver still satisfies the contract.
var _ channel.CredentialResolver = NewStaticCredentialResolver("", "")
