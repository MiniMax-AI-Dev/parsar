package discord

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// Compile-time assertions that the Discord adapter satisfies the neutral
// contract and the optional TextCodec sub-interface.
var (
	_ channel.Channel   = (*Channel)(nil)
	_ channel.TextCodec = (*Channel)(nil)
)

func newTestChannel() *Channel {
	return New(Config{AppID: "A123", BotToken: "bot-test"})
}

func TestPlatform(t *testing.T) {
	if got := newTestChannel().Platform(); got != channel.PlatformDiscord {
		t.Fatalf("Platform() = %q, want %q", got, channel.PlatformDiscord)
	}
}

func TestCapabilitiesDeriveStreamPatches(t *testing.T) {
	c := newTestChannel()
	caps := c.Capabilities()
	if !caps.Edit || !caps.BlockStreaming {
		t.Fatalf("Discord must declare Edit+BlockStreaming, got %+v", caps)
	}
	if caps.MaxMessageLen != discordMaxMessageLen {
		t.Fatalf("MaxMessageLen = %d, want %d", caps.MaxMessageLen, discordMaxMessageLen)
	}
	if got := caps.DerivedStream(); got != channel.StreamPatches {
		t.Fatalf("DerivedStream() = %v, want StreamPatches", got)
	}
	if got := c.Stream(); got != channel.StreamPatches {
		t.Fatalf("Stream() = %v, want StreamPatches", got)
	}
}

func TestCredentialsResolve(t *testing.T) {
	cred, err := newTestChannel().Credentials().Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.AppID != "A123" || cred.AppSecret != "bot-test" {
		t.Fatalf("cred = %+v, want {A123, bot-test}", cred)
	}
	// botID (guild_id) overrides the configured app id.
	cred, err = newTestChannel().Credentials().Resolve(context.Background(), "G999")
	if err != nil {
		t.Fatalf("Resolve(botID): %v", err)
	}
	if cred.AppID != "G999" {
		t.Fatalf("AppID = %q, want G999 (botID override)", cred.AppID)
	}
}

func TestCredentialsMissingBotToken(t *testing.T) {
	c := New(Config{AppID: "A123"}) // no BotToken
	if _, err := c.Credentials().Resolve(context.Background(), ""); err != errNoBotToken {
		t.Fatalf("Resolve err = %v, want errNoBotToken", err)
	}
}

// Verify is a pass-through on the Gateway WebSocket path (the socket is
// authenticated at handshake), returning the body unchanged with no challenge.
func TestVerify_PassesThroughBody(t *testing.T) {
	body := []byte(`{"id":"1"}`)
	verified, challenge, err := newTestChannel().Verify(nil, body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if challenge != "" {
		t.Errorf("challenge = %q, want empty (no Gateway handshake)", challenge)
	}
	if string(verified) != string(body) {
		t.Errorf("verified = %q, want the body unchanged", verified)
	}
}

func TestAgentPromptHintMentionsDiscord(t *testing.T) {
	if got := newTestChannel().AgentPromptHint(); got == "" {
		t.Fatal("AgentPromptHint must be non-empty")
	}
}
