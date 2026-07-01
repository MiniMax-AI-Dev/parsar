package slack

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// errNoBotToken is returned when the adapter is constructed without a bot
// token, so the misconfiguration surfaces at resolve time rather than as an
// opaque 401 from the Slack API later.
var errNoBotToken = errors.New("slack channel: missing bot token (xoxb-…)")

// credentialResolver is the Slack CredentialResolver. Unlike Feishu's
// tenant_access_token (minted from app_id+secret and cached with a TTL), a
// Slack bot token (xoxb-…) is a long-lived credential used directly as the
// Web API bearer. The resolver returns it per call so a rotated token (e.g.
// re-installed app) takes effect without a process restart. AppSecret carries
// the bot token; AppID carries the Slack app id used as the neutral bot id.
type credentialResolver struct {
	appID    string
	botToken string
}

func newCredentialResolver(cfg Config) channel.CredentialResolver {
	return &credentialResolver{
		appID:    strings.TrimSpace(cfg.AppID),
		botToken: strings.TrimSpace(cfg.BotToken),
	}
}

// NewStaticCredentialResolver builds the env/static bot-token resolver from raw
// app id + token, for use as the dbCredentialResolver fallback in main. A blank
// token yields errNoBotToken at resolve time (DB-only deployments rely entirely
// on the per-team secret).
func NewStaticCredentialResolver(appID, botToken string) channel.CredentialResolver {
	return &credentialResolver{
		appID:    strings.TrimSpace(appID),
		botToken: strings.TrimSpace(botToken),
	}
}

// Resolve returns the per-bot credential. botID overrides the configured app
// id when non-empty (multi-workspace install), but the token is shared in 4a;
// per-workspace token lookup lands with the install store in a later slice.
func (r *credentialResolver) Resolve(_ context.Context, botID string) (channel.Credential, error) {
	if r.botToken == "" {
		return channel.Credential{}, errNoBotToken
	}
	appID := r.appID
	if b := strings.TrimSpace(botID); b != "" {
		appID = b
	}
	return channel.Credential{AppID: appID, AppSecret: r.botToken}, nil
}

// SlackBotSecret is a decrypt-ready bot-token secret resolved by Slack team_id.
// It mirrors store.SlackBotSecret so dbCredentialResolver depends only on this
// package-local shape (no import of internal/store), keeping the resolver
// unit-testable with a fake lookup.
type SlackBotSecret struct {
	AppID            string
	EncryptedPayload []byte
}

// SlackBotSecretLookup resolves the kind='slack_bot' secret for a Slack
// team_id. *store.Store satisfies it via ResolveSlackBotSecretByTeam (adapted
// in main, since the return type differs by package). A miss must surface as a
// non-nil error so the resolver can fall back to the static/env token.
type SlackBotSecretLookup interface {
	ResolveSlackBotSecretByTeam(ctx context.Context, teamID string) (SlackBotSecret, error)
}

// SecretDecrypter decrypts a secrets envelope into its cleartext payload map.
// *secrets.Service satisfies it; the resolver reads the xoxb token out of the
// decrypted map under the api_key/token/access_token/value precedence the rest
// of the codebase uses for shared credentials.
type SecretDecrypter interface {
	Decrypt(envelopeJSON []byte) (map[string]any, error)
}

// dbCredentialResolver resolves a Slack bot token per workspace by Slack
// team_id, decrypting the kind='slack_bot' secret on each call so a
// re-installed app rotates without a restart. It mirrors Hermes' team→client
// map with a primary fallback: when no team_id is supplied (single-tenant
// senders, the inbound-ack path) or no per-team secret exists, it defers to the
// configured static/env resolver. Both misses are non-fatal — only an empty
// team_id with no fallback (or a decrypt failure) surfaces as an error.
type dbCredentialResolver struct {
	lookup    SlackBotSecretLookup
	decrypter SecretDecrypter
	// fallback is consulted when team_id is empty or carries no per-team
	// secret. Typically the env/static credentialResolver; may itself return
	// errNoBotToken when no env token is configured (DB-only deployment).
	fallback channel.CredentialResolver
}

// NewDBCredentialResolver builds a per-team Slack credential resolver. lookup
// and decrypter are required; fallback may be nil (DB-only, no env token).
func NewDBCredentialResolver(lookup SlackBotSecretLookup, decrypter SecretDecrypter, fallback channel.CredentialResolver) channel.CredentialResolver {
	return &dbCredentialResolver{lookup: lookup, decrypter: decrypter, fallback: fallback}
}

// Resolve looks up the bot token for the team_id passed as botID. The neutral
// outbound path threads ReplyTarget.TenantKey (Slack team_id) here; the inbound
// action decode passes cb.Team.ID. Empty or unknown team falls through to the
// static/env resolver.
func (r *dbCredentialResolver) Resolve(ctx context.Context, botID string) (channel.Credential, error) {
	teamID := strings.TrimSpace(botID)
	if teamID == "" {
		return r.fallbackResolve(ctx, botID)
	}
	secret, err := r.lookup.ResolveSlackBotSecretByTeam(ctx, teamID)
	if err != nil {
		// No per-team install (or a transient lookup error): prefer the
		// static/env token so a single-tenant deployment keeps working
		// rather than hard-failing an outbound send.
		return r.fallbackResolve(ctx, botID)
	}
	payload, err := r.decrypter.Decrypt(secret.EncryptedPayload)
	if err != nil {
		return channel.Credential{}, fmt.Errorf("slack channel: decrypt bot token for team %s: %w", teamID, err)
	}
	token := strings.TrimSpace(slackBotTokenFromPayload(payload))
	if token == "" {
		return channel.Credential{}, fmt.Errorf("slack channel: slack_bot secret for team %s has no token value", teamID)
	}
	appID := strings.TrimSpace(secret.AppID)
	if appID == "" {
		appID = teamID
	}
	return channel.Credential{AppID: appID, AppSecret: token}, nil
}

// fallbackResolve defers to the static/env resolver, mapping a nil fallback to
// the same missing-token error the static resolver would raise.
func (r *dbCredentialResolver) fallbackResolve(ctx context.Context, botID string) (channel.Credential, error) {
	if r.fallback == nil {
		return channel.Credential{}, errNoBotToken
	}
	return r.fallback.Resolve(ctx, botID)
}

// slackBotTokenFromPayload reads the xoxb token out of a decrypted secret
// payload, using the same key precedence as the capability-credential reader
// (api_key → token → access_token → value) so a slack_bot secret authored like
// any other shared credential resolves without a bespoke shape.
func slackBotTokenFromPayload(payload map[string]any) string {
	for _, key := range []string{"api_key", "token", "access_token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
