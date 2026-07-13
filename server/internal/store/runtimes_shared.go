package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

const SharedRuntimeName = "local-sandbox"

const sharedRuntimeConfigKey = "shared_runtime"

var ErrNoWorkspaceForSharedRuntime = errors.New("runtime: no workspace exists yet for shared-runtime pairing")

type PairSharedRuntimeInput struct {
	Hostname        string
	Version         string
	RunnerPublicKey string
}

func (s *Store) PairSharedRuntime(ctx context.Context, input PairSharedRuntimeInput) (RuntimeRead, error) {
	pubkey := strings.TrimSpace(input.RunnerPublicKey)
	if pubkey == "" {
		return RuntimeRead{}, errors.New("runtime: runner_public_key required")
	}
	workspaceID, err := sqlc.New(s.db).GetOldestActiveWorkspaceID(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeRead{}, ErrNoWorkspaceForSharedRuntime
		}
		return RuntimeRead{}, err
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return RuntimeRead{}, fmt.Errorf("runtime: workspace_id: %w", err)
	}
	cfg := map[string]any{
		sharedRuntimeConfigKey: true,
		"runner_public_key":    pubkey,
		"runtime_pool":         "local_default",
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return RuntimeRead{}, fmt.Errorf("runtime: marshal config: %w", err)
	}
	now := time.Now().UTC()
	row, err := sqlc.New(s.db).UpsertSharedRuntime(ctx, sqlc.UpsertSharedRuntimeParams{
		ID:          mustUUID(newID()),
		WorkspaceID: workspaceUUID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        SharedRuntimeName,
		Provider:    RuntimeProviderAgentDaemon,
		Version:     strings.TrimSpace(input.Version),
		Hostname:    strings.TrimSpace(input.Hostname),
		Config:      cfgJSON,
		Now:         timestamptz(now),
	})
	if err != nil {
		return RuntimeRead{}, err
	}
	runtime := runtimeReadFromUpsertSharedRow(row)
	payload := runtimeAuditPayload(runtime)
	payload["liveness"] = runtime.Liveness
	s.emitRuntimeLifecycleAudit(now, auditRuntimePaired, runtime, payload)
	return runtime, nil
}

func IsSharedRuntime(rt RuntimeRead) bool {
	v, ok := rt.Config[sharedRuntimeConfigKey].(bool)
	return ok && v
}

func runtimeReadFromUpsertSharedRow(r sqlc.UpsertSharedRuntimeRow) RuntimeRead {
	return runtimeReadFromRow(runtimeReadRow(r))
}
