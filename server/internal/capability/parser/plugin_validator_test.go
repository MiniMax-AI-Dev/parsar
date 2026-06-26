package parser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// pluginZipFile is one entry in a synthetic plugin zip. Empty contents
// mean "zero-byte file" (directory marker).
type pluginZipFile struct {
	Name     string
	Contents string
}

func buildPluginZip(t *testing.T, files []pluginZipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatalf("zip create %q: %v", f.Name, err)
		}
		if _, err := w.Write([]byte(f.Contents)); err != nil {
			t.Fatalf("zip write %q: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// validPluginFiles is the baseline fixture for happy-path tests.
func validPluginFiles() []pluginZipFile {
	return []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Contents: `{
  "name": "my-plugin",
  "displayName": "My Plugin",
  "version": "1.0.0",
  "description": "A test plugin",
  "author": {"name": "Alice"},
  "keywords": ["test", "demo"]
}`},
	}
}

func containsString(ss []string, substr string) bool {
	for _, s := range ss {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func TestValidatePluginZip_EmptyBuffer(t *testing.T) {
	t.Parallel()
	res, err := ValidatePluginZip(nil)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res.Valid {
		t.Fatal("expected valid=false on empty buf")
	}
	if !containsString(res.Errors, "empty upload") {
		t.Fatalf("errors = %v, want 'empty upload' hint", res.Errors)
	}
}

func TestValidatePluginZip_ExceedsCap(t *testing.T) {
	t.Parallel()
	huge := make([]byte, MaxPluginZipBytes+1)
	res, err := ValidatePluginZip(huge)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res.Valid {
		t.Fatal("expected valid=false on oversize buf")
	}
	if !containsString(res.Errors, "exceeds") {
		t.Fatalf("errors = %v, want size cap hint", res.Errors)
	}
}

func TestValidatePluginZip_NotAZip(t *testing.T) {
	t.Parallel()
	res, _ := ValidatePluginZip([]byte("this is plainly not a zip file"))
	if res.Valid {
		t.Fatal("expected valid=false on non-zip input")
	}
	if !containsString(res.Errors, "not a valid zip") {
		t.Fatalf("errors = %v, want 'not a valid zip' hint", res.Errors)
	}
}

func TestValidatePluginZip_HappyPath(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, validPluginFiles())
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("expected zero warnings; got %v", res.Warnings)
	}
	if res.Manifest.Name != "my-plugin" {
		t.Fatalf("manifest.name = %q, want my-plugin", res.Manifest.Name)
	}
	if res.Manifest.Version != "1.0.0" {
		t.Fatalf("manifest.version = %q, want 1.0.0", res.Manifest.Version)
	}
	if res.Manifest.Author.Name != "Alice" {
		t.Fatalf("manifest.author.name = %q, want Alice", res.Manifest.Author.Name)
	}
}

func TestValidatePluginZip_MissingManifest(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, []pluginZipFile{
		{Name: "README.md", Contents: "no manifest here"},
	})
	res, _ := ValidatePluginZip(buf)
	if res.Valid {
		t.Fatal("expected valid=false without manifest")
	}
	if !containsString(res.Errors, "missing .claude-plugin/plugin.json") {
		t.Fatalf("errors = %v", res.Errors)
	}
}

func TestValidatePluginZip_MalformedManifest(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Contents: `{not json`},
	})
	res, _ := ValidatePluginZip(buf)
	if res.Valid {
		t.Fatal("expected valid=false on bad JSON")
	}
	if !containsString(res.Errors, "not valid JSON") {
		t.Fatalf("errors = %v", res.Errors)
	}
}

func TestValidatePluginZip_MissingNameField(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Contents: `{"version": "1.0.0"}`},
	})
	res, _ := ValidatePluginZip(buf)
	if res.Valid {
		t.Fatal("expected valid=false when name missing")
	}
	if !containsString(res.Errors, `missing required field "name"`) {
		t.Fatalf("errors = %v", res.Errors)
	}
}

