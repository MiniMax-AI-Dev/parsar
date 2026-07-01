// Package teams — outbound credential resolution (AAD app id + password).
//
// This is the OUTBOUND half of the asymmetric Bot Framework auth (pitfall #1).
// The resolver returns the raw (app id, password) pair; the actual AAD
// client-credentials token exchange happens in outbound.go's sender, cached
// with the token's own expiry. Keeping the exchange out of the resolver mirrors
// Slack (whose resolver returns a long-lived bot token used directly) and keeps
// the resolver a pure secret lookup that a per-tenant DB-backed implementation
// can swap in via WithCredentialResolver.
//
// The inbound JWT verification (verify.go) shares NO token with this path:
// inbound proves "the Connector is calling me", outbound proves "I am the bot
// calling the Connector". Conflating them is the classic Bot Framework 401.
package teams

import (
	"context"
	"errors"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// errNoAppCredentials surfaces when the adapter is built without an app id /
// password, so the misconfiguration appears at resolve time rather than as an
// opaque 401 from the Connector later. An empty-credential build is still valid
// for local Emulator debugging (verification disabled, unsigned Connector), so
// this only bites an actual outbound send.
var errNoAppCredentials = errors.New("teams channel: missing app id/password for outbound Connector auth")

// credentialResolver is the static Teams CredentialResolver built from Config.
// AppID is the Microsoft App Id (the AAD client_id); AppSecret is the app
// password (the AAD client_secret). tenantID pins a single-tenant token
// authority; empty selects the multi-tenant botframework.com authority. The
// sender reads tenantID off the adapter, not the credential, so it is not
// carried on channel.Credential (whose shape is platform-neutral).
type credentialResolver struct {
	appID       string
	appPassword string
}

// newCredentialResolver builds the static resolver from Config. It is the
// default; production may inject a per-tenant DB-backed resolver via
// WithCredentialResolver.
func newCredentialResolver(cfg Config) channel.CredentialResolver {
	return &credentialResolver{
		appID:       strings.TrimSpace(cfg.AppID),
		appPassword: strings.TrimSpace(cfg.AppPassword),
	}
}

// NewStaticCredentialResolver builds the env/static app-credential resolver from
// raw app id + password, for use as a DB-resolver fallback in main. A blank
// password yields errNoAppCredentials at resolve time.
func NewStaticCredentialResolver(appID, appPassword string) channel.CredentialResolver {
	return &credentialResolver{
		appID:       strings.TrimSpace(appID),
		appPassword: strings.TrimSpace(appPassword),
	}
}

// Resolve returns the per-bot app credential. botID overrides the configured
// app id when non-empty (a multi-bot deployment captures the recipient bot's id
// inbound and threads it here), but the password is shared by the static
// resolver; a per-bot password lookup lands with a DB-backed resolver.
func (r *credentialResolver) Resolve(_ context.Context, botID string) (channel.Credential, error) {
	if r.appPassword == "" {
		return channel.Credential{}, errNoAppCredentials
	}
	appID := r.appID
	if b := strings.TrimSpace(botID); b != "" {
		appID = b
	}
	return channel.Credential{AppID: appID, AppSecret: r.appPassword}, nil
}
