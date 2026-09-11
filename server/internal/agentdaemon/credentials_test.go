package agentdaemon

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type runtimeReader struct {
	row       store.RuntimeRead
	found     bool
	err       error
	requested string
}

func (r *runtimeReader) GetRuntime(_ context.Context, id string) (store.RuntimeRead, bool, error) {
	r.requested = id
	return r.row, r.found, r.err
}

func TestProductCredentialsPreserveGatewayAuthorization(t *testing.T) {
	lookupErr := errors.New("lookup failed")
	// Fixed SHA-256 fixture verifies compatibility with already-persisted hashes.
	const hash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	cases := []struct {
		name      string
		config    map[string]any
		kind      string
		found     bool
		lookupErr error
		wantErr   error
	}{
		{"paired", map[string]any{"runner_credential_hash": hash}, "agent_daemon", true, nil, nil},
		{"missing hash", nil, "agent_daemon", true, nil, gateway.ErrAuthBadCredential},
		{"malformed hash", map[string]any{"runner_credential_hash": 123}, "agent_daemon", true, nil, gateway.ErrAuthBadCredential},
		{"wrong runtime", map[string]any{"runner_credential_hash": hash}, "local", true, nil, gateway.ErrAuthWrongRuntimeType},
		{"deleted or absent", nil, "", false, nil, gateway.ErrAuthUnknownDevice},
		{"store failure", nil, "", false, lookupErr, lookupErr},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			reader := &runtimeReader{row: store.RuntimeRead{ID: "device", WorkspaceID: "workspace", Name: "paired device", Type: tt.kind, Config: tt.config}, found: tt.found, err: tt.lookupErr}
			auth := gateway.NewAuthenticator(Credentials{Runtimes: reader})
			got, err := auth.AuthenticateBearer(context.Background(), "device", " abc\n")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got error %v, want %v", err, tt.wantErr)
			}
			if reader.requested != "device" {
				t.Fatalf("lookup device = %q", reader.requested)
			}
			if tt.wantErr == nil && (got.DeviceID != "device" || got.WorkspaceID != "workspace" || got.Name != "paired device") {
				t.Fatalf("identity changed: %+v", got)
			}
		})
	}
}
