package execution

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (d *Dispatcher) captureCompletedArtifacts(ctx context.Context, peer *gateway.Session, session store.Session, environment store.Environment, bound store.ExecutionDevice, turnID string, result Result, status string) (Result, string) {
	if status != store.TurnCompleted || !LocalWorkspaceConfiguration(environment.Configuration) {
		return result, status
	}
	owner, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	err := d.Store.BeginTurnArtifactCapture(owner, session.TenantID, session.ID, turnID, result.AppliedThrough)
	if err == nil {
		err = d.withPreparedWorkspace(owner, peer, session, environment, bound, func(ctx context.Context, handle string) error {
			return peer.ExportWorkspaceOutputs(ctx, proto.WorkspaceExportPayload{Handle: handle, EnvironmentID: environment.ID}, func(body io.Reader) error {
				return d.Store.StageTurnArtifacts(ctx, session.TenantID, session.ID, turnID, environment.ID, body)
			})
		})
	}
	if err == nil {
		return result, status
	}
	// Do not expose native diagnostics or publish partial output after a failed capture.
	result.ErrorCode = "artifact_capture_failed"
	if errors.Is(err, store.ErrUnappliedInputs) {
		result.ErrorCode = "input_not_applied"
	}
	status = store.TurnFailed
	check, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if turn, err := d.Store.GetTurn(check, session.TenantID, session.ID, turnID); err == nil && !turn.CancelRequestedAt.IsZero() {
		status = store.TurnCancelled
	}
	return result, status
}
