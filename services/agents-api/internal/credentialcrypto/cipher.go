// Package credentialcrypto encrypts execution-service credentials at rest.
package credentialcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

const (
	formatVersion byte = 1
	bindingDomain      = "parsar.agents-api.credential"
)

var (
	errInvalidKey        = errors.New("credentialcrypto: invalid encryption key")
	errUnavailable       = errors.New("credentialcrypto: cipher unavailable")
	errInvalidBinding    = errors.New("credentialcrypto: invalid binding")
	errInvalidCiphertext = errors.New("credentialcrypto: invalid ciphertext")
)

// Binding authenticates the credential's owner, identity, purpose and destination.
type Binding struct {
	TenantID     string `json:"tenant_id"`
	VaultID      string `json:"vault_id"`
	CredentialID string `json:"credential_id"`
	AuthType     string `json:"auth_type"`
	Destination  string `json:"destination"`
}

type Cipher struct {
	aead cipher.AEAD
}

// New requires a 32-byte AES key. The operator must limit each key to at most
// 2^32 encryptions, as required by the standard library's random-nonce GCM.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errInvalidKey
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, errUnavailable
	}
	return &Cipher{aead: aead}, nil
}

// Seal returns version || nonce || ciphertext || tag. The AEAD generates and
// prefixes its own nonce; the caller supplies no nonce or mutable output buffer.
func (c *Cipher) Seal(plaintext []byte, b Binding) ([]byte, error) {
	aad, err := additionalData(b)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) seal(plaintext, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errUnavailable
	}
	return c.aead.Seal([]byte{formatVersion}, nil, plaintext, aad), nil
}

// Open returns plaintext only after the ciphertext and complete binding authenticate.
func (c *Cipher) Open(ciphertext []byte, b Binding) ([]byte, error) {
	aad, err := additionalData(b)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}

func (c *Cipher) open(ciphertext, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errUnavailable
	}
	if len(ciphertext) < 1+c.aead.Overhead() || ciphertext[0] != formatVersion {
		return nil, errInvalidCiphertext
	}
	plaintext, err := c.aead.Open(nil, nil, ciphertext[1:], aad)
	if err != nil {
		return nil, errInvalidCiphertext
	}
	return plaintext, nil
}

func additionalData(b Binding) ([]byte, error) {
	for _, value := range []string{b.TenantID, b.VaultID, b.CredentialID, b.AuthType, b.Destination} {
		// JSON replaces invalid UTF-8. Reject it so distinct binding bytes cannot
		// collapse to the same authenticated encoding. Plaintext stays opaque.
		if value == "" || !utf8.ValidString(value) {
			return nil, errInvalidBinding
		}
	}
	aad, err := json.Marshal(struct {
		Domain  string  `json:"domain"`
		Version byte    `json:"version"`
		Binding Binding `json:"binding"`
	}{Domain: bindingDomain, Version: formatVersion, Binding: b})
	if err != nil {
		return nil, errInvalidBinding
	}
	return aad, nil
}
