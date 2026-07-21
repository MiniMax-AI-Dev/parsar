// Package connector defines the AgentConnector abstraction: a single Go
// interface plus a Capabilities declaration that Parsar Server uses to
// hand an agent_run off to any backing agent system (OpenCode local,
// HTTP Agent, ACP, A2A, Webhook).
//
// There is ONE AgentConnector interface; protocol differences are
// declared via Capabilities() and unsupported operations return
// ErrNotSupported. Protocol-specific translation (Parsar ModelRuntime
// -> opencode.json / HTTP request payload / ACP server args) lives in
// sub-packages under runtime/<adapter>.
//
// The connector owns its own session/run state. agent_runs rows
// reference the connector_type but never hold connector-internal
// identifiers as first-class columns; they live in agent_runs.metadata
// when the connector wants to remember them.
package connector

import (
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// SandboxInfo is the per-agent sandbox snapshot returned by
// SandboxStatus. Shared between the agent_daemon provider (producer)
// and the dev admin handlers (consumer) so neither package imports the
// other.
type SandboxInfo struct {
	DeviceID    string
	SandboxID   string
	WorkspaceID string
	CreatedAt   time.Time
	LastUsedAt  time.Time
	// ExpiresAt is the e2b-side TTL. Zero means the provider couldn't
	// reach e2b to fetch live state; the admin handler renders it as
	// "—" rather than failing the whole status response.
	ExpiresAt time.Time
}

// PromptInput is the per-prompt request handed to a connector. The same
// shape feeds both Prompt (sync) and StreamPrompt (async). The connector
// is responsible for materializing/reusing whatever session-like object
// it needs from ConversationID; callers never see that session id.
type PromptInput struct {
	// RunID is the Parsar agent_run row id; the connector uses it
	// to scope any per-run scratch state.
	RunID string

	WorkspaceID    string
	ConversationID string
	AgentID        string
	AgentName      string
	AgentSlug      string

	// TriggerMessageContent is the user-facing message that drives this
	// prompt. The connector decides how to fold it into its own prompt
	// schema.
	TriggerMessageContent string

	// TriggerAttachments carries non-text payloads (today: images
	// downloaded from a Feishu inbound message) that arrived alongside
	// TriggerMessageContent. Connectors that don't understand multimodal
	// input may safely ignore the field.
	TriggerAttachments []store.MessageAttachment

	ConversationInitiatorID string

	// AgentConfig is the agent config (agents.config jsonb).
	AgentConfig map[string]any
}

// PromptOutput is the synchronous Prompt result. Streaming connectors
// also emit a final equivalent via PromptEvent{Type: EventDone}.
type PromptOutput struct {
	// Content is the user-facing reply, already sanitized by the
	// connector; callers must NOT re-sanitize.
	Content string

	// Transcript is the optional raw transcript captured by the
	// connector. Empty when the caller should skip writing one.
	Transcript string

	// Usage is the per-prompt token/cost accounting. Connectors that
	// cannot report real usage (Capabilities.Usage=false) return a
	// best-effort placeholder with Provider/Model filled in.
	Usage store.UsageInput

	// Metadata is opaque structured signal handed back to the caller
	// so it can drive mapUserFacingReason / audit / future analytics.
	// Never put secrets or raw model output here.
	Metadata map[string]any
}

// PromptEventType enumerates the SSE-style events a streaming connector
// can emit.
type PromptEventType string

const (
	// EventDelta is an incremental text fragment of the agent reply.
	EventDelta PromptEventType = "delta"
	// EventThinking is an incremental fragment of the model's reasoning
	// trace. Consumers MAY surface it (collapsed UI affordance), but
	// MUST NOT concatenate it into the reply body.
	EventThinking PromptEventType = "thinking"
	// EventToolCall signals the agent invoked a tool.
	EventToolCall PromptEventType = "tool_call"
	// EventPermissionRequest signals the agent asked for human approval
	// (Capabilities.Permissions=true connectors only).
	EventPermissionRequest PromptEventType = "permission_request"
	// EventPromptForUserChoice signals the agent asked the human to
	// pick from a closed list of options (Claude Code's AskUserQuestion
	// tool). The card the gateway renders writes the selected answer
	// back via AgentConnector.SubmitPromptForUserChoice.
	EventPromptForUserChoice PromptEventType = "prompt_for_user_choice"
	// EventUsage signals token usage was reported (incremental or final).
	EventUsage PromptEventType = "usage"
	// EventError signals the prompt failed; subsequent events MUST be
	// EventDone.
	EventError PromptEventType = "error"
	// EventDone signals the prompt is complete; the connector will
	// emit no more events for this prompt.
	EventDone PromptEventType = "done"
)

// PromptEvent is the streaming counterpart of PromptOutput.
type PromptEvent struct {
	Type PromptEventType

	// Sequence is the in-stream ordinal of this event. Monotonically
	// incremented and populated ONLY for EventDelta and EventDone — both
	// originate inside the StreamPrompt goroutine, which owns the
	// counter.
	//
	// Out-of-band events (EventPermissionRequest, EventToolCall,
	// EventError, EventUsage) are emitted from handlers in different
	// goroutines and carry Sequence=0. Consumers MUST NOT rely on
	// Sequence to order out-of-band events relative to deltas or to
	// each other.
	Sequence uint64

	Delta string

	// Thinking is the reasoning-trace fragment when Type == EventThinking.
	// Kept separate from Delta so downstream renderers can place it
	// behind a "Thinking" disclosure rather than splicing it into the
	// user-visible reply body.
	Thinking string

	Tool *ToolCallEvent

	Permission *PermissionRequest

	// PromptForUserChoice is populated when Type ==
	// EventPromptForUserChoice; the gateway uses it to render an
	// interactive card the human picks an answer from.
	PromptForUserChoice *PromptForUserChoiceRequest

	// Usage is populated when Type == EventUsage; for EventDone the
	// final usage is on the matching PromptOutput.
	Usage *store.UsageInput

	Error string

	// Final is populated when Type == EventDone; carries the equivalent
	// of a sync Prompt result so callers do not need to re-accumulate
	// deltas to persist the final message row.
	Final *PromptOutput
}

// ToolCallEvent describes one tool invocation observed by the connector.
// Only connectors with Capabilities.Audit=true populate Args/Result.
type ToolCallEvent struct {
	ID     string
	Name   string
	Stage  string // "before" | "after"
	Args   map[string]any
	Result map[string]any
}

// PermissionRequest describes an agent's request for human approval.
// Connectors with Capabilities.Permissions=true emit one of these and
// wait (inside the connector) for the matching SubmitPermission call.
type PermissionRequest struct {
	ID       string
	DeviceID string
	Tool     string
	Title    string
	Detail   string
	Payload  map[string]any
}

// PermissionDecision is the human verdict for a PermissionRequest,
// submitted via AgentConnector.SubmitPermission.
type PermissionDecision struct {
	RequestID string
	DeviceID  string
	Approved  bool
	Note      string
	By        string // user id
}

// PromptForUserChoiceOption is one selectable answer the human can
// pick. Mirrors proto.PromptForUserChoiceOption so the daemon → server
// translation is a 1-to-1 copy.
type PromptForUserChoiceOption struct {
	Label       string
	Description string
}

// PromptForUserChoiceQuestion is one question in a (possibly multi-
// question) AskUserQuestion call. Mirrors proto.PromptForUserChoiceQuestion.
type PromptForUserChoiceQuestion struct {
	ID          string
	Header      string
	Question    string
	MultiSelect bool
	Options     []PromptForUserChoiceOption
}

// PromptForUserChoiceRequest is the connector-level snapshot of an
// AskUserQuestion call. The gateway renders an interactive card off
// this; the user's pick comes back via SubmitPromptForUserChoice.
//
// Questions is the canonical multi-question payload. The legacy single-
// question fields (Question / Header / MultiSelect / Options) stay
// populated by daemons that haven't been upgraded so server callers
// have a uniform read path via EffectiveQuestions.
type PromptForUserChoiceRequest struct {
	ID        string
	DeviceID  string
	Questions []PromptForUserChoiceQuestion

	// Legacy single-question fields. New daemons leave these empty.
	Question    string
	Header      string
	MultiSelect bool
	Options     []PromptForUserChoiceOption

	// ToolUseID is the originating Claude Code tool_use id. The
	// daemon keeps a local copy for the tool_result write-back; the
	// server holds it for diagnostic logs only.
	ToolUseID string
}

// EffectiveQuestions returns the question list to render. Prefers the
// new Questions slice; falls back to the legacy single-question fields.
func (r PromptForUserChoiceRequest) EffectiveQuestions() []PromptForUserChoiceQuestion {
	if len(r.Questions) > 0 {
		return r.Questions
	}
	if r.Question == "" && len(r.Options) == 0 {
		return nil
	}
	return []PromptForUserChoiceQuestion{{
		Header:      r.Header,
		Question:    r.Question,
		MultiSelect: r.MultiSelect,
		Options:     r.Options,
	}}
}

// PromptForUserChoiceQuestionAnswer carries one structured response.
// QuestionID is canonical and Answers preserves multi-select values;
// Header and Answer remain compatibility fields for older peers.
type PromptForUserChoiceQuestionAnswer struct {
	QuestionID string
	Answers    []string
	Header     string
	Answer     string
}

// PromptForUserChoiceDecision is the human's answer, submitted via
// AgentConnector.SubmitPromptForUserChoice.
//   - QuestionAnswers carries one entry per question (multi-question
//     path). New gateway callers populate this.
//   - Answers length == 1 for single-select; > 1 for multi-select.
//     Legacy single-question callback path keeps writing this and the
//     daemon treats it as belonging to question 0.
//   - Cancelled=true marks a non-answer (timeout, /cancel) so the
//     daemon can emit a "stop, don't retry" tool_result.
type PromptForUserChoiceDecision struct {
	RequestID       string
	DeviceID        string
	QuestionAnswers []PromptForUserChoiceQuestionAnswer
	Answers         []string
	Cancelled       bool
	Reason          string
	By              string
}
