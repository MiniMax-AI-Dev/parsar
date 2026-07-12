// Package codex is the agent_kind="codex" adapter. It drives the
// OpenAI Codex CLI (codex-rs / @openai/codex) via `codex app-server --stdio`,
// speaking the JSON-RPC 2.0 protocol that the app-server exposes over
// stdio.
//
// Codex differs from the claudecode and opencode adapters in two ways:
//
//  1. The wire protocol is JSON-RPC (request / response / notification /
//     server-request) rather than NDJSON event-stream. See rpc.go.
//
//  2. Multi-turn context lives in a Codex "thread" identified by the
//     thread_id returned in the first `thread/started` notification.
//     The daemon stamps that id into DonePayload.Metadata["agent_session_id"]
//     so the connector's RememberSession path persists it.
//     Subsequent turns spawn a fresh app-server and call `thread/resume`
//     with that id to graft the prior turn's context back in.
package codex

// JsonRpcVersion is the JSON-RPC 2.0 marker carried on every outbound
// frame. Inbound frames omit the field per Codex's app-server convention,
// so the parser does not enforce it on responses.
const JsonRpcVersion = "2.0"

// ---------------------------------------------------------------------------
// JSON-RPC envelopes (over stdio NDJSON)
// ---------------------------------------------------------------------------

// JsonRpcRequest is an outbound RPC call (client → codex). id is a
// daemon-minted 16-hex string; an empty id marks a notification (no
// response expected).
type JsonRpcRequest struct {
	JsonRpc string `json:"jsonrpc"`
	ID      string `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// JsonRpcResponse is an outbound reply to a server-initiated request.
// Either Result or Error must be non-nil.
type JsonRpcResponse struct {
	JsonRpc string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JsonRpcError `json:"error,omitempty"`
}

// JsonRpcError carries a structured failure reply. Codes follow the
// JSON-RPC 2.0 spec (-32601 method-not-found, -32603 internal error,
// etc.).
type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ---------------------------------------------------------------------------
// initialize handshake
// ---------------------------------------------------------------------------

// InitializeCapabilities mirrors codex-rs/app-server/src/protocol.rs.
// experimentalApi=true opts into the granular AskForApproval enum and
// the thread/* notification stream.
type InitializeCapabilities struct {
	ExperimentalAPI    bool      `json:"experimentalApi"`
	RequestAttestation bool      `json:"requestAttestation"`
	OptOutMethods      *[]string `json:"optOutNotificationMethods,omitempty"`
}

type InitializeClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeParams struct {
	ClientInfo   InitializeClientInfo    `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities"`
}

type InitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily,omitempty"`
	PlatformOs     string `json:"platformOs,omitempty"`
}

// ---------------------------------------------------------------------------
// Approval / sandbox policies
// ---------------------------------------------------------------------------

// GranularAskForApproval names each approval gate the codex agent can
// surface. All-false produces a fully silent run; turning any single
// field true makes the corresponding ServerRequest reach the daemon for
// human approval.
//
// JSON tags MUST stay snake_case — codex-rs deserialises this struct
// from a config TOML / RPC param with field names like sandbox_approval,
// and renaming silently breaks the wire.
type GranularAskForApproval struct {
	SandboxApproval    bool `json:"sandbox_approval"`
	Rules              bool `json:"rules"`
	SkillApproval      bool `json:"skill_approval"`
	RequestPermissions bool `json:"request_permissions"`
	MCPElicitations    bool `json:"mcp_elicitations"`
}

// AskForApproval is the discriminated union codex accepts on
// ThreadStartParams.approvalPolicy. The serialiser must emit EITHER
// {"granular": {...}} OR a bare string ("never" / "on-request" /
// "on-failure" / "untrusted"). See MarshalJSON in approval_policy.go.
type AskForApproval struct {
	String   string                  // "" when granular is set
	Granular *GranularAskForApproval // nil when string is set
}

// SandboxMode is the wire-level sandbox setting codex's v2 thread/start
// API accepts. Serializes to a kebab-case string per
// codex-rs/app-server-protocol/src/protocol/v2/shared.rs::SandboxMode.
//
// Earlier (~0.137-era) the field on ThreadStartParams was named
// `sandboxPolicy` and took a tagged object `{type: "dangerFullAccess"}`.
// 0.141.0 renamed it to `sandbox` and flattened it to one of these three
// strings. Sending the old shape no longer errors loudly — codex just
// silently falls back to read-only, which makes prompts terminate
// immediately with no agent output. Keep the constants pinned exactly
// to the kebab values upstream serializes.
type SandboxMode string

