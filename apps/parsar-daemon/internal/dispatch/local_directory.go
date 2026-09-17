package dispatch

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type localDirectoryPreparation struct{}

func prepareLocalDirectory(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
	return localDirectoryPreparation{}, nil
}

func (localDirectoryPreparation) Start(context.Context, string, string, chan<- proto.Envelope) (agent.Session, error) {
	return nil, agent.ErrWorkspaceReadUnsupported
}

func (localDirectoryPreparation) Close() error { return nil }
