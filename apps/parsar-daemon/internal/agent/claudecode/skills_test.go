package claudecode

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// validSkillZipBytes is a minimal SKILL.md-rooted zip.
func validSkillZipBytes(t *testing.T) []byte {
	return buildPluginZipBytes(t, []pluginZipFile{
		{Name: "SKILL.md", Body: "---\nname: code-review\ndescription: Review code\n---\nBody"},
	})
}

func TestInstallSkills_HappyPath_ExtractsAndStampsCacheKey(t *testing.T) {
	t.Parallel()
	body := validSkillZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), workDir, []skillDescriptor{
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("installSkills: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	dir := filepath.Join(workDir, ".claude", "skills", "code-review")
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	stamped, err := os.ReadFile(filepath.Join(dir, ".cache-key"))
	if err != nil {
		t.Fatalf("read cache-key: %v", err)
	}
	want := "code-review@" + sha256Hex(body)
	if string(stamped) != want {
		t.Fatalf("cache-key = %q, want %q", stamped, want)
	}
}

func TestInstallSkills_CacheHitSkipsDownload(t *testing.T) {
	t.Parallel()
	body := validSkillZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	desc := []skillDescriptor{
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	}
	if _, err := installSkills(context.Background(), discardLogger(), workDir, desc); err != nil {
		t.Fatalf("first install: %v", err)
	}
	hitsAfterFirst := srv.Hits()
	if hitsAfterFirst != 1 {
		t.Fatalf("first install hits = %d, want 1", hitsAfterFirst)
	}
	if _, err := installSkills(context.Background(), discardLogger(), workDir, desc); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if got := srv.Hits(); got != hitsAfterFirst {
		t.Fatalf("cache should prevent second download; got %d extra hits", got-hitsAfterFirst)
	}
}

func TestInstallManagedSkillsPrunesInactiveEntries(t *testing.T) {
	body := validSkillZipBytes(t)
	srv := startPluginServer(t, body)
	root := t.TempDir()
	stale := filepath.Join(root, "old-skill")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := InstallManagedSkills(context.Background(), discardLogger(), root, []any{
		map[string]any{
			"name": "code-review", "version": "1.0.0",
			"download_url": srv.URL, "sha256": sha256Hex(body),
		},
	})
	if err != nil {
		t.Fatalf("InstallManagedSkills: %v", err)
	}
	if len(res.SkillDirs) != 1 || res.SkillDirs[0] != filepath.Join(root, "code-review") {
		t.Fatalf("skill dirs = %v", res.SkillDirs)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale skill still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "code-review", "SKILL.md")); err != nil {
		t.Fatalf("active skill missing: %v", err)
	}
}

func TestInstallSkills_SHA256MismatchDemotesToWarning(t *testing.T) {
	t.Parallel()
	body := validSkillZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), workDir, []skillDescriptor{
		// Wrong sha256 (all-zero pattern is 64 hex chars, never matches body)
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: "0000000000000000000000000000000000000000000000000000000000000000"},
	})
	if err != nil {
		t.Fatalf("installSkills should not hard-error on sha mismatch: %v", err)
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(res.Warnings), res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(workDir, ".claude", "skills", "code-review", "SKILL.md")); err == nil {
		t.Fatal("SKILL.md should not exist after sha mismatch")
	}
}

func TestInstallSkills_EmptyListIsNoop(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	res, err := installSkills(context.Background(), discardLogger(), workDir, nil)
	if err != nil {
		t.Fatalf("empty install: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
}

func TestDecodeSkillDescriptors_ArrayShape(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":         "code-review",
			"version":      "1.0.0",
			"download_url": "https://x",
			"sha256":       "0000000000000000000000000000000000000000000000000000000000000000",
		},
	}
	got, warns := decodeSkillDescriptors(raw)
	if len(got) != 1 || len(warns) != 0 {
		t.Fatalf("got=%+v warns=%v", got, warns)
	}
}

func TestDecodeSkillDescriptors_NilAndWrongType(t *testing.T) {
	if got, warns := decodeSkillDescriptors(nil); got != nil || warns != nil {
		t.Fatalf("nil raw should be (nil, nil), got (%v, %v)", got, warns)
	}
	if got, warns := decodeSkillDescriptors("not-array"); got != nil || len(warns) != 1 {
		t.Fatalf("string raw should warn, got got=%v warns=%v", got, warns)
	}
}
