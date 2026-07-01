// Package feishu is the Feishu implementation of channel.Channel.
//
// PR #1 (multi-platform gateway) wired the inbound half — Verify, Normalize,
// Capabilities, Credentials — by delegating to the existing in-place gateway
// and auth/feishu code, so behavior matches the production feishuinbound path
// exactly. PR #3a wired the outbound half: RenderProgress/RenderTerminal
// (cards.go), Reply/Edit/Send (outbound.go), HandleAction (action.go). The
// adapter is not yet on the production path; it exists so the neutral driver
// can compile against a real Channel and so the contract is exercised by
// tests. The heavy machinery (outbound transport, action routing) is injected
// via WithTransport / WithActionRouter; the production bindings land in
// PR #3b / #3c.
package feishu

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)
const feishuMaxCardBytes = 30 * 1024

// agentPromptHint tells the Agent it is talking through Feishu so it formats
// output for interactive cards rather than, say, Slack Block Kit.
const agentPromptHint = "You are responding inside a Feishu (Lark) chat. " +
	"Keep replies concise; long output is rendered into an interactive card."

// Config carries the per-app Feishu credentials and endpoint needed by the
// adapter. PR #1 keeps these on the struct; PR #2+ moves resolution behind
// the CredentialResolver fully.
type Config struct {
	AppID          string
	VerifyToken    string
	EncryptKey     string
	OpenAPIBaseURL string
}

// Channel is the Feishu channel.Channel implementation.
type Channel struct {
	appID       string
	verifyToken string
	encryptKey  string
	creds       channel.CredentialResolver
	// transport injects the outbound machinery (client pool + token cache +
	// per-bot secret resolution) the adapter must not duplicate. nil until a
	// caller supplies WithTransport; outbound I/O methods return
	// ErrNoTransport when it is absent.
	transport Transport
	// actions injects the neutral card-action router. nil until a caller
	// supplies WithActionRouter; HandleAction then decodes + echoes a neutral
	// "received" toast (the production binding lands in PR #3c).
	actions channel.ActionRouter
}

// Option customizes a Channel at construction.
type Option func(*Channel)

// WithTransport injects the outbound Transport the Reply/Edit/Send methods
// use. The production binding wraps the feishuoutbound worker's
// clientFor + resolveCredentials (wired in PR #3b).
func WithTransport(t Transport) Option {
	return func(c *Channel) { c.transport = t }
}

// WithActionRouter injects the neutral ActionRouter HandleAction routes
// decoded card actions through. The production binding wraps the inbound
// manager's permission / credential-form / user-choice handlers (PR #3c).
func WithActionRouter(r channel.ActionRouter) Option {
	return func(c *Channel) { c.actions = r }
}

// Compile-time assertion that *Channel satisfies the contract.
var _ channel.Channel = (*Channel)(nil)

// New builds a Feishu channel adapter from per-app config. Optional
// functional options (e.g. WithTransport) wire the outbound half.
func New(cfg Config, opts ...Option) *Channel {
	c := &Channel{
		appID:       cfg.AppID,
		verifyToken: cfg.VerifyToken,
		encryptKey:  cfg.EncryptKey,
		creds:       newCredentialResolver(cfg),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *Channel) Platform() channel.Platform { return channel.PlatformFeishu }

// Capabilities declares Feishu's flag set. Edit + BlockStreaming yield
// StreamPatches (Feishu can PATCH an interactive card in place).
func (c *Channel) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		ChatTypes:      []string{"dm", "group", "thread"},
		Reactions:      true,
		Edit:           true,
		BlockStreaming: true,
		Reply:          true,
		Threads:        true,
		Media:          true,
		MaxMessageLen:  feishuMaxCardBytes,
	}
}

func (c *Channel) Stream() channel.StreamMode { return c.Capabilities().DerivedStream() }

func (c *Channel) AgentPromptHint() string { return agentPromptHint }

func (c *Channel) Credentials() channel.CredentialResolver { return c.creds }

