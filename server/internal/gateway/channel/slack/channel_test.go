package slack

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// Compile-time assertions that the Slack adapter satisfies the neutral
// contract and the optional TextCodec sub-interface.
var (
	_ channel.Channel   = (*Channel)(nil)
	_ channel.TextCodec = (*Channel)(nil)
)

func newTestChannel() *Channel {
	return New(Config{AppID: "A123", BotToken: "xoxb-test"})
}

func TestPlatform(t *testing.T) {
	if got := newTestChannel().Platform(); got != channel.PlatformSlack {
		t.Fatalf("Platform() = %q, want %q", got, channel.PlatformSlack)
	}
}

func TestCapabilitiesDeriveStreamPatches(t *testing.T) {
	c := newTestChannel()
	caps := c.Capabilities()
	if !caps.Edit || !caps.BlockStreaming {
		t.Fatalf("Slack must declare Edit+BlockStreaming, got %+v", caps)
	}
	if !caps.Threads {
		t.Fatalf("Slack must declare Threads (thread_ts), got %+v", caps)
	}
	if caps.MaxMessageLen != slackMaxMessageLen {
		t.Fatalf("MaxMessageLen = %d, want %d", caps.MaxMessageLen, slackMaxMessageLen)
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
	if cred.AppID != "A123" || cred.AppSecret != "xoxb-test" {
		t.Fatalf("cred = %+v, want {A123, xoxb-test}", cred)
	}
	// botID overrides the configured app id.
	cred, err = newTestChannel().Credentials().Resolve(context.Background(), "Bother")
	if err != nil {
		t.Fatalf("Resolve(botID): %v", err)
	}
	if cred.AppID != "Bother" {
		t.Fatalf("AppID = %q, want Bother (botID override)", cred.AppID)
	}
}

func TestCredentialsMissingBotToken(t *testing.T) {
	c := New(Config{AppID: "A123"}) // no BotToken
	if _, err := c.Credentials().Resolve(context.Background(), ""); err != errNoBotToken {
		t.Fatalf("Resolve err = %v, want errNoBotToken", err)
	}
}

// The inbound decoders (Verify/Normalize) and the action callback
// (HandleAction) are implemented in 4c and covered by verify_test.go,
// event_test.go and action_test.go. The outbound transport (Reply/Send/Edit)
// lands in 4b and is covered by outbound_test.go.

func TestAgentPromptHintMentionsSlack(t *testing.T) {
	if got := newTestChannel().AgentPromptHint(); got == "" {
		t.Fatal("AgentPromptHint must be non-empty")
	}
}
