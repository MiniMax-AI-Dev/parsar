package canonical

import (
	"fmt"
	"strings"
)

// MCPSpec carries one or more MCP stdio servers. HTTP transport is not
// modeled; the import pipeline only accepts stdio.
type MCPSpec struct {
	Servers []MCPServer `json:"servers"`
}

// MCPServer is one launchable MCP stdio server. Command + Args stay separate
// because renderers join them differently (Claude Code accepts string or
// array; OpenCode wants an array).
//
// StartupTimeoutSec=0 means "use scaffold default"; preserved because Codex's
// TOML uses it explicitly.
type MCPServer struct {
	Name              string              `json:"name"`
	Command           string              `json:"command"`
	Args              []string            `json:"args,omitempty"`
	Env               map[string]EnvValue `json:"env,omitempty"`
	StartupTimeoutSec int                 `json:"startup_timeout_sec,omitempty"`
}

// Validate checks structure only — it does NOT resolve cross-table references
// (e.g. SecretID existence). Commit-time checks live in the import handler.
func (m MCPSpec) Validate() error {
	if len(m.Servers) == 0 {
		return fmt.Errorf("%w: at least one server required", ErrInvalidMCP)
	}
	seen := make(map[string]struct{}, len(m.Servers))
	for i, srv := range m.Servers {
		if err := srv.Validate(); err != nil {
			return fmt.Errorf("server[%d]: %w", i, err)
		}
		if _, dup := seen[srv.Name]; dup {
			return fmt.Errorf("%w: duplicate server name %q", ErrInvalidMCP, srv.Name)
		}
		seen[srv.Name] = struct{}{}
	}
	return nil
}

// Validate checks a single server: non-empty name + command, valid env modes.
func (s MCPServer) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("%w: server name is required", ErrInvalidMCP)
	}
	if strings.TrimSpace(s.Command) == "" {
		return fmt.Errorf("%w: server %q: command is required", ErrInvalidMCP, s.Name)
	}
	if s.StartupTimeoutSec < 0 {
		return fmt.Errorf("%w: server %q: startup_timeout_sec must be >= 0", ErrInvalidMCP, s.Name)
	}
	for name, value := range s.Env {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: server %q: empty env name", ErrInvalidMCP, s.Name)
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("server %q env %q: %w", s.Name, name, err)
		}
	}
	return nil
}
