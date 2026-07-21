// Package channel defines the platform-neutral IM channel abstraction. Each
// supported platform (Feishu, Slack, Discord, WeCom, DingTalk) provides a
// Channel implementation in a sub-package. The outbound driver and inbound
// manager program against this interface instead of any single platform.
//
// See docs/multi-platform-gateway.md for the design contract (PR #0).
package channel

import (
	"context"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// Platform identifies an IM platform.
type Platform string

const (
	PlatformFeishu   Platform = "feishu"
	PlatformSlack    Platform = "slack"
	PlatformDiscord  Platform = "discord"
	PlatformTeams    Platform = "teams"
	PlatformWeCom    Platform = "wecom"
	PlatformDingTalk Platform = "dingtalk"
)

// StreamMode tells the outbound driver how progress is surfaced.
type StreamMode int

const (
	// StreamPatches: the platform can edit a single message in place, so the
	// driver streams progress by PATCHing one card (Feishu/Slack/Discord).
	StreamPatches StreamMode = iota
	// StreamTerminalOnly: no mid-flight edit, so the driver only sends the
	// terminal card (WeCom/DingTalk).
	StreamTerminalOnly
)

// Capabilities is a flag set each adapter declares. The driver reads flags
// to choose a path and auto-degrade; it never type-switches on the concrete
// channel. (Absorbs OpenClaw's 12-flag ChannelCapabilities.)
type Capabilities struct {
	ChatTypes      []string // "dm" | "group" | "channel" | "thread"
	Polls          bool
	Reactions      bool // emoji reaction; typing indicator rides this too
	Edit           bool // can PATCH the message body
	BlockStreaming bool // block-level streaming PATCH (typewriter effect)
	Unsend         bool
	Reply          bool
	Threads        bool // native threads
	Media          bool // native image/video/file
	NativeCommands bool // platform-native slash commands
	MaxMessageLen  int  // per-message character cap
}

// DerivedStream derives the StreamMode from capability flags: a platform
// that can edit and stream blocks gets StreamPatches, everything else
// degrades to StreamTerminalOnly.
func (c Capabilities) DerivedStream() StreamMode {
	if c.Edit && c.BlockStreaming {
		return StreamPatches
	}
	return StreamTerminalOnly
}

// ReplyTarget locates where an outbound message goes.
type ReplyTarget struct {
	ExternalChatID   string
	ExternalThreadID string
	ReplyToMessageID string
	// TenantKey is the platform workspace id (Slack team_id, Feishu
	// tenant_key) the inbound router captured for this conversation. A
	// multi-workspace adapter passes it to its CredentialResolver as the
	// botID so the per-tenant bot token is resolved at send time. Empty for
	// single-tenant deployments and the Feishu path, which resolve a static
	// credential and ignore this field.
	TenantKey string
	// SourceAppID is the platform application id captured on the inbound
	// side (Slack/Discord/Feishu app_id). Unlike TenantKey it is known at
	// config-save time, so it is the join key into workspace_im_connectors
	// the outbound resolver uses to fetch the per-workspace bot token. A
	// workspace-dimension resolver prefers this over TenantKey; empty for
	// legacy single-tenant / env-credential paths.
	SourceAppID string
}

// Card is a platform-native rendered payload (Feishu interactive card,
// Slack Block Kit, Discord embed, ...). The driver treats it opaquely:
// it renders, sends and edits cards without understanding their content.
type Card struct {
	MIME    string // platform-specific content type, e.g. "feishu/interactive"
	Payload []byte
}

// ProgressState is the neutral input the driver hands to RenderProgress to
// produce an in-flight ("executing") card. It carries the rich, folded run
// state (tool steps, streamed text) the driver already computes, so the
// adapter renders without reaching back into the store. Slack/Discord render
// the same neutral state into their own native progress UI.
type ProgressState struct {
	Title         string             // card title (typically the agent name)
	Steps         []gateway.StepInfo // folded tool-call steps
	StreamingText string             // assistant streaming text so far
	Elapsed       time.Duration      // run wall-clock so far
	// Now pins the render clock for deterministic output/tests; the adapter
	// substitutes time.Now().UTC() when zero.
	Now  time.Time
	Done bool
}

// TerminalResult is the neutral input for RenderTerminal (Done / Error card).
// Success selects the Done vs Error rendering; the error-path fields are only
// read when Success is false.
type TerminalResult struct {
	Title         string
	StreamingText string
	Steps         []gateway.StepInfo
	Thinking      string
	Elapsed       time.Duration
	Usage         *gateway.UsageStats // nil when no usage rollup available
	Success       bool

	// Error path (Success == false).
	ErrorMessage string // user-visible failure copy; adapter defaults when empty
	RawError     string // un-mapped error appended under the mapped copy
	RunDetailURL string // deep link to the run-detail page; empty suppresses
	GuestHint    string // register prompt for unregistered public-visibility guests
}

// PermissionRequest is the neutral input for RenderPermission: the Allow/Deny
// card the worker emits when an agent run blocks on a dangerous tool call. The
// adapter renders Allow/Deny buttons whose action ids the inbound HandleAction
// maps back to CardActionPermissionAllow / CardActionPermissionDeny.
type PermissionRequest struct {
	Title     string // card title (typically the agent name)
	ToolName  string // tool awaiting approval, e.g. "Bash"
	ToolInput string // human-readable tool input preview
	RequestID string // permission_request_id round-tripped through the button
}

// ChoiceQuestion is one prompt_for_user_choice question. It is a channel-local
// mirror of store.PromptForUserChoiceQuestion so the channel package never
// imports store (the inflight driver maps store → channel before rendering).
type ChoiceQuestion struct {
	Header      string
	Question    string
	MultiSelect bool
	IsOther     bool
	IsSecret    bool
	Options     []string
}

// ChoiceForm is the neutral input for RenderChoiceForm: a multi-question
// single/multi-select form the agent raised via prompt_for_user_choice.
type ChoiceForm struct {
	Title     string
	RequestID string // prompt_for_user_choice request id round-tripped on submit
	Questions []ChoiceQuestion
}

// CredentialForm is the neutral input for RenderCredentialForm: the missing
// capability credentials a run needs before it can proceed. Fields reuse the
// gateway CredentialFormField shape the Feishu builder already consumes; Qkey
// is the minted single-use form key round-tripped through the submit button.
type CredentialForm struct {
	Title  string
	Qkey   string
	Fields []gateway.CredentialFormField
}

// ActionResult is the outcome of handling a card button / action callback.
type ActionResult struct {
	Ack     []byte // optional platform ack payload echoed back to the webhook
	Handled bool
}

// Credential is a resolved, hot-reloadable per-bot credential.
type Credential struct {
	AppID     string
	AppSecret string
}

// CredentialResolver resolves per-bot credentials at call time so vault
// rotations take effect without a process restart.
type CredentialResolver interface {
	Resolve(ctx context.Context, botID string) (Credential, error)
}

// Channel is the platform-neutral IM channel contract. See
// docs/multi-platform-gateway.md §4.1.
type Channel interface {
	Platform() Platform
	Capabilities() Capabilities

	// Verify authenticates the inbound request and handles the URL
	// challenge. Returns the verified (decrypted) body, or a non-empty
	// challenge string the HTTP handler must echo back.
	Verify(r *http.Request, body []byte) (verified []byte, challenge string, err error)

	// Normalize turns a verified platform event into a neutral InboundEvent.
	Normalize(verified []byte) (gateway.InboundEvent, error)

	// Reply sends a plain-text command acknowledgement (not streamed).
	Reply(ctx context.Context, target ReplyTarget, text string) error

	// RenderProgress / RenderTerminal turn neutral state into a native card.
	RenderProgress(ctx context.Context, target ReplyTarget, state ProgressState) (Card, error)
	RenderTerminal(ctx context.Context, target ReplyTarget, result TerminalResult) (Card, error)

	// RenderPermission / RenderChoiceForm / RenderCredentialForm render the
	// interactive cards that block on a user click. Their buttons carry the
	// action ids HandleAction maps to the neutral CardActionKind the inbound
	// ActionRouter dispatches on.
	RenderPermission(ctx context.Context, target ReplyTarget, req PermissionRequest) (Card, error)
	RenderChoiceForm(ctx context.Context, target ReplyTarget, form ChoiceForm) (Card, error)
	RenderCredentialForm(ctx context.Context, target ReplyTarget, form CredentialForm) (Card, error)

	// Stream reports how this channel streams progress.
	Stream() StreamMode
	// Edit PATCHes an existing message (StreamPatches channels).
	Edit(ctx context.Context, target ReplyTarget, ref gateway.MessageRef, card Card) error
	// Send posts a new card and returns its message reference.
	Send(ctx context.Context, target ReplyTarget, card Card) (gateway.MessageRef, error)

	// HandleAction processes a card button / action callback.
	HandleAction(ctx context.Context, payload []byte) (ActionResult, error)

	// AgentPromptHint tells the Agent which platform it is on, influencing
	// output formatting.
	AgentPromptHint() string

	// Credentials returns the hot-reload-friendly credential resolver.
	Credentials() CredentialResolver
}
