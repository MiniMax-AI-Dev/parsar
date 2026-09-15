package store

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestMCPCredentialSelectionAndScopedDecryption(t *testing.T) {
	public, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := credentialcrypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	var vaults []Vault
	for _, owner := range []string{tenant, tenant, foreign} {
		vault, err := s.CreateVault(t.Context(), owner, CreateVaultInput{})
		if err != nil {
			t.Fatal(err)
		}
		vaults = append(vaults, vault)
	}
	token := " \t" + uuid.NewString() + "雪\n"
	destination := "https://mcp.example/tools"
	create := func(vault Vault) Credential {
		t.Helper()
		value, err := s.CreateStaticCredential(t.Context(), vault.TenantID, vault.ID, CreateStaticCredentialInput{Name: "private", MCPServerURL: destination, Token: token})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	first, foreignCredential := create(vaults[0]), create(vaults[2])
	attached := []string{vaults[0].ID, vaults[1].ID}
	requests := []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: destination}, {ServerLabel: "anonymous", ServerURL: "https://anonymous.example/mcp"}}
	bindings, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, requests)
	if err != nil || len(bindings) != 2 || bindings[0].CredentialID != first.ID || bindings[0].AuthType != "static_bearer" || bindings[1].CredentialID != "" {
		t.Fatal("metadata selection or frozen anonymous decision differs", err)
	}
	encoded, err := json.Marshal(bindings)
	if err != nil || bytes.Contains(encoded, []byte(token)) || strings.Contains(string(encoded), "ciphertext") {
		t.Fatal("private binding contains secret material")
	}
	second := create(vaults[1])
	if _, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, requests); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("ambiguous selection was admitted")
	}
	requests[0].CredentialID = &second.ID
	explicit, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, requests)
	if err != nil || explicit[0].CredentialID != second.ID || requests[1].CredentialID != nil {
		t.Fatal("explicit selection did not disambiguate", err)
	}
	for _, tc := range []struct {
		owner   string
		vaults  []string
		id, url string
	}{
		{tenant, attached, foreignCredential.ID, destination}, {foreign, attached, first.ID, destination},
		{tenant, []string{vaults[1].ID}, first.ID, destination}, {tenant, attached, first.ID, destination + "/other"},
		{tenant, []string{vaults[0].ID, vaults[2].ID}, first.ID, destination}, {tenant, []string{uuid.NewString()}, first.ID, destination},
	} {
		_, err := public.ResolveMCPCredentials(t.Context(), tc.owner, tc.vaults, []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: tc.url, CredentialID: &tc.id}})
		if !errors.Is(err, ErrNotFound) {
			t.Fatal("unowned, unattached or wrong-destination selection was admitted")
		}
	}
	pool.Close()
	public, pool = testStore(t)
	cipher, _ = credentialcrypto.New(bytes.Clone(key))
	s = NewWithCredentialCipher(pool, cipher)
	got, err := s.MCPBearerToken(t.Context(), tenant, attached, bindings[0])
	if err != nil || got != token {
		t.Fatal("frozen selection or opaque bytes changed across restart", err)
	}
	if got, err := public.MCPBearerToken(t.Context(), tenant, attached, bindings[0]); !errors.Is(err, ErrCredentialStorageUnavailable) || got != "" {
		t.Fatal("missing key did not fail execution closed")
	}
	key[0] ^= 1
	wrong, _ := credentialcrypto.New(key)
	if got, err := NewWithCredentialCipher(pool, wrong).MCPBearerToken(t.Context(), tenant, attached, bindings[0]); err == nil || got != "" || strings.Contains(err.Error(), token) {
		t.Fatal("wrong key leaked or decrypted a credential")
	}
	for _, mutate := range []func(*MCPCredentialBinding){
		func(b *MCPCredentialBinding) { b.VaultID = vaults[1].ID },
		func(b *MCPCredentialBinding) { b.CredentialID = foreignCredential.ID },
		func(b *MCPCredentialBinding) { b.ServerURL += "/other" },
		func(b *MCPCredentialBinding) { b.AuthType = "other" },
	} {
		binding := bindings[0]
		mutate(&binding)
		if got, err := s.MCPBearerToken(t.Context(), tenant, attached, binding); !errors.Is(err, ErrNotFound) || got != "" {
			t.Fatal("substituted frozen authorization was decrypted")
		}
	}
	for _, scope := range []struct {
		owner  string
		vaults []string
	}{{foreign, attached}, {tenant, []string{vaults[1].ID}}} {
		if got, err := s.MCPBearerToken(t.Context(), scope.owner, scope.vaults, bindings[0]); !errors.Is(err, ErrNotFound) || got != "" {
			t.Fatal("tenant or attachment authorization was bypassed")
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext, 15, get_byte(token_ciphertext,15) # 1) WHERE id=$1", first.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.MCPBearerToken(t.Context(), tenant, attached, bindings[0]); err == nil || got != "" {
		t.Fatal("tampered ciphertext decrypted")
	}
	if _, err := public.GetCredential(t.Context(), tenant, first.VaultID, first.ID); err != nil {
		t.Fatal("safe metadata lookup depended on ciphertext", err)
	}
}
