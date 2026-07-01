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
