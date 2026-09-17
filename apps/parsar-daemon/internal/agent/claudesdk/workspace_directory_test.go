//go:build unix

package claudesdk

import (
	"bufio"

	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func runWorkspaceDirectoryHelper(scanner *bufio.Scanner, state, mode string) {
	for scanner.Scan() {
		var req struct {
			Type, ID string
			Path     string `json:"directory"`
			Max      int    `json:"max_entries"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &req)
		if req.Type == "start" {
			_ = os.WriteFile(filepath.Join(state, "directory-started"), []byte("{}"), 0600)
			continue
		}
		_ = os.WriteFile(filepath.Join(state, "directory-admitted"), []byte("{}"), 0600)
		if mode == "directory-held" {
			for {
				if _, err := os.Stat(filepath.Join(state, "directory-release")); err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		if mode == "directory-exit" {
			return
		}
		entries := []map[string]any{{"name": "file", "kind": "file", "size_bytes": 3}}
		if req.Path == "empty" {
			entries = []map[string]any{}
		}
		response := map[string]any{"type": "workspace_directory", "id": req.ID, "entries": entries, "truncated": false}
		switch req.Path {
		case "uncertain", "invalid", "not_found", "permission":
			response = map[string]any{"type": "workspace_directory", "id": req.ID, "error": req.Path}
		case "bad-kind":
			entries[0]["kind"] = "unknown"
		case "wrong-id":
			response["id"] = "other"
		case "extra":
			response["extra"] = true
		case "bad-size":
			entries[0]["size_bytes"] = -1
		case "missing-size":
			delete(entries[0], "size_bytes")
		case "duplicate":
			response["entries"] = append(entries, entries[0])
		case "escape-name":
			entries[0]["name"] = "../secret"
		case "null":
			response["entries"] = nil
		}

		_ = json.NewEncoder(os.Stdout).Encode(response)
	}
}

func directoryPreparation(t *testing.T, mode string) (*prepared, Config) {
	t.Helper()
	config := preparationFixture(t, mode)
	resource, err := NewPreparationFactory(config)(t.Context(), preparationRequest())
	if err != nil {
		t.Fatal(err)
	}
	p := resource.(*prepared)
	t.Cleanup(func() { _ = p.Cancel(context.Background()) })
	return p, config
}

func TestWorkspaceDirectoryPreparedBoundsAndMetadata(t *testing.T) {
	p, _ := directoryPreparation(t, "directory-normal")
	for _, path := range []string{"/absolute", "../escape", "a//b", "a/./b", "a\\b", "a\x00b", strings.Repeat("界", 3000)} {
		if _, err := p.ListWorkspaceDirectory(t.Context(), path, 4); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatalf("accepted %q: %v", path, err)
		}
	}
	for _, limit := range []int{0, -1, workspaceDirectoryMaxEntries + 1} {
		if _, err := p.ListWorkspaceDirectory(t.Context(), "", limit); err != agent.ErrWorkspaceReadInvalid {
			t.Fatal(limit, err)
		}
	}
	result, err := p.ListWorkspaceDirectory(t.Context(), "", 4)
	if err != nil || result.Truncated || len(result.Entries) != 1 || result.Entries[0].SizeBytes == nil || *result.Entries[0].SizeBytes != 3 {
		t.Fatal(result, err)
	}
	empty, err := p.ListWorkspaceDirectory(t.Context(), "empty", 4)
	if err != nil || len(empty.Entries) != 0 || empty.Entries == nil {
		t.Fatal(empty, err)
	}
	for path, expected := range map[string]error{"not_found": fs.ErrNotExist, "permission": fs.ErrPermission, "invalid": agent.ErrWorkspaceReadInvalid} {
		if _, err := p.ListWorkspaceDirectory(t.Context(), path, 4); err != expected {
			t.Fatal(path, err)
		}
	}
	if _, err := p.ListWorkspaceDirectory(t.Context(), "", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDirectoryDetachAndTransfer(t *testing.T) {
	p, config := directoryPreparation(t, "directory-held")
	ctx, detach := context.WithCancel(t.Context())
	defer detach()
	result := make(chan error, 1)
	go func() { _, err := p.ListWorkspaceDirectory(ctx, "file", 4); result <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "directory-admitted"))
	detach()
	if _, err := p.ListWorkspaceDirectory(t.Context(), "file", 4); err != agent.ErrWorkspaceReadBusy {
		t.Fatal(err)
	}
	out := make(chan proto.Envelope, 16)
	running, err := p.Start(t.Context(), "run", "hello", out)
	if err != nil {
		t.Fatal(err)
	}
	if p.Close() != nil {
		t.Fatal("transferred Close failed")
	}
	if _, err := p.ListWorkspaceDirectory(t.Context(), "file", 4); err != agent.ErrWorkspaceReadUnavailable {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatal("caller detach discarded native wait", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "directory-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitPreparationFile(t, filepath.Join(config.StateDir, "directory-started"))
	if _, err := running.(agent.WorkspaceDirectoryLister).ListWorkspaceDirectory(t.Context(), "file", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceDirectoryUnknownAndRelease(t *testing.T) {
	for _, path := range []string{"uncertain", "wrong-id", "bad-kind", "bad-size", "missing-size", "duplicate", "escape-name", "null", "extra"} {
		t.Run(path, func(t *testing.T) {
			p, _ := directoryPreparation(t, "directory-normal")
			result, err := p.ListWorkspaceDirectory(t.Context(), path, 4)
			if err != agent.ErrWorkspaceReadUncertain || len(result.Entries) != 0 {
				t.Fatal(result, err)
			}
			if _, err := p.ListWorkspaceDirectory(t.Context(), "file", 4); err != agent.ErrWorkspaceReadUncertain && err != agent.ErrWorkspaceReadUnavailable {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"directory-held", "directory-exit"} {
		t.Run(mode, func(t *testing.T) {
			p, config := directoryPreparation(t, mode)
			done := make(chan error, 1)
			go func() { _, err := p.ListWorkspaceDirectory(t.Context(), "file", 4); done <- err }()
			waitPreparationFile(t, filepath.Join(config.StateDir, "directory-admitted"))
			if mode == "directory-held" {
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != agent.ErrWorkspaceReadUncertain {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("native waiter lost on release")
			}
		})
	}
}

func TestWorkspaceDirectoryDeadlineStopsOwnerBeforeUnknown(t *testing.T) {
	p, config := directoryPreparation(t, "directory-held")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.ListWorkspaceDirectory(ctx, "file", 4); done <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "directory-admitted"))
	if err := <-done; err != agent.ErrWorkspaceReadUncertain {
		t.Fatal(err)
	}
	if p.session.process.Context().Err() == nil {
		t.Fatal("uncertain deadline returned before owner cancellation")
	}
	if _, err := p.Start(t.Context(), "late", "hello", make(chan proto.Envelope, 8)); err == nil {
		t.Fatal("unknown owner accepted a new Start")
	}
}
