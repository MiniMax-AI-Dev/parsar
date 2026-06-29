package inflight

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestResolveCredentials_PicksUpRotatedVaultSecret pins the
// hot-rotation contract documented in docs/feishu-routing.md §5.1:
// the driver does NOT cache per-agent app_secret in worker memory.
// Every credential resolution goes through GetSecretPayload, so a
// vault rotation propagates as soon as the next dispatch fires.
//
// The (deleted) P1-era TestWorker_SecretRotationPicksUpNewValue
// pinned the same invariant against the dispatcher's send path.
// This is the driver-only equivalent.
func TestResolveCredentials_PicksUpRotatedVaultSecret(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_rotate"] = happyAgentWithAppID("cli_rotate")
	fs.secrets["secret_happy"] = oneSecret("first-secret")

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatal(err)
	}

	creds1, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_rotate",
		WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if creds1.AppSecret != "first-secret" {
		t.Fatalf("first resolve AppSecret = %q, want first-secret", creds1.AppSecret)
	}

	// Rotate vault.
	fs.mu.Lock()
	fs.secrets["secret_happy"] = oneSecret("rotated-secret")
	fs.mu.Unlock()

	creds2, err := worker.resolveCredentials(context.Background(), gateway.PendingOutboundMessage{
		SourceAppID: "cli_rotate",
		WorkspaceID: "ws-1",
	})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if creds2.AppSecret != "rotated-secret" {
		t.Errorf("second resolve AppSecret = %q, want rotated-secret (no in-memory cache)", creds2.AppSecret)
	}
	if creds2.AppID != creds1.AppID {
		t.Errorf("AppID changed across rotation: %q → %q (rotation should NOT swap app_id)", creds1.AppID, creds2.AppID)
	}
}

// TestInvalidateTokenCacheForApp_DropsTenantClient locks the operator
// escape hatch: after a Bot secret rotation, calling
// InvalidateTokenCacheForApp must drop the cached tenant_access_token
// for that (workspace, app_id) pair without evicting the client
// itself, so the very next dispatch refetches a token using the new
// secret. The (deleted) P1-era cache test pinned this via observable
// HTTP traffic to /tenant_access_token/internal; the driver path
// reuses the same FeishuTenantClient cache.
func TestInvalidateTokenCacheForApp_DropsTenantClient(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_rotate"] = happyAgentWithAppID("cli_rotate")
	fs.secrets["secret_happy"] = happySecret()

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if err != nil {
		t.Fatal(err)
	}

	// Warm the cache.
	c1, err := worker.clientFor("ws-1", "cli_rotate")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := worker.clientFor("ws-1", "cli_rotate")
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Fatalf("clientFor cache miss across calls; want same *FeishuTenantClient")
	}

	// Invalidate only this app's token cache (does NOT drop the
	// client, by contract). The client survives; its token cache
	// is empty. The third call should hand back the same client.
	worker.InvalidateTokenCacheForApp("ws-1", "cli_rotate")
	c3, err := worker.clientFor("ws-1", "cli_rotate")
	if err != nil {
		t.Fatal(err)
	}
	if c3 != c1 {
		t.Errorf("InvalidateTokenCacheForApp dropped the client; want token cache cleared but client retained")
	}

	// ResetClientCache replaces the whole client; the next clientFor
	// should construct a fresh instance.
	worker.ResetClientCache()
	c4, err := worker.clientFor("ws-1", "cli_rotate")
	if err != nil {
		t.Fatal(err)
	}
	if c4 == c1 {
		t.Errorf("ResetClientCache did not replace the cached client")
	}
}

func oneSecret(plainAppSecret string) store.SecretPayload {
	raw, _ := json.Marshal(map[string]string{"app_secret": plainAppSecret})
	return store.SecretPayload{EncryptedPayload: raw}
}
