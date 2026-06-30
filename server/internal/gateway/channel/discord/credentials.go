package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// errNoBotToken is returned when the adapter is constructed without a bot token,
// so the misconfiguration surfaces at resolve time rather than as an opaque 401
// from the Discord API (or a failed Gateway handshake) later.
var errNoBotToken = errors.New("discord channel: missing bot token")

// credentialResolver is the static Discord CredentialResolver. A Discord bot
// token is a long-lived credential used directly as the API/Gateway bearer, so
// the resolver returns it per call — a rotated token (re-issued bot) takes
// effect without a process restart. AppSecret carries the bot token; AppID
// carries the Discord application id used as the neutral bot id.
//
// The per-guild DB-backed resolver (NewDBCredentialResolver) lands in 5b; 5a
// ships only the static/env resolver so the adapter constructs and Credentials()
// resolves under test.
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
// app id + token, for use as the dbCredentialResolver fallback in main (5d). A
// blank token yields errNoBotToken at resolve time (DB-only deployments rely
// entirely on the per-guild secret).
func NewStaticCredentialResolver(appID, botToken string) channel.CredentialResolver {
	return &credentialResolver{
		appID:    strings.TrimSpace(appID),
		botToken: strings.TrimSpace(botToken),
	}
}

// Resolve returns the per-bot credential. botID overrides the configured app id
// when non-empty (multi-guild install threads ReplyTarget.TenantKey = guild_id
// here); the token is shared in 5a — per-guild token lookup lands with the DB
// resolver in 5b.
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

// DiscordBotSecret is a decrypt-ready bot-token secret resolved by Discord
// guild_id. It mirrors store.DiscordBotSecret so dbCredentialResolver depends
// only on this package-local shape (no import of internal/store), keeping the
// resolver unit-testable with a fake lookup.
type DiscordBotSecret struct {
	AppID            string
	EncryptedPayload []byte
}

// DiscordBotSecretLookup resolves the kind='discord_bot' secret for a Discord
// guild_id. *store.Store satisfies it via ResolveDiscordBotSecretByGuild
// (adapted in main, since the return type differs by package). A miss must
// surface as a non-nil error so the resolver can fall back to the static/env
// token.
type DiscordBotSecretLookup interface {
	ResolveDiscordBotSecretByGuild(ctx context.Context, guildID string) (DiscordBotSecret, error)
}

// SecretDecrypter decrypts a secrets envelope into its cleartext payload map.
// *secrets.Service satisfies it; the resolver reads the bot token out of the
// decrypted map under the api_key/token/access_token/value precedence the rest
// of the codebase uses for shared credentials.
type SecretDecrypter interface {
	Decrypt(envelopeJSON []byte) (map[string]any, error)
}

// dbCredentialResolver resolves a Discord bot token per guild by guild_id,
// decrypting the kind='discord_bot' secret on each call so a re-installed bot
// rotates without a restart. It defers to a configured static/env resolver when
// no guild_id is supplied (single-bot senders, the inbound-ack path) or no
// per-guild secret exists. Both misses are non-fatal — only an empty guild_id
// with no fallback (or a decrypt failure) surfaces as an error.
//
// Note: in practice one Discord bot uses one token across all its guilds (the
// Gateway connection is per bot, not per guild), so the DB path mainly supports
// running several distinct bots from one process; the static/env token covers
// the common single-bot deployment.
type dbCredentialResolver struct {
	lookup    DiscordBotSecretLookup
	decrypter SecretDecrypter
	// fallback is consulted when guild_id is empty or carries no per-guild
	// secret. Typically the env/static credentialResolver; may itself return
	// errNoBotToken when no env token is configured (DB-only deployment).
	fallback channel.CredentialResolver
}

// NewDBCredentialResolver builds a per-guild Discord credential resolver. lookup
// and decrypter are required; fallback may be nil (DB-only, no env token).
func NewDBCredentialResolver(lookup DiscordBotSecretLookup, decrypter SecretDecrypter, fallback channel.CredentialResolver) channel.CredentialResolver {
	return &dbCredentialResolver{lookup: lookup, decrypter: decrypter, fallback: fallback}
}

// Resolve looks up the bot token for the guild_id passed as botID. The neutral
// outbound path threads ReplyTarget.TenantKey (Discord guild_id) here; the
// inbound action decode passes the interaction's guild_id. Empty or unknown
// guild falls through to the static/env resolver.
func (r *dbCredentialResolver) Resolve(ctx context.Context, botID string) (channel.Credential, error) {
	guildID := strings.TrimSpace(botID)
	if guildID == "" {
		return r.fallbackResolve(ctx, botID)
	}
	secret, err := r.lookup.ResolveDiscordBotSecretByGuild(ctx, guildID)
	if err != nil {
		// No per-guild install (or a transient lookup error): prefer the
		// static/env token so a single-bot deployment keeps working rather than
		// hard-failing an outbound send.
		return r.fallbackResolve(ctx, botID)
	}
	payload, err := r.decrypter.Decrypt(secret.EncryptedPayload)
	if err != nil {
		return channel.Credential{}, fmt.Errorf("discord channel: decrypt bot token for guild %s: %w", guildID, err)
	}
	token := strings.TrimSpace(discordBotTokenFromPayload(payload))
	if token == "" {
		return channel.Credential{}, fmt.Errorf("discord channel: discord_bot secret for guild %s has no token value", guildID)
	}
	appID := strings.TrimSpace(secret.AppID)
	if appID == "" {
		appID = guildID
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

// discordBotTokenFromPayload reads the bot token out of a decrypted secret
// payload, using the same key precedence as the capability-credential reader
// (api_key → token → access_token → value) so a discord_bot secret authored
// like any other shared credential resolves without a bespoke shape.
func discordBotTokenFromPayload(payload map[string]any) string {
	for _, key := range []string{"api_key", "token", "access_token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