func TestValidatePluginZip_NameRejectsUnsafeChars(t *testing.T) {
	t.Parallel()
	// Only path separators, control chars, and reserved dot names are
	// rejected; everything else matches official Claude Code's relaxed
	// naming.
	bad := []string{
		"with/slash",  // forward slash (path separator)
		"with\\slash", // backslash (path separator)
		"with\x00nul", // NUL byte (string truncation)
		"with\ttab",   // control char (tab)
		"with\nnl",    // control char (newline)
		".",           // reserved
		"..",          // reserved
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]string{"name": name})
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			buf := buildPluginZip(t, []pluginZipFile{
				{Name: ".claude-plugin/plugin.json", Contents: string(payload)},
			})
			res, _ := ValidatePluginZip(buf)
			if res.Valid {
				t.Fatalf("expected valid=false for name %q", name)
			}
			if len(res.Errors) == 0 {
				t.Fatalf("expected errors for name %q", name)
			}
		})
	}
}

func TestValidatePluginZip_NameAcceptsRelaxedShapes(t *testing.T) {
	t.Parallel()
	// Matches official Claude Code's relaxed naming rules — any
	// casing / language / punctuation that survives the unsafe-char
	// filter is accepted.
	good := []string{
		"MyPlugin",                     // capital letters
		"42leading",                    // leading digit
		"_leading",                     // underscore
		"-leading",                     // leading hyphen
		"trailing-",                    // trailing hyphen
		"double--dash",                 // consecutive hyphens
		"with space",                   // space
		"interview-question-generator", // canonical kebab still works
		"面试题目生成助手",                     // pure CJK
		"Plug-in 中英 1.0",               // mixed scripts + punctuation
	}
	for _, name := range good {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]string{"name": name})
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			buf := buildPluginZip(t, []pluginZipFile{
				{Name: ".claude-plugin/plugin.json", Contents: string(payload)},
			})
			res, _ := ValidatePluginZip(buf)
			if !res.Valid {
				t.Fatalf("expected valid=true for name %q, got errors: %v", name, res.Errors)
			}
			if res.Manifest.Name != name {
				t.Fatalf("manifest.Name = %q, want %q", res.Manifest.Name, name)
			}
		})
	}
}

func TestValidatePluginZip_NameKebabCase_HappyVariants(t *testing.T) {
	t.Parallel()
	good := []string{"my-plugin", "plugin", "plugin1", "a-b-c", "linter-v2", "x123-y456"}
	for _, name := range good {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			buf := buildPluginZip(t, []pluginZipFile{
				{Name: ".claude-plugin/plugin.json", Contents: `{"name":"` + name + `","version":"1.0.0"}`},
			})
			res, _ := ValidatePluginZip(buf)
			if !res.Valid {
				t.Fatalf("expected valid=true for name %q; errors=%v", name, res.Errors)
			}
		})
	}
}

