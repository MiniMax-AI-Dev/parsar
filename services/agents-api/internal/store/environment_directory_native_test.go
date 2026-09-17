package store_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativePreparedWorkerDirectory(t *testing.T) {
	testNativePreparedWorkerRemoteEnvironment(t, true)
}

func prepareWorkerDirectoryArtifact(t *testing.T, native string) *nativeHarnessArtifact {
	t.Helper()
	artifact, helper := os.Getenv("PARSAR_CODEX_HARNESS_ARTIFACT"), os.Getenv("PARSAR_DIRECTORY_HELPER_ARTIFACT")
	if !filepath.IsAbs(artifact) || !filepath.IsAbs(helper) {
		t.Skip("qualified private harness and directory helper required")
	}
	t.Setenv("PARSAR_CODEX_HARNESS_BIN", artifact)
	t.Setenv("PARSAR_CODEX_DIRECTORY_HELPER", "/usr/local/bin/agents-api-codex-directory")
	return newNativeHarnessArtifact(t, native, artifact)
}

func verifyWorkerDirectoryReads(t *testing.T, ctx context.Context, h *dispatchHarness, w *execution.Worker, registry *codex.Registry, environment store.Environment, root string) map[string]any {
	t.Helper()
	wait := func() {
		awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor after directory release", func() bool {
			connected, err := registry.Connected(ctx, h.tenant, environment.ID)
			return err == nil && connected
		})
	}
	wait()
	stable := filepath.Join(root, "parsar-daemon", "agent-sessions", "agents-api-"+h.session.ID)
	before := nativeReadStateHashes(t, stable)
	session, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || session.LastTurn == nil {
		t.Fatal("missing completed native Turn")
	}
	result, err := w.ReadEnvironmentDirectory(ctx, environment, "")
	if err != nil || result.Truncated {
		t.Fatal("Core native directory read failed", err)
	}
	found := false
	for _, entry := range result.Entries {
		if entry.Name == "retained.txt" && entry.Kind == "file" && entry.SizeBytes != nil && *entry.SizeBytes == int64(len("remote-file-content\n")) {
			found = true
		}
	}
	if !found {
		t.Fatal("Core directory omitted the real-model generated file")
	}
	if _, err := w.ReadEnvironmentDirectory(ctx, environment, "missing-directory"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("Core missing directory result", err)
	}
	wait()
	after, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || after.LastTurn == nil || after.LastTurn.ID != session.LastTurn.ID || after.LastTurn.Status != store.TurnCompleted || !maps.Equal(before, nativeReadStateHashes(t, stable)) {
		t.Fatal("directory read changed execution history or configuration")
	}
	remaining, err := filepath.Glob(filepath.Join(root, "parsar-daemon", "workspace-read", "read-*"))
	if err != nil || len(remaining) != 0 {
		t.Fatal("Core read returned before temporary state removal")
	}
	return map[string]any{"real_model_file_observed": true, "missing_directory_verified": true, "stable_state_unchanged": true, "turn_unchanged": true, "temporary_state_removed": true}
}
