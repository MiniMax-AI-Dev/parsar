package store

import (
	"context"
	"errors"
	"testing"
)

// TestUpsertWorkspaceFeishuConnector_RequiresBotOpenIDWhenEnabled locks the
// rule that an enabled workspace Feishu connector must carry bot_open_id: it's
// how the inbound path recognizes an @Bot mention and self-sent messages. A
// complete input (with bot_open_id) succeeds; dropping bot_open_id — while
// every other required field is present — is rejected with
// ErrFeishuConnectorIncomplete.
func TestUpsertWorkspaceFeishuConnector_RequiresBotOpenIDWhenEnabled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	st := New(db)

	// Complete input succeeds (websocket mode does not require a verification
	// token, but DOES require bot_open_id).
	if _, err := st.UpsertWorkspaceFeishuConnector(ctx, UpsertWorkspaceFeishuConnectorInput{
		WorkspaceID:  ids.WorkspaceID,
		Enabled:      true,
		AppID:        "cli_ws_bot",
		AppSecretRef: "secret_ref",
		BotOpenID:    "ou_bot_self",
		EventMode:    "websocket",
	}, ids.UserID); err != nil {
		t.Fatalf("complete connector should upsert: %v", err)
	}

	// Same input minus bot_open_id is incomplete.
	if _, err := st.UpsertWorkspaceFeishuConnector(ctx, UpsertWorkspaceFeishuConnectorInput{
		WorkspaceID:  ids.WorkspaceID,
		Enabled:      true,
		AppID:        "cli_ws_bot",
		AppSecretRef: "secret_ref",
		EventMode:    "websocket",
	}, ids.UserID); !errors.Is(err, ErrFeishuConnectorIncomplete) {
		t.Fatalf("missing bot_open_id err = %v, want ErrFeishuConnectorIncomplete", err)
	}

	// A disabled connector may omit every required field, bot_open_id included.
	if _, err := st.UpsertWorkspaceFeishuConnector(ctx, UpsertWorkspaceFeishuConnectorInput{
		WorkspaceID: ids.WorkspaceID,
		Enabled:     false,
		AppID:       "cli_ws_bot",
	}, ids.UserID); err != nil {
		t.Fatalf("disabled connector should skip required-field validation: %v", err)
	}
}

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
