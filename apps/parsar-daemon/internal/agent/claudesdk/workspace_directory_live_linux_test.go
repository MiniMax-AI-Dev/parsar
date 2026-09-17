//go:build linux

package claudesdk

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

func liveWorkspaceDirectoryFixtures(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"directory-empty", "directory-denied"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "directory-denied"), 0); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"directory-inside-link": "directory-empty", "directory-outside-link": filepath.Dir(root)} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
}

func liveWorkspaceDirectories(t *testing.T, ctx context.Context, reader agent.WorkspaceReader, root, stage string) {
	t.Helper()
	lister, ok := reader.(agent.WorkspaceDirectoryLister)
	if !ok {
		t.Fatal("workspace directory owner missing")
	}
	result, err := lister.ListWorkspaceDirectory(ctx, "", 1000)
	if err != nil || result.Truncated {
		t.Fatal("workspace directory failed", stage, err)
	}
	expected, err := os.ReadDir(root)
	if err != nil || len(expected) != len(result.Entries) {
		t.Fatal("directory length differs", stage, err)
	}
	found := make(map[string]agent.WorkspaceDirectoryEntry)
	for _, entry := range result.Entries {
		found[entry.Name] = entry
	}
	for _, entry := range expected {
		actual, ok := found[entry.Name()]
		if !ok {
			t.Fatal("missing directory entry", stage, entry.Name())
		}
		stat, err := os.Lstat(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case stat.Mode().IsRegular():
			// The command heartbeat is intentionally changing during live execution.
			if actual.Kind != "file" || actual.SizeBytes == nil || (entry.Name() != "heartbeat.txt" && *actual.SizeBytes != stat.Size()) {
				t.Fatal("file metadata differs", stage, actual)
			}
		case stat.IsDir():
			if actual.Kind != "directory" || actual.SizeBytes != nil {
				t.Fatal(actual)
			}
		case stat.Mode()&os.ModeSymlink != 0:
			if actual.Kind != "symlink" || actual.SizeBytes != nil {
				t.Fatal(actual)
			}
		}
	}
	empty, err := lister.ListWorkspaceDirectory(ctx, "directory-empty", 2)
	if err != nil || empty.Truncated || len(empty.Entries) != 0 {
		t.Fatal(empty, err)
	}
	bounded, err := lister.ListWorkspaceDirectory(ctx, "", 1)
	if err != nil || !bounded.Truncated || len(bounded.Entries) != 1 {
		t.Fatal(bounded, err)
	}
	for _, path := range []string{"directory-inside-link", "directory-outside-link"} {
		if _, err := lister.ListWorkspaceDirectory(ctx, path, 2); !errors.Is(err, fs.ErrPermission) && !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatal("directory denial missing", path, err)
		}
	}
	if _, err := lister.ListWorkspaceDirectory(ctx, "directory-denied", 2); !errors.Is(err, fs.ErrPermission) {
		t.Fatal("permission denial missing", err)
	}
	if _, err := lister.ListWorkspaceDirectory(ctx, "directory-missing", 2); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "directory-"+stage+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
