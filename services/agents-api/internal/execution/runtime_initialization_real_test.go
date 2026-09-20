package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type installerObservation struct {
	sandbox.Provider
	t *testing.T
}

func (p installerObservation) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	result, err := p.Provider.RunCommand(ctx, r, c)
	if err != nil || result.ExitCode != 0 {
		p.t.Logf("trusted fixture installer exit=%d stdout=%q stderr=%q error=%v", result.ExitCode, result.Stdout, result.Stderr, err)
	}
	return result, err
}

func TestRealE2BInitialFileInstaller(t *testing.T) {
	keyFile, template := os.Getenv("PARSAR_E2B_TEST_KEY_FILE"), os.Getenv("PARSAR_E2B_TEST_TEMPLATE")
	if keyFile == "" || template == "" {
		t.Skip("actual E2B account and qualified Runtime template required")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal("private E2B key unavailable")
	}
	provider, err := e2b.New(e2b.Config{InstallationID: uuid.NewString(), APIKey: strings.TrimSpace(string(key)), Template: template, LeaseSeconds: 7200})
	if err != nil {
		t.Fatal(err)
	}
	p := installerObservation{Provider: provider, t: t}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	b := sandbox.Bootstrap{Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://example.com/api/v1", Credential: uuid.NewString(), NetworkAccess: "enabled"}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := p.Kill(ctx, b.Reference); err != nil {
			t.Error(err)
		}
	})
	if _, err := p.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	for _, body := range [][]byte{{}, []byte("binary\x00\xff\n"), bytes.Repeat([]byte{0xA5}, 50<<20)} {
		file := store.InitialFileMetadata{Path: "/workspace/nested/input.bin"}
		size := int64(len(body))
		file.SizeBytes = &size
		operation, stop := context.WithTimeout(ctx, 2*time.Minute)
		err := installInitialFile(operation, p, b.Reference, file, body)
		stop()
		if err != nil {
			t.Fatal("actual shared installer", err)
		}
		result, err := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"sha256sum", "/environment/workspace/nested/input.bin"}})
		digest := sha256.Sum256(body)
		if err != nil || result.ExitCode != 0 || !strings.HasPrefix(result.Stdout, hex.EncodeToString(digest[:])) {
			t.Fatal("installed bytes differ", err)
		}
	}
	result, err := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"/usr/bin/python3", "-I", "-S", "-c", `from pathlib import Path
outside = Path('/tmp/initial-file-outside')
outside.mkdir()
(outside / 'data').write_text('preserved')
Path('/environment/workspace/escape-parent').symlink_to(outside, target_is_directory=True)
Path('/environment/workspace/escape-file').symlink_to(outside / 'data')
`}})
	if err != nil || result.ExitCode != 0 {
		t.Fatal("symlink fixture", err)
	}
	for _, destination := range []string{"/workspace/escape-parent/data", "/workspace/escape-file"} {
		size := int64(7)
		if err := installInitialFile(ctx, p, b.Reference, store.InitialFileMetadata{Path: destination, SizeBytes: &size}, []byte("changed")); err == nil {
			t.Fatal("initialization followed symlink", destination)
		}
	}
	result, err = p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"cat", "/tmp/initial-file-outside/data"}})
	if err != nil || result.ExitCode != 0 || result.Stdout != "preserved" {
		t.Fatal("initialization changed bytes outside the workspace", err)
	}

}
