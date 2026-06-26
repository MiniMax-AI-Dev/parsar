package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestUpdateAgentFeishuConnector_HappyPathWritesConfigAndAudit(t *testing.T) {
	db, st := openAuditedTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	input := UpdateAgentFeishuConnectorInput{
		AgentID:              ids.ProductAgentID,
		Enabled:              true,
		AppID:                "cli_test_happy_path",
		AppSecretRef:         "sec_app_secret_1",
		VerificationTokenRef: "sec_verify_token_1",
		EncryptKeyRef:        "sec_encrypt_key_1",
		BotOpenID:            "ou_bot_test_1",
		RoutingMode:          "shared",
	}
	change, err := st.UpdateAgentFeishuConnector(ctx, input, ids.UserID)
	if err != nil {
		t.Fatalf("UpdateAgentFeishuConnector: %v", err)
	}
	if change.Noop {
		t.Errorf("expected noop=false on first config; got %+v", change)
	}
	if change.New.AppID != "cli_test_happy_path" || !change.New.Enabled || change.New.RoutingMode != "shared" {
		t.Errorf("change.New = %+v", change.New)
	}
	if change.Old.AppID != "" || change.Old.Enabled {
		t.Errorf("change.Old should be zero snapshot, got %+v", change.Old)
	}

	var raw []byte
	if err := db.QueryRow(ctx, "select config from agents where id = $1::uuid", ids.ProductAgentID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	connectors, _ := cfg["connectors"].(map[string]any)
	feishu, _ := connectors["feishu"].(map[string]any)
	if feishu == nil {
		t.Fatalf("connectors.feishu missing after PATCH; config=%s", raw)
	}
	if feishu["app_id"] != "cli_test_happy_path" {
		t.Errorf("feishu.app_id = %v", feishu["app_id"])
	}
	if feishu["routing_mode"] != "shared" {
		t.Errorf("feishu.routing_mode = %v", feishu["routing_mode"])
	}

	route, err := st.GetAgentByFeishuAppID(ctx, "cli_test_happy_path")
	if err != nil {
		t.Fatalf("GetAgentByFeishuAppID after PATCH: %v", err)
	}
	if route.AgentID != ids.ProductAgentID {
		t.Errorf("route.AgentID = %s, want %s", route.AgentID, ids.ProductAgentID)
	}

	// Async ingester — poll up to 1s.
	deadline := time.Now().Add(time.Second)
	var auditCount int
	for time.Now().Before(deadline) {
		if err := db.QueryRow(ctx, `
			select count(*) from audit_records
			where event_type = 'agent.feishu_connector.updated'
			  and target_id = $1::uuid
			  and payload->>'new_app_id' = 'cli_test_happy_path'
			  and payload->>'routing_mode' = 'shared'
			  and (payload->>'new_enabled')::boolean = true
		`, ids.ProductAgentID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if auditCount != 1 {
		t.Errorf("expected 1 audit record, got %d", auditCount)
	}
}

func TestUpdateAgentFeishuConnector_IncompleteRejectedWhenEnabled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	st := New(db)
	cases := []UpdateAgentFeishuConnectorInput{
		// missing app_id
		{AgentID: ids.ProductAgentID, Enabled: true, AppSecretRef: "x", VerificationTokenRef: "y"},
		// missing app_secret_ref
		{AgentID: ids.ProductAgentID, Enabled: true, AppID: "cli_x", VerificationTokenRef: "y"},
		// missing verification_token_ref
		{AgentID: ids.ProductAgentID, Enabled: true, AppID: "cli_x", AppSecretRef: "x"},
		// all empty
		{AgentID: ids.ProductAgentID, Enabled: true},
	}
	for i, c := range cases {
		if _, err := st.UpdateAgentFeishuConnector(ctx, c, ids.UserID); !errors.Is(err, ErrFeishuConnectorIncomplete) {
			t.Errorf("case %d err = %v, want ErrFeishuConnectorIncomplete", i, err)
		}
	}
}

func TestUpdateAgentFeishuConnector_WebSocketModeDoesNotRequireVerificationToken(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	st := New(db)
	change, err := st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:      ids.ProductAgentID,
		Enabled:      true,
		AppID:        "cli_websocket_qr",
		AppSecretRef: "sec_app_secret",
		EventMode:    "websocket",
	}, ids.UserID)
	if err != nil {
		t.Fatalf("websocket connector should not require verification token: %v", err)
	}
	if change.New.EventMode != "websocket" || change.New.VerificationTokenRef != "" {
		t.Fatalf("unexpected websocket connector snapshot: %+v", change.New)
	}

	var raw []byte
	if err := db.QueryRow(ctx, "select config from agents where id = $1::uuid", ids.ProductAgentID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	feishu, _ := cfg["connectors"].(map[string]any)["feishu"].(map[string]any)
	if feishu["event_mode"] != "websocket" {
		t.Fatalf("expected event_mode=websocket in jsonb, got %s", raw)
	}
}

// The incomplete check must only fire on enabled=true so disabling with
// empty fields succeeds (revert to pre-binding state).
func TestUpdateAgentFeishuConnector_DisabledMaySkipRequired(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	st := New(db)
	change, err := st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID: ids.ProductAgentID,
		Enabled: false,
	}, ids.UserID)
	if err != nil {
		t.Fatalf("disable PATCH unexpectedly failed: %v", err)
	}
	if change.New.Enabled {
		t.Errorf("change.New.Enabled = true, want false")
	}
	// Zero snapshot must not leave the connectors.feishu subtree — kept tidy
	// for partial-index sanity.
	var raw []byte
	if err := db.QueryRow(ctx, "select config from agents where id = $1::uuid", ids.ProductAgentID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw, &cfg)
	if connectors, ok := cfg["connectors"].(map[string]any); ok {
		if _, exists := connectors["feishu"]; exists {
			t.Errorf("connectors.feishu should be absent for zero snapshot; got %s", raw)
		}
	}
}

