package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// APIKey binds a service credential to an execution tenant, not a product user.
// Configuration stores the SHA-256 hex digest, never the plaintext key.
type APIKey struct {
	TokenSHA256 string `json:"token_sha256"`
	TenantID    string `json:"tenant_id"`
}

type Authenticator struct{ tenants map[[32]byte]string }

func NewAuthenticator(keys []APIKey) (*Authenticator, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one Agents API key is required")
	}
	a := &Authenticator{tenants: make(map[[32]byte]string, len(keys))}
	for _, key := range keys {
		tenant, err := uuid.Parse(key.TenantID)
		if err != nil || tenant == uuid.Nil {
			return nil, errors.New("API key tenant_id must be a nonzero UUID")
		}
		digest, err := hex.DecodeString(key.TokenSHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("API key token_sha256 must be a SHA-256 hex digest")
		}
		hash := [32]byte(digest)
		if _, exists := a.tenants[hash]; exists {
			return nil, errors.New("duplicate API key digest")
		}
		a.tenants[hash] = tenant.String()
	}
	return a, nil
}

func (a *Authenticator) tenant(r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	tenant, ok := a.tenants[sha256.Sum256([]byte(parts[1]))]
	return tenant, ok
}
