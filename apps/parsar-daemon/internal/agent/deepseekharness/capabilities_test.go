package deepseekharness

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormaliseMCPRowsStdio(t *testing.T) {
	rows, err := normaliseMCPRows(map[string]any{
		"proof": map[string]any{
			"command": "node",
			"args":    []any{"/opt/mcp/proof.mjs"},
			"env":     map[string]any{"PROOF_TOKEN": "secret"},
		},
	}, "/workspace")
	if err != nil {
		t.Fatalf("normaliseMCPRows: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "@deepseek-ai/dsh-mcp-client" || rows[0].ID != "parsar-mcp-proof" {
		t.Fatalf("rows = %+v", rows)
	}
	cfg, ok := rows[0].Config.(mcpStdioConfig)
	if !ok {
		t.Fatalf("config = %T", rows[0].Config)
	}
	if cfg.Transport != "stdio" || cfg.ServerName != "proof" || cfg.Command != "node" || cfg.CWD != "/workspace" {
		t.Fatalf("config = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Args, []string{"/opt/mcp/proof.mjs"}) || cfg.Env["PROOF_TOKEN"] != "secret" {
		t.Fatalf("config = %+v", cfg)
	}
	if !cfg.FailOnStartupError || cfg.ToolCallTimeoutMS != 60_000 {
		t.Fatalf("startup policy = %+v", cfg)
	}
}

func TestNormaliseMCPRowsHTTPAndStableOrder(t *testing.T) {
	rows, err := normaliseMCPRows(map[string]any{
		"zeta": map[string]any{"url": "https://zeta.example/mcp"},
		"alpha": map[string]any{
			"url":     "https://alpha.example/mcp",
			"headers": map[string]any{"Authorization": "Bearer secret"},
		},
	}, "/workspace")
	if err != nil {
		t.Fatalf("normaliseMCPRows: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "parsar-mcp-alpha" || rows[1].ID != "parsar-mcp-zeta" {
		t.Fatalf("rows are not sorted: %+v", rows)
	}
	cfg, ok := rows[0].Config.(mcpHTTPConfig)
	if !ok {
		t.Fatalf("config = %T", rows[0].Config)
	}
	if cfg.Transport != "streamable-http" || cfg.ServerName != "alpha" || cfg.URL != "https://alpha.example/mcp" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.Headers["Authorization"] != "Bearer secret" || !cfg.FailOnStartupError {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestNormaliseMCPRowsRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want string
	}{
		{"invalid name", map[string]any{"not valid": map[string]any{"command": "node"}}, "invalid MCP server name"},
		{"both transports", map[string]any{"proof": map[string]any{"command": "node", "url": "https://example.com/mcp"}}, "cannot set both"},
		{"missing transport", map[string]any{"proof": map[string]any{}}, "missing command or url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normaliseMCPRows(tc.raw, "/workspace")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestMaterializeManagedSkillsInstallsCachesAndPrunes(t *testing.T) {
	var zipBody bytes.Buffer
	zw := zip.NewWriter(&zipBody)
	w, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("---\nname: parsar-proof\ndescription: proof\n---\nReturn DSH_SKILL_PROOF."))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(zipBody.Bytes())
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(zipBody.Bytes())
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	root := filepath.Join(home, "skills")
	old := filepath.Join(root, "old-managed")
	unmanaged := filepath.Join(root, "unmanaged")
	for _, dir := range []string{old, unmanaged} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(old, ".cache-key"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw := []any{map[string]any{
		"name": "parsar-proof", "version": "1", "download_url": srv.URL,
		"sha256": hex.EncodeToString(digest[:]),
	}}
	launch := serverLaunch{Home: home}
	if err := materializeManagedSkills(context.Background(), launch, raw); err != nil {
		t.Fatalf("first materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "parsar-proof", "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old managed directory was not pruned: %v", err)
	}
	if _, err := os.Stat(unmanaged); err != nil {
		t.Fatalf("unmanaged directory must be preserved: %v", err)
	}
	if err := materializeManagedSkills(context.Background(), launch, raw); err != nil {
		t.Fatalf("cached materialize: %v", err)
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
}

func TestMaterializeManagedSkillsInlineMarkdown(t *testing.T) {
	home := t.TempDir()
	content := "---\nname: parsar-proof\ndescription: proof\n---\nReturn DSH_SKILL_PROOF_20260820.\n"
	raw := []any{map[string]any{
		"name": "parsar-proof", "version": "1", "content": content,
	}}
	launch := serverLaunch{Home: home}
	if err := materializeManagedSkills(context.Background(), launch, raw); err != nil {
		t.Fatalf("materialize inline skill: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, "skills", "parsar-proof", "SKILL.md"))
	if err != nil {
		t.Fatalf("read inline SKILL.md: %v", err)
	}
	if string(got) != content {
		t.Fatalf("SKILL.md = %q, want %q", got, content)
	}
}
