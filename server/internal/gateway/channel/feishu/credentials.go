package feishu

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// credentialResolver is the Feishu CredentialResolver. PR #1 wraps the
// existing gateway.NewFeishuTenantClient for config validation and exposes
// the per-app id; vault-backed app_secret resolution (from
// outbound_credentials.go) is wired in PR #3 when the outbound driver needs
// it. Resolving per call (rather than caching a token here) keeps vault
// rotations hot.
type credentialResolver struct {
	cfg Config
}

func newCredentialResolver(cfg Config) channel.CredentialResolver {
	return &credentialResolver{cfg: cfg}
}

// Resolve returns the per-bot credential. It delegates config validation to
// gateway.NewFeishuTenantClient (which enforces a non-empty app_id and the
// open-platform base URL) so the adapter rejects misconfiguration the same
// way the production tenant client does.
func (r *credentialResolver) Resolve(_ context.Context, botID string) (channel.Credential, error) {
	appID := r.cfg.AppID
	if botID != "" {
		appID = botID
	}
	if _, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: r.cfg.OpenAPIBaseURL,
	}); err != nil {
		return channel.Credential{}, err
	}
	return channel.Credential{AppID: appID}, nil
}
