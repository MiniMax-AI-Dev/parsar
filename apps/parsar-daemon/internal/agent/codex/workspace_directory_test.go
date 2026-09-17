package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

const workspaceDirectorySuccess = `{"directory":{"entries":[{"name":"result.bin","kind":"file","size_bytes":4},{"name":"subdir","kind":"directory","size_bytes":null}],"truncated":false}}` + "\n"

func TestWorkspaceDirectoryValidatesCompleteResponse(t *testing.T) {
	result, err := decodeWorkspaceDirectory([]byte(workspaceDirectorySuccess), 2)
	if err != nil || result.Truncated || len(result.Entries) != 2 || result.Entries[0].SizeBytes == nil || *result.Entries[0].SizeBytes != 4 || result.Entries[1].SizeBytes != nil {
		t.Fatal(result, err)
	}
	for _, frame := range []string{
		`{"directory":{"entries":[],"truncated":null}}`,
		`{"directory":{"entries":null,"truncated":false}}`,
		`{"directory":{"entries":[{"name":"../other","kind":"file","size_bytes":4}],"truncated":false}}`,
		`{"directory":{"entries":[{"name":"file","kind":"file"}],"truncated":false}}`,
		`{"directory":{"entries":[{"name":"link","kind":"symlink","size_bytes":1}],"truncated":false}}`,
		`{"directory":{"entries":[{"name":"file","kind":"file","size_bytes":-1}],"truncated":false}}`,
		`{"directory":{"entries":[{"name":"same","kind":"directory"},{"name":"same","kind":"directory"}],"truncated":false}}`,
		workspaceDirectorySuccess + `{}`,
	} {
		if got, err := decodeWorkspaceDirectory([]byte(frame), 2); !errors.Is(err, agent.ErrWorkspaceReadUncertain) || len(got.Entries) != 0 {
			t.Fatalf("malformed directory succeeded: %+v %v", got, err)
		}
	}
	if _, err := decodeWorkspaceDirectory([]byte(workspaceDirectorySuccess), 1); !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal("oversized response accepted", err)
	}
	result, err = decodeWorkspaceDirectory([]byte(`{"directory":{"entries":[],"truncated":true}}`), 1)
	if err != nil || !result.Truncated {
		t.Fatal("truncated observation lost", result, err)
	}
}

func TestWorkspaceDirectorySharesReadOwnership(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := session.ListWorkspaceDirectory(ctx, "", 2)
		done <- err
	}()
	conn, frame := workspaceReadConnection(t, listener)
	if conn == nil {
		return
	}
	defer conn.Close()
	var request map[string]any
	if json.Unmarshal(frame, &request) != nil || request["environment_id"] != "frozen-environment" || request["path"] != "" || request["operation"] != "list_directory" || request["max_entries"] != float64(2) {
		t.Fatalf("directory binding changed: %s", frame)
	}
	cancel()
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadBusy) {
		t.Fatal("directory detach freed shared slot", err)
	}
	select {
	case err := <-done:
		t.Fatal("directory wait discarded", err)
	default:
	}
	_, _ = conn.Write([]byte(workspaceDirectorySuccess))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/root", "a/../b", "a//b", ".", "../x", "a\\b", "a\n"} {
		if _, err := session.ListWorkspaceDirectory(t.Context(), path, 2); !errors.Is(err, agent.ErrWorkspaceReadInvalid) {
			t.Fatal("invalid directory admitted", path, err)
		}
	}
}

func TestWorkspaceDirectoryUncertaintyFencesFileReads(t *testing.T) {
	session, listener := workspaceReadFixture(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, _ := workspaceReadConnection(t, listener)
		if conn != nil {
			_, _ = conn.Write([]byte("{broken}\n"))
			_ = conn.Close()
		}
	}()
	if _, err := session.ListWorkspaceDirectory(t.Context(), "", 2); !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal(err)
	}
	<-done
	if _, err := session.ReadWorkspaceFile(t.Context(), "file", 4); !errors.Is(err, agent.ErrWorkspaceReadUncertain) {
		t.Fatal("uncertainty lost between operations", err)
	}
}
