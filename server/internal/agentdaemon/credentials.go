package agentdaemon

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type RuntimeReader interface {
	GetRuntime(context.Context, string) (store.RuntimeRead, bool, error)
}

// Credentials projects product runtime records onto the gateway's auth input.
type Credentials struct{ Runtimes RuntimeReader }

var _ gateway.RuntimeStore = Credentials{}

func (c Credentials) GetDeviceCredential(ctx context.Context, id string) (device.Credential, bool, error) {
	rt, ok, err := c.Runtimes.GetRuntime(ctx, id)
	if err != nil || !ok {
		return device.Credential{}, ok, err
	}
	hash, _ := rt.Config["runner_credential_hash"].(string)
	return device.Credential{ID: rt.ID, WorkspaceID: rt.WorkspaceID, Name: rt.Name, Type: rt.Type, CredentialHash: hash}, true, nil
}
