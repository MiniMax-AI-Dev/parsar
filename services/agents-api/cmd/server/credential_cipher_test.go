package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
)

func TestCredentialCipherConfiguration(t *testing.T) {
	t.Setenv("AGENTS_API_CREDENTIAL_KEY_FILE", "")
	t.Setenv("PARSAR_MASTER_KEY", "must-not-be-used")
	if c, err := credentialCipher(); c != nil || err != nil {
		t.Fatal("absent dedicated key must remain disabled", err)
	}
	path := filepath.Join(t.TempDir(), "credential.key")
	t.Setenv("AGENTS_API_CREDENTIAL_KEY_FILE", path)
	if _, err := credentialCipher(); err == nil {
		t.Fatal("missing configured file accepted")
	}
	for _, content := range []string{"", "private-invalid-key", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := credentialCipher(); err == nil || strings.Contains(err.Error(), "private-invalid-key") {
			t.Fatal("invalid configuration accepted or leaked")
		}
	}
	key := bytes.Repeat([]byte{0x91}, 32)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := credentialCipher()
	if err != nil {
		t.Fatal(err)
	}
	binding := credentialcrypto.Binding{TenantID: "tenant", VaultID: "vault", CredentialID: "credential", AuthType: "static_bearer", Destination: "https://example.invalid/mcp"}
	sealed, err := first.Seal([]byte("opaque storage test"), binding)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := credentialCipher()
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Open(sealed, binding)
	if err != nil || string(got) != "opaque storage test" {
		t.Fatal("persisted key did not recover ciphertext", err)
	}
}
