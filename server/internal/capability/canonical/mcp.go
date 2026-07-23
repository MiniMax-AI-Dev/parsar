package canonical

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	MCPTransportStdio          = "stdio"
	MCPTransportStreamableHTTP = "streamable-http"
)

// MCPSpec carries one or more MCP servers. Existing specs omit transport and
// therefore continue to resolve as stdio.
type MCPSpec struct {
	Servers []MCPServer `json:"servers"`
}

// MCPServer is either a launchable stdio process or a streamable HTTP URL.
// Command + Args stay separate because renderers join them differently.
//
// StartupTimeoutSec=0 means "use scaffold default"; preserved because Codex's
// TOML uses it explicitly.
type MCPServer struct {
	Name              string              `json:"name"`
	Transport         string              `json:"transport,omitempty"`
	URL               string              `json:"url,omitempty"`
	Headers           map[string]EnvValue `json:"headers,omitempty"`
	Command           string              `json:"command,omitempty"`
	Args              []string            `json:"args,omitempty"`
	Env               map[string]EnvValue `json:"env,omitempty"`
	StartupTimeoutSec int                 `json:"startup_timeout_sec,omitempty"`
}

func (s MCPServer) EffectiveTransport() string {
	if strings.TrimSpace(s.Transport) == "" {
		return MCPTransportStdio
	}
	return strings.ToLower(strings.TrimSpace(s.Transport))
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
	if s.StartupTimeoutSec < 0 {
		return fmt.Errorf("%w: server %q: startup_timeout_sec must be >= 0", ErrInvalidMCP, s.Name)
	}
	switch s.EffectiveTransport() {
	case MCPTransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("%w: server %q: command is required", ErrInvalidMCP, s.Name)
		}
		if strings.TrimSpace(s.URL) != "" {
			return fmt.Errorf("%w: server %q: stdio transport must not set url", ErrInvalidMCP, s.Name)
		}
		if len(s.Headers) > 0 {
			return fmt.Errorf("%w: server %q: stdio transport must not set headers", ErrInvalidMCP, s.Name)
		}
	case MCPTransportStreamableHTTP:
		parsed, err := url.Parse(strings.TrimSpace(s.URL))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return fmt.Errorf("%w: server %q: streamable-http url must be an http or https URL without embedded credentials", ErrInvalidMCP, s.Name)
		}
		if strings.TrimSpace(s.Command) != "" || len(s.Args) > 0 || len(s.Env) > 0 {
			return fmt.Errorf("%w: server %q: streamable-http transport must not set command, args, or env", ErrInvalidMCP, s.Name)
		}
	default:
		return fmt.Errorf("%w: server %q: unsupported transport %q", ErrInvalidMCP, s.Name, s.Transport)
	}
	for name, value := range s.Env {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%w: server %q: empty env name", ErrInvalidMCP, s.Name)
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("server %q env %q: %w", s.Name, name, err)
		}
	}
	for name, value := range s.Headers {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("%w: server %q: invalid header name", ErrInvalidMCP, s.Name)
		}
		if value.Mode == EnvModeInlineSecret {
			return fmt.Errorf("%w: server %q header %q: inline_secret is not supported", ErrInvalidMCP, s.Name, name)
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("server %q header %q: %w", s.Name, name, err)
		}
	}
	return nil
}
