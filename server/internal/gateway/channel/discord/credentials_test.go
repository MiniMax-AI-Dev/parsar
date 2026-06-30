package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSecretLookup stands in for the store: it returns a canned secret for one
// guild and a not-found error for everything else, recording the guild it was
// asked about so the test can assert the resolver forwarded the right key.
type fakeSecretLookup struct {
	byGuild map[string]DiscordBotSecret
	asked   string
}

func (f *fakeSecretLookup) ResolveDiscordBotSecretByGuild(_ context.Context, guildID string) (DiscordBotSecret, error) {
	f.asked = guildID
	if s, ok := f.byGuild[guildID]; ok {
		return s, nil
	}
	return DiscordBotSecret{}, errors.New("no discord_bot secret for guild " + guildID)
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

func TestDBCredentialResolver_ResolvesPerGuildToken(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{
		"G_ACME": {AppID: "A_ACME", EncryptedPayload: []byte("env-acme")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-acme": {"token": "bot-acme-123"},
	}}
	r := NewDBCredentialResolver(lookup, dec, nil)

	cred, err := r.Resolve(context.Background(), "G_ACME")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if lookup.asked != "G_ACME" {
		t.Errorf("lookup asked for guild %q, want G_ACME", lookup.asked)
	}
	if cred.AppSecret != "bot-acme-123" {
		t.Errorf("token = %q, want bot-acme-123", cred.AppSecret)
	}
	if cred.AppID != "A_ACME" {
		t.Errorf("app id = %q, want A_ACME (from secret metadata)", cred.AppID)
	}
}

func TestDBCredentialResolver_AppIDFallsBackToGuild(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{
		"G_NOAPP": {EncryptedPayload: []byte("env-noapp")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-noapp": {"access_token": "bot-noapp"},
	}}
	cred, err := NewDBCredentialResolver(lookup, dec, nil).Resolve(context.Background(), "G_NOAPP")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppID != "G_NOAPP" {
		t.Errorf("app id = %q, want the guild id as fallback", cred.AppID)
	}
	if cred.AppSecret != "bot-noapp" {
		t.Errorf("token = %q, want bot-noapp (access_token key)", cred.AppSecret)
	}
}

func TestDBCredentialResolver_UnknownGuildFallsBackToStatic(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{}} // every guild misses
	dec := &fakeDecrypter{}
	fallback := NewStaticCredentialResolver("A_ENV", "bot-env-default")
	r := NewDBCredentialResolver(lookup, dec, fallback)

	cred, err := r.Resolve(context.Background(), "G_UNKNOWN")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppSecret != "bot-env-default" {
		t.Errorf("token = %q, want the static env fallback", cred.AppSecret)
	}
}

func TestDBCredentialResolver_EmptyGuildUsesFallback(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{}}
	r := NewDBCredentialResolver(lookup, &fakeDecrypter{}, NewStaticCredentialResolver("A_ENV", "bot-env"))

	cred, err := r.Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppSecret != "bot-env" {
		t.Errorf("token = %q, want fallback for empty guild", cred.AppSecret)
	}
	if lookup.asked != "" {
		t.Errorf("empty guild must not hit the store; asked %q", lookup.asked)
	}
}

func TestDBCredentialResolver_NoFallbackNoGuildErrors(t *testing.T) {
	r := NewDBCredentialResolver(&fakeSecretLookup{byGuild: map[string]DiscordBotSecret{}}, &fakeDecrypter{}, nil)
	if _, err := r.Resolve(context.Background(), ""); !errors.Is(err, errNoBotToken) {
		t.Fatalf("Resolve(empty, nil fallback) err = %v, want errNoBotToken", err)
	}
}

func TestDBCredentialResolver_DecryptFailureSurfaces(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{
		"G_BAD": {EncryptedPayload: []byte("corrupt")},
	}}
	dec := &fakeDecrypter{decryptErr: errors.New("gcm: message authentication failed")}
	// A decrypt failure is a real misconfiguration (wrong master key) — it must
	// NOT silently fall back to the static token.
	r := NewDBCredentialResolver(lookup, dec, NewStaticCredentialResolver("A", "bot-should-not-be-used"))
	if _, err := r.Resolve(context.Background(), "G_BAD"); err == nil {
		t.Fatal("Resolve must surface a decrypt failure, not fall back")
	}
}

func TestDBCredentialResolver_EmptyTokenValueErrors(t *testing.T) {
	lookup := &fakeSecretLookup{byGuild: map[string]DiscordBotSecret{
		"G_EMPTY": {EncryptedPayload: []byte("env-empty")},
	}}
	dec := &fakeDecrypter{payloads: map[string]map[string]any{
		"env-empty": {"unrelated": "value"}, // no token-shaped key
	}}
	r := NewDBCredentialResolver(lookup, dec, nil)
	if _, err := r.Resolve(context.Background(), "G_EMPTY"); err == nil {
		t.Fatal("a discord_bot secret with no token value must error")
	}
}

// compile-time guard: the static fallback resolver still satisfies the contract.
var _ channel.CredentialResolver = NewStaticCredentialResolver("", "")
