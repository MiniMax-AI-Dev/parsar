package gateway

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// RuntimeTypeAgentDaemon is the value runtimes.type takes for rows
// that back an agent_daemon device. The DB has no CHECK constraint on
// runtimes.type so this is a Go-side invariant; the listRuntimes admin
// endpoint's allowlist must be updated alongside it.
const RuntimeTypeAgentDaemon = "agent_daemon"

var ErrAuthMissingParams = errors.New("agentdaemon auth: missing device_id / token / version")

var ErrAuthUnknownDevice = errors.New("agentdaemon auth: unknown device")

// ErrAuthWrongRuntimeType is returned when the runtimes row exists
// but is registered as a different runtime_type.
var ErrAuthWrongRuntimeType = errors.New("agentdaemon auth: runtime_type mismatch")

// ErrAuthBadCredential folds "no credential on row" together with
// "wrong credential" so probes can't distinguish them.
var ErrAuthBadCredential = errors.New("agentdaemon auth: bad credential")

var ErrAuthIncompatibleVersion = errors.New("agentdaemon auth: incompatible protocol version")

// RuntimeStore is the slice of *store.Store the gateway needs for auth.
type RuntimeStore interface {
	GetRuntime(ctx context.Context, runtimeID string) (store.RuntimeRead, bool, error)
}

// AuthenticatedRuntime is the result of a successful credential check.
type AuthenticatedRuntime struct {
	DeviceID    string
	WorkspaceID string
	Name        string
}

// Authenticator validates the (device_id, token, version) trio that
// the daemon presents on /agent-daemon/ws upgrade and on
// /agent-daemon/bootstrap.
type Authenticator struct {
	store RuntimeStore
}

func NewAuthenticator(store RuntimeStore) *Authenticator {
	return &Authenticator{store: store}
}

// AuthenticateBearer runs the runtime credential check. Returns a
// populated AuthenticatedRuntime on success or one of the typed Err*
// sentinels on failure so the caller can map them to the appropriate
// WS close code / HTTP status.
func (a *Authenticator) AuthenticateBearer(ctx context.Context, deviceID, bearer string) (AuthenticatedRuntime, error) {
	if a == nil || a.store == nil {
		return AuthenticatedRuntime{}, fmt.Errorf("agentdaemon auth: authenticator not configured")
	}
	if deviceID == "" || bearer == "" {
		return AuthenticatedRuntime{}, ErrAuthMissingParams
	}
	rt, ok, err := a.store.GetRuntime(ctx, deviceID)
	if err != nil {
		return AuthenticatedRuntime{}, fmt.Errorf("agentdaemon auth: store: %w", err)
	}
	if !ok {
		return AuthenticatedRuntime{}, ErrAuthUnknownDevice
	}
	if rt.Type != RuntimeTypeAgentDaemon {
		return AuthenticatedRuntime{}, ErrAuthWrongRuntimeType
	}
	storedHash, _ := rt.Config["runner_credential_hash"].(string)
	if storedHash == "" {
		// Pairing never completed, or someone wiped the credential
		// out-of-band. Fail closed.
		return AuthenticatedRuntime{}, ErrAuthBadCredential
	}
	presented := store.HashRuntimeCredential(bearer)
	if subtle.ConstantTimeCompare([]byte(presented), []byte(storedHash)) != 1 {
		return AuthenticatedRuntime{}, ErrAuthBadCredential
	}
	return AuthenticatedRuntime{
		DeviceID:    rt.ID,
		WorkspaceID: rt.WorkspaceID,
		Name:        rt.Name,
	}, nil
}
