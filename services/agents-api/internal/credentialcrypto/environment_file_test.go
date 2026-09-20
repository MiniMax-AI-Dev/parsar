package credentialcrypto

import (
	"bytes"
	"testing"
)

func TestEnvironmentFileCipherOwnershipAndPurpose(t *testing.T) {
	cipher, err := New(bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	bound := EnvironmentFileBinding{TenantID: "tenant", Resource: "session", OwnerID: "session", FileID: "file"}
	plain := []byte("confidential-file-canary\x00\xff")
	sealed, err := cipher.SealEnvironmentFile(plain, bound)
	if err != nil || bytes.Contains(sealed, plain) {
		t.Fatal("invalid confidential encoding", err)
	}
	got, err := cipher.OpenEnvironmentFile(sealed, bound)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatal("roundtrip", err)
	}
	for _, change := range []func(*EnvironmentFileBinding){
		func(b *EnvironmentFileBinding) { b.TenantID = "foreign" },
		func(b *EnvironmentFileBinding) { b.Resource = "environment_template" },
		func(b *EnvironmentFileBinding) { b.OwnerID = "other-session" },
		func(b *EnvironmentFileBinding) { b.FileID = "other-file" },
	} {
		foreign := bound
		change(&foreign)
		if _, err := cipher.OpenEnvironmentFile(sealed, foreign); err == nil {
			t.Fatal("accepted different file owner")
		}
	}
	if _, err := cipher.Open(sealed, Binding{"tenant", "session", "file", "static_bearer", "destination"}); err == nil {
		t.Fatal("accepted file as Vault credential")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := cipher.OpenEnvironmentFile(sealed, bound); err == nil {
		t.Fatal("accepted corrupt file")
	}
}
