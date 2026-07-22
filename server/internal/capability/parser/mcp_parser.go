package parser

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// MCPParseResult is what a preview call returns to the HTTP handler.
// SuggestedName is the first server name, used to pre-fill the commit form.
type MCPParseResult struct {
	Spec          canonical.Spec
	Warnings      []string
	SuggestedName string
}

// ParseMCP converts a pasted MCP server snippet to a canonical.Spec.
// Accepts Claude Code JSON (mcpServers/env), OpenCode JSON
// (mcpServers/environment), and Codex TOML ([mcp_servers.<name>]).
//
// Hard rule: this function does NOT examine env values to guess what's a
// secret. Every entry is EnvModeLiteral; users mark sensitive entries in
// the import UI.
func ParseMCP(raw string, format SourceFormat) (MCPParseResult, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return MCPParseResult{}, ErrEmptyInput
	}
	switch format {
	case SourceFormatJSON:
		return parseMCPJSON(trimmed)
	case SourceFormatTOML:
		return parseMCPTOML(trimmed)
	case SourceFormatMarkdown:
		return MCPParseResult{}, fmt.Errorf("%w: MCP parser does not accept markdown", ErrUnsupportedSourceFormat)
	default:
		return MCPParseResult{}, fmt.Errorf("%w: %q", ErrUnsupportedSourceFormat, format)
	}
}

// --- JSON shape used by both Claude Code and OpenCode ---

// jsonMCPDocument tolerates wrapped (`{"mcpServers": {...}}`) and bare maps.
// We try wrapped first because that's the vendor-documented form; bare is
// re-parsed when the wrapper key is absent.
type jsonMCPDocument struct {
	MCPServers map[string]jsonMCPServer `json:"mcpServers"`
}

