// Package codex implements the native executor registry, separate from the public Agents API.
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type EnvironmentStore interface {
	GetEnvironment(context.Context, string, string) (store.Environment, error)
}

// ScopedKey binds a purpose-specific native transport credential to one Environment.
type ScopedKey struct {
	TokenSHA256   string `json:"token_sha256"`
	TenantID      string `json:"tenant_id"`
	EnvironmentID string `json:"environment_id"`
}

type Config struct {
	Store          EnvironmentStore
	CheckOwnership func(context.Context) error
	PublicURL      string
	Keys           []ScopedKey
	HarnessKeys    []ScopedKey
}

func validateConfig(c Config) (string, map[[32]byte]ScopedKey, error) {
	if c.Store == nil || c.CheckOwnership == nil || len(c.Keys) == 0 {
		return "", nil, errors.New("executor registry requires Store, execution ownership and scoped keys")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", nil, errors.New("executor URL must be an absolute origin without credentials")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", nil, errors.New("executor HTTP is allowed only on loopback")
		}
		u.Scheme = "ws"
	default:
		return "", nil, errors.New("executor URL must use HTTPS or loopback HTTP")
	}
	keys, err := scopedKeys(c.Keys)
	return strings.TrimRight(u.String(), "/"), keys, err
}

func scopedKeys(bindings []ScopedKey) (map[[32]byte]ScopedKey, error) {
	keys := make(map[[32]byte]ScopedKey, len(bindings))
	for _, key := range bindings {
		tenant, e1 := uuid.Parse(key.TenantID)
		environment, e2 := uuid.Parse(key.EnvironmentID)
		digest, e3 := hex.DecodeString(key.TokenSHA256)
		if e1 != nil || e2 != nil || e3 != nil || tenant == uuid.Nil || environment == uuid.Nil || len(digest) != sha256.Size {
			return nil, errors.New("transport key requires tenant, Environment and SHA-256 digest")
		}
		hash := [32]byte(digest)
		if _, exists := keys[hash]; exists {
			return nil, errors.New("duplicate transport key digest")
		}
		key.TenantID, key.EnvironmentID = tenant.String(), environment.String()
		keys[hash] = key
	}
	return keys, nil
}

func credential(req *http.Request, environment string, keys map[[32]byte]ScopedKey) (ScopedKey, bool) {
	parts := strings.Fields(req.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ScopedKey{}, false
	}
	key, ok := keys[sha256.Sum256([]byte(parts[1]))]
	return key, ok && key.EnvironmentID == environment
}
