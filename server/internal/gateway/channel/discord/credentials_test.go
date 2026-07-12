package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/testutil"
)

type fakeSecretLookup struct {
	byGuild map[string]DiscordBotSecret
	asked   string
}

func (f *fakeSecretLookup) ResolveDiscordBotSecretByGuild(_ context.Context, guildID string) (DiscordBotSecret, error) {
	f.asked = guildID
	if secret, ok := f.byGuild[guildID]; ok {
		return secret, nil
	}
	return DiscordBotSecret{}, errors.New("no discord_bot secret for guild " + guildID)
}

func TestDBCredentialResolverContract(t *testing.T) {
	testutil.RunCredentialResolverContract(t, testutil.CredentialResolverContract{
		Platform:      "discord",
		Tenant:        "G_ACME",
		UnknownTenant: "G_UNKNOWN",
		NoAppTenant:   "G_NOAPP",
		BadTenant:     "G_BAD",
		EmptyTenant:   "G_EMPTY",
		ResolvedToken: "bot-acme-123",
		FallbackToken: "bot-env-default",
		NewResolver: func(secrets map[string]testutil.CredentialSecret, decrypter *testutil.FakeDecrypter, fallback channel.CredentialResolver) (channel.CredentialResolver, func() string) {
			lookup := &fakeSecretLookup{byGuild: make(map[string]DiscordBotSecret, len(secrets))}
			for guildID, secret := range secrets {
				lookup.byGuild[guildID] = DiscordBotSecret(secret)
			}
			return NewDBCredentialResolver(lookup, decrypter, fallback), func() string { return lookup.asked }
		},
		NewFallback:  NewStaticCredentialResolver,
		MissingToken: errNoBotToken,
	})
}

var _ channel.CredentialResolver = NewStaticCredentialResolver("", "")
