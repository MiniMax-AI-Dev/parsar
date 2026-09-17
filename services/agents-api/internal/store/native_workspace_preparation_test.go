package store_test

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (a *nativeHarnessArtifact) observeReadPreparation(t *testing.T, ctx context.Context, peer *gateway.Session, request proto.PromptRequestPayload, root string) {
	t.Helper()
	info, found, known := peer.AgentKindStatus(request.AgentKind)
	if !known || !found || !info.Capabilities.WorkspaceReadPreparation {
		t.Fatal("native daemon omitted read preparation capability")
	}
	stable := filepath.Join(root, "parsar-daemon", "agent-sessions", request.AgentStateKey)
	before := nativeReadStateHashes(t, stable)
	read := proto.PromptRequestPayload{AgentKind: request.AgentKind, AgentStateKey: request.AgentStateKey,
		RemoteEnvironment: request.RemoteEnvironment, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true}
	var owner nativeHarnessOwner
	var state string
	_, events, _ := daemonPreparedRemotePromptWithReady(t, ctx, peer, read, nil, func(handle string) bool {
		owner = a.current(t)
		var err error
		state, err = os.Readlink(filepath.Join("/proc", strconv.Itoa(owner.PID), "cwd"))
		if err != nil || !strings.HasPrefix(state, filepath.Join(root, "parsar-daemon", "workspace-read")+"/") {
			t.Fatal("native reader did not use owned temporary state")
		}
		result, err := peer.ListWorkspaceDirectory(ctx, proto.WorkspaceReadPayload{EnvironmentID: a.environment, Handle: handle, MaxEntries: proto.WorkspaceDirectoryMaxEntries})
		if err != nil || result.Outcome != "completed" || result.Directory == nil || result.Directory.Truncated {
			t.Fatal("temporary native directory read failed", err, result.Outcome, result.ErrorCode)
		}
		found := false
		for _, entry := range result.Directory.Entries {
			if entry.Name == "retained.txt" && entry.Kind == "file" && entry.SizeBytes != nil && *entry.SizeBytes == int64(len("remote-file-content\n")) {
				found = true
			}
		}
		if !found {
			t.Fatal("read preparation omitted the real-model generated file")
		}
		return false
	}, nil)
	if owner.PID == 0 || state == "" || daemonRemotePreparationFailed(events) {
		t.Fatal("temporary native preparation did not complete")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("release acknowledged before temporary state removal", err)
	}
	if _, err := os.Stat(filepath.Join("/proc", strconv.Itoa(owner.PID))); !os.IsNotExist(err) {
		t.Fatal("release acknowledged before native process exit", err)
	}
	if !maps.Equal(before, nativeReadStateHashes(t, stable)) {
		t.Fatal("temporary read changed original configuration or native history")
	}
	a.proof["read_only_preparation"] = map[string]any{"owner": owner, "released": true, "temporary_state_removed": true, "stable_state_unchanged": true, "real_model_file_observed": true}
}

func nativeReadStateHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			result[relative] = nativeHarnessFileHash(t, path)
		}
		return nil
	})
	if err != nil || len(result) == 0 {
		t.Fatal("cannot observe existing native state", err)
	}
	return result
}
