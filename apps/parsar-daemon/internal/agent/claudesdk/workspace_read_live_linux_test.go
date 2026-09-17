//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

type liveWorkspaceRead struct {
	Stage     string `json:"stage"`
	Path      string `json:"path"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	Truncated bool   `json:"truncated"`
}

func liveWorkspaceReadFixtures(t *testing.T, root string) {
	t.Helper()
	liveWorkspaceDirectoryFixtures(t, root)
	for path, data := range map[string][]byte{"read-binary.bin": bytes.Repeat([]byte{0, 255, 128, 1}, 64), "read-empty.bin": {}, "read-large.bin": bytes.Repeat([]byte{0, 255, 128, 1}, (workspaceReadMaxBytes+40)/4)} {
		if err := os.WriteFile(filepath.Join(root, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func liveWorkspaceReads(t *testing.T, ctx context.Context, reader agent.WorkspaceReader, root, stage string, paths ...string) []liveWorkspaceRead {
	t.Helper()
	liveWorkspaceDirectories(t, ctx, reader, root, stage)
	var proof []liveWorkspaceRead
	for _, path := range paths {
		expected, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		result, err := reader.ReadWorkspaceFile(ctx, path, workspaceReadMaxBytes)
		if err != nil {
			t.Fatal("actual native workspace read failed", stage, path, err)
		}
		truncated := len(expected) > workspaceReadMaxBytes
		if truncated {
			expected = expected[:workspaceReadMaxBytes]
		}
		if !bytes.Equal(result.Data, expected) || result.Truncated != truncated {
			t.Fatal("native bytes differ", stage, path, len(result.Data), result.Truncated)
		}
		proof = append(proof, liveWorkspaceRead{Stage: stage, Path: path, Bytes: len(result.Data), SHA256: fmt.Sprintf("%x", sha256.Sum256(result.Data)), Truncated: result.Truncated})
	}
	return proof
}
