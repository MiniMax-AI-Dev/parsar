package inflight

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// workspaceConnector builds an enabled feishu workspace_im_connectors read
// row pointing at the given app_secret_ref — the frontend-configured source
// of truth the outbound resolver now consults first.
func workspaceConnector(appID, secretRef string) store.WorkspaceConnectorRead {
	return store.WorkspaceConnectorRead{
		ID:          "conn-" + appID,
		WorkspaceID: "ws-conn",
		Platform:    "feishu",
		AppID:       appID,
		Enabled:     true,
		Config: map[string]any{
			"app_secret_ref": secretRef,
			"event_mode":     "websocket",
			"bot_open_id":    "ou_bot",
		},
	}
}

// TestResolveCredentials_PrefersWorkspaceConnector: a frontend-configured
// connector is the source of truth — resolution reads its app_secret_ref from
// the vault even when the app_id would ALSO match the env-default shared bot.
// This closes the historic inbound/outbound asymmetry (inbound already routed
// through workspace_im_connectors; outbound did not).
func TestResolveCredentials_PrefersWorkspaceConnector(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.workspaceConnectors = map[string]store.WorkspaceConnectorRead{
		"cli_default": workspaceConnector("cli_default", "secret_conn"),
	}
	fs.secrets["secret_conn"] = oneSecret("connector-secret")

	// Configure an env-default bot with the SAME app_id but a different
	// secret; the connector must win.
	worker, err := NewWorker(Options{
		Store:            fs,
		Secrets:          fakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_default", AppSecret: "env-default-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_default",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds.AppID != "cli_default" {
		t.Errorf("AppID = %q, want cli_default", creds.AppID)
	}
	if creds.AppSecret != "connector-secret" {
		t.Errorf("AppSecret = %q, want connector-secret (frontend connector must win over env default)", creds.AppSecret)
	}
}

// TestResolveCredentials_ConnectorOnlyBotCanReply: a bot configured ONLY in the
// frontend (no env-default, no legacy agents.config row) resolves outbound
// credentials from its workspace connector. Before the fix this returned
// ErrUnresolvableOutbound ("no live agent") and the bot could receive but
// never reply.
func TestResolveCredentials_ConnectorOnlyBotCanReply(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.workspaceConnectors = map[string]store.WorkspaceConnectorRead{
		"cli_frontend": workspaceConnector("cli_frontend", "secret_conn"),
	}
	fs.secrets["secret_conn"] = oneSecret("frontend-secret")

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_frontend",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds.AppID != "cli_frontend" || creds.AppSecret != "frontend-secret" {
		t.Errorf("creds = %+v, want {cli_frontend frontend-secret}", creds)
	}
}

// TestResolveCredentials_FallsBackToEnvDefault: no workspace connector for the
// app_id → fall through to the env-default shared bot. Preserves the existing
// default-bot fast path.
func TestResolveCredentials_FallsBackToEnvDefault(t *testing.T) {
	t.Parallel()
	fs := newFakeStore() // no workspace connectors seeded

	worker, err := NewWorker(Options{
		Store:            fs,
		Secrets:          fakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_default", AppSecret: "env-default-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_default",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds.AppSecret != "env-default-secret" {
		t.Errorf("AppSecret = %q, want env-default-secret (env fallback)", creds.AppSecret)
	}
}

// TestResolveCredentials_FallsBackToLegacyAgentConfig: no connector, no
// env-default → fall through to the legacy agents.config route. Preserves
// backward compatibility for bots wired the old way.
func TestResolveCredentials_FallsBackToLegacyAgentConfig(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_legacy"] = happyAgentWithAppID("cli_legacy")
	fs.secrets["secret_happy"] = oneSecret("legacy-secret")

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_legacy",
		WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds.AppSecret != "legacy-secret" {
		t.Errorf("AppSecret = %q, want legacy-secret (legacy agents.config fallback)", creds.AppSecret)
	}
}

// TestResolveCredentials_ConnectorMissingSecretFallsThrough: a connector row
// exists but its vault payload is unreadable — resolution must NOT dead-letter
// on this bookkeeping miss; it falls through to the env default so a valid
// fallback credential can still deliver.
func TestResolveCredentials_ConnectorMissingSecretFallsThrough(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.workspaceConnectors = map[string]store.WorkspaceConnectorRead{
		"cli_default": workspaceConnector("cli_default", "secret_missing"), // never seeded into fs.secrets
	}

	worker, err := NewWorker(Options{
		Store:            fs,
		Secrets:          fakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_default", AppSecret: "env-default-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}

	creds, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_default",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if creds.AppSecret != "env-default-secret" {
		t.Errorf("AppSecret = %q, want env-default-secret (fall through on connector payload miss)", creds.AppSecret)
	}
}

// TestResolveCredentials_NoCredentialAnywhereDeadLetters: no connector, no env
// default, no legacy agent → the terminal dead-letter signal so the driver
// stops retrying.
func TestResolveCredentials_NoCredentialAnywhereDeadLetters(t *testing.T) {
	t.Parallel()
	fs := newFakeStore() // nothing seeded

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_ghost",
	})
	if !errors.Is(err, gateway.ErrUnresolvableOutbound) {
		t.Fatalf("err = %v, want ErrUnresolvableOutbound", err)
	}
}
