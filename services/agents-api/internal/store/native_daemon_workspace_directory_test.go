package store_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func (a *nativeHarnessArtifact) observeDaemonDirectories(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase string, retained bool) {
	t.Helper()
	target := proto.WorkspaceReadPayload{EnvironmentID: a.environment, Handle: a.handle, MaxEntries: proto.WorkspaceDirectoryMaxEntries}
	if a.runID != "" {
		target.Handle, target.RunID = "", a.runID
	}
	for _, limit := range []int{proto.WorkspaceDirectoryMaxEntries, 1} {
		target.MaxEntries = limit
		result, err := a.peer.ListWorkspaceDirectory(ctx, target)
		if err != nil || result.Outcome != "completed" || !result.CloseAcknowledged ||
			!proto.ValidWorkspaceDirectory(result.Directory, limit) || result.Directory.Truncated != (limit == 1) {
			t.Fatal("daemon native directory read differs", phase, limit, err, result.Outcome, result.ErrorCode)
		}
		if limit > 1 {
			files := make(map[string]int64)
			for _, entry := range result.Directory.Entries {
				if entry.Kind == "file" && entry.SizeBytes != nil {
					files[entry.Name] = *entry.SizeBytes
				}
			}
			want := map[string]int64{"bounded-read.bin": int64(len(nativeHarnessReadBinary())), "bounded-empty.bin": 0}
			if retained {
				want["retained.txt"] = int64(len("remote-file-content\n"))
			}
			for name, size := range want {
				if got, ok := files[name]; !ok || got != size {
					t.Fatal("daemon directory omitted native file metadata", phase, name, got, size)
				}
			}
		}
		observations, _ := a.proof["daemon_directory_observations"].([]map[string]any)
		a.proof["daemon_directory_observations"] = append(observations, map[string]any{
			"phase": phase, "owner": owner, "handle": target.Handle, "run_id": target.RunID,
			"max_entries": limit, "directory": result.Directory, "close_acknowledged": result.CloseAcknowledged,
		})
	}
	for _, check := range []struct{ environment, path, code string }{
		{uuid.NewString(), "", "resource_unavailable"},
		{a.environment, "missing-" + uuid.NewString(), "not_found"},
		{a.environment, "../outside", "invalid_request"},
	} {
		target.EnvironmentID, target.Path = check.environment, check.path
		result, err := a.peer.ListWorkspaceDirectory(ctx, target)
		if err != nil || result.Outcome != "rejected" || result.ErrorCode != check.code || result.Directory != nil || len(result.Data) != 0 {
			t.Fatal("daemon directory rejection differs", phase, err, result.Outcome, result.ErrorCode)
		}
	}
	if a.current(t) != owner {
		t.Fatal("daemon directory read replaced the native execution owner")
	}
}
