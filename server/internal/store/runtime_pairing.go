package store

// Pairing token mint + hash for Local Runtime registration. Only the
// sha256 hash lives in DB (runtimes.pairing_token_hash); on pair
// handshake we look up by hash and clear it, so a leaked token is
// useless once consumed.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// RuntimePairingTokenTTL is the default lifetime applied when callers
// don't override.
const RuntimePairingTokenTTL = 15 * time.Minute

// RuntimePairingTokenPrefix is a human-recognizable marker so a leaked
// token is obvious in logs / chat.
const RuntimePairingTokenPrefix = "rtk_"

// RuntimeCredentialPrefix marks the long-lived bearer the runner
// presents on every post-pair request. Different prefix from
// RuntimePairingTokenPrefix so logs can distinguish leaked secrets at
// a glance.
const RuntimeCredentialPrefix = "rtc_"

// MintRuntimePairingToken returns a fresh (plaintext, hash) pair. The
// plaintext MUST be returned to the API caller exactly once and never
// persisted.
//
// Error is non-nil only on crypto/rand failure — callers should
// propagate as a 500, not retry.
func MintRuntimePairingToken() (plaintext, hash string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("runtime pairing token: rand: %w", err)
	}
	body := hex.EncodeToString(buf[:])
	plaintext = RuntimePairingTokenPrefix + body
	hash = HashRuntimePairingToken(plaintext)
	return plaintext, hash, nil
}

// HashRuntimePairingToken returns the sha256 hex of the supplied
// plaintext. Whitespace is trimmed because operators paste tokens
// through chat clients that occasionally append a newline.
func HashRuntimePairingToken(plaintext string) string {
	plaintext = strings.TrimSpace(plaintext)
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// MintRuntimeCredential returns a fresh (plaintext, hash) bearer the
// runner uses on every post-pair request. Hash lives in
// runtimes.config.runner_credential_hash; plaintext is returned
// exactly once by POST /api/v1/runtimes/pair and never persisted.
func MintRuntimeCredential() (plaintext, hash string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", "", fmt.Errorf("runtime credential: rand: %w", err)
	}
	body := hex.EncodeToString(buf[:])
	plaintext = RuntimeCredentialPrefix + body
	hash = HashRuntimeCredential(plaintext)
	return plaintext, hash, nil
}

// HashRuntimeCredential is the inverse of MintRuntimeCredential. Kept
// separate from HashRuntimePairingToken so a future KDF change on one
// path doesn't force a coordinated change on the other.
func HashRuntimeCredential(plaintext string) string {
	return HashRuntimePairingToken(plaintext)
}
