package cli

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// Config is the resolved environment the CLI uses to talk to Parsar.
// Only ServerURL + RunnerToken are required; the rest are informational
// and surface via `parsar version` / `parsar sync`.
type Config struct {
	ServerURL      string // PARSAR_SERVER_URL — must be parseable absolute URL
	RunnerToken    string // PARSAR_RUNNER_TOKEN — bearer credential
	RuntimeID      string // PARSAR_RUNTIME_ID
	WorkspaceID    string // PARSAR_WORKSPACE_ID
	UserID         string // PARSAR_USER_ID
	Connector      string // PARSAR_CONNECTOR — "claude" | "opencode" | "codex"
	AgentID        string // PARSAR_AGENT_ID
	ConversationID string // PARSAR_CONVERSATION_ID
}

func loadConfigFromEnv() (Config, error) {
	cfg := Config{
		ServerURL:      strings.TrimSpace(os.Getenv("PARSAR_SERVER_URL")),
		RunnerToken:    strings.TrimSpace(os.Getenv("PARSAR_RUNNER_TOKEN")),
		RuntimeID:      strings.TrimSpace(os.Getenv("PARSAR_RUNTIME_ID")),
		WorkspaceID:    strings.TrimSpace(os.Getenv("PARSAR_WORKSPACE_ID")),
		UserID:         strings.TrimSpace(os.Getenv("PARSAR_USER_ID")),
		Connector:      strings.TrimSpace(os.Getenv("PARSAR_CONNECTOR")),
		AgentID:        strings.TrimSpace(os.Getenv("PARSAR_AGENT_ID")),
		ConversationID: strings.TrimSpace(os.Getenv("PARSAR_CONVERSATION_ID")),
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.ServerURL == "" {
		return fmt.Errorf("PARSAR_SERVER_URL is required")
	}
	if c.RunnerToken == "" {
		return fmt.Errorf("PARSAR_RUNNER_TOKEN is required")
	}
	u, err := url.Parse(c.ServerURL)
	if err != nil {
		return fmt.Errorf("PARSAR_SERVER_URL: %w", err)
	}
	if !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("PARSAR_SERVER_URL must be an absolute URL with host")
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("PARSAR_SERVER_URL scheme must be http or https, got %q", u.Scheme)
	}
	return nil
}
