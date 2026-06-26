package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type stubRuntimeStore struct {
	row    store.RuntimeRead
	ok     bool
	getErr error
}

func (s *stubRuntimeStore) GetRuntime(_ context.Context, _ string) (store.RuntimeRead, bool, error) {
	return s.row, s.ok, s.getErr
}

func TestAuthenticator_RejectsMissingParams(t *testing.T) {
	auth := NewAuthenticator(&stubRuntimeStore{})
	_, err := auth.AuthenticateBearer(context.Background(), "", "tok")
	if !errors.Is(err, ErrAuthMissingParams) {
		t.Fatalf("missing device_id: got %v", err)
	}
	_, err = auth.AuthenticateBearer(context.Background(), "dev", "")
	if !errors.Is(err, ErrAuthMissingParams) {
		t.Fatalf("missing bearer: got %v", err)
	}
}

func TestAuthenticator_RejectsUnknownDevice(t *testing.T) {
	auth := NewAuthenticator(&stubRuntimeStore{ok: false})
	_, err := auth.AuthenticateBearer(context.Background(), "missing", "tok")
	if !errors.Is(err, ErrAuthUnknownDevice) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthenticator_RejectsWrongRuntimeType(t *testing.T) {
	// A legacy local runtime row trying to dial in as agent_daemon
	// must fail — otherwise a paired local credential could open an
	// agent_daemon WS and bypass the device picker.
	row := store.RuntimeRead{
		ID:     "dev-1",
		Type:   "local",
		Config: map[string]any{"runner_credential_hash": store.HashRuntimeCredential("tok")},
	}
	auth := NewAuthenticator(&stubRuntimeStore{row: row, ok: true})
	_, err := auth.AuthenticateBearer(context.Background(), "dev-1", "tok")
	if !errors.Is(err, ErrAuthWrongRuntimeType) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthenticator_RejectsBadCredential(t *testing.T) {
	row := store.RuntimeRead{
		ID:     "dev-1",
		Type:   RuntimeTypeAgentDaemon,
		Config: map[string]any{"runner_credential_hash": store.HashRuntimeCredential("real-tok")},
	}
	auth := NewAuthenticator(&stubRuntimeStore{row: row, ok: true})
	_, err := auth.AuthenticateBearer(context.Background(), "dev-1", "wrong-tok")
	if !errors.Is(err, ErrAuthBadCredential) {
		t.Fatalf("got %v", err)
	}
	// Missing hash also folds into bad_credential so an attacker
	// can't distinguish "row exists but credential never stored"
	// from "credential mismatch".
	rowNoHash := row
	rowNoHash.Config = map[string]any{}
	auth = NewAuthenticator(&stubRuntimeStore{row: rowNoHash, ok: true})
	_, err = auth.AuthenticateBearer(context.Background(), "dev-1", "any-tok")
	if !errors.Is(err, ErrAuthBadCredential) {
		t.Fatalf("no-hash should fold to bad_credential, got %v", err)
	}
}

func TestAuthenticator_AcceptsValidCredential(t *testing.T) {
	row := store.RuntimeRead{
		ID:          "dev-1",
		WorkspaceID: "wks-1",
		Name:        "alice-mac",
		Type:        RuntimeTypeAgentDaemon,
		Config:      map[string]any{"runner_credential_hash": store.HashRuntimeCredential("real-tok")},
	}
	auth := NewAuthenticator(&stubRuntimeStore{row: row, ok: true})
	got, err := auth.AuthenticateBearer(context.Background(), "dev-1", "real-tok")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if got.DeviceID != "dev-1" || got.WorkspaceID != "wks-1" || got.Name != "alice-mac" {
		t.Fatalf("unexpected AuthenticatedRuntime: %+v", got)
	}
}
