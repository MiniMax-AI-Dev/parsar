// Package runtimecrypto implements envelope-encryption for shipping
// sensitive payloads (model API keys, per-run secrets) from the
// Parsar server to a paired Agent Daemon without putting plaintext
// on the wire.
//
// Algorithm: NaCl sealed box (X25519 + XSalsa20-Poly1305) via
// box.SealAnonymous. Nonce = BLAKE2b-24(ephPub || recipientPub).
// Wire format: base64(stdEncoding) so it travels safely inside JSON.
// The committed static fixture (testdata/wire_v1.json) keeps the wire
// format locked across refactors.
//
// Threat model:
//   - Server logs / debug dumps / DB middlewares see ciphertext only.
//   - Daemon-machine compromise is OUT of scope (private key lives
//     there and the host is trusted by definition).
//   - Server compromise is OUT of scope (a malicious server could
//     skip encryption entirely).
package runtimecrypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/nacl/box"
)

// PublicKeySize / PrivateKeySize match nacl/box (32 bytes Curve25519).
const (
	PublicKeySize  = 32
	PrivateKeySize = 32
)

// ErrInvalidPublicKey wraps any reason the recipient public key is
// unusable (wrong length, bad base64). API layer returns 400.
var ErrInvalidPublicKey = errors.New("runtime crypto: invalid public key")

// ErrDecryptFailed is returned when ciphertext fails authentication
// (tampered, wrong recipient key, malformed envelope). Generic by
// design — exposing the specific reason helps attackers.
var ErrDecryptFailed = errors.New("runtime crypto: decrypt failed")

// SealForRuntime encrypts plaintext for the runtime identified by its
// base64-encoded X25519 public key (as stored in
// runtimes.config.runner_public_key).
//
// Output is base64(stdEncoding) of an anonymous sealed box: each call
// generates a fresh ephemeral keypair and embeds the public half in
// the envelope. Receiver derives the shared key from that and its own
// private key.
func SealForRuntime(plaintext []byte, runnerPublicKeyB64 string) (string, error) {
	pub, err := decodePublicKey(runnerPublicKeyB64)
	if err != nil {
		return "", err
	}
	sealed, err := box.SealAnonymous(nil, plaintext, &pub, rand.Reader)
	if err != nil {
		return "", fmt.Errorf("runtime crypto: seal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// OpenSeal decrypts a SealForRuntime output using the recipient's
// keypair. Kept here for round-trip tests and to lock the wire format
// down in one place.
func OpenSeal(cipherB64 string, publicKey, privateKey [32]byte) ([]byte, error) {
	sealed, err := base64.StdEncoding.DecodeString(cipherB64)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	out, ok := box.OpenAnonymous(nil, sealed, &publicKey, &privateKey)
	if !ok {
		return nil, ErrDecryptFailed
	}
	return out, nil
}

// GenerateRuntimeKeypair returns a fresh (publicKey, privateKey) pair
// as base64. Caller MUST persist the private key with mode 0600 and
// never log it.
func GenerateRuntimeKeypair() (publicKeyB64, privateKeyB64 string, err error) {
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("runtime crypto: keygen: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub[:]),
		base64.StdEncoding.EncodeToString(priv[:]),
		nil
}

// DecodeKey turns a base64-encoded 32-byte key into a [32]byte so
// daemon code and the API layer share one parser instead of inlining
// base64 + length checks at every caller.
func DecodeKey(b64 string) ([32]byte, error) {
	var out [32]byte
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return out, ErrInvalidPublicKey
	}
	if len(raw) != PublicKeySize {
		return out, ErrInvalidPublicKey
	}
	copy(out[:], raw)
	return out, nil
}

func decodePublicKey(b64 string) ([32]byte, error) {
	return DecodeKey(b64)
}
