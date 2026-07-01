// Package discord is the Discord implementation of channel.Channel.
//
// PR #5 brings Discord onto the neutral gateway. Like Slack it lands in stacked
// slices so each is independently reviewable and never touches the Feishu/Slack
// production paths:
//
//   - 5a: adapter skeleton — identity, Capabilities, Credentials,
//     AgentPromptHint — plus the pure Embed/component renderers (embed.go) and
//     the Markdown TextCodec (textcodec.go). The inbound half (Verify/Normalize),
//     outbound transport (Reply/Send/Edit) and action decode (HandleAction) are
//     stubs (stub.go) so *Channel satisfies channel.Channel and the renderers can
//     be locked by golden tests. No discordgo dependency yet: 5a only marshals
//     Discord embed/component JSON.
//   - 5b: outbound transport over discordgo (ChannelMessageSendComplex /
//     ChannelMessageEditComplex) in outbound.go, plus the per-guild DB credential
//     resolver in credentials.go.
//   - 5c: the pure inbound decoders — Verify (Gateway WS is authenticated at
//     handshake, so per-event verification is a pass-through), Normalize
//     (MESSAGE_CREATE → neutral InboundEvent) and HandleAction (INTERACTION_CREATE
//     component / modal submit → neutral CardAction), routed through an injected
//     ActionRouter — plus the long-lived Gateway WebSocket runner.
//   - 5d: config-gated wire-in (Gateway runner + driver/manager) + main.go.
//
// Discord differs from Slack in two ways that shape the design: it is Markdown-
// native (textcodec.go is near pass-through, not a mrkdwn rewrite) and its
// interactive components carry round-trip data only in a ≤100-char custom_id
// (no separate button "value"), so the neutral action id and its value are
// packed into the custom_id as "<action>:<value>" (see embed.go customID).
//
// See docs/multi-platform-gateway.md §4.1 for the contract.
package discord

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// discordMaxMessageLen is Discord's per-message character cap. A normal bot
// message body is limited to 2000 characters; the driver reads this to decide
// when to split output (Truncate, textcodec.go).
const discordMaxMessageLen = 2000

// agentPromptHint tells the Agent it is talking through Discord so it formats
// output for Discord's Markdown dialect (which, unlike Slack mrkdwn, is standard
// Markdown) and keeps within the 2000-char per-message budget.
const agentPromptHint = "You are responding inside a Discord channel. " +
	"Discord renders standard Markdown (**bold**, *italic*, `code`, ```fenced blocks```, [text](url) links). " +
	"Keep replies concise; messages are capped at 2000 characters and long output is split across messages."

// Config carries the Discord credentials the adapter needs. BotToken authenticates
// both the REST API (outbound, 5b) and the Gateway WebSocket (inbound, 5c). AppID
// is the Discord application id used as the neutral bot id when no guild-scoped
// credential applies.
type Config struct {
	AppID    string
	BotToken string
}

// Channel is the Discord channel.Channel implementation.
type Channel struct {
	appID string
	creds channel.CredentialResolver

	// newSender builds the outbound transport from a resolved bot token. It is a
	// field (not a direct discordgo.New call) so tests inject a fake sender via
	// WithSenderFactory; production uses defaultSenderFactory (discordgo,
	// outbound.go), which New defaults it to.
	newSender func(token string) discordSender

	// actions injects the neutral card-action router HandleAction routes decoded
	// interactions through. nil until a caller supplies WithActionRouter.
	actions channel.ActionRouter

	// picks accumulates a choice form's string-select picks across the separate
	// interactions Discord delivers, until the Submit click drains them
	// (action.go / pickstore.go). nil until a caller supplies WithPickStore; the
	// runner injects one so the pure adapter holds no live state.
	picks ComponentPickStore
}

// Option customizes a Channel at construction.
type Option func(*Channel)

// WithSenderFactory overrides how the bot token is turned into a discordSender.
// Tests pass a fake; production leaves the default (discordgo), wired in 5b.
func WithSenderFactory(f func(token string) discordSender) Option {
	return func(c *Channel) { c.newSender = f }
}

// WithActionRouter injects the neutral ActionRouter HandleAction routes decoded
// interactions through. The production binding wraps the inbound manager's
// permission / credential-form / user-choice handlers (wired with the runner).
func WithActionRouter(r channel.ActionRouter) Option {
	return func(c *Channel) { c.actions = r }
}

// WithPickStore injects the component pick accumulator HandleAction records
// string-select picks into and drains at submit time (pickstore.go). The runner
// owns it so the pure adapter holds no live per-message state; tests pass a
// MemoryPickStore. When nil, a choice-form submit decodes with no folded picks.
func WithPickStore(s ComponentPickStore) Option {
	return func(c *Channel) { c.picks = s }
}

// WithCredentialResolver overrides the bot-token resolver. Production injects a
// DB-backed per-guild resolver (NewDBCredentialResolver, 5b) so a multi-guild
// deployment mints the right bot token per call; the default is the static/env
// resolver built from Config. Tests pass a fake.
func WithCredentialResolver(r channel.CredentialResolver) Option {
	return func(c *Channel) {
		if r != nil {
			c.creds = r
		}
	}
}

// Compile-time assertions that *Channel satisfies the contract and the optional
// TextCodec sub-interface (implemented in textcodec.go).
var (
	_ channel.Channel   = (*Channel)(nil)
	_ channel.TextCodec = (*Channel)(nil)
)

// New builds a Discord channel adapter from config.
func New(cfg Config, opts ...Option) *Channel {
	c := &Channel{
		appID:     cfg.AppID,
		creds:     newCredentialResolver(cfg),
		newSender: defaultSenderFactory,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Channel) Platform() channel.Platform { return channel.PlatformDiscord }

// Capabilities declares Discord's flag set. Discord can edit a message in place
// (ChannelMessageEditComplex) and re-render its embeds, so Edit + BlockStreaming
// yield StreamPatches — the same in-place streaming mode as Feishu and Slack.
//
// Threads is left false for 5a: Discord supports native threads, but creating /
// targeting them is deferred until the inbound Normalize fills ExternalThreadID
// (see plan). "thread" stays in ChatTypes because the bot can still operate
// inside an existing thread channel.
func (c *Channel) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		ChatTypes:      []string{"dm", "channel", "thread"},
		Reactions:      true,
		Edit:           true, // ChannelMessageEditComplex
		BlockStreaming: true, // re-render the same message's embeds
		Reply:          true,
		Threads:        false, // conservative for 5a; native thread creation deferred
		Media:          true,
		NativeCommands: true, // application (slash) commands
		MaxMessageLen:  discordMaxMessageLen,
	}
}

func (c *Channel) Stream() channel.StreamMode { return c.Capabilities().DerivedStream() }

func (c *Channel) AgentPromptHint() string { return agentPromptHint }

func (c *Channel) Credentials() channel.CredentialResolver { return c.creds }

// --- Inbound half: implemented in 5c (verify.go / event.go) -----------------
// --- Outbound transport: implemented in 5b (see outbound.go) ----------------
// --- Action callback: implemented in 5c (see action.go / interaction.go) ----
