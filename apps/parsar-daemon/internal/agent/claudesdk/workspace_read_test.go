//go:build unix

package claudesdk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func runWorkspaceReadHelper(scanner *bufio.Scanner, state, mode string) {
	for scanner.Scan() {
		var req struct {
			Type, ID, Path string
			Max            int `json:"max_bytes"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &req)
		if req.Type == "start" {
			_ = os.WriteFile(filepath.Join(state, "read-started"), []byte("{}"), 0600)
			continue
		}
		_ = os.WriteFile(filepath.Join(state, "read-admitted"), []byte("{}"), 0600)
		if mode == "read-held" {
			for {
				if _, err := os.Stat(filepath.Join(state, "read-release")); err == nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
		}
		if mode == "read-exit" {
			return
		}
		data := bytes.Repeat([]byte{0, 255, 1, 128}, req.Max/4)
		if req.Path == "empty" {
			data = nil
		}
		encoded := base64.StdEncoding.EncodeToString(data)
		truncated := req.Path == "prefix"
		response := map[string]any{"type": "workspace_read", "id": req.ID, "data_base64": encoded, "truncated": truncated}
		switch req.Path {
		case "uncertain":
			response = map[string]any{"type": "workspace_read", "id": req.ID, "error": "uncertain"}
		case "bad-base64":
			response["data_base64"] = "!"
		case "wrong-id":
			response["id"] = "other"
		case "extra":
			response["extra"] = true
		case "oversize":
			response["data_base64"] = base64.StdEncoding.EncodeToString(make([]byte, req.Max+1))
		case "invalid":
			response = map[string]any{"type": "workspace_read", "id": req.ID, "error": "invalid"}
		}
		_ = json.NewEncoder(os.Stdout).Encode(response)
	}
}

func readPreparation(t *testing.T, mode string) (*prepared, Config) {
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

func TestWorkspaceReadPreparedBoundsAndBinary(t *testing.T) {
	p, _ := readPreparation(t, "read-normal")
	for _, path := range []string{"", "/absolute", "../escape", "a//b", "a/./b", "a\\b", "a\x00b", strings.Repeat("界", 3000)} {
		if _, err := p.ReadWorkspaceFile(t.Context(), path, 4); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatalf("accepted %q: %v", path, err)
		}
	}
	for _, limit := range []int{0, -1, workspaceReadMaxBytes + 1} {
		if _, err := p.ReadWorkspaceFile(t.Context(), "file", limit); err != agent.ErrWorkspaceReadInvalid {
			t.Fatal(limit, err)
		}
	}
	for _, path := range []string{"file", "prefix", "empty"} {
		result, err := p.ReadWorkspaceFile(t.Context(), path, workspaceReadMaxBytes)
		expected := bytes.Repeat([]byte{0, 255, 1, 128}, workspaceReadMaxBytes/4)
		if path == "empty" {
			expected = nil
		}
		if err != nil || !bytes.Equal(result.Data, expected) || result.Truncated != (path == "prefix") {
			t.Fatal(path, err, len(result.Data), result.Truncated)
		}
	}
	if _, err := p.ReadWorkspaceFile(t.Context(), "invalid", 4); err != agent.ErrWorkspaceReadInvalid {
		t.Fatal(err)
	}
	if _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadDetachAndTransfer(t *testing.T) {
	p, config := readPreparation(t, "read-held")
	ctx, detach := context.WithCancel(t.Context())
	defer detach()
	result := make(chan error, 1)
	go func() { _, err := p.ReadWorkspaceFile(ctx, "file", 4); result <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
	detach()
	if _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); err != agent.ErrWorkspaceReadBusy {
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
	if _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); err != agent.ErrWorkspaceReadUnavailable {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		t.Fatal("caller detach discarded native wait", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(config.StateDir, "read-release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-started"))
	if _, err := running.(agent.WorkspaceReader).ReadWorkspaceFile(t.Context(), "file", 4); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadUnknownAndRelease(t *testing.T) {
	for _, path := range []string{"uncertain", "wrong-id", "bad-base64", "oversize", "extra"} {
		t.Run(path, func(t *testing.T) {
			p, _ := readPreparation(t, "read-normal")
			result, err := p.ReadWorkspaceFile(t.Context(), path, 4)
			if err != agent.ErrWorkspaceReadUncertain || len(result.Data) != 0 {
				t.Fatal(result, err)
			}
			if _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); err != agent.ErrWorkspaceReadUncertain && err != agent.ErrWorkspaceReadUnavailable {
				t.Fatal(err)
			}
		})
	}
	for _, mode := range []string{"read-held", "read-exit"} {
		t.Run(mode, func(t *testing.T) {
			p, config := readPreparation(t, mode)
			done := make(chan error, 1)
			go func() { _, err := p.ReadWorkspaceFile(t.Context(), "file", 4); done <- err }()
			waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
			if mode == "read-held" {
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

func TestWorkspaceReadDeadlineStopsOwnerBeforeUnknown(t *testing.T) {
	p, config := readPreparation(t, "read-held")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.ReadWorkspaceFile(ctx, "file", 4); done <- err }()
	waitPreparationFile(t, filepath.Join(config.StateDir, "read-admitted"))
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
