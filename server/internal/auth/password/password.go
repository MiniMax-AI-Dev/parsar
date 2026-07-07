// Package password wraps bcrypt and go-password-validator so the
// rest of the codebase has one place to hash, verify and validate
// local email/password credentials.
//
// Hash format is bcrypt's native $2a$<cost>$<salt+hash> — parseable
// by any bcrypt library, salt embedded, cost embedded, no bespoke
// serialization on our side.
package password

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"unicode/utf8"

	passwordvalidator "github.com/wagslane/go-password-validator"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is returned by Compare when the hash and
// password do not match, or when the stored hash is malformed. The
// caller MUST map this to HTTP 401 and MUST NOT surface the detail
// to the client — same response for "user not found" and "wrong
// password" is the whole point.
var ErrInvalidCredentials = errors.New("password: invalid credentials")

const (
	// bcryptCost of 12 costs ~250ms per hash on modern CPUs. Login
	// happens once per user per session (30 days by default) so
	// this is a comfortable trade-off. Bumping to 13/14 is a
	// two-character edit if we ever want to slow attackers further.
	bcryptCost = 12

	// minEntropyBits rejects "password", "12345678", "qwertyui" etc.
	// 60 bits ≈ "correct-horse-battery" territory. coder uses 52.
	minEntropyBits = 60

	// maxPasswordLen is bcrypt's hard limit (72 bytes). We reject
	// longer inputs at the door so callers get a clean 400 rather
	// than a silently-truncated hash.
	maxPasswordLen = 72
)

// dummyHash is a fixed, valid bcrypt hash used by Compare when the
// stored hash is empty. Comparing against it burns the same CPU as
// a real check so an attacker probing which emails exist sees the
// same latency regardless. The plaintext behind it is random and
// discarded — no password can ever match.
//
// Generated once at process start.
var dummyHash []byte

func init() {
	// A 32-byte random string is far above any user's password
	// entropy; the bcrypt output is deterministic-ish (salt is
	// baked in) so we simply hold onto it.
	var seed [32]byte
	if _, err := rand.Read(seed[:]); err != nil {
		panic("password: seed dummy hash: " + err.Error())
	}
	h, err := bcrypt.GenerateFromPassword([]byte(hex.EncodeToString(seed[:])), bcryptCost)
	if err != nil {
		panic("password: build dummy hash: " + err.Error())
	}
	dummyHash = h
}

// Hash produces a bcrypt hash. Callers should Validate first; Hash
// itself only guards the hard length limit.
func Hash(plain string) (string, error) {
	if len(plain) == 0 {
		return "", errors.New("password: empty")
	}
	if len(plain) > maxPasswordLen {
		return "", errors.New("password: too long")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// Compare returns nil on match, ErrInvalidCredentials otherwise. An
// empty hashed argument triggers a constant-time-equivalent bcrypt
// call against dummyHash so callers can pass through the "user
// not found" branch without a timing side-channel.
func Compare(hashed, plain string) error {
	if hashed == "" {
		// Burn the same CPU as a real check.
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
		return ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// Validate enforces our password policy: bcrypt byte-length ceiling
// and a minimum entropy floor. Returns a plain-English error suitable
// for surfacing to the client as a form validation message.
func Validate(plain string) error {
	if utf8.RuneCountInString(plain) == 0 {
		return errors.New("password is required")
	}
	if len(plain) > maxPasswordLen {
		return errors.New("password must be at most 72 bytes")
	}
	if err := passwordvalidator.Validate(plain, minEntropyBits); err != nil {
		return err
	}
	return nil
}
