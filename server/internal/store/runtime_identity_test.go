package store

import (
	"context"
	"encoding/json"
	"testing"

	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestResolveRuntimeIdentityRoundTrip exercises the bearer-only lookup
// the runner_credential middleware sits on, including the jsonb-path WHERE.
func TestResolveRuntimeIdentityRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	if _, found, err := s.ResolveRuntimeIdentity(ctx, ""); err != nil || found {
		t.Fatalf("empty plaintext: got found=%v err=%v, want false/nil", found, err)
	}

	// Middleware must not distinguish "no credential" from "wrong credential".
	if _, found, err := s.ResolveRuntimeIdentity(ctx, "rtc_nonexistent"); err != nil || found {
		t.Fatalf("unknown plaintext: got found=%v err=%v, want false/nil", found, err)
	}

	pair, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        "agent_daemon",
		Name:        "sandbox-resolve",
		Provider:    "agent_daemon",
		OwnerUserID: ids.UserID,
		Config: map[string]any{
			"agent_id":  ids.ProductAgentID,
			"connector": "claude_code",
		},
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	if _, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           pair.PairingToken,
		Hostname:        "sandbox-1",
		Version:         "0.0.1",
		RunnerPublicKey: "fakepubkey==",
	}); err != nil {
		t.Fatalf("consume token: %v", err)
	}
	plaintext, hash, err := MintRuntimeCredential()
	if err != nil {
		t.Fatalf("mint credential: %v", err)
	}
	if err := s.SetRuntimeRunnerCredentialHash(ctx, pair.Runtime.ID, hash); err != nil {
		t.Fatalf("set credential hash: %v", err)
	}

	id, found, err := s.ResolveRuntimeIdentity(ctx, plaintext)
	if err != nil || !found {
		t.Fatalf("resolve: found=%v err=%v", found, err)
	}
	if id.RuntimeID != pair.Runtime.ID {
		t.Errorf("RuntimeID = %q, want %q", id.RuntimeID, pair.Runtime.ID)
	}
	if id.WorkspaceID != ids.WorkspaceID {
		t.Errorf("WorkspaceID = %q, want %q", id.WorkspaceID, ids.WorkspaceID)
	}
	if id.RuntimeType != "agent_daemon" {
		t.Errorf("RuntimeType = %q, want agent_daemon", id.RuntimeType)
	}
	if id.OwnerUserID == nil || *id.OwnerUserID != ids.UserID {
		t.Errorf("OwnerUserID = %v, want %q", id.OwnerUserID, ids.UserID)
	}
	if id.AgentID == nil || *id.AgentID != ids.ProductAgentID {
		t.Errorf("AgentID = %v, want %q", id.AgentID, ids.ProductAgentID)
	}
	if id.ConnectorName == nil || *id.ConnectorName != "claude_code" {
		t.Errorf("ConnectorName = %v, want claude_code", id.ConnectorName)
	}
	if id.ConversationID != nil {
		t.Errorf("ConversationID = %v, want nil (key not set)", id.ConversationID)
	}

	other, _, err := MintRuntimeCredential()
	if err != nil {
		t.Fatalf("mint other: %v", err)
	}
	if _, found, err := s.ResolveRuntimeIdentity(ctx, other); err != nil || found {
		t.Fatalf("wrong plaintext: got found=%v err=%v, want false/nil", found, err)
	}

	if err := s.SoftDeleteRuntime(ctx, pair.Runtime.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, found, err := s.ResolveRuntimeIdentity(ctx, plaintext); err != nil || found {
		t.Fatalf("after delete: got found=%v err=%v, want false/nil", found, err)
	}
}

