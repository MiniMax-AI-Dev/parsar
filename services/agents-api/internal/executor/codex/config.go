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

// ExecutorKey grants registration for one Environment only; it is not an API or device key.
type ExecutorKey struct {
	TokenSHA256   string `json:"token_sha256"`
	TenantID      string `json:"tenant_id"`
	EnvironmentID string `json:"environment_id"`
}

type Config struct {
	Store          EnvironmentStore
	CheckOwnership func(context.Context) error
	PublicURL      string
	Keys           []ExecutorKey
}

func validateConfig(c Config) (string, map[[32]byte]ExecutorKey, error) {
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
	keys := make(map[[32]byte]ExecutorKey, len(c.Keys))
	for _, key := range c.Keys {
		tenant, e1 := uuid.Parse(key.TenantID)
		environment, e2 := uuid.Parse(key.EnvironmentID)
		digest, e3 := hex.DecodeString(key.TokenSHA256)
		if e1 != nil || e2 != nil || e3 != nil || tenant == uuid.Nil || environment == uuid.Nil || len(digest) != sha256.Size {
			return "", nil, errors.New("executor key requires tenant, Environment and SHA-256 digest")
		}
		hash := [32]byte(digest)
		if _, exists := keys[hash]; exists {
			return "", nil, errors.New("duplicate executor key digest")
		}
		key.TenantID, key.EnvironmentID = tenant.String(), environment.String()
		keys[hash] = key
	}
	return strings.TrimRight(u.String(), "/"), keys, nil
}

func (r *Registry) credential(req *http.Request, environment string) (ExecutorKey, bool) {
	parts := strings.Fields(req.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ExecutorKey{}, false
	}
	key, ok := r.keys[sha256.Sum256([]byte(parts[1]))]
	return key, ok && key.EnvironmentID == environment
}
