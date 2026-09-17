package localworkspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestNativeLocalDirectoryConfinement(t *testing.T) {
	helper := os.Getenv("PARSAR_LOCAL_DIRECTORY_TEST_HELPER")
	if helper == "" {
		t.Skip("actual pinned directory helper required")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "nested", "data.bin"), filepath.Join(outside, "secret")} {
		if err := os.WriteFile(path, []byte{0, 1, 255, 17}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	b, err := New(uuid.NewString(), uuid.NewString(), root, helper)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.ListWorkspaceDirectory(t.Context(), "nested", 10)
	if err != nil || got.Truncated || len(got.Entries) != 1 || got.Entries[0].Name != "data.bin" || got.Entries[0].SizeBytes == nil || *got.Entries[0].SizeBytes != 4 {
		t.Fatalf("native file metadata: %+v %v", got, err)
	}
	if _, err := b.ListWorkspaceDirectory(t.Context(), "escape", 10); err == nil {
		t.Fatal("native helper followed an external symlink")
	}
	got, err = b.ListWorkspaceDirectory(t.Context(), "", 1)
	if err != nil || !got.Truncated || len(got.Entries) != 1 {
		t.Fatalf("native directory bound: %+v %v", got, err)
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root + "-original") })
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListWorkspaceDirectory(t.Context(), "", 10); err == nil {
		t.Fatal("native helper followed a replaced root")
	}
}
