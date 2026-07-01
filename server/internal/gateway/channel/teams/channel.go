// Package teams is the Microsoft Teams implementation of channel.Channel.
//
// Teams rides the Microsoft Bot Framework, whose transport differs from the
// Slack/Discord adapters in two ways that this package is built around — the
// same two pitfalls the Slack/Discord slices called out:
//
//   - Inbound and outbound auth are NOT symmetric. Inbound the Bot Framework
//     signs every webhook POST with a JWT bearer (RS256, JWKS-backed) whose
//     audience is the bot's Microsoft App Id; verify.go checks that. Outbound
//     the bot authenticates itself to the Connector service with an AAD
//     client-credentials bearer minted from (app id, password); credentials.go
//     mints and caches that. The two never share a token — conflating them is
//     the classic Bot Framework 401.
//   - There is no single "channel id you post to" that also groups a thread.
//     The postable id is the Activity's conversation.id (which for a channel
//     reply already carries the thread's ;messageid=root), so ExternalChatID
//     carries it verbatim and outbound POSTs to it. serviceUrl (the regional
//     Connector base URL) is per-conversation and has no ReplyTarget slot, so
//     it rides a conversation-reference cache (convref.go) the runner primes on
//     each inbound and the outbound path reads back.
//
// Scope (inbound): Verify + Normalize + the webhook runner, synchronous command
// replies (/list, /select, rejection hints) via Reply, and Adaptive Card
// Action.Submit button round-trips via HandleAction. Agent async answers do not
// stream back to Teams yet — the inflight outbound worker is still Feishu-bound,
// matching the current Slack/Discord state. The full outbound transport
// (Reply/Send/Edit) is nonetheless implemented so command acks and card swaps
// work.
//
// See docs/multi-platform-gateway.md §4.1 for the contract.
package teams

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// teamsMaxMessageLen is Teams' practical per-message budget. A Teams message
// (and an Adaptive Card body) caps around 28 KB; the driver reads this to
// decide when to split output.
const teamsMaxMessageLen = 28000

// agentPromptHint tells the Agent it is talking through Microsoft Teams so it
// formats output for the Teams markdown subset (which a TextBlock renders) and
// keeps replies short enough to fit one Adaptive Card.
const agentPromptHint = "You are responding inside a Microsoft Teams chat. " +
	"Keep replies concise; long output is rendered into an Adaptive Card. " +
	"Use basic Markdown (bold, italics, links, lists); Teams renders a subset."

// Config carries the Bot Framework credentials the adapter needs. AppID is the
// Microsoft App Id (the bot's registration id) — it is both the JWT audience
// verify.go enforces inbound and the client_id credentials.go mints an AAD
// token with outbound. AppPassword is the app secret used for that token
// exchange. TenantID, when set, pins a single-tenant token authority; empty
// uses the multi-tenant botframework.com authority.
type Config struct {
	AppID       string
	AppPassword string
	TenantID    string
}

// Channel is the Teams channel.Channel implementation.
type Channel struct {
	appID string
	creds channel.CredentialResolver

	// botLocalID is the bot's Teams channel-account id ("28:<AppID>") — the id
	// a user's @mention entity carries and the recipient.id on an inbound
	// activity. The mention gate matches MentionedUserIDs against it, so the
	// runner reads it off the adapter rather than re-deriving the "28:" prefix.
	botLocalID string

	// tenantID pins the AAD token authority for a single-tenant bot; empty
	// selects the multi-tenant botframework.com authority.
	tenantID string

	// convRefs caches per-conversation serviceUrl/tenant so the outbound path
	// can address the right regional Connector endpoint. The runner primes it
	// on every inbound (RememberConversation); Reply/Send/Edit read it back
	// keyed by ExternalChatID (the conversation id).
	convRefs ConversationStore

	// verifier authenticates the inbound JWT bearer. nil disables verification
	// (local Bot Framework Emulator debugging, mirroring Slack's empty
	// signingSecret skip).
	verifier TokenVerifier

	// newSender builds the outbound Connector transport from a resolved
	// credential. A field (not a direct call) so tests inject a fake via
	// WithSenderFactory; production uses the default AAD-token-minting sender.
	newSender func(cred channel.Credential) teamsSender

	// actions injects the neutral card-action router HandleAction routes decoded
	// Action.Submit payloads through. nil until WithActionRouter; HandleAction
	// then echoes a neutral "received" ack so a click never hangs.
	actions channel.ActionRouter
}

// Option customizes a Channel at construction.
type Option func(*Channel)

