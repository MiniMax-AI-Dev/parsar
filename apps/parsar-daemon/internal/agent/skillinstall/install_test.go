package skillinstall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInstallInlineSkillMaterializesAndCaches(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: proof\ndescription: proof\n---\nReturn DSH_SKILL_PROOF.\n"
	descriptor := Descriptor{Name: "proof", Version: "1", Content: content}

	first, err := Install(context.Background(), testLogger(), root, []Descriptor{descriptor})
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if len(first.Warnings) != 0 || len(first.SkillDirs) != 1 {
		t.Fatalf("first result = %+v", first)
	}
	dir := first.SkillDirs[0]
	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if string(got) != content {
		t.Fatalf("SKILL.md = %q, want %q", got, content)
	}
	sum := sha256.Sum256([]byte(content))
	wantKey := "proof@" + hex.EncodeToString(sum[:])
	cacheKey, err := os.ReadFile(filepath.Join(dir, ".cache-key"))
	if err != nil {
		t.Fatalf("read cache key: %v", err)
	}
	if string(cacheKey) != wantKey {
		t.Fatalf("cache key = %q, want %q", cacheKey, wantKey)
	}

	if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Install(context.Background(), testLogger(), root, []Descriptor{descriptor})
	if err != nil {
		t.Fatalf("cached install: %v", err)
	}
	if len(second.SkillDirs) != 1 {
		t.Fatalf("cached result = %+v", second)
	}
	if _, err := os.Stat(filepath.Join(dir, "sentinel")); err != nil {
		t.Fatalf("cache miss unexpectedly replaced directory: %v", err)
	}
}

func TestDecodeAcceptsInlineAndRejectsAmbiguousSource(t *testing.T) {
	raw := []any{
		map[string]any{"name": "inline", "version": "1", "content": "body"},
		map[string]any{
			"name": "ambiguous", "content": "body", "download_url": "https://example.test/x.zip",
			"sha256": strings.Repeat("a", 64),
		},
	}
	got, warnings := Decode(raw)
	if len(got) != 1 || got[0].Name != "inline" || got[0].Content != "body" {
		t.Fatalf("decoded = %+v", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "exactly one") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestPruneTreatsInlineSkillAsManaged(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "old")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, ".cache-key"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Prune(root, []Descriptor{{Name: "current", Content: "body"}}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old managed directory still exists: %v", err)
	}
}
