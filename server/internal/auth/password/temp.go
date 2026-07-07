package password

import (
	"crypto/rand"
	"errors"
	"math/big"
)

// GenerateTemp mints a human-transcribable temporary password: 16
// alphanumeric characters drawn from a confusables-free alphabet
// ("l", "I", "O", "o", "0", "1" removed). The result is >= 90 bits
// of entropy which easily clears the 60-bit floor Validate enforces
// on the login path.
//
// Used by the invite flow: the admin sees this plaintext exactly
// once in a modal, copies it to the invitee out-of-band, and the
// server stores only the bcrypt hash. There is no forced rotation —
// the invitee is welcome to keep this password or change it later.
func GenerateTemp() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	const length = 16
	if len(alphabet) < 2 {
		return "", errors.New("password: alphabet too small")
	}
	max := big.NewInt(int64(len(alphabet)))
	buf := make([]byte, length)
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}