// TestResolveRuntimeIdentityMinimalConfig: workspace-scoped CLIs without an
// agent binding rely on the nil-vs-empty distinction on pointer fields.
func TestResolveRuntimeIdentityMinimalConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	pair, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "minimal-runtime",
		Provider:    RuntimeProviderAgentDaemon,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	if _, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           pair.PairingToken,
		Hostname:        "h",
		Version:         "v",
		RunnerPublicKey: "k",
	}); err != nil {
		t.Fatalf("consume token: %v", err)
	}
	plaintext, hash, err := MintRuntimeCredential()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if err := s.SetRuntimeRunnerCredentialHash(ctx, pair.Runtime.ID, hash); err != nil {
		t.Fatalf("set hash: %v", err)
	}

	id, found, err := s.ResolveRuntimeIdentity(ctx, plaintext)
	if err != nil || !found {
		t.Fatalf("resolve: found=%v err=%v", found, err)
	}
	if id.OwnerUserID != nil {
		t.Errorf("OwnerUserID = %v, want nil", id.OwnerUserID)
	}
	if id.AgentID != nil {
		t.Errorf("AgentID = %v, want nil", id.AgentID)
	}
	if id.ConnectorName != nil {
		t.Errorf("ConnectorName = %v, want nil", id.ConnectorName)
	}
	if id.ConversationID != nil {
		t.Errorf("ConversationID = %v, want nil", id.ConversationID)
	}
}

// TestResolveRuntimeIdentityIgnoresPairingHash: the bearer-only resolver MUST
// match on runner_credential_hash, not pairing_token_hash — a runtime in
// pending_pairing has a pairing hash but no runner credential.
func TestResolveRuntimeIdentityIgnoresPairingHash(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	pair, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "pending-pair",
		Provider:    RuntimeProviderAgentDaemon,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	// Pairing token (never promoted to a runner credential) must not resolve.
	if _, found, err := s.ResolveRuntimeIdentity(ctx, pair.PairingToken); err != nil || found {
		t.Fatalf("pairing token: got found=%v err=%v, want false/nil", found, err)
	}
}

func TestConfigString(t *testing.T) {
	cfg := map[string]any{
		"present":      "value",
		"empty_string": "",
		"non_string":   42,
	}
	cases := []struct {
		key  string
		want *string
	}{
		{"present", strPtr("value")},
		{"empty_string", nil}, // empty → nil so callers see "absent"
		{"non_string", nil},   // wrong type → nil, not panic
		{"absent", nil},
	}
	for _, c := range cases {
		got := configString(cfg, c.key)
		switch {
		case got == nil && c.want == nil:
			continue
		case got == nil || c.want == nil:
			t.Errorf("configString(%q) = %v, want %v", c.key, got, c.want)
		case *got != *c.want:
			t.Errorf("configString(%q) = %q, want %q", c.key, *got, *c.want)
		}
	}
}

// TestResolveRuntimeIdentityHandlesNullConfig guards against config=NULL.
// The DB default is '{}'::jsonb, but the read path must still not panic.
func TestResolveRuntimeIdentityHandlesNullConfig(t *testing.T) {
	row := sqlc.GetRuntimeByCredentialHashRow{
		ID:          "11111111-1111-1111-1111-111111111111",
		WorkspaceID: "22222222-2222-2222-2222-222222222222",
		Type:        "local",
		Config:      nil,
		OwnerUserID: pgtype.UUID{Valid: false},
	}
	cfg := unmarshalJSONOrEmpty(row.Config)
	if got := configString(cfg, "agent_id"); got != nil {
		t.Errorf("configString(nil config) = %v, want nil", got)
	}
}

func strPtr(s string) *string { return &s }

func jsonRoundTrip(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return unmarshalJSONOrEmpty(b)
}

func TestConfigStringSurvivesJSONRoundTrip(t *testing.T) {
	cfg := jsonRoundTrip(t, map[string]any{
		"agent_id":  "abc-123",
		"connector": "claude_code",
	})
	if got := configString(cfg, "agent_id"); got == nil || *got != "abc-123" {
		t.Errorf("agent_id after round trip: %v", got)
	}
	if got := configString(cfg, "connector"); got == nil || *got != "claude_code" {
		t.Errorf("connector after round trip: %v", got)
	}
}