// jsonMCPServer covers both Claude Code ("env") and OpenCode ("environment").
// If both appear on one server we prefer "env" and warn.
type jsonMCPServer struct {
	Command           any               `json:"command"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	Environment       map[string]string `json:"environment"`
	URL               string            `json:"url"`
	StartupTimeoutSec int               `json:"startup_timeout_sec"`
	Enabled           *bool             `json:"enabled"`
	Type              string            `json:"type"`
}

func parseMCPJSON(raw string) (MCPParseResult, error) {
	// Probe top-level shape: (a) wrapped with mcpServers → use it; (b) object
	// without mcpServers → treat as bare inner map; (c) not an object → syntax error.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return MCPParseResult{}, fmt.Errorf("mcp parse: %w", err)
	}
	var doc jsonMCPDocument
	if _, hasWrapper := probe["mcpServers"]; hasWrapper {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return MCPParseResult{}, fmt.Errorf("mcp parse: %w", err)
		}
	} else {
		var bare map[string]jsonMCPServer
		if err := json.Unmarshal([]byte(raw), &bare); err != nil {
			return MCPParseResult{}, fmt.Errorf("mcp parse: %w", err)
		}
		doc.MCPServers = bare
	}
	if len(doc.MCPServers) == 0 {
		return MCPParseResult{}, fmt.Errorf("mcp parse: no mcpServers entries found")
	}
	return buildMCPResult(doc.MCPServers)
}

func buildMCPResult(servers map[string]jsonMCPServer) (MCPParseResult, error) {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var warnings []string
	out := canonical.MCPSpec{Servers: make([]canonical.MCPServer, 0, len(names))}
	for _, name := range names {
		srv := servers[name]
		if strings.TrimSpace(name) == "" {
			warnings = append(warnings, "ignored server with empty name")
			continue
		}
		transport, err := normalizeMCPTransport(srv.Type, srv.URL)
		if err != nil {
			return MCPParseResult{}, fmt.Errorf("mcp parse: server %q: %w", name, err)
		}
		var command string
		var args []string
		var env map[string]canonical.EnvValue
		if transport == canonical.MCPTransportStdio {
			command, args, err = flattenMCPCommand(srv.Command, srv.Args)
			if err != nil {
				return MCPParseResult{}, fmt.Errorf("mcp parse: server %q command: %w", name, err)
			}
			var envWarnings []string
			env, envWarnings = mergeMCPEnv(name, srv.Env, srv.Environment)
			warnings = append(warnings, envWarnings...)
		} else if srv.Command != nil || len(srv.Args) > 0 || len(srv.Env) > 0 || len(srv.Environment) > 0 {
			return MCPParseResult{}, fmt.Errorf("mcp parse: server %q: remote HTTP entries must not set command, args, env, or environment", name)
		}
		if srv.Enabled != nil && !*srv.Enabled {
			warnings = append(warnings, fmt.Sprintf("server %q has enabled=false — the parser preserves the entry but the renderer will treat all imported servers as enabled", name))
		}
		out.Servers = append(out.Servers, canonical.MCPServer{
			Name:              name,
			Transport:         transport,
			URL:               strings.TrimSpace(srv.URL),
			Command:           command,
			Args:              args,
			Env:               env,
			StartupTimeoutSec: srv.StartupTimeoutSec,
		})
	}
	if len(out.Servers) == 0 {
		return MCPParseResult{}, fmt.Errorf("mcp parse: no usable servers after filtering")
	}
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP:           &out,
	}
	return MCPParseResult{
		Spec:          spec,
		Warnings:      warnings,
		SuggestedName: out.Servers[0].Name,
	}, nil
}

func normalizeMCPTransport(rawType, rawURL string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(rawType))
	hasURL := strings.TrimSpace(rawURL) != ""
	switch t {
	case "", canonical.MCPTransportStdio, "local":
		if hasURL {
			return canonical.MCPTransportStreamableHTTP, nil
		}
		return canonical.MCPTransportStdio, nil
	case "http", canonical.MCPTransportStreamableHTTP, "remote":
		if !hasURL {
			return "", fmt.Errorf("type=%q requires url", t)
		}
		return canonical.MCPTransportStreamableHTTP, nil
	case "sse", "ws", "websocket":
		return "", fmt.Errorf("type=%q is not supported; use streamable HTTP", t)
	default:
		return "", fmt.Errorf("unsupported type=%q", t)
	}
}

// flattenMCPCommand normalizes "command" + "args" to a single executable +
// positional args. Vendor docs use a string; some examples use an array.
// When given an array we split the first element off as the executable.
func flattenMCPCommand(command any, args []string) (string, []string, error) {
	switch v := command.(type) {
	case nil:
		return "", nil, fmt.Errorf("command is required")
	case string:
		cmd := strings.TrimSpace(v)
		if cmd == "" {
			return "", nil, fmt.Errorf("command is empty")
		}
		return cmd, append([]string(nil), args...), nil
	case []any:
		if len(v) == 0 {
			return "", nil, fmt.Errorf("command array is empty")
		}
		first, ok := v[0].(string)
		if !ok || strings.TrimSpace(first) == "" {
			return "", nil, fmt.Errorf("command[0] must be a non-empty string")
		}
		rest := make([]string, 0, len(v)-1+len(args))
		for i, item := range v[1:] {
			s, ok := item.(string)
			if !ok {
				return "", nil, fmt.Errorf("command[%d] must be string", i+1)
			}
			rest = append(rest, s)
		}
		rest = append(rest, args...)
		return first, rest, nil
	default:
		return "", nil, fmt.Errorf("unsupported command shape %T", command)
	}
}

// mergeMCPEnv combines Claude Code "env" and OpenCode "environment" into one
// canonical map. Every entry is EnvModeLiteral — no auto-guessing.
func mergeMCPEnv(serverName string, env, environment map[string]string) (map[string]canonical.EnvValue, []string) {
	var warnings []string
	if len(env) == 0 && len(environment) == 0 {
		return nil, nil
	}
	merged := make(map[string]string, len(env)+len(environment))
	for k, v := range env {
		merged[k] = v
	}
	for k, v := range environment {
		if existing, ok := merged[k]; ok && existing != v {
			warnings = append(warnings, fmt.Sprintf("server %q env key %q appears in both env and environment with different values — keeping env's value", serverName, k))
			continue
		}
		merged[k] = v
	}
	out := make(map[string]canonical.EnvValue, len(merged))
	for k, v := range merged {
		out[k] = canonical.EnvValue{Mode: canonical.EnvModeLiteral, Literal: v}
	}
	return out, warnings
}

// --- TOML shape used by Codex ---
//
// tomlCodexDocument matches Codex config.toml. Env table is nested under each
// server table so a server-less env can't be ambiguous.
//
//	[mcp_servers.github]
//	command = "docker"
//	args = ["run", "..."]
//
//	[mcp_servers.github.env]
//	GITHUB_TOKEN = "ghp_xxx"
type tomlCodexDocument struct {
	MCPServers map[string]tomlCodexServer `toml:"mcp_servers"`
}

type tomlCodexServer struct {
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env"`
	URL               string            `toml:"url"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec"`
}

func parseMCPTOML(raw string) (MCPParseResult, error) {
	var doc tomlCodexDocument
	if _, err := toml.Decode(raw, &doc); err != nil {
		return MCPParseResult{}, fmt.Errorf("mcp parse: toml decode: %w", err)
	}
	if len(doc.MCPServers) == 0 {
		return MCPParseResult{}, fmt.Errorf("mcp parse: no [mcp_servers.*] tables found")
	}
	// Bridge to the JSON intermediate so buildMCPResult handles wrapping + warnings once.
	bridge := make(map[string]jsonMCPServer, len(doc.MCPServers))
	for name, srv := range doc.MCPServers {
		bridge[name] = jsonMCPServer{
			Command:           srv.Command,
			Args:              srv.Args,
			Env:               srv.Env,
			URL:               srv.URL,
			StartupTimeoutSec: srv.StartupTimeoutSec,
		}
	}
	return buildMCPResult(bridge)
}
