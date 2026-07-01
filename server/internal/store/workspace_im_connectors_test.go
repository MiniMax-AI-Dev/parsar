package store

import (
	"context"
	"errors"
	"testing"
)

// TestUpsertWorkspaceTeamsConnector_Incomplete covers the DB-free validation
// gate: an enabled Teams connector missing app_id or app_password_ref returns
// ErrTeamsConnectorIncomplete before any persistence, so a zero *Store suffices.
func TestUpsertWorkspaceTeamsConnector_Incomplete(t *testing.T) {
	s := &Store{}
	cases := []struct {
		name  string
		input UpsertWorkspaceTeamsConnectorInput
	}{
		{
			name:  "missing app_id",
			input: UpsertWorkspaceTeamsConnectorInput{WorkspaceID: "ws", Enabled: true, AppPasswordRef: "ref"},
		},
		{
			name:  "missing app_password_ref",
			input: UpsertWorkspaceTeamsConnectorInput{WorkspaceID: "ws", Enabled: true, AppID: "app"},
		},
		{
			name:  "whitespace-only app_password_ref",
			input: UpsertWorkspaceTeamsConnectorInput{WorkspaceID: "ws", Enabled: true, AppID: "app", AppPasswordRef: "   "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.UpsertWorkspaceTeamsConnector(context.Background(), tc.input, "actor")
			if !errors.Is(err, ErrTeamsConnectorIncomplete) {
				t.Fatalf("want ErrTeamsConnectorIncomplete, got %v", err)
			}
		})
	}
}

// TestWorkspaceTeamsConnectorSnapshot verifies the jsonb config shape (column
// fields excluded) and the isZero blank detection.
func TestWorkspaceTeamsConnectorSnapshot(t *testing.T) {
	snap := WorkspaceTeamsConnectorSnapshot{
		Enabled:        true,
		AppID:          "app-1",
		AppPasswordRef: "secret-ref",
		TenantID:       "tenant-1",
	}
	cfg := snap.toConfigMap()
	if got := cfg["app_password_ref"]; got != "secret-ref" {
		t.Errorf("app_password_ref = %v, want secret-ref", got)
	}
	if got := cfg["tenant_id"]; got != "tenant-1" {
		t.Errorf("tenant_id = %v, want tenant-1", got)
	}
	if _, ok := cfg["app_id"]; ok {
		t.Error("column field app_id must not leak into the jsonb config map")
	}
	if _, ok := cfg["enabled"]; ok {
		t.Error("column field enabled must not leak into the jsonb config map")
	}

	if !(WorkspaceTeamsConnectorSnapshot{}).isZero() {
		t.Error("blank snapshot must be zero")
	}
	if snap.isZero() {
		t.Error("populated snapshot must not be zero")
	}
}
