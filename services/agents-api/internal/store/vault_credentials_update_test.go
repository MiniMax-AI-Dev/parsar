package store

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestStaticCredentialUpdatePreservesBindingsAndReplacesCurrentSecret(t *testing.T) {
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
	firstToken, endpoint := uuid.NewString(), "https://mcp.example/tools"
	original, err := s.CreateStaticCredential(t.Context(), tenant, vaults[0].ID, CreateStaticCredentialInput{Name: "Retained name", MCPServerURL: endpoint, Token: firstToken})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := s.CreateStaticCredential(t.Context(), tenant, vaults[1].ID, CreateStaticCredentialInput{Name: "Unrelated", MCPServerURL: endpoint, Token: firstToken})
	if err != nil {
		t.Fatal(err)
	}
	attached := []string{vaults[0].ID}
	var sessions []Session
	var bindings []MCPCredentialBinding
	for index, id := range []*string{nil, &original.ID} {
		selected, err := public.ResolveMCPCredentials(t.Context(), tenant, attached, []MCPCredentialRequest{{ServerLabel: "tools", ServerURL: endpoint, CredentialID: id}})
		if err != nil || len(selected) != 1 {
			t.Fatal("binding setup failed", err)
		}
		configuration, _ := json.Marshal(map[string]any{
			"agent":       map[string]any{"model": "model", "tools": []any{map[string]any{"type": "mcp", "server_label": "tools", "transport": map[string]string{"type": "http", "server_url": endpoint}, "connection_origin": "service", "credential_id": id}}},
			"environment": map[string]string{"type": "none"}, "vault_ids": attached, "mcp_credentials": selected,
		})
		session, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: []string{"implicit", "explicit"}[index], Configuration: configuration})
		if err != nil {
			t.Fatal(err)
		}
		sessions, bindings = append(sessions, session), append(bindings, selected[0])
	}
	readCiphertext := func(id string) []byte {
		t.Helper()
		var ciphertext []byte
		if err := pool.QueryRow(t.Context(), "SELECT token_ciphertext FROM vault_credentials WHERE id=$1", id).Scan(&ciphertext); err != nil {
			t.Fatal("private ciphertext observation failed")
		}
		return ciphertext
	}
	prior, unrelatedCiphertext := readCiphertext(original.ID), readCiphertext(unrelated.ID)
	lastToken := " \t" + uuid.NewString() + " 雪\n"
	for _, token := range []string{"", lastToken, lastToken} {
		updated, err := s.UpdateStaticCredential(t.Context(), tenant, original.VaultID, original.ID, UpdateStaticCredentialInput{Token: token})
		if err != nil {
			t.Fatal("token replacement failed")
		}
		want := original
		want.UpdatedAt = updated.UpdatedAt
		if !reflect.DeepEqual(updated, want) || updated.UpdatedAt.Before(original.UpdatedAt) {
			t.Fatal("replacement changed immutable metadata")
		}
		current := readCiphertext(original.ID)
		if bytes.Equal(prior, current) || len(token) > 0 && bytes.Contains(current, []byte(token)) {
			t.Fatal("replacement reused ciphertext or stored plaintext")
		}
		for _, binding := range bindings {
			got, err := s.MCPBearerToken(t.Context(), tenant, attached, binding)
			if err != nil || got != token {
				t.Fatal("existing selection did not read the exact committed replacement")
			}
		}
		prior = current
	}
	before, err := public.GetCredential(t.Context(), tenant, original.VaultID, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertUnchanged := func() {
		t.Helper()
		after, err := public.GetCredential(t.Context(), tenant, original.VaultID, original.ID)
		if err != nil || !reflect.DeepEqual(after, before) || !bytes.Equal(readCiphertext(original.ID), prior) {
			t.Fatal("failed replacement changed the existing row")
		}
	}
	for _, scope := range []struct{ tenant, vault, id string }{
		{foreign, original.VaultID, original.ID}, {tenant, vaults[1].ID, original.ID},
		{tenant, vaults[2].ID, original.ID}, {tenant, original.VaultID, uuid.NewString()}, {tenant, "invalid", original.ID},
	} {
		if _, err := s.UpdateStaticCredential(t.Context(), scope.tenant, scope.vault, scope.id, UpdateStaticCredentialInput{Token: "rejected"}); !errors.Is(err, ErrNotFound) {
			t.Fatal("unowned or invalid replacement was admitted")
		}
		assertUnchanged()
	}
	for _, writer := range []*Store{public, NewWithCredentialCipher(pool, &credentialcrypto.Cipher{})} {
		if _, err := writer.UpdateStaticCredential(t.Context(), tenant, original.VaultID, original.ID, UpdateStaticCredentialInput{Token: "rejected"}); err == nil {
			t.Fatal("missing or unusable cipher admitted replacement")
		}
		assertUnchanged()
	}
	// A real PostgreSQL mutation failure must preserve both ciphertext and time.
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	readOnly := *s
	readOnly.queries = s.queries.WithTx(tx)
	_, updateErr := readOnly.UpdateStaticCredential(t.Context(), tenant, original.VaultID, original.ID, UpdateStaticCredentialInput{Token: "rejected"})
	_ = tx.Rollback(t.Context())
	if updateErr == nil || updateErr.Error() != "credential update failed" {
		t.Fatal("database write failure was accepted or exposed")
	}
	assertUnchanged()
	// A stale destination from a prior metadata read cannot authorize the UPDATE.
	tenantID, _ := parseID(tenant)
	vaultID, _ := parseID(original.VaultID)
	credentialID, _ := parseID(original.ID)
	_, err = s.queries.UpdateStaticCredential(t.Context(), sqlc.UpdateStaticCredentialParams{TenantID: tenantID, VaultID: vaultID, ID: credentialID, McpServerUrl: endpoint + "/other", TokenCiphertext: prior})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("mutation failed to recheck immutable destination")
	}
	assertUnchanged()
	// Replacing a damaged old payload needs no old-token decryption.
	if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext, 15, get_byte(token_ciphertext,15) # 1) WHERE id=$1", original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateStaticCredential(t.Context(), tenant, original.VaultID, original.ID, UpdateStaticCredentialInput{Token: lastToken}); err != nil {
		t.Fatal("replacement tried to decrypt the old token")
	}
	pool.Close()
	public, pool = testStore(t)
	cipher, _ = credentialcrypto.New(bytes.Clone(key))
	s = NewWithCredentialCipher(pool, cipher)
	for index, session := range sessions {
		got, err := s.GetSession(t.Context(), tenant, session.ID)
		if err != nil || !bytes.Equal(got.Configuration, session.Configuration) || got.LastTurn != nil {
			t.Fatal("replacement changed an existing Session")
		}
		current, err := s.MCPBearerToken(t.Context(), tenant, attached, bindings[index])
		if err != nil || current != lastToken {
			t.Fatal("reopened dispatch lookup lost the replacement")
		}
	}
	// Competing whole-secret replacements may win in either order, never tear.
	left, right := uuid.NewString()+strings.Repeat("L", 513), uuid.NewString()+strings.Repeat("R", 1025)
	start, results := make(chan struct{}), make(chan error, 2)
	for _, token := range []string{left, right} {
		go func() {
			<-start
			_, err := s.UpdateStaticCredential(t.Context(), tenant, original.VaultID, original.ID, UpdateStaticCredentialInput{Token: token})
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent replacement failed")
		}
	}
	current, err := s.MCPBearerToken(t.Context(), tenant, attached, bindings[0])
	if err != nil || current != left && current != right {
		t.Fatal("concurrent replacements produced an incomplete secret")
	}
	if !bytes.Equal(readCiphertext(unrelated.ID), unrelatedCiphertext) {
		t.Fatal("replacement changed an unrelated Credential")
	}
}