// WithSenderFactory overrides how a resolved credential becomes a teamsSender.
// Tests pass a fake; production leaves the default (AAD-token Connector sender).
func WithSenderFactory(f func(cred channel.Credential) teamsSender) Option {
	return func(c *Channel) {
		if f != nil {
			c.newSender = f
		}
	}
}

// WithActionRouter injects the neutral ActionRouter HandleAction routes decoded
// card actions through.
func WithActionRouter(r channel.ActionRouter) Option {
	return func(c *Channel) { c.actions = r }
}

// WithCredentialResolver overrides the credential resolver. Production may inject
// a per-tenant DB-backed resolver; the default is the static resolver built from
// Config.
func WithCredentialResolver(r channel.CredentialResolver) Option {
	return func(c *Channel) {
		if r != nil {
			c.creds = r
		}
	}
}

// WithConversationStore overrides the conversation-reference cache (e.g. a
// persistent store shared across replicas). The default is an in-memory store.
func WithConversationStore(s ConversationStore) Option {
	return func(c *Channel) {
		if s != nil {
			c.convRefs = s
		}
	}
}

// WithTokenVerifier overrides the inbound JWT verifier. Production injects a
// JWKS-backed Bot Framework verifier; passing nil (the default when AppID is
// empty) disables verification for local debugging.
func WithTokenVerifier(v TokenVerifier) Option {
	return func(c *Channel) { c.verifier = v }
}

// Compile-time assertions that *Channel satisfies the contract and the optional
// TextCodec sub-interface.
var (
	_ channel.Channel   = (*Channel)(nil)
	_ channel.TextCodec = (*Channel)(nil)
)

// New builds a Teams channel adapter from Bot Framework config.
func New(cfg Config, opts ...Option) *Channel {
	appID := trim(cfg.AppID)
	c := &Channel{
		appID:      appID,
		creds:      newCredentialResolver(cfg),
		botLocalID: botLocalIDFor(appID),
		tenantID:   trim(cfg.TenantID),
		convRefs:   NewMemoryConversationStore(),
		newSender:  nil, // set below so it can close over c
	}
	c.newSender = c.defaultSenderFactory
	for _, o := range opts {
		o(c)
	}
	return c
}

// BotLocalID returns the bot's Teams channel-account id ("28:<AppID>") the
// mention gate matches against. Empty when no AppID was configured.
func (c *Channel) BotLocalID() string { return c.botLocalID }

// RememberConversation primes the conversation-reference cache so a later
// outbound send can address the conversation's regional Connector endpoint. The
// runner calls it on every inbound before routing.
func (c *Channel) RememberConversation(conversationID string, ref ConversationRef) {
	if c.convRefs == nil {
		return
	}
	c.convRefs.Put(conversationID, ref)
}

// RememberInbound extracts the conversation id and its routing ref from a
// verified inbound activity, primes the conversation-reference cache, and
// returns the conversation id (empty when the payload carries none). The runner
// calls it on EVERY inbound before routing — including for activities Normalize
// later rejects (a card submit rides a message activity, but belt-and-suspenders
// keeps the serviceUrl/tenant fresh) — so a synchronous reply or a card-action
// ack posted in the same request can address the right regional Connector.
func (c *Channel) RememberInbound(verified []byte) string {
	convID, ref, ok := conversationRefFrom(verified)
	if !ok {
		return ""
	}
	c.RememberConversation(convID, ref)
	return convID
}

func (c *Channel) Platform() channel.Platform { return channel.PlatformTeams }

// Capabilities declares Teams' flag set. Teams can edit a posted activity in
// place (PUT activity) so Edit is true, but block-level streaming PATCHes hit
// Connector rate limits, so BlockStreaming stays false and the stream mode
// degrades to terminal-only.
func (c *Channel) Capabilities() channel.Capabilities {
	return channel.Capabilities{
		ChatTypes:      []string{"dm", "channel", "group", "thread"},
		Reactions:      false,
		Edit:           true, // PUT /v3/conversations/{id}/activities/{activityId}
		BlockStreaming: false,
		Reply:          true,
		Threads:        true,
		Media:          true,
		NativeCommands: false,
		MaxMessageLen:  teamsMaxMessageLen,
	}
}

func (c *Channel) Stream() channel.StreamMode { return c.Capabilities().DerivedStream() }

func (c *Channel) AgentPromptHint() string { return agentPromptHint }

func (c *Channel) Credentials() channel.CredentialResolver { return c.creds }
