package proto

// This package lives at the repo-root module so both the server-side
// gateway/connector AND apps/parsar-daemon can import it. That rules out
// importing server/internal/... (Go's internal-package rule), so wire
// types like Usage are declared here in full rather than imported from
// store.UsageInput. The connector layer translates at the boundary;
// the wire schema stays decoupled from upstream Go type edits.

// Type constants for daemon → server frames. Names match
// connector.PromptEvent.Type 1:1 so the gateway can translate without
// a per-event lookup table.
const (
	// TypeDelta carries an incremental text fragment. Daemon
	// accumulates these so the matching done frame can carry the
	// full Final.Content.
	TypeDelta = "delta"

	// TypeThinking carries an internal-thinking fragment. Gateway
	// forwards as a plain EventDelta so existing renderers keep
	// working.
	TypeThinking = "thinking"

	// TypeToolCall carries a tool invocation. Stage=="before" runs
	// before the call, "after" runs after.
	TypeToolCall = "tool_call"

	// TypePermissionRequest carries an agent's request for human
	// approval. Envelope.ID = "perm_<8hex>" minted by the daemon.
	TypePermissionRequest = "permission_request"

	// TypePermissionCancel signals the agent withdrew an earlier
	// permission request (e.g. its internal timeout fired). Used by
	// the gateway to unblock pending SubmitPermission calls.
	TypePermissionCancel = "permission_cancel"

	// TypePromptForUserChoice asks the human to pick one (or more)
	// answers from a closed list before the agent can continue. Used
	// to intercept Claude Code's built-in AskUserQuestion tool so the
	// daemon doesn't deadlock waiting for a tool_result no one will
	// send. Envelope.ID = "ask_<8hex>" minted by the daemon.
	TypePromptForUserChoice = "prompt_for_user_choice"

	// TypeUsage reports incremental token / cost usage.
	TypeUsage = "usage"

	// TypeError signals the prompt failed. Daemon MUST emit a Done
	// frame immediately after to close the stream.
	TypeError = "error"

	// TypeDone signals the prompt completed. Payload carries the
	// equivalent of a sync PromptOutput.
	TypeDone = "done"

	// TypeHeartbeat is the daemon's liveness signal. Carries no ID.
	// Gateway uses arrival time to detect dead sessions.
	TypeHeartbeat = "heartbeat"
)

// DeltaPayload carries an incremental text fragment from the agent.
type DeltaPayload struct {
	Delta    string `json:"delta"`
	Sequence uint64 `json:"sequence"`
}

// ThinkingPayload carries an internal-thinking fragment.
type ThinkingPayload struct {
	Text     string `json:"text"`
	Sequence uint64 `json:"sequence,omitempty"`
}

// ToolCallPayload carries a tool invocation event. Stage is "before"
// when the agent is about to call the tool, "after" when the result
// is back.
type ToolCallPayload struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Stage  string         `json:"stage"`
	Args   map[string]any `json:"args,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// PermissionRequestPayload carries an agent's request for human
// approval. The gateway promotes Envelope.ID to PermissionRequest.ID
// so caller and daemon agree on which request is which.
type PermissionRequestPayload struct {
	Tool    string         `json:"tool"`
	Title   string         `json:"title"`
	Detail  string         `json:"detail,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// PromptForUserChoiceOption is one button / checkbox the user can
