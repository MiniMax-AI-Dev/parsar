package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mcpServerConfig is the daemon-internal MCP server config flattened
// from agent_options["mcp_servers"] (rendered by render.TargetCodex /
// claudecode's mcpServers JSON shape). Written into <CODEX_HOME>/config.toml
// before spawning the app-server child.
type mcpServerConfig struct {
	Name    string
	URL     string
	Headers map[string]string
	Command string
	Args    []string
	Env     map[string]string
}

// writeCodexMCPConfig writes a `[mcp_servers.<name>]` TOML table per
// server into <codexHome>/config.toml. Servers are sorted by name so
// the file is deterministic and diffable.
//
// Appends to the file rather than truncating because
// writeCodexProviderConfig writes to the same path. Both writers run
// once per prompt and CODEX_HOME is fresh per-run, so appending stays
// idempotent.
//
// The TOML shape mirrors codex-rs/config/src/mcp_types.rs::McpServerConfig::Stdio
// — only the stdio transport is emitted today (no streamableHttp). The
// canonical capability spec doesn't model http MCP yet, so there's
// nothing to render for that branch.
func writeCodexMCPConfig(codexHome string, servers map[string]mcpServerConfig) error {
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return fmt.Errorf("codex: mkdir CODEX_HOME %s: %w", codexHome, err)
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		srv := servers[name]
		// TOML table name. Use a quoted key form for safety against
		// names that contain dots / dashes / unicode.
		b.WriteString("[mcp_servers.")
		b.WriteString(tomlQuoteString(name))
		b.WriteString("]\n")
		if srv.URL != "" {
			b.WriteString(`url = `)
			b.WriteString(tomlQuoteString(srv.URL))
			b.WriteByte('\n')
			if len(srv.Headers) > 0 {
				headerKeys := make([]string, 0, len(srv.Headers))
				for key := range srv.Headers {
					headerKeys = append(headerKeys, key)
				}
				sort.Strings(headerKeys)
				b.WriteString("http_headers = {")
				for index, key := range headerKeys {
					if index > 0 {
						b.WriteString(", ")
					}
					b.WriteString(tomlQuoteString(key))
					b.WriteString(" = ")
					b.WriteString(tomlQuoteString(srv.Headers[key]))
				}
				b.WriteString("}\n")
			}
			b.WriteByte('\n')
			continue
		}
		b.WriteString(`command = `)
		b.WriteString(tomlQuoteString(srv.Command))
		b.WriteByte('\n')
		if len(srv.Args) > 0 {
			b.WriteString("args = [")
			for i, a := range srv.Args {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(tomlQuoteString(a))
			}
			b.WriteString("]\n")
		}
		if len(srv.Env) > 0 {
			envKeys := make([]string, 0, len(srv.Env))
			for k := range srv.Env {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)
			b.WriteString("\n[mcp_servers.")
			b.WriteString(tomlQuoteString(name))
			b.WriteString(".env]\n")
			for _, k := range envKeys {
				b.WriteString(tomlQuoteString(k))
				b.WriteString(" = ")
				b.WriteString(tomlQuoteString(srv.Env[k]))
				b.WriteByte('\n')
			}
		}
		b.WriteByte('\n')
	}

	path := filepath.Join(codexHome, "config.toml")
	return appendConfigTOML(path, b.String())
}

// tomlQuoteString returns a TOML basic-string literal (double-quoted)
// with the documented escape set applied — \" \\ \n \r \t plus
// \uXXXX for control chars. Matches the TOML 1.0 spec for basic strings.
func tomlQuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
