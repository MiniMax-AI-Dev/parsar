package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func (a *nativeHarnessArtifact) observeDaemonReads(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase string, retained bool) {
	t.Helper()
	target := proto.WorkspaceReadPayload{EnvironmentID: a.environment, Handle: a.handle, MaxBytes: proto.WorkspaceReadMaxBytes}
	if a.runID != "" {
		target.Handle, target.RunID = "", a.runID
	}
	cases := []struct {
		path string
		data []byte
	}{
		{"bounded-read.bin", nativeHarnessReadBinary()},
		{"bounded-empty.bin", []byte{}},
	}
	if retained {
		cases = append(cases, struct {
			path string
			data []byte
		}{"retained.txt", []byte("remote-file-content\n")})
	}
	for _, check := range cases {
		target.Path = check.path
		result, err := a.peer.ReadWorkspaceFile(ctx, target)
		want := check.data[:min(len(check.data), target.MaxBytes)]
		if err != nil || result.Outcome != "completed" || !result.CloseAcknowledged ||
			!bytes.Equal(result.Data, want) || result.Truncated != (len(check.data) > target.MaxBytes) {
			t.Fatal("daemon native workspace read differs", phase, check.path, err, result.Outcome, result.ErrorCode)
		}
		digest := sha256.Sum256(result.Data)
		observations, _ := a.proof["daemon_read_observations"].([]map[string]any)
		a.proof["daemon_read_observations"] = append(observations, map[string]any{
			"phase": phase, "path": check.path, "owner": owner, "handle": target.Handle, "run_id": target.RunID,
			"bytes": len(result.Data), "sha256": hex.EncodeToString(digest[:]), "truncated": result.Truncated, "close_acknowledged": true,
		})
	}
	for _, check := range []struct{ environment, path, code string }{
		{uuid.NewString(), "bounded-read.bin", "resource_unavailable"},
		{a.environment, "missing-" + uuid.NewString(), "not_found"},
	} {
		target.EnvironmentID, target.Path = check.environment, check.path
		result, err := a.peer.ReadWorkspaceFile(ctx, target)
		if err != nil || result.Outcome != "rejected" || result.ErrorCode != check.code || len(result.Data) != 0 {
			t.Fatal("daemon read rejection differs", phase, err, result.Outcome, result.ErrorCode)
		}
	}
	if a.current(t) != owner {
		t.Fatal("daemon workspace read replaced the native execution owner")
	}
	a.observeDaemonDirectories(t, ctx, owner, phase, retained)
}
