package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestStaticCredentialsPersistEncryptedAndRemainScoped(t *testing.T) {
	withoutKey, pool := testStore(t)
	ctx := t.Context()
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	var vaults []Vault
	for _, owner := range []string{tenant, tenant, foreignTenant} {
		vault, err := withoutKey.CreateVault(ctx, owner, CreateVaultInput{})
		if err != nil {
			t.Fatal(err)
		}
		vaults = append(vaults, vault)
	}
	key, randomToken := make([]byte, 32), make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(randomToken); err != nil {
		t.Fatal(err)
	}
	newCipher := func(key []byte) *credentialcrypto.Cipher {
		c, err := credentialcrypto.New(key)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	s := NewWithCredentialCipher(pool, newCipher(key))
	canary := hex.EncodeToString(randomToken)
	opaque := " \t" + canary + " 凭据\n" + strings.Repeat("x", 300) + " "
	tokens := []string{opaque, opaque, ""}
	var records []Credential
	before := time.Now().Add(-time.Second)
	for _, token := range tokens {
		input := CreateStaticCredentialInput{Name: "MCP credential", MCPServerURL: "https://mcp.example/tools", Token: token}
		record, err := s.CreateStaticCredential(ctx, tenant, vaults[0].ID, input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(record.ID); err != nil || record.VaultID != vaults[0].ID || record.Name != input.Name || record.AuthType != "static_bearer" || record.MCPServerURL != input.MCPServerURL || record.CreatedAt.Before(before) || record.CreatedAt.After(time.Now().Add(time.Second)) || !record.CreatedAt.Equal(record.UpdatedAt) {
			t.Fatal("credential metadata or database timestamps differ")
		}
		metadata, err := json.Marshal(record)
		if err != nil || bytes.Contains(metadata, []byte(canary)) {
			t.Fatal("credential metadata contains token plaintext")
		}
		records = append(records, record)
	}
	if records[0].ID == records[1].ID {
		t.Fatal("separate creates reused a credential identity")
	}
	valid := CreateStaticCredentialInput{Name: "Rejected", MCPServerURL: "https://mcp.example/tools", Token: opaque}
	if _, err := withoutKey.CreateStaticCredential(ctx, tenant, vaults[0].ID, valid); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("missing encryption key did not fail writes closed")
	}
	for _, target := range []struct{ tenant, vault string }{{tenant, vaults[2].ID}, {foreignTenant, vaults[0].ID}, {tenant, uuid.NewString()}} {
		if _, err := s.CreateStaticCredential(ctx, target.tenant, target.vault, valid); !errors.Is(err, ErrNotFound) {
			t.Fatal("creation admitted an unowned or missing Vault")
		}
	}
	for _, target := range []struct{ tenant, vault, credential string }{
		{tenant, vaults[1].ID, records[0].ID}, {foreignTenant, vaults[0].ID, records[0].ID},
		{tenant, vaults[2].ID, records[0].ID}, {tenant, uuid.NewString(), records[0].ID}, {tenant, vaults[0].ID, uuid.NewString()},
	} {
		if _, err := s.GetCredential(ctx, target.tenant, target.vault, target.credential); !errors.Is(err, ErrNotFound) {
			t.Fatal("unowned, wrong-Vault or missing credential was disclosed")
		}
	}
	pool.Close()
	withoutKey, pool = testStore(t)
	restartedCipher := newCipher(bytes.Clone(key))
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 1
	wrongCipher := newCipher(wrongKey)
	s = NewWithCredentialCipher(pool, restartedCipher)
	var ciphertexts [][]byte
	for i, record := range records {
		for _, reader := range []*Store{withoutKey, s, NewWithCredentialCipher(pool, wrongCipher)} {
			got, err := reader.GetCredential(ctx, tenant, record.VaultID, record.ID)
			if err != nil || !reflect.DeepEqual(got, record) {
				t.Fatal("safe metadata recovery depended on the encryption key", err)
			}
		}
		var ciphertext []byte
		var storageType string
		if err := pool.QueryRow(ctx, "SELECT token_ciphertext, pg_typeof(token_ciphertext)::text FROM vault_credentials WHERE id=$1 AND vault_id=$2", record.ID, record.VaultID).Scan(&ciphertext, &storageType); err != nil || storageType != "bytea" || len(ciphertext) < 29 || bytes.Contains(ciphertext, []byte(canary)) {
			t.Fatal("credential ciphertext was not stored as private bytea", err)
		}
		binding := credentialcrypto.Binding{TenantID: tenant, VaultID: record.VaultID, CredentialID: record.ID, AuthType: record.AuthType, Destination: record.MCPServerURL}
		plaintext, err := restartedCipher.Open(ciphertext, binding)
		if err != nil || !bytes.Equal(plaintext, []byte(tokens[i])) {
			t.Fatal("private restart decryption did not preserve token bytes", err)
		}
		if plaintext, err := wrongCipher.Open(ciphertext, binding); err == nil || plaintext != nil {
			t.Fatal("wrong key decrypted persisted ciphertext")
		}
		ciphertexts = append(ciphertexts, ciphertext)
	}
	// Version 1 prefixes the standard library's 12-byte random nonce.
	if bytes.Equal(ciphertexts[0], ciphertexts[1]) || bytes.Equal(ciphertexts[0][1:13], ciphertexts[1][1:13]) {
		t.Fatal("same token in distinct records reused ciphertext or nonce")
	}
	// Change one persisted binding field. Metadata GET must still work without a
	// key, while private decryption must reject the altered stored destination.
	if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET mcp_server_url=$1 WHERE id=$2", "https://other.example/tools", records[0].ID); err != nil {
		t.Fatal(err)
	}
	changed, err := withoutKey.GetCredential(ctx, tenant, vaults[0].ID, records[0].ID)
	if err != nil || changed.MCPServerURL != "https://other.example/tools" {
		t.Fatal("metadata GET unexpectedly required decryption", err)
	}
	binding := credentialcrypto.Binding{TenantID: tenant, VaultID: changed.VaultID, CredentialID: changed.ID, AuthType: changed.AuthType, Destination: changed.MCPServerURL}
	if plaintext, err := restartedCipher.Open(ciphertexts[0], binding); err == nil || plaintext != nil {
		t.Fatal("persisted destination substitution authenticated")
	}
	if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET mcp_server_url=$1 WHERE id=$2", records[0].MCPServerURL, records[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, vault := range vaults {
		got, err := withoutKey.GetVault(ctx, vault.TenantID, vault.ID)
		if err != nil || !reflect.DeepEqual(got, vault) {
			t.Fatal("credential operations changed an owning Vault", err)
		}
	}
	var count, sessions int
	if err := pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM vault_credentials WHERE vault_id=ANY($1::uuid[])), (SELECT count(*) FROM sessions WHERE tenant_id=ANY($2::uuid[]))", []string{vaults[0].ID, vaults[1].ID, vaults[2].ID}, []string{tenant, foreignTenant}).Scan(&count, &sessions); err != nil || count != len(records) || sessions != 0 {
		t.Fatal("rejected requests wrote rows or credential operations created Sessions", err)
	}
}
