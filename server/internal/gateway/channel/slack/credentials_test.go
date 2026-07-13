package slack

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/testutil"
)

type fakeSecretLookup struct {
	byTeam map[string]SlackBotSecret
	asked  string
}

func (f *fakeSecretLookup) ResolveSlackBotSecretByTeam(_ context.Context, teamID string) (SlackBotSecret, error) {
	f.asked = teamID
	if secret, ok := f.byTeam[teamID]; ok {
		return secret, nil
	}
	return SlackBotSecret{}, errors.New("no slack_bot secret for team " + teamID)
}

func TestDBCredentialResolverContract(t *testing.T) {
	testutil.RunCredentialResolverContract(t, testutil.CredentialResolverContract{
		Platform:      "slack",
		Tenant:        "T_ACME",
		UnknownTenant: "T_UNKNOWN",
		NoAppTenant:   "T_NOAPP",
		BadTenant:     "T_BAD",
		EmptyTenant:   "T_EMPTY",
		ResolvedToken: "xoxb-acme-123",
		FallbackToken: "xoxb-env-default",
		NewResolver: func(secrets map[string]testutil.CredentialSecret, decrypter *testutil.FakeDecrypter, fallback channel.CredentialResolver) (channel.CredentialResolver, func() string) {
			lookup := &fakeSecretLookup{byTeam: make(map[string]SlackBotSecret, len(secrets))}
			for teamID, secret := range secrets {
				lookup.byTeam[teamID] = SlackBotSecret(secret)
			}
			return NewDBCredentialResolver(lookup, decrypter, fallback), func() string { return lookup.asked }
		},
		NewFallback:  NewStaticCredentialResolver,
		MissingToken: errNoBotToken,
	})
}

var _ channel.CredentialResolver = NewStaticCredentialResolver("", "")
