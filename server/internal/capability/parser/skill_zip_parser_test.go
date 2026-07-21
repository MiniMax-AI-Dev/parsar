package parser

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func buildZip(t *testing.T, entries []struct{ name, content string }) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatalf("zip create %q: %v", e.name, err)
		}
		if e.content != "" {
			if _, err := io.WriteString(f, e.content); err != nil {
				t.Fatalf("zip write %q: %v", e.name, err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

const minimalSkillMd = "---\n" +
	"name: code-reviewer\n" +
	"description: Review a diff and call out risky changes\n" +
	"---\n" +
	"You are a careful code reviewer.\n"

func TestParseSkillZip_HappyPath_SkillMdOnly(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	if res.Spec.Skill == nil {
		t.Fatal("spec.Skill nil")
	}
	if len(res.Spec.Skill.Files) != 0 {
		t.Fatalf("SKILL.md-only zip should not require supporting files, got %+v", res.Spec.Skill.Files)
	}
}

// TestParseSkillZip_HappyPath_RootLayout: SKILL.md + references/ + scripts/
// at the zip root. Output has stable sort order and correct Kind.
func TestParseSkillZip_HappyPath_RootLayout(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/log-recipes.md", "# Log recipes\nQuery patterns.\n"},
		{"references/db-recipes.md", "# DB recipes\nQuery patterns.\n"},
		{"scripts/setup.sh", "#!/usr/bin/env bash\necho ok\n"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if sk == nil {
		t.Fatalf("spec.Skill nil")
	}
	if sk.Slug != "code-reviewer" {
		t.Fatalf("slug: %q", sk.Slug)
	}
	if !strings.Contains(sk.Instruction, "careful code reviewer") {
		t.Fatalf("instruction missing body: %q", sk.Instruction)
	}
	if len(sk.Files) != 3 {
		t.Fatalf("want 3 files, got %d: %+v", len(sk.Files), sk.Files)
	}
	// Stable sort guarantees:
	wantPaths := []string{"references/db-recipes.md", "references/log-recipes.md", "scripts/setup.sh"}
	for i, want := range wantPaths {
		if sk.Files[i].Path != want {
			t.Fatalf("files[%d].Path: want %q, got %q", i, want, sk.Files[i].Path)
		}
	}
	if sk.Files[0].Kind != canonical.SkillFileKindMarkdown {
		t.Fatalf("references/*.md should be markdown, got %q", sk.Files[0].Kind)
	}
	if sk.Files[2].Kind != canonical.SkillFileKindScript {
		t.Fatalf("scripts/*.sh should be script, got %q", sk.Files[2].Kind)
	}
}

// TestParseSkillZip_HappyPath_WrappingRootDir: `zip -r my-skill.zip my-skill/`
// case — wrapping dir transparently stripped.
func TestParseSkillZip_HappyPath_WrappingRootDir(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"my-skill/SKILL.md", minimalSkillMd},
		{"my-skill/references/foo.md", "# foo\n"},
		{"my-skill/scripts/foo.py", "print('ok')\n"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if len(sk.Files) != 2 {
		t.Fatalf("want 2 files after strip, got %d: %+v", len(sk.Files), sk.Files)
	}
	for _, f := range sk.Files {
		if strings.HasPrefix(f.Path, "my-skill/") {
			t.Fatalf("wrapping prefix not stripped: %q", f.Path)
		}
	}
}

// TestParseSkillZip_HappyPath_WrappingRootDirWithDirEntries: regression for
// `zip -r foo foo/` zips that include bare directory entries. detectSingleRoot
// must skip dir-only entries (which lose their slash in normalizeZipPath)
// when inferring the wrapper, else it short-circuits on "my-skill" and
// SKILL.md fails to resolve.
func TestParseSkillZip_HappyPath_WrappingRootDirWithDirEntries(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"my-skill/", ""},
		{"my-skill/references/", ""},
		{"my-skill/SKILL.md", minimalSkillMd},
		{"my-skill/references/foo.md", "# foo\n"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if len(sk.Files) != 1 {
		t.Fatalf("want 1 file after strip, got %d: %+v", len(sk.Files), sk.Files)
	}
	if sk.Files[0].Path != "references/foo.md" {
		t.Fatalf("wrapping prefix not stripped: %q", sk.Files[0].Path)
	}
}

func TestParseSkillZip_DirectoryEntriesAreNotImported(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/", ""},
		{"references/foo.md", "# foo\n"},
		{"docs/", ""},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	if got := len(res.Spec.Skill.Files); got != 1 {
		t.Fatalf("want 1 imported file, got %d: %+v", got, res.Spec.Skill.Files)
	}
	if res.Spec.Skill.Files[0].Path != "references/foo.md" {
		t.Fatalf("directory marker was imported: %+v", res.Spec.Skill.Files)
	}
}

func TestParseSkillZip_MissingSkillMd_Rejected(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"references/foo.md", "no entry point\n"},
	})
	_, err := ParseSkillZip(buf)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("error should mention SKILL.md, got %v", err)
	}
}

