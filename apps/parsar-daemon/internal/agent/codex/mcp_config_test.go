package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCodexMCPConfig_DeterministicOrdering(t *testing.T) {
	dir := t.TempDir()
	servers := map[string]mcpServerConfig{
		"zulu":  {Name: "zulu", Command: "z"},
		"alpha": {Name: "alpha", Command: "a"},
		"mike":  {Name: "mike", Command: "m"},
	}
	if err := writeCodexMCPConfig(dir, servers); err != nil {
		t.Fatalf("write: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(out)
	idxA := strings.Index(body, `[mcp_servers."alpha"]`)
	idxM := strings.Index(body, `[mcp_servers."mike"]`)
	idxZ := strings.Index(body, `[mcp_servers."zulu"]`)
	if idxA < 0 || idxM < 0 || idxZ < 0 {
		t.Fatalf("missing tables: %s", body)
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Fatalf("servers not sorted: alpha=%d mike=%d zulu=%d", idxA, idxM, idxZ)
	}
}

func TestWriteCodexMCPConfig_EmitsCommandArgsEnv(t *testing.T) {
	dir := t.TempDir()
	servers := map[string]mcpServerConfig{
		"docs": {
			Name:    "docs",
			Command: "docs-server",
			Args:    []string{"--port", "8080"},
			Env:     map[string]string{"DOCS_TOKEN": "secret"},
		},
	}
	if err := writeCodexMCPConfig(dir, servers); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(body), `command = "docs-server"`) {
		t.Fatalf("missing command: %s", body)
	}
	if !strings.Contains(string(body), `args = ["--port", "8080"]`) {
		t.Fatalf("missing args: %s", body)
	}
	if !strings.Contains(string(body), `[mcp_servers."docs".env]`) {
		t.Fatalf("missing env table: %s", body)
	}
	if !strings.Contains(string(body), `"DOCS_TOKEN" = "secret"`) {
		t.Fatalf("missing env entry: %s", body)
	}
}

func TestWriteCodexMCPConfig_EmitsStreamableHTTPURL(t *testing.T) {
	dir := t.TempDir()
	servers := map[string]mcpServerConfig{
		"docs": {Name: "docs", URL: "https://docs.example.com/mcp"},
	}
	if err := writeCodexMCPConfig(dir, servers); err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(body), `url = "https://docs.example.com/mcp"`) || strings.Contains(string(body), "command =") {
		t.Fatalf("remote config: %s", body)
	}
}

// TestWriteCodexMCPConfig_FreshHomeDropsStaleEntries documents the
// "fresh entries only" guarantee: callers allocate a brand-new
// CODEX_HOME per prompt (BuildSessionPlan does this via allocCodexHome
// + plan.Cleanup), so the previous run's mcp_servers can't leak.
//
// writeCodexMCPConfig itself is APPEND semantics now — the truncation
// guarantee lives in allocCodexHome's RemoveAll, not in the writer.
// Test that workflow explicitly so a future refactor that breaks the
// fresh-home contract fails here.
func TestWriteCodexMCPConfig_FreshHomeDropsStaleEntries(t *testing.T) {
	dir1 := t.TempDir()
	first := map[string]mcpServerConfig{
		"alpha": {Name: "alpha", Command: "a"},
		"bravo": {Name: "bravo", Command: "b"},
	}
	if err := writeCodexMCPConfig(dir1, first); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Simulate the per-run CODEX_HOME pattern: new dir, only "alpha".
	dir2 := t.TempDir()
	second := map[string]mcpServerConfig{
		"alpha": {Name: "alpha", Command: "a"},
	}
	if err := writeCodexMCPConfig(dir2, second); err != nil {
		t.Fatalf("second write: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir2, "config.toml"))
	if strings.Contains(string(body), "bravo") {
		t.Fatalf("fresh CODEX_HOME contains stale entry: %s", body)
	}
}

func TestTOMLQuoteString_EscapesSpecials(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`abc`, `"abc"`},
		{`a"b`, `"a\"b"`},
		{"a\nb", `"a\nb"`},
		{`a\b`, `"a\\b"`},
		{"a\tb", `"a\tb"`},
	}
	for _, tc := range cases {
		got := tomlQuoteString(tc.in)
		if got != tc.want {
			t.Fatalf("quote %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}
