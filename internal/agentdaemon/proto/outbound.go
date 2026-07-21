package proto

// Type constants for server → daemon frames.
const (
	// TypePromptRequest triggers one prompt cycle. Envelope.ID = RunID;
	// the daemon stamps every resulting upstream frame with the same
	// ID so the gateway can fan them back to the matching StreamPrompt
	// subscriber.
	TypePromptRequest = "prompt_request"

	// TypePromptCancel aborts an in-flight prompt. Envelope.ID =
	// RunID. Idempotent — cancelling an unknown / already-finished
	// run is a no-op on the daemon side.
	TypePromptCancel = "prompt_cancel"

	// TypePermissionDecision delivers a human verdict back to the
	// daemon. Envelope.ID = the perm_<8hex> id the daemon minted in
	// the matching permission_request.
	TypePermissionDecision = "permission_decision"

	// TypePromptForUserChoiceDecision delivers the human's answer back
	// to the daemon. Envelope.ID = the ask_<8hex> id the daemon minted
	// in the matching prompt_for_user_choice frame.
	TypePromptForUserChoiceDecision = "prompt_for_user_choice_decision"

	// TypeDeviceShutdown asks the daemon to exit gracefully (SIGTERM
	// child processes, flush state, close the socket). Ignored by
	// long-lived local devices unless the operator explicitly
	// requested it.
	TypeDeviceShutdown = "device_shutdown"
)

// PromptRequestPayload is the daemon-side view of a connector.PromptInput,
// trimmed to fields a daemon agent actually needs. Kept separate from
// PromptInput so future agent implementations can evolve the wire shape
// without touching the connector surface.
type PromptRequestPayload struct {
	// AgentKind selects which agent implementation the daemon
	// dispatches to.
	AgentKind string `json:"agent_kind"`

	// ConversationID lets the daemon scope per-conversation state
	// (Claude --resume session id, scratch dir).
	ConversationID string `json:"conversation_id"`

	// RunID is the Parsar agent_run id; mirrored back on every
	// upstream frame via Envelope.ID.
	RunID string `json:"run_id"`

	// Prompt is the user-facing message that drives this turn.
	Prompt string `json:"prompt"`

	// Attachments carries non-text payloads (images from inbound
	// messages) alongside Prompt. The daemon-side agent decides how
	// to fold them in: claude_code re-encodes them into Anthropic
	// image content blocks on the stdin-driven JSON input loop.
	// Silently ignored when the agent doesn't understand multimodal
	// input — Prompt alone still drives the run.
	Attachments []PromptAttachment `json:"attachments,omitempty"`

	// WorkDir is the cwd for the agent subprocess. Local mode: user's
	// chosen project root. Sandbox mode: empty — the daemon falls
	// back to a per-conversation scratch dir so plugin installs and
	// the subprocess cwd stay on the same tree.
	WorkDir string `json:"work_dir,omitempty"`

	// AgentOptions carries agent-specific overrides (model, mode,
	// allowed_tools, system_prompt, mcp_servers, plugin_dirs, env,
	// ...). The daemon's agent interprets these; the gateway never
	// inspects them.
	AgentOptions map[string]any `json:"agent_options,omitempty"`

	// AgentSessionID is the upstream engine session id to resume.
	AgentSessionID string `json:"agent_session_id,omitempty"`

	// AgentStateKey is the stable daemon-side state directory key.
	AgentStateKey string `json:"agent_state_key,omitempty"`
}

// PromptAttachment is one piece of non-text user input the daemon-side
// agent should fold into the turn alongside Prompt. The field set is
// forward-compatible with file/audio so a wire-schema bump isn't
// required when those land.
//
// DataBase64 is standard-base64 raw bytes; the daemon decodes once
// before forwarding to its agent adapter (claude_code re-wraps as an
// Anthropic image content block on stdin). MIME is forwarded verbatim
// so the agent picks the right block shape (image/png vs image/jpeg).
type PromptAttachment struct {
	Kind       string `json:"kind"`
	MIME       string `json:"mime"`
	DataBase64 string `json:"data_base64"`
}

// PromptCancelPayload is intentionally empty; the run identity is on
// Envelope.ID. Declared so future fields (e.g. Reason) don't require a
// wire-format bump.
type PromptCancelPayload struct{}

// PermissionDecisionPayload carries the human verdict. UpdatedInput
// lets the approver edit the tool input before letting the call
// proceed (Claude Code's allow-with-changes path).
type PermissionDecisionPayload struct {
	DeliveryID   string         `json:"delivery_id"`
	Approved     bool           `json:"approved"`
	Message      string         `json:"message,omitempty"`
	UpdatedInput map[string]any `json:"updated_input,omitempty"`
}

// PromptForUserChoiceQuestionAnswer carries one (question, answer)
// pair from a multi-question submit. QuestionID is the canonical key;
// Header and Answer remain as compatibility fields for older peers.
type PromptForUserChoiceQuestionAnswer struct {
	QuestionID string   `json:"question_id,omitempty"`
	Answers    []string `json:"answers,omitempty"`
	Header     string   `json:"header,omitempty"`
	Answer     string   `json:"answer,omitempty"`
}

// PromptForUserChoiceDecisionPayload carries the human's pick. The
// daemon turns this into a tool_result JSON the agent's stdin
// consumes.
//
//   - QuestionAnswers carries one entry per question, keyed by stable
//     QuestionID with the selected values preserved as an array.
//   - Answers length == 1 for single-select; length N for multi-select.
//     Legacy single-question callers may still write this; the daemon
//     treats it as "all answers belong to question 0".
//   - Cancelled=true marks a non-answer (timeout, /cancel). Reason is
//     a short machine tag (e.g. "timeout"); the daemon converts it
//     into a tool_result message the LLM understands.
type PromptForUserChoiceDecisionPayload struct {
	DeliveryID      string                              `json:"delivery_id"`
	QuestionAnswers []PromptForUserChoiceQuestionAnswer `json:"question_answers,omitempty"`
	Answers         []string                            `json:"answers,omitempty"`
	Cancelled       bool                                `json:"cancelled,omitempty"`
	Reason          string                              `json:"reason,omitempty"`
}

// DeviceShutdownPayload tells the daemon why we're closing it (for log
// lines / metrics on the daemon side). Optional.
type DeviceShutdownPayload struct {
	Reason string `json:"reason,omitempty"`
}