func TestValidatePluginZip_ComponentsInsideClaudePluginDir_Warn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files, pluginZipFile{Name: ".claude-plugin/commands/test.md", Contents: "---\nname: test\n---\nbody"})
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true (warning, not error); errors=%v", res.Errors)
	}
	if !containsString(res.Warnings, "commands") {
		t.Fatalf("expected commands-in-.claude-plugin warning; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_CommandMissingFrontmatter_Warn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files,
		pluginZipFile{Name: "commands/with.md", Contents: "---\nname: with\n---\nbody"},
		pluginZipFile{Name: "commands/without.md", Contents: "Just a body, no frontmatter."},
	)
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if !containsString(res.Warnings, "commands/without.md") {
		t.Fatalf("expected warning on missing frontmatter; warnings=%v", res.Warnings)
	}
	if containsString(res.Warnings, "commands/with.md") {
		t.Fatalf("with-frontmatter file should NOT warn; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_SkillMissingSKILL_md_Warn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files, pluginZipFile{Name: "skills/mything/README.md", Contents: "not the skill manifest"})
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if !containsString(res.Warnings, "skills/mything") {
		t.Fatalf("expected warning on missing SKILL.md; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_SkillWithSKILL_md_NoWarn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files,
		pluginZipFile{Name: "skills/mything/SKILL.md", Contents: "---\nname: mything\ndescription: a thing\n---\nbody"},
	)
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if containsString(res.Warnings, "missing SKILL.md") {
		t.Fatalf("present SKILL.md should NOT warn; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_BadHooksJSON_Warn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files, pluginZipFile{Name: "hooks/hooks.json", Contents: `{garbage`})
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if !containsString(res.Warnings, "hooks/hooks.json") {
		t.Fatalf("expected warning on bad hooks.json; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_BadMcpJSON_Warn(t *testing.T) {
	t.Parallel()
	files := validPluginFiles()
	files = append(files, pluginZipFile{Name: ".mcp.json", Contents: `not json`})
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if !containsString(res.Warnings, ".mcp.json") {
		t.Fatalf("expected warning on bad .mcp.json; warnings=%v", res.Warnings)
	}
}

func TestValidatePluginZip_WrappingRootDirIsTransparent(t *testing.T) {
	t.Parallel()
	// `zip -r my-plugin/` wraps entries under `my-plugin/`; the
	// validator must strip that prefix silently.
	wrapped := []pluginZipFile{
		{Name: "my-plugin/.claude-plugin/plugin.json", Contents: `{"name":"my-plugin","version":"1.0.0"}`},
		{Name: "my-plugin/commands/test.md", Contents: "---\nname: test\n---\nbody"},
	}
	buf := buildPluginZip(t, wrapped)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true for wrapped zip; errors=%v", res.Errors)
	}
	if res.Manifest.Name != "my-plugin" {
		t.Fatalf("manifest.name = %q, want my-plugin", res.Manifest.Name)
	}
}

func TestValidatePluginZip_MacosxMetadataIgnored(t *testing.T) {
	t.Parallel()
	// __MACOSX/ sibling must not be treated as a wrapping root.
	files := []pluginZipFile{
		{Name: "__MACOSX/", Contents: ""},
		{Name: "__MACOSX/._plugin.json", Contents: "binary metadata blob"},
		{Name: ".claude-plugin/plugin.json", Contents: `{"name":"my-plugin","version":"1.0.0"}`},
	}
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
}

func TestValidatePluginZip_WindowsBackslashPaths(t *testing.T) {
	t.Parallel()
	files := []pluginZipFile{
		{Name: `.claude-plugin\plugin.json`, Contents: `{"name":"my-plugin","version":"1.0.0"}`},
	}
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true for backslash-pathed zip; errors=%v", res.Errors)
	}
}

func TestValidatePluginZip_AllWarningsAggregated(t *testing.T) {
	t.Parallel()
	// Confirms the validator doesn't short-circuit after the first
	// warning — all distinct warning paths must fire on one fixture.
	files := []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Contents: `{"name":"my-plugin","version":"1.0.0"}`},
		{Name: ".claude-plugin/commands/embedded.md", Contents: "---\nname: x\n---"},
		{Name: "commands/no-frontmatter.md", Contents: "no frontmatter here"},
		{Name: "skills/orphan/help.md", Contents: "no SKILL.md sibling"},
		{Name: "hooks/hooks.json", Contents: `{ broken`},
		{Name: ".mcp.json", Contents: `{ also broken`},
	}
	buf := buildPluginZip(t, files)
	res, _ := ValidatePluginZip(buf)
	if !res.Valid {
		t.Fatalf("expected valid=true (only warnings expected); errors=%v", res.Errors)
	}
	want := []string{
		"commands", // nested-under-.claude-plugin
		"commands/no-frontmatter.md",
		"skills/orphan",
		"hooks/hooks.json",
		".mcp.json",
	}
	for _, hint := range want {
		if !containsString(res.Warnings, hint) {
			t.Fatalf("missing warning containing %q; warnings=%v", hint, res.Warnings)
		}
	}
}