const (
	SandboxReadOnly        SandboxMode = "read-only"
	SandboxWorkspaceWrite  SandboxMode = "workspace-write"
	SandboxDangerFullAcces SandboxMode = "danger-full-access"
)

// SandboxPolicy is the legacy compound type kept for internal
// representation only — codex still echoes it back on some response
// shapes (turn_context inside rollout files, for example). It is NOT
// what ThreadStartParams.Sandbox takes on the wire.
type SandboxPolicy struct {
	Type                SandboxMode `json:"type"`
	WritableRoots       []string    `json:"writableRoots,omitempty"`
	NetworkAccess       bool        `json:"networkAccess,omitempty"`
	ExcludeTmpdirEnvVar bool        `json:"excludeTmpdirEnvVar,omitempty"`
	ExcludeSlashTmp     bool        `json:"excludeSlashTmp,omitempty"`
}

// ---------------------------------------------------------------------------
// thread/start, thread/resume, thread/list
// ---------------------------------------------------------------------------

type ThreadStartParams struct {
	Cwd            string         `json:"cwd"`
	Model          string         `json:"model,omitempty"`
	ModelProvider  string         `json:"modelProvider,omitempty"`
	ApprovalPolicy AskForApproval `json:"approvalPolicy"`
	// Sandbox is the v0.141+ field name; previously called sandboxPolicy
	// and took a tagged-enum object. Wire format now is a kebab-case
	// string: "read-only" / "workspace-write" / "danger-full-access".
	// Sending the old object shape causes codex to silently default to
	// read-only, which terminates the turn before the model can reply.
	Sandbox               SandboxMode `json:"sandbox,omitempty"`
	DeveloperInstructions string      `json:"developerInstructions,omitempty"`
	RuntimeWorkspaceRoots []string    `json:"runtimeWorkspaceRoots,omitempty"`
}

type Thread struct {
	ID         string `json:"id"`
	Cwd        string `json:"cwd,omitempty"`
	Preview    string `json:"preview,omitempty"`
	UpdatedAt  int64  `json:"updatedAt,omitempty"`
	CreatedAt  int64  `json:"createdAt,omitempty"`
	Status     any    `json:"status,omitempty"`
	Path       string `json:"path,omitempty"`
	CLIVersion string `json:"cliVersion,omitempty"`
}

type ThreadStartResult struct {
	Thread         Thread          `json:"thread"`
	Model          string          `json:"model,omitempty"`
	ApprovalPolicy *AskForApproval `json:"approvalPolicy,omitempty"`
	Sandbox        *SandboxPolicy  `json:"sandbox,omitempty"`
}

type ThreadResumeParams struct {
	ThreadID string `json:"threadId"`
}

// ---------------------------------------------------------------------------
// turn/start, turn/interrupt
// ---------------------------------------------------------------------------

// UserInputType discriminates a UserInput element. Codex accepts mixed
// arrays of text + image entries on a single turn.
type UserInputType string

const (
	UserInputText       UserInputType = "text"
	UserInputLocalImage UserInputType = "localImage"
	UserInputRemoteImg  UserInputType = "image"
)

// UserInput is one element of TurnStartParams.Input. Each variant uses a
// distinct field set; producers must populate only the fields that
// match Type.
type UserInput struct {
	Type UserInputType `json:"type"`
	// Text variant
	Text         string `json:"text,omitempty"`
	TextElements []any  `json:"text_elements,omitempty"`
	// LocalImage variant
	Path string `json:"path,omitempty"`
	// Image (remote URL) variant
	URL string `json:"url,omitempty"`
}

type TurnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
}

type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
}

type TurnUsage struct {
	InputTokens          int `json:"inputTokens,omitempty"`
	OutputTokens         int `json:"outputTokens,omitempty"`
	CachedInputTokens    int `json:"cachedInputTokens,omitempty"`
	CacheReadInputTokens int `json:"cacheReadInputTokens,omitempty"`
	TotalTokens          int `json:"totalTokens,omitempty"`
}

type Turn struct {
	ID     string     `json:"id"`
	Usage  *TurnUsage `json:"usage,omitempty"`
	Status string     `json:"status,omitempty"`
	// Error is populated on turn.status="failed" payloads. codex packs
	// upstream provider errors (gateway 4xx/5xx, model-server failures)
	// here as a JSON-encoded inner blob in `message`; the
	// CodexErrorInfo field categorises ("rate_limit", "context_window",
	// "other") and AdditionalDetails carries structured retry hints.
	Error *TurnError `json:"error,omitempty"`
}

