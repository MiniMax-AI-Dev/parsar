package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// TestParseMCP_ClaudeCodeJSON: env wrapped in EnvModeLiteral (no auto-guessing),
// SuggestedName = first sorted server name.
func TestParseMCP_ClaudeCodeJSON(t *testing.T) {
	raw := `{
		"mcpServers": {
			"github": {
				"command": "docker",
				"args": ["run", "-i", "--rm", "ghcr.io/github/github-mcp-server"],
				"env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_xxx"}
			}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.Kind != canonical.KindMCP {
		t.Fatalf("kind: want mcp, got %q", res.Spec.Kind)
	}
	if got := len(res.Spec.MCP.Servers); got != 1 {
		t.Fatalf("servers: want 1, got %d", got)
	}
	srv := res.Spec.MCP.Servers[0]
	if srv.Name != "github" {
		t.Fatalf("server name: want github, got %q", srv.Name)
	}
	if srv.Command != "docker" {
		t.Fatalf("command: want docker, got %q", srv.Command)
	}
	if got := len(srv.Args); got != 4 {
		t.Fatalf("args length: want 4, got %d", got)
	}
	ev, ok := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"]
	if !ok {
		t.Fatalf("env key missing: %+v", srv.Env)
	}
	// Even an obvious-looking token must default to literal.
	if ev.Mode != canonical.EnvModeLiteral {
		t.Fatalf("env mode: want literal, got %q — auto-guessing a token is forbidden", ev.Mode)
	}
	if ev.Literal != "ghp_xxx" {
		t.Fatalf("env literal: want ghp_xxx, got %q", ev.Literal)
	}
	if res.SuggestedName != "github" {
		t.Fatalf("suggested name: want github, got %q", res.SuggestedName)
	}
}

// TestParseMCP_OpenCodeJSON: env map under "environment" instead of "env".
func TestParseMCP_OpenCodeJSON(t *testing.T) {
	raw := `{
		"mcpServers": {
			"context7": {
				"command": "npx",
				"args": ["-y", "@upstash/context7-mcp"],
				"environment": {"CONTEXT7_TOKEN": "tkn_yyy"}
			}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv := res.Spec.MCP.Servers[0]
	ev := srv.Env["CONTEXT7_TOKEN"]
	if ev.Mode != canonical.EnvModeLiteral || ev.Literal != "tkn_yyy" {
		t.Fatalf("environment->env merge failed: %+v", ev)
	}
}

// TestParseMCP_BareMapFallback: pasting just the inner mcpServers map (no
// outer wrapper) still parses — vendor docs sometimes show only the inner object.
func TestParseMCP_BareMapFallback(t *testing.T) {
	raw := `{
		"playwright": {
			"command": "npx",
			"args": ["@playwright/mcp"]
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.MCP.Servers[0].Name != "playwright" {
		t.Fatalf("unexpected server: %+v", res.Spec.MCP.Servers)
	}
}

// TestParseMCP_CommandAsArray: first element is the executable, rest get
// prepended to args.
func TestParseMCP_CommandAsArray(t *testing.T) {
	raw := `{
		"mcpServers": {
			"x": {
				"command": ["docker", "run", "-i"],
				"args": ["ghcr.io/x"]
			}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv := res.Spec.MCP.Servers[0]
	if srv.Command != "docker" {
		t.Fatalf("command head: want docker, got %q", srv.Command)
	}
	want := []string{"run", "-i", "ghcr.io/x"}
	if got := strings.Join(srv.Args, " "); got != strings.Join(want, " ") {
		t.Fatalf("args: want %v, got %v", want, srv.Args)
	}
}

func TestParseMCP_CodexTOML(t *testing.T) {
	raw := `
[mcp_servers.github]
command = "docker"
args = ["run", "-i", "ghcr.io/github/github-mcp-server"]
startup_timeout_sec = 30

[mcp_servers.github.env]
GITHUB_PERSONAL_ACCESS_TOKEN = "ghp_zzz"
`
	res, err := ParseMCP(raw, SourceFormatTOML)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Spec.MCP.Servers) != 1 {
		t.Fatalf("server count: want 1, got %d", len(res.Spec.MCP.Servers))
	}
	srv := res.Spec.MCP.Servers[0]
	if srv.Name != "github" {
		t.Fatalf("name: %q", srv.Name)
	}
	if srv.StartupTimeoutSec != 30 {
		t.Fatalf("startup timeout: want 30, got %d", srv.StartupTimeoutSec)
	}
	ev := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"]
	if ev.Mode != canonical.EnvModeLiteral || ev.Literal != "ghp_zzz" {
		t.Fatalf("env not preserved as literal: %+v", ev)
	}
}

// TestParseMCP_MultipleServersSortedAndFirstSuggested: SuggestedName picks
// the alphabetically-first server so paste UX is deterministic regardless
// of map iteration order.
func TestParseMCP_MultipleServersSortedAndFirstSuggested(t *testing.T) {
	raw := `{
		"mcpServers": {
			"zoo": {"command": "z"},
			"alpha": {"command": "a"},
			"middle": {"command": "m"}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(res.Spec.MCP.Servers); got != 3 {
		t.Fatalf("server count: %d", got)
	}
	if res.SuggestedName != "alpha" {
		t.Fatalf("first-sorted suggested name: want alpha, got %q", res.SuggestedName)
	}
	wantOrder := []string{"alpha", "middle", "zoo"}
	for i, want := range wantOrder {
		if got := res.Spec.MCP.Servers[i].Name; got != want {
			t.Fatalf("servers[%d].Name: want %q, got %q", i, want, got)
		}
	}
}

// TestParseMCP_HTTPTransportEmitsWarning: HTTP/SSE servers parse but warn
// since downstream renderers may not handle them.
func TestParseMCP_HTTPTransportEmitsWarning(t *testing.T) {
	raw := `{
		"mcpServers": {
			"http-server": {"command": "x", "type": "http"}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.Warnings) == 0 || !strings.Contains(strings.Join(res.Warnings, "|"), "type=") {
		t.Fatalf("expected a warning mentioning type=, got %v", res.Warnings)
	}
}

// TestParseMCP_EnvConflictPrefersEnv: env wins over environment on key
// collision, with a warning.
func TestParseMCP_EnvConflictPrefersEnv(t *testing.T) {
	raw := `{
		"mcpServers": {
			"x": {
				"command": "x",
				"env": {"K": "from-env"},
				"environment": {"K": "from-environment"}
			}
		}
	}`
	res, err := ParseMCP(raw, SourceFormatJSON)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	srv := res.Spec.MCP.Servers[0]
	if srv.Env["K"].Literal != "from-env" {
		t.Fatalf("conflict resolution wrong: %+v", srv.Env["K"])
	}
	if len(res.Warnings) == 0 {
		t.Fatalf("expected a warning about env conflict")
	}
}

func TestParseMCP_EmptyInput(t *testing.T) {
	_, err := ParseMCP("   ", SourceFormatJSON)
	if !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("want ErrEmptyInput, got %v", err)
	}
}

func TestParseMCP_UnsupportedFormat(t *testing.T) {
	_, err := ParseMCP(`# heading`, SourceFormatMarkdown)
	if !errors.Is(err, ErrUnsupportedSourceFormat) {
		t.Fatalf("want ErrUnsupportedSourceFormat, got %v", err)
	}
}

func TestParseMCP_NoServersIsError(t *testing.T) {
	_, err := ParseMCP(`{"mcpServers":{}}`, SourceFormatJSON)
	if err == nil {
		t.Fatalf("expected error for empty mcpServers")
	}
}
