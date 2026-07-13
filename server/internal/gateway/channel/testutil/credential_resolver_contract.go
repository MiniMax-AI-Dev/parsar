package testutil

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

type CredentialSecret struct {
	AppID            string
	EncryptedPayload []byte
}

type CredentialResolverContract struct {
	Platform      string
	Tenant        string
	UnknownTenant string
	NoAppTenant   string
	BadTenant     string
	EmptyTenant   string
	ResolvedToken string
	FallbackToken string
	NewResolver   func(map[string]CredentialSecret, *FakeDecrypter, channel.CredentialResolver) (channel.CredentialResolver, func() string)
	NewFallback   func(string, string) channel.CredentialResolver
	MissingToken  error
}

type FakeDecrypter struct {
	Payloads   map[string]map[string]any
	DecryptErr error
}

func (f *FakeDecrypter) Decrypt(envelope []byte) (map[string]any, error) {
	if f.DecryptErr != nil {
		return nil, f.DecryptErr
	}
	if payload, ok := f.Payloads[string(envelope)]; ok {
		return payload, nil
	}
	return map[string]any{}, nil
}

func RunCredentialResolverContract(t *testing.T, contract CredentialResolverContract) {
	t.Helper()

	t.Run("resolves tenant token", func(t *testing.T) {
		resolver, asked := contract.NewResolver(map[string]CredentialSecret{
			contract.Tenant: {AppID: "A_ACME", EncryptedPayload: []byte("env-acme")},
		}, &FakeDecrypter{Payloads: map[string]map[string]any{
			"env-acme": {"token": contract.ResolvedToken},
		}}, nil)

		credential, err := resolver.Resolve(context.Background(), contract.Tenant)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		assertLookupKey(t, asked, contract.Tenant)
		if credential.AppSecret != contract.ResolvedToken {
			t.Errorf("token = %q, want %q", credential.AppSecret, contract.ResolvedToken)
		}
		if credential.AppID != "A_ACME" {
			t.Errorf("app id = %q, want A_ACME", credential.AppID)
		}
	})

	t.Run("app id falls back to tenant", func(t *testing.T) {
		resolver, asked := contract.NewResolver(map[string]CredentialSecret{
			contract.NoAppTenant: {EncryptedPayload: []byte("env-noapp")},
		}, &FakeDecrypter{Payloads: map[string]map[string]any{
			"env-noapp": {"access_token": "token-noapp"},
		}}, nil)

		credential, err := resolver.Resolve(context.Background(), contract.NoAppTenant)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		assertLookupKey(t, asked, contract.NoAppTenant)
		if credential.AppID != contract.NoAppTenant {
			t.Errorf("app id = %q, want tenant id %q", credential.AppID, contract.NoAppTenant)
		}
		if credential.AppSecret != "token-noapp" {
			t.Errorf("token = %q, want token-noapp", credential.AppSecret)
		}
	})

	t.Run("unknown tenant falls back to static", func(t *testing.T) {
		resolver, asked := contract.NewResolver(nil, &FakeDecrypter{}, contract.NewFallback("A_ENV", contract.FallbackToken))
		credential, err := resolver.Resolve(context.Background(), contract.UnknownTenant)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		assertLookupKey(t, asked, contract.UnknownTenant)
		if credential.AppSecret != contract.FallbackToken {
			t.Errorf("token = %q, want fallback %q", credential.AppSecret, contract.FallbackToken)
		}
	})

	t.Run("empty tenant uses fallback without lookup", func(t *testing.T) {
		resolver, asked := contract.NewResolver(nil, &FakeDecrypter{}, contract.NewFallback("A_ENV", contract.FallbackToken))
		credential, err := resolver.Resolve(context.Background(), "")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		assertLookupKey(t, asked, "")
		if credential.AppSecret != contract.FallbackToken {
			t.Errorf("token = %q, want fallback %q", credential.AppSecret, contract.FallbackToken)
		}
	})

	t.Run("empty tenant without fallback errors", func(t *testing.T) {
		resolver, asked := contract.NewResolver(nil, &FakeDecrypter{}, nil)
		if _, err := resolver.Resolve(context.Background(), ""); !errors.Is(err, contract.MissingToken) {
			t.Fatalf("Resolve(empty, nil fallback) err = %v, want %v", err, contract.MissingToken)
		}
		assertLookupKey(t, asked, "")
	})

	t.Run("decrypt failure surfaces", func(t *testing.T) {
		resolver, asked := contract.NewResolver(map[string]CredentialSecret{
			contract.BadTenant: {EncryptedPayload: []byte("corrupt")},
		}, &FakeDecrypter{DecryptErr: errors.New("gcm: message authentication failed")}, contract.NewFallback("A", "must-not-be-used"))
		if _, err := resolver.Resolve(context.Background(), contract.BadTenant); err == nil {
			t.Fatal("Resolve must surface a decrypt failure, not fall back")
		}
		assertLookupKey(t, asked, contract.BadTenant)
	})

	t.Run("empty token value errors", func(t *testing.T) {
		resolver, asked := contract.NewResolver(map[string]CredentialSecret{
			contract.EmptyTenant: {EncryptedPayload: []byte("env-empty")},
		}, &FakeDecrypter{Payloads: map[string]map[string]any{
			"env-empty": {"unrelated": "value"},
		}}, nil)
		if _, err := resolver.Resolve(context.Background(), contract.EmptyTenant); err == nil {
			t.Fatalf("a %s bot secret with no token value must error", contract.Platform)
		}
		assertLookupKey(t, asked, contract.EmptyTenant)
	})
}

func assertLookupKey(t *testing.T, asked func() string, want string) {
	t.Helper()
	if got := asked(); got != want {
		t.Errorf("lookup key = %q, want %q", got, want)
	}
}