// pick when answering a PromptForUserChoice. Label is the human-
// readable choice; Description is optional inline help.
type PromptForUserChoiceOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PromptForUserChoiceQuestion is one question in a (possibly multi-
// question) AskUserQuestion call. Mirrors the Claude Code built-in
// schema verbatim so the daemon doesn't translate the shape twice.
type PromptForUserChoiceQuestion struct {
	ID          string                      `json:"id"`
	Header      string                      `json:"header,omitempty"`
	Question    string                      `json:"question"`
	MultiSelect bool                        `json:"multi_select,omitempty"`
	Options     []PromptForUserChoiceOption `json:"options"`
}

// PromptForUserChoicePayload carries the AskUserQuestion interception.
//
// AskID is the daemon-minted "ask_<8hex>" handle the server uses to
// route SubmitPromptForUserChoice back to the right session. It rides
// on the payload (not Envelope.ID) because Envelope.ID is reserved for
// the run id — that's the field server-side session.dispatch fans on
// to deliver the frame to the run's subscriber channel. ToolUseID is
// the originating Claude Code tool_use id; empty when the call came
// through the control_request channel (CCRequestID then identifies the
// daemon-side waiter instead but it doesn't ride on the wire).
//
// The legacy single-question fields (Question / Header / MultiSelect /
// Options) stay on the wire so older server/db snapshots can still be
// decoded. New code writes Questions; readers must call
// EffectiveQuestions to get a unified view across both shapes.
type PromptForUserChoicePayload struct {
	AskID     string                        `json:"ask_id"`
	Questions []PromptForUserChoiceQuestion `json:"questions,omitempty"`
	ToolUseID string                        `json:"tool_use_id,omitempty"`

	// Legacy single-question fields — read-only on the new path. Empty
	// when Questions is populated.
	Question    string                      `json:"question,omitempty"`
	Header      string                      `json:"header,omitempty"`
	MultiSelect bool                        `json:"multi_select,omitempty"`
	Options     []PromptForUserChoiceOption `json:"options,omitempty"`
}

// EffectiveQuestions returns the question list a consumer should
// render. Prefers the new Questions slice; falls back to the legacy
// single-question fields so old payloads still work after a restart.
func (p PromptForUserChoicePayload) EffectiveQuestions() []PromptForUserChoiceQuestion {
	if len(p.Questions) > 0 {
		return p.Questions
	}
	if p.Question == "" && len(p.Options) == 0 {
		return nil
	}
	return []PromptForUserChoiceQuestion{{
		Header:      p.Header,
		Question:    p.Question,
		MultiSelect: p.MultiSelect,
		Options:     p.Options,
	}}
}

// Usage mirrors server/internal/store.UsageInput on the wire — field
// names and JSON tags identical so the connector boundary copies with
// a one-liner translator. Redeclared (not imported) because this
// package must stay free of server/internal dependencies.
type Usage struct {
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	InputTokens  int32          `json:"input_tokens,omitempty"`
	OutputTokens int32          `json:"output_tokens,omitempty"`
	CostUSD      float64        `json:"cost_usd,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

// UsagePayload carries a Usage update mid-stream.
type UsagePayload struct {
	Usage
}

// ErrorPayload reports a prompt-level failure.
type ErrorPayload struct {
	Error string `json:"error"`
}

// DonePayload mirrors connector.PromptOutput shape. Redeclared (not
// embedded) so a refactor of PromptOutput doesn't silently flip the
// wire shape.
type DonePayload struct {
	Content    string         `json:"content"`
	Transcript string         `json:"transcript,omitempty"`
	Usage      Usage          `json:"usage,omitzero"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

const (
	DoneMetaAgentSessionID   = "agent_session_id"
	DoneMetaAgentSessionType = "agent_session_type"
)

// AgentKindCapabilities describes what a daemon-side agent_kind can
// do inside one prompt session. Runtime-level capabilities such as
// cancellation belong to the daemon connector itself; these bits are
// the engine-specific surface the UI uses for filtering and copy.
type AgentKindCapabilities struct {
	Streaming   bool `json:"streaming,omitempty"`
	Permissions bool `json:"permissions,omitempty"`
	Usage       bool `json:"usage,omitempty"`
	Resume      bool `json:"resume,omitempty"`
}

// SupportedAgentKind is one daemon-advertised agent engine. Daemons
// can report unavailable kinds with Available=false when the adapter
// exists but the underlying CLI binary is missing.
type SupportedAgentKind struct {
	Kind         string                `json:"kind"`
	Available    bool                  `json:"available"`
	Version      string                `json:"version,omitempty"`
	Capabilities AgentKindCapabilities `json:"capabilities,omitempty"`
}

// HeartbeatPayload is the daemon's liveness ping. supported_agent_kinds
// is preferred over the legacy claude_available flag; old daemons may
// still send only claude_available, and the server infers claude_code
// support.
type HeartbeatPayload struct {
	Timestamp           int64                `json:"ts"`
	ActiveRequests      int                  `json:"active_requests"`
	DaemonVersion       string               `json:"daemon_version,omitempty"`
	ClaudeAvailable     bool                 `json:"claude_available,omitempty"`
	SupportedAgentKinds []SupportedAgentKind `json:"supported_agent_kinds,omitempty"`
}
