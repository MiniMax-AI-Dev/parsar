package store_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func (a *nativeHarnessArtifact) observeDirectories(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase, local string, retained bool) {
	t.Helper()
	for _, name := range []string{"directory-fixture", "directory-empty"} {
		if err := os.MkdirAll(filepath.Join(local, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(local, "directory-fixture", "child.bin"), []byte{0, 1, 255}, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(local, "directory-outside")
	outside := "/tmp/parsar-directory-isolation-" + a.environment
	if err := exec.CommandContext(ctx, "docker", "exec", a.container, "mkdir", "-p", outside+"/child").Run(); err != nil {
		t.Fatal("outside directory fixture was not created", err)
	}
	if err := exec.CommandContext(ctx, "docker", "exec", a.container, "test", "-d", outside+"/child").Run(); err != nil {
		t.Fatal("outside directory fixture does not exist", err)
	}
	if err := os.Symlink(outside, link); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	type directoryResult struct {
		Entries []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Size *int64 `json:"size_bytes"`
		} `json:"entries"`
		Truncated bool `json:"truncated"`
	}
	for _, check := range []struct {
		path      string
		limit     int
		required  map[string]int64
		truncated bool
	}{
		{"", 4096, map[string]int64{"bounded-read.bin": int64(len(nativeHarnessReadBinary())), "bounded-empty.bin": 0}, false},
		{"directory-fixture", 16, map[string]int64{"child.bin": 3}, false},
		{"directory-empty", 16, map[string]int64{}, false},
		{"", 1, nil, true},
	} {
		response := a.request(t, ctx, owner, map[string]any{"environment_id": a.environment, "path": check.path, "operation": "list_directory", "max_entries": check.limit}, 8<<20)
		var directory directoryResult
		if response.Error != "" || len(response.Read) != 0 || len(response.Metadata) != 0 || json.Unmarshal(response.Directory, &directory) != nil || directory.Entries == nil || len(directory.Entries) > check.limit || directory.Truncated != check.truncated {
			t.Fatal("native directory observation failed", phase, check.path, response.Error, string(response.Directory))
		}
		files := make(map[string]int64)
		for _, entry := range directory.Entries {
			if filepath.Base(entry.Name) != entry.Name {
				t.Fatal("directory entry escaped", entry.Name)
			}
			if entry.Kind == "file" && entry.Size != nil {
				files[entry.Name] = *entry.Size
			}
		}
		if check.path == "" && !check.truncated && retained {
			check.required["retained.txt"] = int64(len("remote-file-content\n"))
		}
		for name, size := range check.required {
			if got, ok := files[name]; !ok || got != size {
				t.Fatal("directory metadata mismatch", phase, name, got, size)
			}
		}
		if check.path == "directory-empty" && len(directory.Entries) != 0 {
			t.Fatal("empty directory changed")
		}
		observations, _ := a.proof["directory_observations"].([]map[string]any)
		a.proof["directory_observations"] = append(observations, map[string]any{"phase": phase, "path": check.path, "owner": owner, "result": response.Directory})
	}
	for _, path := range []string{"../outside", "/etc", "directory-outside", "directory-outside/child"} {
		response := a.request(t, ctx, owner, map[string]any{"environment_id": a.environment, "path": path, "operation": "list_directory", "max_entries": 16}, 16<<10)
		if response.Error == "" || len(response.Directory) != 0 {
			t.Fatal("directory isolation admitted outside view", path)
		}
	}
	if a.current(t) != owner {
		t.Fatal("directory operation replaced native execution owner")
	}
}
