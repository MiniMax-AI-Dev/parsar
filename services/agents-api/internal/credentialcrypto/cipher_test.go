package credentialcrypto

import (
	"bytes"
	"testing"
)

func testBinding() Binding {
	return Binding{
		TenantID: "tenant-a", VaultID: "vault-a", CredentialID: "credential-a",
		AuthType: "static_bearer", Destination: "https://mcp.example/tools",
	}
}

func testCipher(t *testing.T, key []byte) *Cipher {
	t.Helper()
	c, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCipherRoundTripAcrossInstancesAndFreshness(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	c := testCipher(t, key)
	reopened := testCipher(t, bytes.Clone(key))
	for _, plaintext := range [][]byte{nil, {}, []byte("  token\n"), {0, 0xff, 0xc3, 0x28, 0x80, 0}} {
		original := bytes.Clone(plaintext)
		first, err := c.Seal(plaintext, testBinding())
		if err != nil {
			t.Fatal(err)
		}
		second, err := c.Seal(plaintext, testBinding())
		if err != nil || bytes.Equal(first, second) {
			t.Fatal("repeated encryption did not produce fresh ciphertext")
		}
		if len(first) != 1+28+len(plaintext) || first[0] != 1 {
			t.Fatal("unexpected ciphertext format")
		}
		for _, encrypted := range [][]byte{first, second} {
			copyOfCiphertext := bytes.Clone(encrypted)
			got, err := reopened.Open(encrypted, testBinding())
			if err != nil || !bytes.Equal(got, original) {
				t.Fatal("new cipher instance did not recover exact plaintext bytes", err)
			}
			if !bytes.Equal(encrypted, copyOfCiphertext) || !bytes.Equal(plaintext, original) {
				t.Fatal("encryption or decryption modified caller-owned input")
			}
		}
	}
}

func TestCipherRejectsWrongKeyAndModifiedCiphertext(t *testing.T) {
	c := testCipher(t, bytes.Repeat([]byte{0x42}, 32))
	sealed, err := c.Seal(bytes.Repeat([]byte("opaque-token"), 4), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := testCipher(t, bytes.Repeat([]byte{0x24}, 32))
	if got, err := wrongKey.Open(sealed, testBinding()); err == nil || got != nil {
		t.Fatal("wrong key returned plaintext")
	}
	for _, offset := range []int{0, 1, 13, len(sealed) - 1} {
		changed := bytes.Clone(sealed)
		changed[offset] ^= 0x80
		if got, err := c.Open(changed, testBinding()); err == nil || got != nil {
			t.Fatalf("modified version/nonce/payload/tag at offset %d accepted", offset)
		}
	}
	for _, truncated := range [][]byte{nil, {}, sealed[:1], sealed[:28], sealed[:len(sealed)-1]} {
		if got, err := c.Open(truncated, testBinding()); err == nil || got != nil {
			t.Fatal("truncated ciphertext returned plaintext")
		}
	}
}

func TestCipherAuthenticatesEveryBindingField(t *testing.T) {
	c := testCipher(t, bytes.Repeat([]byte{0x42}, 32))
	sealed, err := c.Seal([]byte("synthetic-private-token"), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tenant", "vault", "credential", "auth", "destination"} {
		t.Run(field, func(t *testing.T) {
			changed := testBinding()
			switch field {
			case "tenant":
				changed.TenantID = "another-tenant"
			case "vault":
				changed.VaultID = "another-vault"
			case "credential":
				changed.CredentialID = "another-credential"
			case "auth":
				changed.AuthType = "another-purpose"
			case "destination":
				changed.Destination = "https://other.example/tools"
			}
			if got, err := c.Open(sealed, changed); err == nil || got != nil {
				t.Fatal("substituted binding returned plaintext")
			}
		})
	}
	first := testBinding()
	first.TenantID, first.VaultID = "a", "bc"
	sealed, err = c.Seal([]byte("synthetic-private-token"), first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.TenantID, second.VaultID = "ab", "c"
	if got, err := c.Open(sealed, second); err == nil || got != nil {
		t.Fatal("ambiguous concatenation of binding fields authenticated")
	}
}

func TestCipherRejectsInvalidConstructionAndBinding(t *testing.T) {
	for _, length := range []int{0, 16, 24, 31, 33} {
		if c, err := New(bytes.Repeat([]byte{0x42}, length)); err == nil || c != nil {
			t.Fatal("incorrect AES key size accepted")
		}
	}
	for _, c := range []*Cipher{nil, {}} {
		if got, err := c.Seal([]byte("synthetic-private-token"), testBinding()); err == nil || got != nil {
			t.Fatal("unavailable cipher accepted encryption")
		}
		if got, err := c.Open([]byte("synthetic-ciphertext"), testBinding()); err == nil || got != nil {
			t.Fatal("unavailable cipher accepted decryption")
		}
	}
	c := testCipher(t, bytes.Repeat([]byte{0x42}, 32))
	sealed, err := c.Seal([]byte("synthetic-private-token"), testBinding())
	if err != nil {
		t.Fatal(err)
	}
	for index := range 5 {
		for _, invalid := range []string{"", string([]byte{0xff})} {
			binding := testBinding()
			fields := []*string{&binding.TenantID, &binding.VaultID, &binding.CredentialID, &binding.AuthType, &binding.Destination}
			*fields[index] = invalid
			if got, err := c.Seal([]byte("synthetic-private-token"), binding); err == nil || got != nil {
				t.Fatal("invalid binding accepted for encryption")
			}
			if got, err := c.Open(sealed, binding); err == nil || got != nil {
				t.Fatal("invalid binding accepted for decryption")
			}
		}
	}
}
