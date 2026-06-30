// Package slack is the Slack implementation of channel.Channel.
//
// PR #4 brings Slack onto the neutral gateway. It lands in stacked slices so
// each is independently reviewable and never touches the Feishu production
// path:
//
//   - 4a: adapter skeleton — identity, Capabilities, Credentials,
//     AgentPromptHint — plus the pure Block Kit renderers (blockkit.go) and the
//     mrkdwn TextCodec (textcodec.go). The inbound half (Verify/Normalize),
//     outbound transport (Reply/Edit/Send) and action decode (HandleAction)
//     are stubs so *Channel satisfies channel.Channel and the renderers can be
//     locked by golden tests. No slack-go dependency yet: 4a only marshals
//     Block Kit JSON.
//   - 4b: outbound transport over slack-go (chat.postMessage / chat.update) in
//     outbound.go. Reply/Send/Edit resolve the bot token per call and talk to a
//     small slackSender seam that a fake can stand in for under test.
//   - 4c (this slice): the pure inbound decoders — Verify (verify.go:
//     signing-secret HMAC + url_verification challenge), Normalize (event.go:
//     app_mention / message → neutral InboundEvent) and HandleAction (action.go:
//     button-only block_actions → neutral CardAction, routed through an injected
//     ActionRouter). No live websocket: the Socket Mode runner that drives these
//     decoders lands with the config-gated wire-in. Button-only — no modals,
//     matching the Hermes/OpenClaw reference implementations.
//   - 4d: config-gated wire-in (Socket Mode runner + driver/manager) + main.go.
//
// See docs/multi-platform-gateway.md §4.1 for the contract.
package slack

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// slackMaxMessageLen is Slack's practical Block Kit budget (~40 KB across all
// blocks). The driver reads it to decide when to split output.
const slackMaxMessageLen = 40000

// agentPromptHint tells the Agent it is talking through Slack so it formats
// output for Block Kit and Slack mrkdwn (single *bold*, _italic_, <url|text>),
// which differ from Markdown and from Feishu interactive cards.
const agentPromptHint = "You are responding inside a Slack channel. " +
	"Keep replies concise; long output is rendered into Block Kit blocks. " +
	"Use Slack mrkdwn (single *bold*, _italic_, <url|text> links), not Markdown."

// Config carries the per-workspace Slack credentials the adapter needs.
// BotToken (xoxb-…) authenticates Web API calls; AppToken (xapp-…,
// connections:write) opens the Socket Mode websocket (used in 4c); AppID is
// the Slack app id used as the neutral bot id. SigningSecret is only needed
// for the HTTP Events API path, which this adapter does not use (Socket Mode),
// but it is carried so a future HTTP entry point can verify request HMACs.
type Config struct {
	AppID         string
	BotToken      string
	AppToken      string
	SigningSecret string
}

// Channel is the Slack channel.Channel implementation.
type Channel struct {
	appID string
	creds channel.CredentialResolver

	// signingSecret verifies inbound Events API request HMACs (verify.go). It
	// is empty on the Socket Mode path (the websocket is authenticated once at
	// handshake), in which case Verify skips the per-request HMAC check.
	signingSecret string

	// newSender builds the outbound transport from a resolved bot token. It is
	// a field (not a direct slack.New call) so tests inject a fake sender via
	// WithSenderFactory; production uses defaultSenderFactory (slack-go).
	newSender func(token string) slackSender

	// actions injects the neutral card-action router HandleAction routes
	// decoded block_actions through. nil until a caller supplies
	// WithActionRouter; HandleAction then decodes + echoes a neutral "received"
	// ack (the production binding lands with the wire-in).
	actions channel.ActionRouter
}

// Option customizes a Channel at construction. WithSenderFactory swaps the
// outbound transport so the I/O paths (Reply/Send/Edit) are unit-testable
// without slack-go's HTTP client — mirroring how the Feishu adapter injects
// its transport.
type Option func(*Channel)

// WithSenderFactory overrides how the bot token is turned into a slackSender.
// Tests pass a fake; production leaves the default (slack-go).
func WithSenderFactory(f func(token string) slackSender) Option {
	return func(c *Channel) { c.newSender = f }
}

// WithActionRouter injects the neutral ActionRouter HandleAction routes decoded
// card actions through. The production binding wraps the inbound manager's
// permission / credential-form / user-choice handlers (wired with the runner).
func WithActionRouter(r channel.ActionRouter) Option {
	return func(c *Channel) { c.actions = r }
}

// WithCredentialResolver overrides the bot-token resolver. Production injects a
// DB-backed per-team resolver (NewDBCredentialResolver) so a multi-workspace
// deployment mints the right xoxb token per call; the default is the static/env
// resolver built from Config. Tests pass a fake.
func WithCredentialResolver(r channel.CredentialResolver) Option {
	return func(c *Channel) {
		if r != nil {
			c.creds = r
		}
	}
}

// Compile-time assertions that *Channel satisfies the contract and the
// optional TextCodec sub-interface (implemented in textcodec.go).
var (
	_ channel.Channel   = (*Channel)(nil)
	_ channel.TextCodec = (*Channel)(nil)
)

// New builds a Slack channel adapter from per-app config.
func New(cfg Config, opts ...Option) *Channel {
	c := &Channel{
		appID:         cfg.AppID,
		creds:         newCredentialResolver(cfg),
		signingSecret: cfg.SigningSecret,
		newSender:     defaultSenderFactory,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Channel) Platform() channel.Platform { return channel.PlatformSlack }

// Capabilities declares Slack's flag set. Slack can edit a message in place
// (chat.update) and stream block updates, so Edit + BlockStreaming yield
// StreamPatches — the same in-place streaming mode as Feishu.
func (c *Channel) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		ChatTypes:      []string{"dm", "channel", "group", "thread"},
		Reactions:      true,
		Edit:           true, // chat.update
		BlockStreaming: true, // re-render the same message's blocks
		Reply:          true,
		Threads:        true, // thread_ts
		Media:          true,
		NativeCommands: true, // slash commands
		MaxMessageLen:  slackMaxMessageLen,
	}
}

func (c *Channel) Stream() channel.StreamMode { return c.Capabilities().DerivedStream() }

func (c *Channel) AgentPromptHint() string { return agentPromptHint }

func (c *Channel) Credentials() channel.CredentialResolver { return c.creds }

// --- Inbound half: implemented in 4c (verify.go / event.go) -----------------
// --- Outbound transport: implemented in 4b (see outbound.go) ----------------
// --- Action callback: implemented in 4c (see action.go) ---------------------