func TestParseSkillZip_EmptyBuffer(t *testing.T) {
	_, err := ParseSkillZip(nil)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
}

func TestParseSkillZip_NotAZip(t *testing.T) {
	_, err := ParseSkillZip([]byte("not a zip at all"))
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
}

// TestParseSkillZip_OversizeZip_Rejected: 8 MiB cap is checked on len()
// before zip.NewReader, so this never touches the parser.
func TestParseSkillZip_OversizeZip_Rejected(t *testing.T) {
	buf := make([]byte, MaxSkillZipBytes+1)
	_, err := ParseSkillZip(buf)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("error should mention cap, got %v", err)
	}
}

// TestParseSkillZip_PathTraversal_Rejected: silently sanitising "../foo"
// would let a crafted zip overwrite neighbouring tenants' files if the
// runtime materialises the spec to disk. Reject outright.
func TestParseSkillZip_PathTraversal_Rejected(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/../../etc/passwd", "evil"},
	})
	_, err := ParseSkillZip(buf)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
	if !strings.Contains(err.Error(), "parent-directory") {
		t.Fatalf("error should mention parent-directory, got %v", err)
	}
}

func TestParseSkillZip_AbsolutePathRejected(t *testing.T) {
	for _, name := range []string{"/etc/passwd", `C:\\Windows\\system.ini`} {
		t.Run(name, func(t *testing.T) {
			buf := buildZip(t, []struct{ name, content string }{
				{"SKILL.md", minimalSkillMd},
				{name, "evil"},
			})
			_, err := ParseSkillZip(buf)
			if !errors.Is(err, ErrInvalidSkillZip) {
				t.Fatalf("want ErrInvalidSkillZip, got %v", err)
			}
			if !strings.Contains(err.Error(), "absolute path") {
				t.Fatalf("error should mention absolute path, got %v", err)
			}
		})
	}
}

func TestParseSkillZip_ImportsArbitrarySupportingDirectories(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/foo.md", "ref\n"},
		{"assets/screenshot.png", "binary blob"},
		{"docs/readme.md", "supporting documentation"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if len(sk.Files) != 3 {
		t.Fatalf("want all 3 supporting files, got %d: %+v", len(sk.Files), sk.Files)
	}
	wantPaths := []string{"assets/screenshot.png", "docs/readme.md", "references/foo.md"}
	for i, want := range wantPaths {
		if sk.Files[i].Path != want {
			t.Fatalf("files[%d].Path: want %q, got %q", i, want, sk.Files[i].Path)
		}
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "ignored") {
			t.Fatalf("arbitrary supporting directories should not be ignored: %q", w)
		}
	}
}

// TestParseSkillZip_MacOSMetadataStripped: Finder's `__MACOSX/` and
// `.DS_Store` should be silently dropped, not warned.
func TestParseSkillZip_MacOSMetadataStripped(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"__MACOSX/SKILL.md", "junk"},
		{".DS_Store", "junk"},
		{"references/.DS_Store", "junk"},
		{"references/foo.md", "ref\n"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if len(sk.Files) != 1 {
		t.Fatalf("want 1 file (only references/foo.md), got %d: %+v", len(sk.Files), sk.Files)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "__MACOSX") || strings.Contains(w, ".DS_Store") {
			t.Fatalf("macOS metadata should be silently dropped, not warned: %q", w)
		}
	}
}

