// Package codex implements the native executor registry, separate from the public Agents API.
package codex

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type EnvironmentStore interface {
	GetEnvironment(context.Context, string, string) (store.Environment, error)
	AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error)
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
}

func validateConfig(c Config) (string, error) {
	if c.Store == nil || c.CheckOwnership == nil {
		return "", errors.New("executor registry requires Store and execution ownership")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", errors.New("executor URL must be an absolute origin without credentials")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", errors.New("executor HTTP is allowed only on loopback")
		}
		u.Scheme = "ws"
	default:
		return "", errors.New("executor URL must use HTTPS or loopback HTTP")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
