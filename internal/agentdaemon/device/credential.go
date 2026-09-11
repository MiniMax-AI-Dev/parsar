package device

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// Credential is the minimal device identity needed for gateway authentication.
// CredentialHash is never sent over the daemon protocol.
type Credential struct {
	ID             string
	WorkspaceID    string
	Name           string
	Type           string
	CredentialHash string
}

// HashCredential preserves the paired runtime bearer format, including trimming
// whitespace appended when operators paste tokens.
func HashCredential(plaintext string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plaintext)))
	return hex.EncodeToString(sum[:])
}