func TestUpdateAgentFeishuConnector_AppIDInUseRejected(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	st := New(db)

	_, err := st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:              ids.ProductAgentID,
		Enabled:              true,
		AppID:                "cli_uniqueness_test",
		AppSecretRef:         "sec_a",
		VerificationTokenRef: "sec_b",
	}, ids.UserID)
	if err != nil {
		t.Fatalf("first PATCH: %v", err)
	}
	// Same app_id to the SAME agent: idempotent rebind, not a uniqueness collision.
	_, err = st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:              ids.ProductAgentID,
		Enabled:              true,
		AppID:                "cli_uniqueness_test",
		AppSecretRef:         "sec_a_changed",
		VerificationTokenRef: "sec_b",
	}, ids.UserID)
	if err != nil {
		t.Fatalf("idempotent rebind to same agent should succeed, got: %v", err)
	}
	_, err = st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:              ids.BackendAgentID,
		Enabled:              true,
		AppID:                "cli_uniqueness_test",
		AppSecretRef:         "sec_x",
		VerificationTokenRef: "sec_y",
	}, ids.UserID)
	if !errors.Is(err, ErrFeishuAppIDInUse) {
		t.Errorf("second agent err = %v, want ErrFeishuAppIDInUse", err)
	}
}

func TestUpdateAgentFeishuConnector_NoopSuppressesAudit(t *testing.T) {
	db, st := openAuditedTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	input := UpdateAgentFeishuConnectorInput{
		AgentID:              ids.ProductAgentID,
		Enabled:              true,
		AppID:                "cli_noop_test",
		AppSecretRef:         "sec_a",
		VerificationTokenRef: "sec_b",
	}
	if _, err := st.UpdateAgentFeishuConnector(ctx, input, ids.UserID); err != nil {
		t.Fatal(err)
	}
	// Let the first audit event land before the noop replay.
	time.Sleep(150 * time.Millisecond)

	change, err := st.UpdateAgentFeishuConnector(ctx, input, ids.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if !change.Noop {
		t.Errorf("expected noop=true on identical replay, got %+v", change)
	}
	// Give the (would-be) second audit time to land before asserting absence.
	time.Sleep(150 * time.Millisecond)

	var auditCount int
	if err := db.QueryRow(ctx, `
		select count(*) from audit_records
		where event_type = 'agent.feishu_connector.updated'
		  and target_id = $1::uuid
	`, ids.ProductAgentID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Errorf("expected exactly 1 audit record after noop replay, got %d", auditCount)
	}
}
