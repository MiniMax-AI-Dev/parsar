package interaction

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type RunReader interface {
	GetAgentRun(ctx context.Context, runID string) (store.AgentRunDetailRead, error)
}

type RegistryDelivery struct {
	Runs     RunReader
	Registry *connector.Registry
}

func (d RegistryDelivery) SubmitPermission(ctx context.Context, runID string, decision connector.PermissionDecision) error {
	conn, err := d.connector(ctx, runID)
	if err != nil {
		return err
	}
	return conn.SubmitPermission(ctx, decision)
}

func (d RegistryDelivery) SubmitPromptForUserChoice(ctx context.Context, runID string, decision connector.PromptForUserChoiceDecision) error {
	conn, err := d.connector(ctx, runID)
	if err != nil {
		return err
	}
	return conn.SubmitPromptForUserChoice(ctx, decision)
}

func (d RegistryDelivery) connector(ctx context.Context, runID string) (connector.AgentConnector, error) {
	if d.Runs == nil || d.Registry == nil {
		return nil, errors.New("interaction delivery is unavailable")
	}
	run, err := d.Runs.GetAgentRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	return d.Registry.Get(run.ConnectorType)
}
