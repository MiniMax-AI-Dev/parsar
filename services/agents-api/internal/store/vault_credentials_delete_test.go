package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCredentialDeletionScopeBindingAndRestart(t *testing.T) {
	public, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := bytes.Repeat([]byte{41}, 32)
	cipher, err := credentialcrypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	vault, err := s.CreateVault(t.Context(), tenant, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := s.CreateVault(t.Context(), tenant, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name string) Credential {
		t.Helper()
		value, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, CreateStaticCredentialInput{Name: name, MCPServerURL: "https://mcp.example/tools", Token: name + "-secret"})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	original := create("original")
	attached := []string{vault.ID}
	selected, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: original.MCPServerURL}})
	if err != nil || len(selected) != 1 {
		t.Fatal("initial automatic selection failed", err)
	}
	configuration, _ := json.Marshal(map[string]any{"agent": map[string]string{"model": "model"}, "vault_ids": attached, "mcp_credentials": selected})
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "retained", Configuration: configuration}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := s.MCPBearerToken(t.Context(), tenant, attached, selected[0])
	if err != nil || retained != "original-secret" {
		t.Fatal("pre-delete dispatch lookup failed")
	}
	sibling := create("sibling")
	for _, scope := range []struct{ tenant, vault, id string }{
		{foreign, vault.ID, original.ID}, {tenant, wrong.ID, original.ID},
		{tenant, vault.ID, uuid.NewString()}, {tenant, "invalid", original.ID}, {tenant, vault.ID, "invalid"},
	} {
		if _, err := public.DeleteCredential(t.Context(), scope.tenant, scope.vault, scope.id); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign or invalid delete was accepted", err)
		}
	}
	// An actual database write failure must leave the resource and token intact.
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := *public
	readOnly.queries = public.queries.WithTx(tx)
	_, deletionErr := readOnly.DeleteCredential(t.Context(), tenant, vault.ID, original.ID)
	_ = tx.Rollback(t.Context())
	if deletionErr == nil || deletionErr.Error() != "credential deletion failed" {
		t.Fatal("failed mutation was accepted or exposed")
	}
	if value, err := public.GetCredential(t.Context(), tenant, vault.ID, original.ID); err != nil || !reflect.DeepEqual(value, original) {
		t.Fatal("rejected deletion changed the resource", err)
	}
	if token, err := s.MCPBearerToken(t.Context(), tenant, attached, selected[0]); err != nil || token != retained {
		t.Fatal("rejected deletion changed the stored token")
	}
	// Delete through a keyless Store, even if the stored payload is damaged.
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=decode('00','hex') WHERE id=$1", original.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := public.DeleteCredential(t.Context(), tenant, vault.ID, original.ID); err != nil || id != original.ID {
		t.Fatal("keyless deletion failed", err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM vault_credentials WHERE id=$1", original.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("deleted row or ciphertext remains")
	}
	pool.Close()
	public, pool = testStore(t)
	cipher, _ = credentialcrypto.New(bytes.Clone(key))
	s = NewWithCredentialCipher(pool, cipher)
	if _, err := public.DeleteCredential(t.Context(), tenant, vault.ID, original.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("repeat deletion did not stay absent")
	}
	if _, err := public.GetCredential(t.Context(), tenant, vault.ID, original.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted metadata reappeared after restart")
	}
	if _, err := s.UpdateStaticCredential(t.Context(), tenant, vault.ID, original.ID, UpdateStaticCredentialInput{Token: "replacement"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("replacement resurrected a deleted credential")
	}
	if _, err := s.MCPBearerToken(t.Context(), tenant, attached, selected[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("frozen selection fell back to another token")
	}
	if _, err := s.ResolveMCPCredentials(t.Context(), tenant, attached, []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: original.MCPServerURL, CredentialID: &original.ID}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted explicit selection was admitted")
	}
	page, err := public.ListCredentials(t.Context(), tenant, vault.ID, "", 100, true, []string{"active", "archived"})
	if err != nil || len(page.Credentials) != 1 || !reflect.DeepEqual(page.Credentials[0], sibling) {
		t.Fatal("deletion changed a sibling or list membership", err)
	}
	if value, err := public.GetVault(t.Context(), tenant, vault.ID); err != nil || !reflect.DeepEqual(value, vault) {
		t.Fatal("deletion changed its parent Vault")
	}
	if retry, err := public.CreateSession(t.Context(), tenant, input); err != nil || retry.ID != session.ID || !bytes.Equal(retry.Configuration, session.Configuration) {
		t.Fatal("deletion changed frozen creation identity", err)
	}
}

func TestCredentialDeletionConcurrentReplacementCannotResurrect(t *testing.T) {
	public, pool := testStore(t)
	tenant := uuid.NewString()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	vault, err := s.CreateVault(t.Context(), tenant, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		value, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, CreateStaticCredentialInput{Name: "competing", MCPServerURL: "https://mcp.example/tools", Token: "before"})
		if err != nil {
			t.Fatal(err)
		}
		start, updated := make(chan struct{}), make(chan error, 1)
		go func() {
			<-start
			_, err := s.UpdateStaticCredential(t.Context(), tenant, vault.ID, value.ID, UpdateStaticCredentialInput{Token: "after"})
			updated <- err
		}()
		close(start)
		_, deleted := public.DeleteCredential(t.Context(), tenant, vault.ID, value.ID)
		updateErr := <-updated
		if deleted != nil || updateErr != nil && !errors.Is(updateErr, ErrNotFound) {
			t.Fatal("competing update/delete failed unexpectedly", deleted, updateErr)
		}
		if _, err := public.GetCredential(t.Context(), tenant, vault.ID, value.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("concurrent update resurrected deleted metadata")
		}
	}
}