// TestParseSkillZip_FrontmatterInvalid_StillImports: when YAML and the
// line-based fallback both recover nothing, Slug is left empty and the
// commit handler fills one in from the form name.
func TestParseSkillZip_FrontmatterInvalid_StillImports(t *testing.T) {
	badSkillMd := "---\n: not yaml :\n---\nbody\n"
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", badSkillMd},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("parse should succeed and let commit-time fallback decide slug, got %v", err)
	}
	if res.Spec.Skill.Slug != "" {
		t.Fatalf("slug should be empty when frontmatter is undecodable, got %q", res.Spec.Skill.Slug)
	}
	if !strings.Contains(res.Spec.Skill.Instruction, "body") {
		t.Fatalf("instruction body should still be populated, got %q", res.Spec.Skill.Instruction)
	}
	sawWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "frontmatter") {
			sawWarning = true
			break
		}
	}
	if !sawWarning {
		t.Fatalf("expected a frontmatter warning to surface to the UI, got %v", res.Warnings)
	}
}

// TestParseSkillZip_NoFrontmatter_StillImports: importable with empty Slug;
// commit derives the slug from the form name.
func TestParseSkillZip_NoFrontmatter_StillImports(t *testing.T) {
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", "just a body, no frontmatter\n"},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("parse should succeed when SKILL.md lacks frontmatter, got %v", err)
	}
	if res.Spec.Skill.Slug != "" {
		t.Fatalf("slug should be empty, got %q", res.Spec.Skill.Slug)
	}
}

func TestParseSkillZip_TooManyEntries_Rejected(t *testing.T) {
	entries := make([]struct{ name, content string }, 0, maxSkillEntryCount+2)
	entries = append(entries, struct{ name, content string }{"SKILL.md", minimalSkillMd})
	for i := range maxSkillEntryCount + 1 {
		entries = append(entries, struct{ name, content string }{
			name:    "references/f" + strings.Repeat("x", 1) + intToStr(i) + ".md",
			content: "x",
		})
	}
	buf := buildZip(t, entries)
	_, err := ParseSkillZip(buf)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
	if !strings.Contains(err.Error(), "entries") {
		t.Fatalf("error should mention entries, got %v", err)
	}
}

// TestParseSkillZip_PerEntryOversize_Skipped: one fat file is skipped with
// a warning rather than rejecting the whole zip.
func TestParseSkillZip_PerEntryOversize_Skipped(t *testing.T) {
	// 1.1 MiB just over the per-entry cap (still under the 8 MiB zip cap).
	big := strings.Repeat("x", int(maxSkillEntryBytes)+100)
	buf := buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/normal.md", "fine\n"},
		{"references/oversize.md", big},
	})
	res, err := ParseSkillZip(buf)
	if err != nil {
		t.Fatalf("ParseSkillZip: %v", err)
	}
	sk := res.Spec.Skill
	if len(sk.Files) != 1 || sk.Files[0].Path != "references/normal.md" {
		t.Fatalf("expected only normal.md, got %+v", sk.Files)
	}
	hasOversizeWarn := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "oversize.md") && strings.Contains(w, "cap") {
			hasOversizeWarn = true
		}
	}
	if !hasOversizeWarn {
		t.Fatalf("expected oversize warning, got warnings=%v", res.Warnings)
	}
}

// TestParseSkillZip_CumulativeOversize_Rejected: sum-of-decompressed cap.
// Each entry is under the per-entry guard; only the running tally trips.
// Reject (don't truncate) — a half-imported skill whose references break
// at runtime is worse than a clear oversize error.
func TestParseSkillZip_CumulativeOversize_Rejected(t *testing.T) {
	const perEntry = maxSkillEntryBytes / 2
	entryCount := int(maxSkillTotalDecompressedBytes/perEntry) + 2
	if entryCount >= maxSkillEntryCount {
		t.Skipf("perEntry=%d makes entryCount=%d collide with maxSkillEntryCount=%d — pick a smaller perEntry", perEntry, entryCount, maxSkillEntryCount)
	}
	chunk := strings.Repeat("y", int(perEntry))
	entries := make([]struct{ name, content string }, 0, entryCount+1)
	entries = append(entries, struct{ name, content string }{"SKILL.md", minimalSkillMd})
	for i := range entryCount {
		entries = append(entries, struct{ name, content string }{
			name:    "references/file" + intToStr(i) + ".md",
			content: chunk,
		})
	}
	buf := buildZip(t, entries)
	_, err := ParseSkillZip(buf)
	if !errors.Is(err, ErrInvalidSkillZip) {
		t.Fatalf("want ErrInvalidSkillZip, got %v", err)
	}
	if !strings.Contains(err.Error(), "total decompressed") {
		t.Fatalf("error should mention total decompressed, got %v", err)
	}
}

func intToStr(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
