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
	"github.com/jackc/pgx/v5/pgconn"
)

func TestVaultDeletionCascadeBindingAndRestart(t *testing.T) {
	public, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	key := bytes.Repeat([]byte{43}, 32)
	cipher, err := credentialcrypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	createVault := func() Vault {
		t.Helper()
		v, err := public.CreateVault(t.Context(), tenant, CreateVaultInput{})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	vault, retained, empty := createVault(), createVault(), createVault()
	create := func(id, name, url string) Credential {
		t.Helper()
		v, err := s.CreateStaticCredential(t.Context(), tenant, id, CreateStaticCredentialInput{Name: name, MCPServerURL: url, Token: name + "-secret"})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	original := create(vault.ID, "original", "https://mcp.example/tools")
	attached := []string{vault.ID, retained.ID}
	selected, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: original.MCPServerURL}})
	if err != nil || len(selected) != 1 {
		t.Fatal("initial unique selection failed", err)
	}
	configuration, _ := json.Marshal(map[string]any{"agent": map[string]string{"model": "model"}, "vault_ids": attached, "mcp_credentials": selected})
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "retained", Configuration: configuration}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	extra := create(vault.ID, "archived", "https://mcp.example/other")
	sibling := create(retained.ID, "sibling", original.MCPServerURL)
	if _, err := pool.Exec(t.Context(), "UPDATE vaults SET status='archived' WHERE id=$1", vault.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET status='archived' WHERE id=$1", extra.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []struct{ tenant, id string }{{foreign, vault.ID}, {tenant, uuid.NewString()}, {tenant, "invalid"}, {"invalid", vault.ID}} {
		if _, err := public.DeleteVault(t.Context(), scope.tenant, scope.id); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign or invalid deletion was accepted", err)
		}
	}
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	transactional := *public
	transactional.queries = public.queries.WithTx(tx)
	_, deletionErr := transactional.DeleteVault(t.Context(), tenant, vault.ID)
	_ = tx.Rollback(t.Context())
	if deletionErr == nil || deletionErr.Error() != "vault deletion failed" {
		t.Fatal("failed mutation was accepted or exposed")
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	transactional.queries = public.queries.WithTx(tx)
	if _, err := transactional.DeleteVault(t.Context(), tenant, vault.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("cascade was not visible in the deletion transaction", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, err := public.GetVault(t.Context(), tenant, vault.ID); err != nil || !reflect.DeepEqual(value, vault) {
		t.Fatal("rollback changed the Vault", err)
	}
	for _, expected := range []Credential{original, extra} {
		if value, err := public.GetCredential(t.Context(), tenant, vault.ID, expected.ID); err != nil || !reflect.DeepEqual(value, expected) {
			t.Fatal("rollback changed a child", err)
		}
	}
	if token, err := s.MCPBearerToken(t.Context(), tenant, attached, selected[0]); err != nil || token != "original-secret" {
		t.Fatal("rejected deletion changed the stored token")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=decode('00','hex') WHERE vault_id=$1", vault.ID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []Vault{empty, vault} {
		if id, err := public.DeleteVault(t.Context(), tenant, target.ID); err != nil || id != target.ID {
			t.Fatal("keyless deletion failed", err)
		}
	}
	if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("committed parent or encrypted children remain", err)
	}
	pool.Close()
	public, pool = testStore(t)
	cipher, _ = credentialcrypto.New(bytes.Clone(key))
	s = NewWithCredentialCipher(pool, cipher)
	for _, target := range []Vault{empty, vault} {
		if _, err := public.GetVault(t.Context(), tenant, target.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted Vault reappeared after restart")
		}
		if _, err := public.DeleteVault(t.Context(), tenant, target.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("repeated deletion did not remain absent")
		}
	}
	for _, child := range []Credential{original, extra} {
		if _, err := public.GetCredential(t.Context(), tenant, vault.ID, child.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted child reappeared")
		}
		if _, err := s.UpdateStaticCredential(t.Context(), tenant, vault.ID, child.ID, UpdateStaticCredentialInput{Token: "replacement"}); !errors.Is(err, ErrNotFound) {
			t.Fatal("replacement recreated a deleted child")
		}
	}
	if _, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, CreateStaticCredentialInput{Name: "late", MCPServerURL: original.MCPServerURL, Token: "late"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("new child was admitted under a deleted Vault")
	}
	if _, err := s.MCPBearerToken(t.Context(), tenant, attached, selected[0]); !errors.Is(err, ErrNotFound) {
		t.Fatal("frozen binding reselected a credential in another attached Vault")
	}
	if _, err := public.ListCredentials(t.Context(), tenant, vault.ID, "", 100, true, []string{"active", "archived"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted parent remained listable")
	}
	if value, err := public.GetVault(t.Context(), tenant, retained.ID); err != nil || !reflect.DeepEqual(value, retained) {
		t.Fatal("deletion changed another Vault")
	}
	if value, err := public.GetCredential(t.Context(), tenant, retained.ID, sibling.ID); err != nil || !reflect.DeepEqual(value, sibling) {
		t.Fatal("deletion changed another Vault's credential")
	}
	if retry, err := public.CreateSession(t.Context(), tenant, input); err != nil || retry.ID != session.ID || !bytes.Equal(retry.Configuration, session.Configuration) {
		t.Fatal("deletion changed frozen creation identity", err)
	}
}

func TestVaultDeletionConcurrentChildMutations(t *testing.T) {
	public, pool := testStore(t)
	tenant := uuid.NewString()
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{44}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	for range 8 {
		vault, err := s.CreateVault(t.Context(), tenant, CreateVaultInput{})
		if err != nil {
			t.Fatal(err)
		}
		input := CreateStaticCredentialInput{Name: "competing", MCPServerURL: "https://mcp.example/tools", Token: "before"}
		value, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		start, created, updated, removed := make(chan struct{}), make(chan error, 1), make(chan error, 1), make(chan error, 1)
		go func() {
			<-start
			_, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, input)
			created <- err
		}()
		go func() {
			<-start
			_, err := s.UpdateStaticCredential(t.Context(), tenant, vault.ID, value.ID, UpdateStaticCredentialInput{Token: "after"})
			updated <- err
		}()
		go func() {
			<-start
			_, err := public.DeleteCredential(t.Context(), tenant, vault.ID, value.ID)
			removed <- err
		}()
		close(start)
		_, deleted := public.DeleteVault(t.Context(), tenant, vault.ID)
		createErr, updateErr, removeErr := <-created, <-updated, <-removed
		var constraint *pgconn.PgError
		if createErr != nil && !errors.Is(createErr, ErrNotFound) && !(errors.As(createErr, &constraint) && constraint.Code == "23503") {
			t.Fatal("competing creation failed unexpectedly", createErr)
		}
		if deleted != nil || updateErr != nil && !errors.Is(updateErr, ErrNotFound) || removeErr != nil && !errors.Is(removeErr, ErrNotFound) {
			t.Fatal("competing mutation failed unexpectedly", deleted, updateErr, removeErr)
		}
		var count int
		if err := pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM vaults WHERE id=$1)+(SELECT count(*) FROM vault_credentials WHERE vault_id=$1)", vault.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("concurrent mutation resurrected deleted resources", err)
		}
	}
}