// TurnError mirrors codex's turn error payload. The Message field is
// the human-readable error body — for OpenAI-Responses-style gateways
// it typically contains a stringified JSON {"error":{"code","message",
// "type"}}. The daemon surfaces it verbatim so operators can paste it
// directly into a ticket; UI may pretty-print at a later stage.
type TurnError struct {
	Message           string         `json:"message,omitempty"`
	CodexErrorInfo    string         `json:"codexErrorInfo,omitempty"`
	AdditionalDetails map[string]any `json:"additionalDetails,omitempty"`
}

// ---------------------------------------------------------------------------
// ThreadItem (the variants we map; the rest fall through to a generic
// catch-all so codex upgrades that add new item kinds do not break
// parsing).
// ---------------------------------------------------------------------------

// ThreadItem is decoded loosely: parser keeps the raw JSON around so
// future fields can be inspected via a second unmarshal without round-
// tripping every variant through a hand-written struct.
type ThreadItem struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	// agentMessage / reasoning
	Text        string   `json:"text,omitempty"`
	Summary     []string `json:"summary,omitempty"`
	Content     []string `json:"content,omitempty"`
	SummaryText string   `json:"summary_text,omitempty"`

	// commandExecution
	Command  string `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Status   string `json:"status,omitempty"`

	// fileChange
	Changes []map[string]any `json:"changes,omitempty"`

	// mcpToolCall
	Server    string `json:"server,omitempty"`
	Tool      string `json:"tool,omitempty"`
	Arguments any    `json:"arguments,omitempty"`

	// dynamicToolCall
	Namespace string `json:"namespace,omitempty"`

	// webSearch
	Query string `json:"query,omitempty"`
}

// ---------------------------------------------------------------------------
// Notification params we subscribe to (param shapes only — method names
// live as constants in session.go's handler registration).
// ---------------------------------------------------------------------------

type ThreadStartedNotification struct {
	Thread Thread `json:"thread"`
}

type TurnStartedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type TurnCompletedNotification struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

type ItemStartedNotification struct {
	ThreadID    string     `json:"threadId"`
	TurnID      string     `json:"turnId"`
	Item        ThreadItem `json:"item"`
	StartedAtMs int64      `json:"startedAtMs,omitempty"`
}

type ItemCompletedNotification struct {
	ThreadID      string     `json:"threadId"`
	TurnID        string     `json:"turnId"`
	Item          ThreadItem `json:"item"`
	CompletedAtMs int64      `json:"completedAtMs,omitempty"`
}

type AgentMessageDeltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type ReasoningDeltaNotification = AgentMessageDeltaNotification

type ThreadTokenUsageUpdatedNotification struct {
	ThreadID string    `json:"threadId"`
	Usage    TurnUsage `json:"usage"`
}

type ErrorNotification struct {
	Message string `json:"message,omitempty"`
}

// ---------------------------------------------------------------------------
// Approval ServerRequest params (suppressed by the all-false granular
// policy today; left here so server_requests.go can compile against
// these shapes when surfaceApprovals is later flipped on).
// ---------------------------------------------------------------------------

type CommandExecutionRequestApprovalParams struct {
	ThreadID string  `json:"threadId"`
	TurnID   string  `json:"turnId"`
	ItemID   string  `json:"itemId"`
	Command  *string `json:"command,omitempty"`
	Cwd      *string `json:"cwd,omitempty"`
	Reason   *string `json:"reason,omitempty"`
}

type FileChangeRequestApprovalParams struct {
	ThreadID  string  `json:"threadId"`
	TurnID    string  `json:"turnId"`
	ItemID    string  `json:"itemId"`
	Reason    *string `json:"reason,omitempty"`
	GrantRoot *string `json:"grantRoot,omitempty"`
}

type PermissionsRequestApprovalParams struct {
	ThreadID    string  `json:"threadId"`
	TurnID      string  `json:"turnId"`
	ItemID      string  `json:"itemId"`
	Cwd         string  `json:"cwd"`
	Reason      *string `json:"reason,omitempty"`
	Permissions any     `json:"permissions,omitempty"`
}

// CommandExecutionApprovalDecision is the verdict the daemon writes
// back to a Codex approval ServerRequest. "accept" / "decline" / "cancel"
// / "acceptForSession".
type CommandExecutionApprovalDecision = string

// ApprovalDecisionResult is the result body in a JSON-RPC response for
// any of the three approval requests.
type ApprovalDecisionResult struct {
	Decision CommandExecutionApprovalDecision `json:"decision"`
}
