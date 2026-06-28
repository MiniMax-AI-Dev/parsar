package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	guuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type Store struct {
	db                  sqlc.DBTX
	audit               *audit.Ingester
	streamingDispatcher StreamingDispatcher
}

// StreamingDispatchInput is the per-run handoff to the post-commit
// streaming-dispatch hook. Kept tiny so Store doesn't depend on the
// dev / connector packages (cycle).
type StreamingDispatchInput struct {
	RunID          string
	ConversationID string
	ConnectorType  string
}

// StreamingDispatcher kicks off a freshly-created AgentRun for streaming
// connectors. Fire-and-forget: implementations log + call FailAgentRun on
// internal errors rather than blocking the post-commit hook.
type StreamingDispatcher interface {
	Start(ctx context.Context, in StreamingDispatchInput)
}

type Option func(*Store)

// WithAudit attaches an asynchronous audit Ingester. nil is permitted.
func WithAudit(ing *audit.Ingester) Option {
	return func(s *Store) {
		s.audit = ing
	}
}

func New(db sqlc.DBTX, opts ...Option) *Store {
	s := &Store{db: db}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SetStreamingDispatcher installs the post-commit streaming dispatch hook
// after Store construction (main builds Store first, then the dispatcher,
// then wires it back). Safe to call with nil.
func (s *Store) SetStreamingDispatcher(d StreamingDispatcher) {
	s.streamingDispatcher = d
}

// emitAuditEvent forwards an event to the configured audit ingester.
// Nil-safe; Emit errors are observability-only and intentionally swallowed —
// business code MUST NOT fail because audit emit failed.
func (s *Store) emitAuditEvent(ev audit.Event) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Emit(ev)
}

// dispatchPendingStreaming runs the post-commit streaming-dispatch hook for
// freshly-created AgentRuns on a streaming connector. Nil-safe.
// StreamingDispatcher.Start is fire-and-forget.
func (s *Store) dispatchPendingStreaming(ctx context.Context, pending []StreamingDispatchInput) {
	if s.streamingDispatcher == nil || len(pending) == 0 {
		return
	}
	for _, in := range pending {
		s.streamingDispatcher.Start(ctx, in)
	}
}

// dispatchNextQueuedRunAfter is invoked by run terminators AFTER their
// transaction commits. Looks for the oldest queued run on the same
// (conversation, project_agent) and hands it to the streaming dispatcher.
// Nil-safe. Errors are swallowed — missed dequeues get picked up by a
// subsequent terminator or the next inbound message.
func (s *Store) dispatchNextQueuedRunAfter(ctx context.Context, finishedRunID string) {
	if s.streamingDispatcher == nil {
		return
	}
	next, err := s.DequeueNextRunForConversationAgent(ctx, finishedRunID)
	if err != nil || next == nil {
		return
	}
	// Only streaming connectors participate in the per-conversation serial queue.
	if !connectorNeedsStreamingDispatch(next.ConnectorType) {
		return
	}
	s.streamingDispatcher.Start(ctx, StreamingDispatchInput{
		RunID:          next.RunID,
		ConversationID: next.ConversationID,
		ConnectorType:  next.ConnectorType,
	})
}

type DevFixtureIDs struct {
	UserID                string
	FeishuAuthIdentityID  string
	WorkspaceID           string
	WorkspaceMemberID     string
	ProjectID             string
	ProductAgentID        string
	BackendAgentID        string
	TestAgentID           string
	ProductProjectAgentID string
	BackendProjectAgentID string
	TestProjectAgentID    string
	ConversationID        string
}

type DevSeedResult struct {
	CredentialKinds  int64
	Users            int64
	AuthIdentities   int64
	Workspaces       int64
	WorkspaceMembers int64
	Projects         int64
	Agents           int64
	ProjectAgents    int64
	Conversations    int64
}

// MessageAttachment is non-text content alongside a user message.
// Persistence: messages.metadata.attachments as
// {kind, mime, size, data_base64} maps.
type MessageAttachment struct {
	Kind string `json:"kind"`

	// MIME is the upstream-reported content type. Falls back to a
	// kind-specific default when upstream omits it.
	MIME string `json:"mime"`

	// Size is the byte count before base64 expansion. Cached so
	// observability surfaces don't have to decode.
	Size int `json:"size,omitempty"`

	// DataBase64 is the standard-base64 encoded raw bytes. Callers
	// MUST NOT base64-encode again on persistence.
	DataBase64 string `json:"data_base64"`
}

type CreateInboundIMMessageInput struct {
	ConversationTitle string
	ConversationForm  string
	SenderEmail       string
	Text              string
	Mentions          []string
	Source            string
	Gateway           string
	ExternalUserID    string
	// InitiatorUserID is an optional pre-resolved Parsar user_id.
	// When set, it short-circuits the gateway-subject / email lookup
	// path and is used directly as the message sender and agent_run
	// requested_by. Intended for INTERNAL re-enqueue callers that
	// already know the originating user (e.g. the ADR-004 credential-
	// form submit handler, which has the user_id on the slot and
	// would otherwise need to translate open_id → union_id via a
	// Feishu API round-trip just to populate ExternalUserID).
	//
	// External callers should leave this empty and rely on
	// ExternalUserID / SenderEmail resolution like the original inbound
	// path. When both are supplied, InitiatorUserID wins.
	InitiatorUserID string
	// SenderOpenID is the raw platform-side per-app sender identifier
	// (Feishu `open_id`). Stored in messages.metadata.sender_open_id so the
	// credential-form submit callback (which only carries open_id, not
	// union_id) can verify the click came from the same person.
	SenderOpenID      string
	ExternalChatID    string
	ExternalThreadID  string
	ExternalMessageID string
	TargetAgentID     string
	SourceAppID       string
	Metadata          map[string]any
}

type CreateInboundIMMessageResult struct {
	MessageID      string
	RunIDs         []string
	Mentions       []string
	CreatedAt      time.Time
	WorkspaceID    string
	ProjectID      string
	ConversationID string
}

type SendUserMessageToConversationInput struct {
	ConversationID    string
	UserID            string
	Content           string
	MentionedAgentIDs []string
}

type SendUserMessageToConversationResult struct {
	Message MessageRead
	RunIDs  []string
}

type CompleteAgentRunInput struct {
	RunID      string
	Source     string
	Content    string
	Transcript string
	Usage      UsageInput
}

type MarkAgentRunRunningResult struct {
	RunID          string    `json:"run_id"`
	WorkspaceID    string    `json:"workspace_id"`
	ProjectID      string    `json:"project_id"`
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	StartedAt      time.Time `json:"started_at"`
}

type SendAssistantMessageFromRunInput struct {
	RunID      string
	Source     string
	Content    string
	Transcript string
	Usage      UsageInput
}

type UsageInput struct {
	Provider     string         `json:"provider,omitempty"`
	Model        string         `json:"model,omitempty"`
	InputTokens  int32          `json:"input_tokens,omitempty"`
	OutputTokens int32          `json:"output_tokens,omitempty"`
	CostUSD      float64        `json:"cost_usd,omitempty"`
	Raw          map[string]any `json:"raw,omitempty"`
}

type CompleteAgentRunResult struct {
	RunID           string
	MessageID       string
	Status          string
	WorkspaceID     string
	ProjectID       string
	ConversationID  string
	ProjectAgentID  string
	AgentID         string
	ChildRunIDs     []string
	SkippedMentions []SkippedAgentMention
	StartedAt       time.Time
	FinishedAt      time.Time
	Usage           UsageLogRead
}

type SkippedAgentMention struct {
	Mention        string `json:"mention"`
	ProjectAgentID string `json:"project_agent_id,omitempty"`
	Reason         string `json:"reason"`
}

type ProjectAgentRead struct {
	ProjectAgentID string         `json:"project_agent_id"`
	ProjectID      string         `json:"project_id"`
	AgentID        string         `json:"agent_id"`
	Name           string         `json:"name"`
	Slug           string         `json:"slug"`
	Description    string         `json:"description"`
	ConnectorType  string         `json:"connector_type"`
	Status         string         `json:"status"`
	Runtime        *string        `json:"runtime,omitempty"`
	Config         map[string]any `json:"config"`
	AgentConfig    map[string]any `json:"agent_config"`

	// Visibility carries the workspace-level Agent visibility. Defaults to "workspace".
	Visibility string `json:"visibility,omitempty"`

	// CreatedByUserID / CreatedByName carry the Agent's owner so the
	// admin list can disambiguate same-named Agents. Name is empty when
	// the creating user is gone or the row pre-dates the field.
	CreatedByUserID string `json:"created_by_user_id,omitempty"`
	CreatedByName   string `json:"created_by_name,omitempty"`

	EnabledAt time.Time `json:"enabled_at"`

	// Explicit runtime binding on the project_agent. Empty when no
	// runtime is bound — dispatch is blocked in that state. RuntimeKind
	// mirrors runtimes.type.
	RuntimeID       string `json:"runtime_id,omitempty"`
	RuntimeName     string `json:"runtime_name,omitempty"`
	RuntimeKind     string `json:"runtime_kind,omitempty"`
	RuntimeLiveness string `json:"runtime_liveness,omitempty"`

	// Currently-bound sandbox. Empty when none. SandboxStatus mirrors
	// sandboxes.lifecycle_status, keyed off the same
	// `allocation_status = 'bound' AND killed_at IS NULL` predicate as
	// GetActiveSandboxBindingForAgent.
	SandboxExternalID string `json:"sandbox_external_id,omitempty"`
	SandboxStatus     string `json:"sandbox_status,omitempty"`
}

type WorkspaceSettingsRead struct {
	WorkspaceID string `json:"workspace_id"`
}

type WorkspaceRuntimeSettingsRead struct {
	WorkspaceID               string         `json:"workspace_id"`
	RuntimeCredentialSecretID string         `json:"runtime_credential_secret_id,omitempty"`
	RuntimeConfig             map[string]any `json:"runtime_config"`
	RuntimeCredentialMasked   string         `json:"runtime_credential_masked,omitempty"`
	// SandboxAgentCount is the number of active project-agent bindings in
	// this workspace whose daemon_mode is 'sandbox'.
	SandboxAgentCount int64 `json:"sandbox_agent_count"`
}

type AgentSummary struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	Name          string         `json:"name"`
	Slug          string         `json:"slug"`
	Description   string         `json:"description"`
	ConnectorType string         `json:"connector_type"`
	Visibility    string         `json:"visibility,omitempty"`
	Status        string         `json:"status"`
	Capabilities  []string       `json:"capabilities"`
	Config        map[string]any `json:"config"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type ProjectAgentSummary struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	ProjectID   string         `json:"project_id"`
	AgentID     string         `json:"agent_id"`
	Status      string         `json:"status"`
	Config      map[string]any `json:"config"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type InitialAgentCapabilityInput struct {
	CapabilityVersionID string
	Configuration       map[string]any
	// PinningMode is "latest" or "pinned"; empty falls back to the
	// store-side default ("pinned"). The create-agent dialog defaults the
	// dropdown to "latest" so reuploads of a workspace capability are
	// picked up automatically.
	PinningMode string
}

type CreateAgentInput struct {
	WorkspaceID         string
	ProjectID           string
	Name                string
	Description         string
	ConnectorType       string
	SystemPrompt        string
	DefaultModelID      string
	Capabilities        []string
	CapabilitiesSet     bool
	InitialCapabilities []InitialAgentCapabilityInput
	Runtime             string
	ProjectAgentConfig  map[string]any
	Visibility          string
	Slug                string
	CreatedBy           string
}

type CreateAgentResult struct {
	Agent               AgentSummary          `json:"agent"`
	ProjectAgent        ProjectAgentSummary   `json:"project_agent"`
	InitialCapabilities []AgentCapabilityRead `json:"initial_capabilities,omitempty"`
}

type UpdateAgentInput struct {
	AgentID         string
	ActorID         string
	Name            *string
	Description     *string
	ConnectorType   *string
	SystemPrompt    *string
	DefaultModelID  *string
	Capabilities    []string
	CapabilitiesSet bool
	// Config carries new agent_config keys (today: credential_bindings,
	// model_credential_binding). Only these two keys are merged; the rest
	// are owned by dedicated setters above.
	Config    map[string]any
	ConfigSet bool
}

type DeleteAgentResult struct {
	Agent                   AgentSummary `json:"agent"`
	DetachedProjectAgentIDs []string     `json:"detached_project_agent_ids"`
}

type HTTPAgentRunInvocation = AgentRunInvocation

type AgentRunInvocation struct {
	RunID                 string `json:"run_id"`
	WorkspaceID           string `json:"workspace_id"`
	ProjectID             string `json:"project_id"`
	ConversationID        string `json:"conversation_id"`
	ProjectAgentID        string `json:"project_agent_id"`
	AgentID               string `json:"agent_id"`
	AgentName             string `json:"agent_name"`
	AgentSlug             string `json:"agent_slug"`
	RequestedByType       string `json:"requested_by_type"`
	RequestedByID         string `json:"requested_by_id"`
	ConnectorType         string `json:"connector_type"`
	Status                string `json:"status"`
	TriggerMessageContent string `json:"trigger_message_content"`
	// TriggerAttachments carries non-text payloads alongside
	// TriggerMessageContent. Connectors that don't forward attachments
	// can ignore the field.
	TriggerAttachments []MessageAttachment `json:"trigger_attachments,omitempty"`
	AgentConfig        map[string]any      `json:"agent_config"`
	ProjectAgentConfig map[string]any      `json:"project_agent_config"`
}

type ConfigureDevProjectAgentConnectorInput struct {
	ProjectAgentID string
	ConnectorType  string
	Endpoint       string
	SecretID       string
	Model          string
	ModelID        string
	Workdir        string
	SystemPrompt   string
}

type ConfigureDevProjectAgentConnectorResult struct {
	ProjectAgentID     string         `json:"project_agent_id"`
	ProjectID          string         `json:"project_id"`
	AgentID            string         `json:"agent_id"`
	Name               string         `json:"name"`
	Slug               string         `json:"slug"`
	ConnectorType      string         `json:"connector_type"`
	AgentConfig        map[string]any `json:"agent_config"`
	ProjectAgentConfig map[string]any `json:"project_agent_config,omitempty"`
}

type ConfigureProjectAgentProfileInput struct {
	ProjectAgentID string
	ModelID        string
	Workdir        string
	SystemPrompt   string
	Config         map[string]any
}

type ClaimHTTPAgentRunResult struct {
	RunID   string `json:"run_id"`
	Claimed bool   `json:"claimed"`
}

type FailAgentRunInput struct {
	RunID  string
	Source string
	Reason string
}

type RequeueAgentRunInput struct {
	RunID  string
	Source string
	Reason string
}

type RequeueAgentRunResult struct {
	RunID          string `json:"run_id"`
	WorkspaceID    string `json:"workspace_id"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
	ProjectAgentID string `json:"project_agent_id"`
	Status         string `json:"status"`
}

type ConfigureDevConversationExternalRefInput struct {
	ConversationID   string
	Gateway          string
	ExternalChatID   string
	ExternalThreadID string
}

type ConfigureDevConversationExternalRefResult struct {
	ConversationID   string `json:"conversation_id"`
	WorkspaceID      string `json:"workspace_id"`
	ProjectID        string `json:"project_id"`
	Platform         string `json:"platform"`
	ExternalID       string `json:"external_id"`
	ExternalThreadID string `json:"external_thread_id"`
}

type CreateProjectConversationInput struct {
	ProjectID string
	Title     string
	// Surface ∈ {web, im, api}. Empty defaults to "web".
	Surface string
	// Form ∈ {thread, group, dm, oneshot}. Empty defaults based on Surface:
	//   web → thread, im → group, api → oneshot.
	Form     string
	Metadata map[string]any
	// PrimaryAgentID, when set, identifies the project-scoped Agent this
	// conversation is bound to. Must be the id of an active project_agent
	// belonging to ProjectID. Persisted under metadata["primary_agent_id"].
	// Empty string means no agent bound.
	PrimaryAgentID string
}

type ConversationRead struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	ProjectID   string         `json:"project_id"`
	Surface     string         `json:"surface"`
	Form        string         `json:"form"`
	Title       string         `json:"title"`
	Status      string         `json:"status"`
	Metadata    map[string]any `json:"metadata"`
	// PrimaryAgentID / PrimaryAgentName are derived fields, hydrated from
	// metadata.primary_agent_id + a JOIN against project_agents / agents.
	PrimaryAgentID   string    `json:"primary_agent_id,omitempty"`
	PrimaryAgentName string    `json:"primary_agent_name,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ConversationListItem struct {
	ConversationRead
	MessageCount          int64      `json:"message_count"`
	LastMessageAt         *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview    string     `json:"last_message_preview,omitempty"`
	LastMessageSenderType string     `json:"last_message_sender_type,omitempty"`
}

type ConversationTimelineRead struct {
	ConversationID string              `json:"conversation_id"`
	Messages       []MessageRead       `json:"messages"`
	AgentRuns      []AgentRunBriefRead `json:"agent_runs"`
}

type MessageRead struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	ProjectID      string `json:"project_id"`
	ConversationID string `json:"conversation_id"`
	SenderType     string `json:"sender_type"`
	SenderID       string `json:"sender_id"`
	// Kind is the semantic message bucket (message / system / error / etc.).
	Kind string `json:"kind"`
	// ContentFormat is how the content blob should be rendered
	// (text / markdown / json / opencode_stream / ...).
	ContentFormat string              `json:"content_format"`
	Content       string              `json:"content"`
	Metadata      map[string]any      `json:"metadata"`
	CreatedAt     time.Time           `json:"created_at"`
	Runs          []AgentRunBriefRead `json:"runs,omitempty"`
}

type GatewayOutboundMessageRead struct {
	MessageRead
	Gateway          string `json:"gateway"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id,omitempty"`

	// SourceAppID identifies which Bot application sent the original
	// inbound message; the outbound worker uses it to resolve per-Agent
	// Feishu credentials.
	SourceAppID string `json:"source_app_id,omitempty"`

	// RetryCount carries the dispatcher's running counter (read from
	// metadata.gateway_retry_count). 0 on the first attempt; the
	// poller uses it to decide between Retry and DeadLetter outcomes.
	RetryCount int `json:"retry_count,omitempty"`
}

type MarkGatewayOutboundDeliveredInput struct {
	MessageID string
	// Deprecated: ignored by the store; the inflight slot holds
	// external_msg_id instead.
	DeliveryID string
}

type MarkGatewayOutboundDeliveredResult struct {
	MessageID string         `json:"message_id"`
	Metadata  map[string]any `json:"metadata"`
}

// ToolStepRead is a compact per-invocation representation of one tool
// call observed during an agent run.
type ToolStepRead struct {
	ToolCallID string         `json:"tool_call_id"`
	Name       string         `json:"name"`
	Status     string         `json:"status"` // "running" | "completed"
	Args       map[string]any `json:"args,omitempty"`
	Result     map[string]any `json:"result,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type AgentRunBriefRead struct {
	ID               string         `json:"id"`
	WorkspaceID      string         `json:"workspace_id"`
	ProjectID        string         `json:"project_id"`
	ConversationID   string         `json:"conversation_id"`
	TriggerMessageID string         `json:"trigger_message_id,omitempty"`
	OutputMessageID  string         `json:"output_message_id,omitempty"`
	ProjectAgentID   string         `json:"project_agent_id"`
	AgentID          string         `json:"agent_id"`
	AgentName        string         `json:"agent_name"`
	AgentSlug        string         `json:"agent_slug"`
	ConnectorType    string         `json:"connector_type"`
	Status           string         `json:"status"`
	UserFacingReason string         `json:"user_facing_reason,omitempty"`
	Steps            []ToolStepRead `json:"steps,omitempty"`
	// QueuePosition is the 1-indexed position of this run in its
	// (conversation, project_agent) serial-queue lane, populated only
	// for status='queued' rows.
	QueuePosition int        `json:"queue_position,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type AgentRunDetailRead struct {
	AgentRunBriefRead
	RequestedByType string               `json:"requested_by_type"`
	RequestedByID   string               `json:"requested_by_id,omitempty"`
	ExternalRunID   string               `json:"external_run_id,omitempty"`
	Metadata        map[string]any       `json:"metadata"`
	Transcript      string               `json:"transcript,omitempty"`
	UpdatedAt       time.Time            `json:"updated_at"`
	OutputMessage   *MessageRead         `json:"output_message,omitempty"`
	Artifacts       []ArtifactRead       `json:"artifacts"`
	Usage           []UsageLogRead       `json:"usage"`
	Events          []AgentRunEventRead  `json:"events"`
	Runtime         *AgentRunRuntimeRead `json:"runtime,omitempty"`
}

type AgentRunRuntimeRead struct {
	ID               string          `json:"id,omitempty"`
	Name             string          `json:"name,omitempty"`
	Type             string          `json:"type,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	ConnectorType    string          `json:"connector_type,omitempty"`
	AgentKind        string          `json:"agent_kind,omitempty"`
	RuntimeMode      string          `json:"runtime_mode,omitempty"`
	ExecutionPlace   string          `json:"execution_place,omitempty"`
	GovernanceMode   string          `json:"governance_mode,omitempty"`
	DeviceID         string          `json:"device_id,omitempty"`
	SandboxID        string          `json:"sandbox_id,omitempty"`
	ManagedModelID   string          `json:"managed_model_id,omitempty"`
	Capabilities     map[string]bool `json:"capabilities,omitempty"`
	Liveness         string          `json:"liveness,omitempty"`
	Hostname         string          `json:"hostname,omitempty"`
	Version          string          `json:"version,omitempty"`
	LastHeartbeatAt  *time.Time      `json:"last_heartbeat_at,omitempty"`
	WorkingDirectory string          `json:"working_directory,omitempty"`
	CapturedAt       *time.Time      `json:"captured_at,omitempty"`
}

type RecordAgentRunExecutionSnapshotInput struct {
	RunID            string
	ConnectorType    string
	RuntimeID        string
	DeviceID         string
	AgentKind        string
	RuntimeMode      string
	WorkingDirectory string
	ManagedModelID   string
	SandboxID        string
	Capabilities     map[string]bool
}

type ArtifactRead struct {
	ID         string `json:"id"`
	AgentRunID string `json:"agent_run_id"`
	Name       string `json:"name"`
	// Medium is how the artifact is stored / addressed (file / link / inline).
	Medium string `json:"medium"`
	// Kind is the artifact's business semantics
	// (log / transcript / code-patch / screenshot / ...). Free-form.
	Kind       string         `json:"kind"`
	URI        string         `json:"uri"`
	Visibility string         `json:"visibility"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
}

type AuditRecordRead struct {
	ID          int64          `json:"id"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Source      string         `json:"source"`
	EventType   string         `json:"event_type"`
	ActorType   string         `json:"actor_type"`
	ActorID     string         `json:"actor_id,omitempty"`
	TargetType  string         `json:"target_type,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	ProjectID   string         `json:"project_id,omitempty"`
	Payload     map[string]any `json:"payload"`
}

// ListAuditRecordsFilter expresses the optional filters accepted by
// Store.ListAuditRecords. Limit is mandatory and capped server-side.
type ListAuditRecordsFilter struct {
	WorkspaceID string
	ProjectID   string
	Source      string
	EventType   string
	ActorID     string
	TargetType  string
	TargetID    string
	Since       time.Time
	Until       time.Time
}

type UsageLogRead struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	ProjectID    string         `json:"project_id"`
	AgentRunID   string         `json:"agent_run_id"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	InputTokens  int32          `json:"input_tokens"`
	OutputTokens int32          `json:"output_tokens"`
	CostUSD      float64        `json:"cost_usd"`
	Raw          map[string]any `json:"raw"`
	CreatedAt    time.Time      `json:"created_at"`
}

type CreateSecretInput struct {
	WorkspaceID string // accepted for caller-compat; secrets are org-global
	Name        string
	Kind        string
	Provider    string
	AuthType    string
	Payload     map[string]any
	Masked      string
	CreatedBy   string
	// CredentialKindCode is optional metadata that pins a capability_inline
	// secret to a single credential_kinds.code. Used by the agent-creation
	// shared-binding picker to filter secrets by the kind they hold.
	CredentialKindCode string
}

type SecretRead struct {
	ID         string         `json:"id"`
	Slug       string         `json:"slug"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Provider   string         `json:"provider"`
	AuthType   string         `json:"auth_type"`
	KeyVersion string         `json:"key_version"`
	Status     string         `json:"status"`
	Masked     string         `json:"masked"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type SecretPayload struct {
	SecretRead
	EncryptedPayload []byte
}

// CreateModelInput carries the fields of a new shared model.
// Credential mode is one-of inline_secret / credential_ref.
type CreateModelInput struct {
	Name               string
	ProviderType       string
	Adapter            string
	BaseURL            string
	ModelKey           string
	CredentialMode     string // "inline_secret" | "credential_ref"
	SecretID           string // when mode=inline_secret
	CredentialKindCode string // when mode=credential_ref
	Config             map[string]any
	CreatedBy          string
}

// UpdateModelInput carries the editable fields of a shared model.
// CredentialMode is NOT editable — change semantics by recreating the model.
type UpdateModelInput struct {
	ModelID            string
	Name               string
	ModelKey           string
	BaseURL            string
	SecretID           string // for inline_secret mode
	CredentialKindCode string // for credential_ref mode
	Config             map[string]any
}

type ModelRead struct {
	ID                 string         `json:"id"`
	Slug               string         `json:"slug"`
	Name               string         `json:"name"`
	ProviderType       string         `json:"provider_type"`
	Adapter            string         `json:"adapter"`
	BaseURL            string         `json:"base_url"`
	ModelKey           string         `json:"model_key"`
	CredentialMode     string         `json:"credential_mode"`
	SecretID           string         `json:"secret_id,omitempty"`
	CredentialKindCode string         `json:"credential_kind_code,omitempty"`
	Status             string         `json:"status"`
	Config             map[string]any `json:"config"`
	CreatedBy          string         `json:"created_by,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

// ModelRuntime is the flattened runtime view consumed by the agentdaemon /
// opencode connector model_injection paths. Provider info is now inlined.
// CredentialMode determines which secret-source is populated:
//   - "inline_secret"  → EncryptedPayload is set (from secrets table)
//   - "credential_ref" → EncryptedPayload is set (from user_credentials), per-caller
type ModelRuntime struct {
	ModelID            string         `json:"model_id"`
	ModelName          string         `json:"model_name"`
	ModelKey           string         `json:"model_key"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	ModelConfig        map[string]any `json:"model_config"`
	ProviderType       string         `json:"provider_type"`
	Adapter            string         `json:"adapter"`
	BaseURL            string         `json:"base_url"`
	CredentialMode     string         `json:"credential_mode"`
	SecretID           string         `json:"secret_id,omitempty"`
	CredentialKindCode string         `json:"credential_kind_code,omitempty"`
	EncryptedPayload   []byte         `json:"-"`
	// Deprecated: kept for backward-compat with renderer fields that read
	// ProviderConfig. New code should use ModelConfig.
	ProviderConfig map[string]any `json:"provider_config"`
}

// WorkspaceMemberRead is a workspace-level membership row joined with the user.
type WorkspaceMemberRead struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	UserID      string    `json:"user_id"`
	Role        string    `json:"role"`
	UserEmail   string    `json:"user_email"`
	UserName    string    `json:"user_name"`
	UserStatus  string    `json:"user_status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UserWorkspaceRead is one workspace the calling user is a member of,
// joined with that membership's role.
type UserWorkspaceRead struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	Visibility string    `json:"visibility"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UserRead is the current signed-in user's profile projection. AvatarURL is
// provider metadata (currently Feishu OIDC), not a core users column.
type UserRead struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	AvatarURL string    `json:"avatar_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WorkspaceProjectRead is one active project inside a workspace. Drives
// the project picker nested inside the header workspace switcher.
type WorkspaceProjectRead struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

var ErrUnknownMention = errors.New("unknown active project agent mention")
var ErrUnknownConversation = errors.New("unknown active conversation")
var ErrUnknownSender = errors.New("unknown active sender")
var ErrUnknownAgentRun = errors.New("unknown agent run")
var ErrUnknownMessage = errors.New("unknown message")
var ErrUnknownProject = errors.New("unknown active project")
var ErrUnknownWorkspace = errors.New("unknown active workspace")
var ErrUnknownUser = errors.New("unknown user")
var ErrDuplicateWorkspaceSlug = errors.New("workspace slug already in use")
var ErrDuplicateProjectSlug = errors.New("project slug already in use in this workspace")
var ErrDuplicateAgentSlug = errors.New("agent slug already in use in this workspace")
var ErrDuplicateModelProviderSlug = errors.New("model provider slug already in use in this workspace")
var ErrUnknownAgent = errors.New("unknown active agent")
var ErrInvalidAgentVisibility = errors.New("invalid agent visibility (allowed: workspace, tenant, public)")
var ErrFeishuConnectorIncomplete = errors.New("feishu connector enabled requires app_id, app_secret_ref, and verification_token_ref")
var ErrFeishuAppIDInUse = errors.New("another active agent has already registered this Feishu bot app_id")
var ErrUnknownCapability = errors.New("unknown capability")
var ErrUnknownCapabilityVersion = errors.New("unknown capability version")
var ErrImmutable = errors.New("immutable capability version")
var ErrMarketplaceCapabilityUnavailable = errors.New("marketplace capability unavailable")
var ErrMarketplaceDependents = errors.New("workspace has marketplace dependents")

// CapabilityHasBindingsError 表示删除 capability 时被 agent_capabilities 引用阻塞。
// Count 是阻塞时的引用数;HTTP 层用它构造 409 响应里的 binding_count。
type CapabilityHasBindingsError struct {
	CapabilityID string
	Count        int64
}

func (e *CapabilityHasBindingsError) Error() string {
	return fmt.Sprintf("capability %s has %d agent bindings", e.CapabilityID, e.Count)
}

var ErrUnknownUserCredential = errors.New("unknown user credential")
var ErrInvalidCredentialKind = errors.New("invalid credential kind")
var ErrUnknownAgentCapability = errors.New("unknown agent capability")
var ErrInFlightAgentRuns = errors.New("agent has in-flight runs")
var ErrInvalidWorkspaceInput = errors.New("invalid workspace input")
var ErrInvalidProjectInput = errors.New("invalid project input")
var ErrUnknownConversationForRead = errors.New("unknown active conversation")
var ErrAgentRunNotCompletable = errors.New("agent run is not completable")
var ErrAgentRunNotStartable = errors.New("agent run is not startable")

// ErrAgentRunBlockedByQueue is returned by MarkAgentRunRunning when another
// run for the same (conversation, project_agent) pair is already running.
// Callers MUST NOT surface this as an error — the run stays queued and is
// dispatched when the sibling terminates.
var ErrAgentRunBlockedByQueue = errors.New("agent run blocked by in-flight sibling")
var ErrInvalidProjectAgent = errors.New("invalid active project agent relation")
var ErrInvalidHTTPConnector = errors.New("agent run is not configured for http connector")
var ErrUnknownWorkspaceMember = errors.New("unknown active workspace member")
var ErrInvalidMemberRole = errors.New("invalid member role")
var ErrNotMember = errors.New("not an active member")

// 工作区主动申请加入(self-service join request)相关错误:
//
//	ErrJoinRequestAlreadyHandled — Approve/Reject 时目标行已被其他 admin 处理,
//	  WHERE status='pending' 守卫导致 0 行受影响。handler 返回 409。
var ErrJoinRequestAlreadyHandled = errors.New("join request already handled")

// validMemberRoles mirrors the workspace_members.role CHECK constraint
// so the API layer can reject bad roles before PostgreSQL.
var validMemberRoles = map[string]struct{}{
	"owner":  {},
	"admin":  {},
	"member": {},
	"viewer": {},
}

// Role constants — keep in sync with validMemberRoles and the CHECK
// constraint in the schema.
const (
	memberRoleOwner = "owner"

	// Membership status (workspace_members.status CHECK 约束):
	//   - active:正式成员,所有 RBAC / 列表查询都基于这个状态
	//   - pending:用户自助申请,等待 owner/admin 审批
	//   - rejected:申请被拒;保留行做审计,UNIQUE 索引排除它以便用户再申请
	memberStatusActive   = "active"
	memberStatusPending  = "pending"
	memberStatusRejected = "rejected"

	// Workspace visibility:
	//   - private:仅邀请,不出现在发现列表(默认)
	//   - public:任何登录用户可发现并申请加入
	workspaceVisibilityPrivate = "private"
	workspaceVisibilityPublic  = "public"
)

func IsValidWorkspaceVisibility(v string) bool {
	return v == workspaceVisibilityPrivate || v == workspaceVisibilityPublic
}

func IsValidMemberRole(role string) bool {
	_, ok := validMemberRoles[role]
	return ok
}

func (s *Store) GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return "", err
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return "", err
	}
	role, err := sqlc.New(s.db).GetWorkspaceMemberRole(ctx, sqlc.GetWorkspaceMemberRoleParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: workspace=%s user=%s", ErrNotMember, workspaceID, userID)
		}
		return "", err
	}
	return role, nil
}

// IsActiveWorkspaceMember is a bool wrapper around GetWorkspaceMemberRole.
// A missing row is NOT a database error — it just means "not a member".
func (s *Store) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	_, err := s.GetWorkspaceMemberRole(ctx, workspaceID, userID)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotMember) {
		return false, nil
	}
	return false, err
}

// GetProjectWorkspace resolves a project id to its owning workspace.
// Used by the RBAC bridge that maps project-scoped routes onto the
// workspace role check (workspace is the only membership tier now).
func (s *Store) GetProjectWorkspace(ctx context.Context, projectID string) (string, error) {
	pjUUID, err := uuid(projectID)
	if err != nil {
		return "", err
	}
	workspaceID, err := sqlc.New(s.db).GetProjectWorkspace(ctx, pjUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
		}
		return "", err
	}
	return workspaceID, nil
}

func (s *Store) GetWorkspaceSettings(ctx context.Context, workspaceID string) (WorkspaceSettingsRead, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return WorkspaceSettingsRead{}, err
	}
	id, err := sqlc.New(s.db).GetWorkspaceSettings(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceSettingsRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
		}
		return WorkspaceSettingsRead{}, err
	}
	return WorkspaceSettingsRead{WorkspaceID: id}, nil
}

func (s *Store) GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (WorkspaceRuntimeSettingsRead, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return WorkspaceRuntimeSettingsRead{}, err
	}
	queries := sqlc.New(s.db)
	row, err := queries.GetWorkspaceRuntimeSettings(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceRuntimeSettingsRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
		}
		return WorkspaceRuntimeSettingsRead{}, err
	}
	count, err := queries.CountSandboxAgentsInWorkspace(ctx, wsUUID)
	if err != nil {
		return WorkspaceRuntimeSettingsRead{}, err
	}
	return WorkspaceRuntimeSettingsRead{
		WorkspaceID:               row.WorkspaceID,
		RuntimeCredentialSecretID: row.RuntimeCredentialSecretID,
		RuntimeConfig:             decodeJSONMap(row.RuntimeConfig),
		RuntimeCredentialMasked:   row.CredentialMasked,
		SandboxAgentCount:         count,
	}, nil
}

// SetWorkspaceRuntimeCredentialSecret flips the workspace's runtime
// credential pointer to a secret the caller already inserted.
// Overwriting is allowed; the prior referenced row stays in `secrets`
// as an orphan for audit trail.
func (s *Store) SetWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, secretID string, now time.Time) error {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	if _, err := uuid(secretID); err != nil {
		return err
	}
	return sqlc.New(s.db).SetWorkspaceRuntimeCredentialSecret(ctx, sqlc.SetWorkspaceRuntimeCredentialSecretParams{
		WorkspaceID: wsUUID,
		SecretID:    strings.TrimSpace(secretID),
		Now:         timestamptz(now),
	})
}

// ClearWorkspaceRuntimeCredentialSecret nulls the workspace's runtime
// credential pointer AND soft-deletes the prior active credential secret
// in a single transaction. NO-OP when the workspace has no current pointer.
func (s *Store) ClearWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, name, kind string, now time.Time) error {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)
	// Read current pointer first; if it's empty there's no secret to soft-delete.
	settings, err := q.GetWorkspaceRuntimeSettings(ctx, wsUUID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("read workspace runtime settings: %w", err)
		}
	}
	if currentSecretID := strings.TrimSpace(settings.RuntimeCredentialSecretID); currentSecretID != "" {
		secretUUID, err := uuid(currentSecretID)
		if err == nil {
			if err := q.SoftDeleteWorkspaceRuntimeCredentialSecret(ctx, sqlc.SoftDeleteWorkspaceRuntimeCredentialSecretParams{
				SecretID: secretUUID,
				Now:      timestamptz(now),
			}); err != nil {
				return fmt.Errorf("soft-delete runtime credential: %w", err)
			}
		}
	}
	if err := q.ClearWorkspaceRuntimeCredentialSecret(ctx, sqlc.ClearWorkspaceRuntimeCredentialSecretParams{
		WorkspaceID: wsUUID,
		Now:         timestamptz(now),
	}); err != nil {
		return fmt.Errorf("clear runtime credential pointer: %w", err)
	}
	return tx.Commit(ctx)
}

// RegisterWorkspaceRuntimeCredentialInput carries the encrypted-once payload
// + masked preview (encrypt / mask happen in the dev handler so the master
// key never touches the store layer).
type RegisterWorkspaceRuntimeCredentialInput struct {
	WorkspaceID      string
	Name             string
	Kind             string
	Provider         string
	AuthType         string
	EncryptedPayload []byte
	Metadata         map[string]any
	Masked           string
	CreatedBy        string
	Now              time.Time
}

// RegisterWorkspaceRuntimeCredential is the upsert-aware path the admin
// RuntimeCredentialCard PUT handler uses. Atomic transaction:
//
//  1. Soft-delete the workspace's currently-pointed credential secret (NO-OP
//     when no current pointer).
//  2. Insert the new encrypted secret row.
//  3. Flip workspaces.config.runtime_credential_secret_id to the new ID.
//
// metadata is initialised with {"masked": <input.Masked>} so the existing
// GetWorkspaceRuntimeSettings join keeps returning the redacted preview.
func (s *Store) RegisterWorkspaceRuntimeCredential(ctx context.Context, in RegisterWorkspaceRuntimeCredentialInput) (SecretRead, error) {
	wsUUID, err := uuid(in.WorkspaceID)
	if err != nil {
		return SecretRead{}, err
	}
	createdBy := nullableUUID(in.CreatedBy)
	metadata := in.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	if strings.TrimSpace(in.Masked) != "" {
		metadata["masked"] = strings.TrimSpace(in.Masked)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return SecretRead{}, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return SecretRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	// Step 1 — soft-delete the workspace's currently-pointed credential
	// secret (if any). NO-OP when no prior pointer.
	settings, err := q.GetWorkspaceRuntimeSettings(ctx, wsUUID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return SecretRead{}, fmt.Errorf("read workspace runtime settings: %w", err)
	}
	if currentSecretID := strings.TrimSpace(settings.RuntimeCredentialSecretID); currentSecretID != "" {
		if secretUUID, err := uuid(currentSecretID); err == nil {
			if err := q.SoftDeleteWorkspaceRuntimeCredentialSecret(ctx, sqlc.SoftDeleteWorkspaceRuntimeCredentialSecretParams{
				SecretID: secretUUID,
				Now:      timestamptz(in.Now),
			}); err != nil {
				return SecretRead{}, fmt.Errorf("soft-delete prior runtime credential: %w", err)
			}
		}
	}

	// Step 2 — insert the new credential secret.
	row, err := q.CreateSecret(ctx, sqlc.CreateSecretParams{
		ID:               mustUUID(newID()),
		Slug:             generateAutoSlug("secret"),
		Name:             strings.TrimSpace(in.Name),
		Kind:             secretKind(in.Kind),
		Provider:         strings.TrimSpace(in.Provider),
		AuthType:         strings.TrimSpace(in.AuthType),
		EncryptedPayload: in.EncryptedPayload,
		KeyVersion:       "v1",
		Metadata:         metadataJSON,
		CreatedBy:        createdBy,
		Now:              timestamptz(in.Now),
	})
	if err != nil {
		return SecretRead{}, fmt.Errorf("insert runtime credential secret: %w", err)
	}

	// Step 3 — flip the workspace pointer to the new secret.
	if err := q.SetWorkspaceRuntimeCredentialSecret(ctx, sqlc.SetWorkspaceRuntimeCredentialSecretParams{
		WorkspaceID: wsUUID,
		SecretID:    row.ID,
		Now:         timestamptz(in.Now),
	}); err != nil {
		return SecretRead{}, fmt.Errorf("set runtime credential pointer: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return SecretRead{}, err
	}
	return secretReadFromCreateRow(row), nil
}

func (s *Store) PatchWorkspaceSettings(ctx context.Context, workspaceID string) (WorkspaceSettingsRead, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return WorkspaceSettingsRead{}, err
	}
	id, err := sqlc.New(s.db).UpdateWorkspaceSettings(ctx, sqlc.UpdateWorkspaceSettingsParams{WorkspaceID: wsUUID, Now: timestamptz(time.Now().UTC())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceSettingsRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
		}
		return WorkspaceSettingsRead{}, err
	}
	return WorkspaceSettingsRead{WorkspaceID: id}, nil
}

func (s *Store) GetAgent(ctx context.Context, agentID string) (AgentSummary, error) {
	agentUUID, err := uuid(agentID)
	if err != nil {
		return AgentSummary{}, err
	}
	row, err := sqlc.New(s.db).GetAgentForUpdate(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentSummary{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return AgentSummary{}, err
	}
	summary := agentSummaryFromRow(row.ID, row.WorkspaceID, row.Name, row.Slug, row.Description, row.ConnectorType, row.Status, row.Config, row.CreatedAt, row.UpdatedAt)
	summary.Visibility = row.Visibility
	return summary, nil
}

// FeishuAgentRoute is the projection the Feishu inbound router needs.
// Config is raw bytes so callers decode the connector subtree without
// coupling this package to the connector schema.
type FeishuAgentRoute struct {
	AgentID         string
	WorkspaceID     string
	WorkspaceName   string
	AgentName       string
	AgentSlug       string
	Visibility      string
	Config          []byte
	CreatedByUserID string
}

// FeishuSharedBotAgent is one selectable target shown by a shared Feishu
// Bot's /list command. The shared Bot owns the app credentials, while the
// selected Agent owns the workspace/project execution semantics.
type FeishuSharedBotAgent struct {
	AgentID        string
	WorkspaceID    string
	WorkspaceName  string
	WorkspaceSlug  string
	AgentName      string
	AgentSlug      string
	Visibility     string
	ProjectID      string
	ProjectName    string
	ProjectAgentID string
}

type GatewaySessionSelectionInput struct {
	Platform         string
	ExternalID       string
	ExternalThreadID string
	AgentID          string
	Metadata         map[string]any
}

// ErrUnknownFeishuAgent is returned when no enabled Feishu connector
// matches the supplied Bot App ID. Not an auth failure — the Bot is just
// not registered with this Parsar instance.
var ErrUnknownFeishuAgent = errors.New("no active agent has registered this Feishu Bot app_id")

// GetAgentByFeishuAppID resolves a Bot App ID to the registered Agent.
// Returns ErrUnknownFeishuAgent when no enabled active Agent claims it.
func (s *Store) GetAgentByFeishuAppID(ctx context.Context, appID string) (FeishuAgentRoute, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return FeishuAgentRoute{}, fmt.Errorf("%w: empty app_id", ErrUnknownFeishuAgent)
	}
	row, err := sqlc.New(s.db).GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeishuAgentRoute{}, fmt.Errorf("%w: app_id=%s", ErrUnknownFeishuAgent, appID)
		}
		return FeishuAgentRoute{}, err
	}
	return FeishuAgentRoute{
		AgentID:         row.AID,
		WorkspaceID:     row.AWorkspaceID,
		WorkspaceName:   row.WorkspaceName,
		AgentName:       row.AgentName,
		AgentSlug:       row.AgentSlug,
		Visibility:      row.Visibility,
		Config:          row.Config,
		CreatedByUserID: row.CreatedByUserID,
	}, nil
}

// GetAgentByID returns the same route projection as GetAgentByFeishuAppID,
// keyed by Agent ID. Callers still re-run visibility before dispatching.
func (s *Store) GetAgentByID(ctx context.Context, agentID string) (FeishuAgentRoute, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return FeishuAgentRoute{}, fmt.Errorf("%w: empty agent_id", ErrUnknownFeishuAgent)
	}
	agentUUID, err := uuid(agentID)
	if err != nil {
		return FeishuAgentRoute{}, err
	}
	var route FeishuAgentRoute
	err = s.db.QueryRow(ctx, `
		select a.id::text, a.workspace_id::text, w.name,
		       a.name, a.slug, a.visibility, a.config,
		       coalesce(a.created_by::text, '')
		from agents a
		join workspaces w on w.id = a.workspace_id
		where a.id = $1::uuid
		  and a.status = 'active'
		  and a.deleted_at is null
		  and w.deleted_at is null
		limit 1
	`, agentUUID).Scan(&route.AgentID, &route.WorkspaceID, &route.WorkspaceName, &route.AgentName, &route.AgentSlug, &route.Visibility, &route.Config, &route.CreatedByUserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeishuAgentRoute{}, fmt.Errorf("%w: agent_id=%s", ErrUnknownFeishuAgent, agentID)
		}
		return FeishuAgentRoute{}, err
	}
	return route, nil
}

// ListFeishuSharedBotAgents returns active Agents the Feishu sender may
// select from a shared Bot. Guests see public Agents only; registered users
// also see tenant Agents + Agents in workspaces they belong to. Agents with
// their own active Feishu Bot binding are excluded.
func (s *Store) ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]FeishuSharedBotAgent, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	var senderParam any
	if strings.TrimSpace(senderUserID) != "" {
		senderUUID, err := uuid(senderUserID)
		if err != nil {
			return nil, err
		}
		senderParam = senderUUID
	}
	var excludeParam any
	if strings.TrimSpace(excludeAgentID) != "" {
		excludeUUID, err := uuid(excludeAgentID)
		if err != nil {
			return nil, err
		}
		excludeParam = excludeUUID
	}
	rows, err := s.db.Query(ctx, `
		select distinct on (a.id)
		  a.id::text,
		  a.workspace_id::text,
		  w.name,
		  w.slug,
		  a.name,
		  a.slug,
		  a.visibility,
		  p.id::text,
		  p.name,
		  pa.id::text
		from agents a
		join workspaces w on w.id = a.workspace_id
		join project_agents pa on pa.agent_id = a.id
		  and pa.workspace_id = a.workspace_id
		  and pa.status = 'active'
		  and pa.deleted_at is null
		join projects p on p.id = pa.project_id
		  and p.workspace_id = a.workspace_id
		  and p.status = 'active'
		  and p.deleted_at is null
		left join workspace_members wm on wm.workspace_id = a.workspace_id
		  and wm.user_id = $1::uuid
		  and wm.deleted_at is null
		where a.status = 'active'
		  and a.deleted_at is null
		  and w.deleted_at is null
		  and ($2::uuid is null or a.id <> $2::uuid)
		  and not (
		    coalesce((a.config->'connectors'->'feishu'->>'enabled')::boolean, false) = true
		    and coalesce(a.config->'connectors'->'feishu'->>'app_id', '') <> ''
		  )
		  and (
		    ($1::uuid is null and a.visibility = 'public')
		    or ($1::uuid is not null and (a.visibility in ('tenant', 'public') or wm.user_id is not null))
		  )
		order by a.id, w.name asc, a.name asc, pa.created_at asc
		limit $3
	`, senderParam, excludeParam, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := []FeishuSharedBotAgent{}
	for rows.Next() {
		var item FeishuSharedBotAgent
		if err := rows.Scan(
			&item.AgentID,
			&item.WorkspaceID,
			&item.WorkspaceName,
			&item.WorkspaceSlug,
			&item.AgentName,
			&item.AgentSlug,
			&item.Visibility,
			&item.ProjectID,
			&item.ProjectName,
			&item.ProjectAgentID,
		); err != nil {
			return nil, err
		}
		agents = append(agents, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return agents, nil
}

var ErrUnknownGatewaySessionSelection = errors.New("no gateway session agent selection")

func (s *Store) UpsertGatewaySessionSelection(ctx context.Context, input GatewaySessionSelectionInput) error {
	platform := strings.TrimSpace(input.Platform)
	externalID := strings.TrimSpace(input.ExternalID)
	externalThreadID := strings.TrimSpace(input.ExternalThreadID)
	if platform == "" || externalID == "" {
		return fmt.Errorf("%w: platform and external_id are required", ErrUnknownGatewaySessionSelection)
	}
	agentUUID, err := uuid(input.AgentID)
	if err != nil {
		return err
	}
	metadata := []byte(`{}`)
	if len(input.Metadata) > 0 {
		metadata, err = json.Marshal(input.Metadata)
		if err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(ctx, `
		insert into gateway_sessions(
		  id, platform, external_id, external_thread_id, selected_agent_id, metadata, created_at, updated_at
		) values ($1::uuid, $2, $3, $4, $5::uuid, $6::jsonb, $7, $7)
		on conflict (platform, external_id, external_thread_id)
		do update set selected_agent_id = excluded.selected_agent_id,
		              metadata = gateway_sessions.metadata || excluded.metadata,
		              updated_at = excluded.updated_at
	`, mustUUID(newID()), platform, externalID, externalThreadID, agentUUID, metadata, timestamptz(now))
	return err
}

func (s *Store) GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error) {
	platform = strings.TrimSpace(platform)
	externalID = strings.TrimSpace(externalID)
	externalThreadID = strings.TrimSpace(externalThreadID)
	if platform == "" || externalID == "" {
		return "", fmt.Errorf("%w: platform and external_id are required", ErrUnknownGatewaySessionSelection)
	}
	var agentID string
	err := s.db.QueryRow(ctx, `
		select selected_agent_id::text
		from gateway_sessions
		where platform = $1
		  and external_id = $2
		  and external_thread_id = $3
		  and selected_agent_id is not null
		limit 1
	`, platform, externalID, externalThreadID).Scan(&agentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: platform=%s external_id=%s external_thread_id=%s", ErrUnknownGatewaySessionSelection, platform, externalID, externalThreadID)
		}
		return "", err
	}
	return agentID, nil
}

// ClearGatewaySessionSelection wipes the saved selected_agent_id for a
// chat × thread tuple. Returns no error when no row matches.
func (s *Store) ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error {
	platform = strings.TrimSpace(platform)
	externalID = strings.TrimSpace(externalID)
	externalThreadID = strings.TrimSpace(externalThreadID)
	if platform == "" || externalID == "" {
		return fmt.Errorf("%w: platform and external_id are required", ErrUnknownGatewaySessionSelection)
	}
	_, err := s.db.Exec(ctx, `
		delete from gateway_sessions
		where platform = $1
		  and external_id = $2
		  and external_thread_id = $3
	`, platform, externalID, externalThreadID)
	return err
}

// ListFeishuWebSocketAgents returns every active Agent whose Feishu
// connector is enabled and configured for event websocket delivery. The
// websocket manager reconciles this list periodically.
func (s *Store) ListFeishuWebSocketAgents(ctx context.Context) ([]FeishuAgentRoute, error) {
	rows, err := s.db.Query(ctx, `
		select a.id::text, a.workspace_id::text, w.name,
		       a.name, a.slug, a.visibility, a.config,
		       coalesce(a.created_by::text, '')
		from agents a
		join workspaces w on w.id = a.workspace_id
		where a.deleted_at is null
		  and a.status = 'active'
		  and (a.config->'connectors'->'feishu'->>'enabled')::boolean = true
		  and lower(coalesce(a.config->'connectors'->'feishu'->>'event_mode', 'webhook')) = 'websocket'
		  and coalesce(a.config->'connectors'->'feishu'->>'app_id', '') <> ''
		order by a.workspace_id, a.id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	routes := []FeishuAgentRoute{}
	for rows.Next() {
		var route FeishuAgentRoute
		if err := rows.Scan(
			&route.AgentID,
			&route.WorkspaceID,
			&route.WorkspaceName,
			&route.AgentName,
			&route.AgentSlug,
			&route.Visibility,
			&route.Config,
			&route.CreatedByUserID,
		); err != nil {
			return nil, err
		}
		routes = append(routes, route)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return routes, nil
}

// ErrUnknownFeishuUser is returned by FindUserIDByFeishuUnionID when no
// auth_identities row links the supplied union_id. This is the signal that
// the sender is "unregistered" for the visibility gate.
var ErrUnknownFeishuUser = errors.New("no Parsar user linked to this Feishu union_id")

// GetFeishuConnectorDiagnostics returns a compact observation snapshot for
// the Agent's Feishu Bot binding. Reads only aggregate metadata; never
// exposes secret refs or raw content.
func (s *Store) GetFeishuConnectorDiagnostics(ctx context.Context, agentID string) (FeishuConnectorDiagnosticsRead, error) {
	agentUUID, err := uuid(strings.TrimSpace(agentID))
	if err != nil {
		return FeishuConnectorDiagnosticsRead{}, err
	}
	row, err := sqlc.New(s.db).GetFeishuConnectorDiagnostics(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return FeishuConnectorDiagnosticsRead{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return FeishuConnectorDiagnosticsRead{}, err
	}
	return FeishuConnectorDiagnosticsRead{
		AgentID:                row.AgentID,
		WorkspaceID:            row.WorkspaceID,
		Configured:             row.Configured,
		Enabled:                row.Enabled,
		EventMode:              normalizeFeishuEventMode(row.EventMode),
		AppIDSet:               row.AppIDSet,
		AppSecretSet:           row.AppSecretSet,
		VerificationTokenSet:   row.VerificationTokenSet,
		EncryptKeySet:          row.EncryptKeySet,
		BotOpenIDSet:           row.BotOpenIDSet,
		ConversationCount:      int(row.ConversationCount),
		InboundMessageCount:    int(row.InboundMessageCount),
		OutboundMessageCount:   int(row.OutboundMessageCount),
		PendingOutboundCount:   int(row.PendingOutboundCount),
		RetryingOutboundCount:  int(row.RetryingOutboundCount),
		DeadOutboundCount:      int(row.DeadOutboundCount),
		DeliveredOutboundCount: int(row.DeliveredOutboundCount),
		LastInboundAt:          pgOptionalTime(row.LastInboundAt),
		LastOutboundAt:         pgOptionalTime(row.LastOutboundAt),
		LastDeliveredAt:        pgOptionalTime(row.LastDeliveredAt),
		LastError:              strings.TrimSpace(row.LastError),
		LastErrorAt:            pgOptionalTime(row.LastErrorAt),
	}, nil
}

// FindUserIDByFeishuUnionID resolves an inbound Feishu sender to the
// matching Parsar user_id. Returns ErrUnknownFeishuUser when the sender
// has never signed in.
func (s *Store) FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error) {
	unionID = strings.TrimSpace(unionID)
	if unionID == "" {
		return "", fmt.Errorf("%w: empty union_id", ErrUnknownFeishuUser)
	}
	userID, err := sqlc.New(s.db).FindUserByFeishuUnionID(ctx, unionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: union_id=%s", ErrUnknownFeishuUser, unionID)
		}
		return "", err
	}
	return userID, nil
}

// AgentVisibilityChange is the payload UpdateAgentVisibility returns.
type AgentVisibilityChange struct {
	AgentID       string    `json:"agent_id"`
	WorkspaceID   string    `json:"workspace_id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	OldVisibility string    `json:"old_visibility"`
	NewVisibility string    `json:"new_visibility"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Noop is true when the new visibility equals the old one. The handler
	// still returns 200 so idempotent PATCH replays don't fail; audit is
	// suppressed.
	Noop bool `json:"noop,omitempty"`
}

// UpdateAgentVisibility writes the new visibility to agents.visibility.
// Validates up-front before round-tripping to the DB. Emits an audit event
// when the value actually changes. RBAC is enforced by the caller.
func (s *Store) UpdateAgentVisibility(ctx context.Context, agentID, newVisibility, actorID string) (AgentVisibilityChange, error) {
	agentUUID, err := uuid(agentID)
	if err != nil {
		return AgentVisibilityChange{}, err
	}
	newVisibility = strings.TrimSpace(newVisibility)
	if !isValidAgentVisibility(newVisibility) {
		return AgentVisibilityChange{}, fmt.Errorf("%w: %q", ErrInvalidAgentVisibility, newVisibility)
	}
	now := time.Now().UTC()
	row, err := sqlc.New(s.db).UpdateAgentVisibility(ctx, sqlc.UpdateAgentVisibilityParams{
		Visibility: newVisibility,
		Now:        timestamptz(now),
		ID:         agentUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentVisibilityChange{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return AgentVisibilityChange{}, err
	}
	change := AgentVisibilityChange{
		AgentID:       row.AgentsID,
		WorkspaceID:   row.AgentsWorkspaceID,
		Name:          row.Name,
		Slug:          row.Slug,
		OldVisibility: row.OldVisibility,
		NewVisibility: row.NewVisibility,
		UpdatedAt:     pgTime(row.UpdatedAt),
		Noop:          row.OldVisibility == row.NewVisibility,
	}
	if !change.Noop {
		s.emitAgentAudit(now, actorID, auditAgentVisibilityChanged, "agent", change.AgentID, change.WorkspaceID, "", map[string]any{
			"from": change.OldVisibility,
			"to":   change.NewVisibility,
			"slug": change.Slug,
		})
	}
	return change, nil
}

func isValidAgentVisibility(v string) bool {
	switch v {
	case "workspace", "tenant", "public":
		return true
	default:
		return false
	}
}

// FeishuConnectorSnapshot mirrors the agents.config.connectors.feishu
// subtree in a flat shape. Lives in store/ to avoid a circular import on
// gateway.FeishuConnectorConfig.
type FeishuConnectorSnapshot struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
	RoutingMode          string `json:"routing_mode"`
}

// UpdateAgentFeishuConnectorInput drives the PATCH endpoint that binds
// an Agent to a Feishu Bot self-built app.
type UpdateAgentFeishuConnectorInput struct {
	AgentID              string
	Enabled              bool
	AppID                string
	AppSecretRef         string
	VerificationTokenRef string
	EncryptKeyRef        string // optional — only required when Feishu event encryption is on
	BotOpenID            string // optional — used to dedup self-sent messages
	EventMode            string // webhook (manual default) or websocket (QR provisioning default)
	RoutingMode          string // direct (default) or shared (/list + /select router)
}

// AgentFeishuConnectorChange is the payload UpdateAgentFeishuConnector returns.
type AgentFeishuConnectorChange struct {
	AgentID     string                  `json:"agent_id"`
	WorkspaceID string                  `json:"workspace_id"`
	Name        string                  `json:"name"`
	Slug        string                  `json:"slug"`
	Old         FeishuConnectorSnapshot `json:"old"`
	New         FeishuConnectorSnapshot `json:"new"`
	UpdatedAt   time.Time               `json:"updated_at"`

	// Noop is true when New deep-equals Old. Handler still returns 200;
	// audit is suppressed.
	Noop bool `json:"noop,omitempty"`
}

// FeishuConnectorDiagnosticsRead is the read-only admin observation
// snapshot for one Agent's Feishu Bot binding. Omits app_id and *_ref
// strings — operators only need to know whether pieces are present.
type FeishuConnectorDiagnosticsRead struct {
	AgentID                string     `json:"agent_id"`
	WorkspaceID            string     `json:"workspace_id"`
	Configured             bool       `json:"configured"`
	Enabled                bool       `json:"enabled"`
	EventMode              string     `json:"event_mode"`
	AppIDSet               bool       `json:"app_id_set"`
	AppSecretSet           bool       `json:"app_secret_set"`
	VerificationTokenSet   bool       `json:"verification_token_set"`
	EncryptKeySet          bool       `json:"encrypt_key_set"`
	BotOpenIDSet           bool       `json:"bot_open_id_set"`
	ConversationCount      int        `json:"conversation_count"`
	InboundMessageCount    int        `json:"inbound_message_count"`
	OutboundMessageCount   int        `json:"outbound_message_count"`
	PendingOutboundCount   int        `json:"pending_outbound_count"`
	RetryingOutboundCount  int        `json:"retrying_outbound_count"`
	DeadOutboundCount      int        `json:"dead_outbound_count"`
	DeliveredOutboundCount int        `json:"delivered_outbound_count"`
	LastInboundAt          *time.Time `json:"last_inbound_at,omitempty"`
	LastOutboundAt         *time.Time `json:"last_outbound_at,omitempty"`
	LastDeliveredAt        *time.Time `json:"last_delivered_at,omitempty"`
	LastError              string     `json:"last_error,omitempty"`
	LastErrorAt            *time.Time `json:"last_error_at,omitempty"`
}

// UpdateAgentFeishuConnector writes the supplied Feishu Bot config into
// agents.config.connectors.feishu. When Enabled=true, AppID + AppSecretRef
// are required; VerificationTokenRef required only in webhook mode
// (ErrFeishuConnectorIncomplete). app_id cannot collide with another
// active+enabled Agent (ErrFeishuAppIDInUse). RBAC enforced by caller.
func (s *Store) UpdateAgentFeishuConnector(ctx context.Context, input UpdateAgentFeishuConnectorInput, actorID string) (AgentFeishuConnectorChange, error) {
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return AgentFeishuConnectorChange{}, fmt.Errorf("%w: empty agent_id", ErrUnknownAgent)
	}
	normalized := FeishuConnectorSnapshot{
		Enabled:              input.Enabled,
		AppID:                strings.TrimSpace(input.AppID),
		AppSecretRef:         strings.TrimSpace(input.AppSecretRef),
		VerificationTokenRef: strings.TrimSpace(input.VerificationTokenRef),
		EncryptKeyRef:        strings.TrimSpace(input.EncryptKeyRef),
		BotOpenID:            strings.TrimSpace(input.BotOpenID),
		EventMode:            normalizeFeishuEventMode(input.EventMode),
		RoutingMode:          normalizeFeishuRoutingMode(input.RoutingMode),
	}
	if normalized.Enabled {
		if normalized.AppID == "" || normalized.AppSecretRef == "" || (normalized.EventMode != "websocket" && normalized.VerificationTokenRef == "") {
			return AgentFeishuConnectorChange{}, ErrFeishuConnectorIncomplete
		}
	}
	// app_id uniqueness — collision with another active+enabled Agent is
	// always wrong because the inbound router would pick the first match.
	if normalized.AppID != "" {
		existing, err := s.GetAgentByFeishuAppID(ctx, normalized.AppID)
		switch {
		case err == nil:
			if existing.AgentID != agentID {
				return AgentFeishuConnectorChange{}, fmt.Errorf("%w: app_id=%s already on agent=%s", ErrFeishuAppIDInUse, normalized.AppID, existing.AgentID)
			}
		case errors.Is(err, ErrUnknownFeishuAgent):
			// nobody else uses this app_id — proceed.
		default:
			return AgentFeishuConnectorChange{}, fmt.Errorf("feishu connector uniqueness probe: %w", err)
		}
	}

	now := time.Now().UTC()
	agentUUID, err := uuid(agentID)
	if err != nil {
		return AgentFeishuConnectorChange{}, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return AgentFeishuConnectorChange{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	current, err := queries.GetAgentForUpdate(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentFeishuConnectorChange{}, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return AgentFeishuConnectorChange{}, err
	}

	// Decode current config; extract the old snapshot so we can emit the
	// before/after audit payload and detect noop replays.
	config := decodeJSONMap(current.Config)
	old := readFeishuSnapshot(config)

	// Merge new snapshot back into connectors.feishu. Drop the subtree when
	// the new snapshot is fully empty so partial-index predicates don't pin
	// a useless row.
	connectors := nestedMap(config, "connectors")
	if normalized.isZero() {
		delete(connectors, "feishu")
		if len(connectors) == 0 {
			delete(config, "connectors")
		} else {
			config["connectors"] = connectors
		}
	} else {
		connectors["feishu"] = normalized.toJSONMap()
		config["connectors"] = connectors
	}

	encoded, err := json.Marshal(nonNilMap(config))
	if err != nil {
		return AgentFeishuConnectorChange{}, err
	}
	row, err := queries.UpdateAgentCRUD(ctx, sqlc.UpdateAgentCRUDParams{
		ID:            agentUUID,
		Name:          current.Name,
		Description:   current.Description,
		ConnectorType: current.ConnectorType,
		Config:        encoded,
		Now:           timestamptz(now),
	})
	if err != nil {
		return AgentFeishuConnectorChange{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentFeishuConnectorChange{}, err
	}

	change := AgentFeishuConnectorChange{
		AgentID:     row.ID,
		WorkspaceID: row.WorkspaceID,
		Name:        row.Name,
		Slug:        row.Slug,
		Old:         old,
		New:         normalized,
		UpdatedAt:   pgTime(row.UpdatedAt),
		Noop:        old == normalized,
	}
	if !change.Noop {
		// Audit payload omits *_ref values to keep the admin UI's
		// "feishu bot configured" filter cleaner.
		s.emitAgentAudit(now, actorID, auditAgentFeishuConnectorUpdated, "agent", change.AgentID, change.WorkspaceID, "", map[string]any{
			"slug":            change.Slug,
			"old_enabled":     change.Old.Enabled,
			"new_enabled":     change.New.Enabled,
			"old_app_id":      change.Old.AppID,
			"new_app_id":      change.New.AppID,
			"event_mode":      change.New.EventMode,
			"routing_mode":    change.New.RoutingMode,
			"bot_open_id_set": change.New.BotOpenID != "",
		})
	}
	return change, nil
}

// readFeishuSnapshot extracts the connectors.feishu subtree from the
// decoded jsonb config. Returns the zero snapshot when absent.
func readFeishuSnapshot(config map[string]any) FeishuConnectorSnapshot {
	connectors, _ := config["connectors"].(map[string]any)
	feishu, _ := connectors["feishu"].(map[string]any)
	if feishu == nil {
		return FeishuConnectorSnapshot{}
	}
	enabled, _ := feishu["enabled"].(bool)
	return FeishuConnectorSnapshot{
		Enabled:              enabled,
		AppID:                jsonString(feishu, "app_id"),
		AppSecretRef:         jsonString(feishu, "app_secret_ref"),
		VerificationTokenRef: jsonString(feishu, "verification_token_ref"),
		EncryptKeyRef:        jsonString(feishu, "encrypt_key_ref"),
		BotOpenID:            jsonString(feishu, "bot_open_id"),
		EventMode:            normalizeFeishuEventMode(jsonString(feishu, "event_mode")),
		RoutingMode:          normalizeFeishuRoutingMode(jsonString(feishu, "routing_mode")),
	}
}

func jsonString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// nestedMap returns config[key] as a map[string]any, creating it when
// absent or non-map (defensive against hand-edited jsonb).
func nestedMap(config map[string]any, key string) map[string]any {
	if existing, ok := config[key].(map[string]any); ok {
		return existing
	}
	return map[string]any{}
}

func (s FeishuConnectorSnapshot) isZero() bool {
	return !s.Enabled && s.AppID == "" && s.AppSecretRef == "" && s.VerificationTokenRef == "" && s.EncryptKeyRef == "" && s.BotOpenID == "" && (s.EventMode == "" || s.EventMode == "webhook") && (s.RoutingMode == "" || s.RoutingMode == "direct")
}

func (s FeishuConnectorSnapshot) toJSONMap() map[string]any {
	return map[string]any{
		"enabled":                s.Enabled,
		"app_id":                 s.AppID,
		"app_secret_ref":         s.AppSecretRef,
		"verification_token_ref": s.VerificationTokenRef,
		"encrypt_key_ref":        s.EncryptKeyRef,
		"bot_open_id":            s.BotOpenID,
		"event_mode":             normalizeFeishuEventMode(s.EventMode),
		"routing_mode":           normalizeFeishuRoutingMode(s.RoutingMode),
	}
}

func normalizeFeishuEventMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "websocket", "ws", "event_websocket":
		return "websocket"
	default:
		return "webhook"
	}
}

func normalizeFeishuRoutingMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared", "router", "command_router":
		return "shared"
	default:
		return "direct"
	}
}

// CountActiveFeishuBotAgents reports how many active Agents have an
// enabled Feishu connector. Used by the OSS lazy-mode gate to refuse
// starting with more than one such Agent sharing the platform App ID.
func (s *Store) CountActiveFeishuBotAgents(ctx context.Context) (int, error) {
	n, err := sqlc.New(s.db).CountActiveFeishuBotAgents(ctx)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// AddWorkspaceMemberInput drives admin-side member add. The user record is
// created on the fly when the email is new (or reused if on file).
type AddWorkspaceMemberInput struct {
	WorkspaceID string
	Email       string
	Name        string
	Role        string
	Now         time.Time
}

// AddWorkspaceMemberResult is the membership row plus user-side metadata.
// `UserCreated` tells the caller whether a brand-new user row was minted
// or the email matched a pre-existing user.
type AddWorkspaceMemberResult struct {
	Member      WorkspaceMemberRead `json:"member"`
	UserCreated bool                `json:"user_created"`
}

// RemoveWorkspaceMemberResult is the soft-deleted ws_member row.
type RemoveWorkspaceMemberResult struct {
	Member WorkspaceMemberRead `json:"member"`
}

var ErrUnknownProjectAgent = errors.New("unknown active project agent")
var ErrInvalidConnectorType = errors.New("invalid connector type")
var ErrUnknownSecret = errors.New("unknown secret")
var ErrUnknownModelProvider = errors.New("unknown model provider")
var ErrUnknownModel = errors.New("unknown model")
var ErrModelDisabled = errors.New("model or provider disabled")

const defaultReadLimit int32 = 100
const defaultMaxAgentChainDepth int32 = 3

// Every admin / runtime emit in this file uses ActorType: audit.ActorTypeSystem
// because the dev auth path doesn't yet surface a real caller identity.
// ActorID is best-effort sourced from input.CreatedBy / input.ActorID.

const (
	auditIMMessageCreated = "im.message.created"
	auditUserMessageSent  = "user.message.sent"
	auditAgentRunCreated  = "agent_run.created"
	auditAgentRunClaimed  = "agent_run.claimed"
	// AuditAgentRunCancelled is exported so dev package emit calls share it.
	AuditAgentRunCancelled           = "agent_run.cancelled"
	auditAgentRunCompleted           = "agent_run.completed"
	auditAgentRunFailed              = "agent_run.failed"
	auditHTTPAgentCompleted          = "http_agent.completed"
	auditHTTPAgentFailed             = "http_agent.failed"
	auditAgentRunRequeued            = "agent_run.requeued"
	auditAgentToAgentChildRunCreated = "agent_to_agent.child_run.created"
	auditRuntimeCreated              = "runtime.created"
	auditRuntimePaired               = "runtime.paired"
	auditRuntimeUpdated              = "runtime.updated"
	auditRuntimeDeleted              = "runtime.deleted"
	auditRuntimeOnline               = "runtime.online"
	auditProjectAgentDisabled        = "project_agent.disabled"
	auditProjectAgentEnabled         = "project_agent.enabled"
	auditAgentCreated                = "agent.created"
	auditAgentUpdated                = "agent.updated"
	auditAgentVisibilityChanged      = "agent.visibility.changed"
	auditAgentFeishuConnectorUpdated = "agent.feishu_connector.updated"
	auditAgentDeleted                = "agent.deleted"
	auditProjectAgentAttached        = "project_agent.attached"
	auditProjectAgentDetached        = "project_agent.detached"
	auditWorkspaceMemberAdded        = "workspace_member.added"
	auditWorkspaceMemberRoleUpdated  = "workspace_member.role_updated"
	auditWorkspaceMemberRemoved      = "workspace_member.removed"
	auditWorkspaceJoinRequested      = "workspace_join.requested"
	auditWorkspaceJoinApproved       = "workspace_join.approved"
	auditWorkspaceJoinRejected       = "workspace_join.rejected"
	auditWorkspaceJoinWithdrawn      = "workspace_join.withdrawn"
	auditWorkspaceCreated            = "workspace.created"
	auditWorkspaceUpdated            = "workspace.updated"
	auditWorkspaceArchived           = "workspace.archived"
	auditProjectCreated              = "project.created"
	auditProjectUpdated              = "project.updated"
	auditProjectArchived             = "project.archived"
	auditSecretCreated               = "secret.created"
	auditSecretDisabled              = "secret.disabled"
	auditModelCreated                = "model.created"
	auditModelUpdated                = "model.updated"
	auditModelDisabled               = "model.disabled"
	auditModelDeleted                = "model.deleted"
	auditSourceIM                    = "im"
	auditSourceGateway               = "gateway"
	auditSourceRuntime               = "runtime"
	auditSourceHTTPAgent             = "http_agent"
	auditSourceDevMemberWrite        = "dev_member_write"
	auditSourceDevWorkspaceWrite     = "dev_workspace_write"
	auditSourceDevProjectWrite       = "dev_project_write"
	auditSourceDevSecretWrite        = "dev_secret_write"
	auditSourceDevModelRegistryWrite = "dev_model_registry_write"
)

func (s *Store) GetAgentRunInvocation(ctx context.Context, runID string) (AgentRunInvocation, error) {
	queries := sqlc.New(s.db)
	runUUID, err := uuid(runID)
	if err != nil {
		return AgentRunInvocation{}, err
	}

	row, err := queries.GetAgentRunInvocation(ctx, runUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			exists, existsErr := queries.AgentRunExists(ctx, runUUID)
			if existsErr != nil {
				return AgentRunInvocation{}, existsErr
			}
			if exists {
				return AgentRunInvocation{}, fmt.Errorf("%w: %s", ErrInvalidProjectAgent, runID)
			}
			return AgentRunInvocation{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		return AgentRunInvocation{}, err
	}
	triggerMetadata := decodeJSONMap(row.TriggerMessageMetadata)
	return AgentRunInvocation{
		RunID:                 row.RID,
		WorkspaceID:           row.RWorkspaceID,
		ProjectID:             row.RProjectID,
		ConversationID:        row.RConversationID,
		ProjectAgentID:        row.RProjectAgentID,
		AgentID:               row.PaAgentID,
		AgentName:             row.AgentName,
		AgentSlug:             row.AgentSlug,
		RequestedByType:       row.RequestedByType,
		RequestedByID:         row.RequestedByID,
		ConnectorType:         row.ConnectorType,
		Status:                row.Status,
		TriggerMessageContent: applyTriggerMessagePrefix(triggerMetadata, row.TriggerMessageContent),
		TriggerAttachments:    DecodeMessageAttachments(triggerMetadata),
		AgentConfig:           decodeJSONMap(row.AgentConfig),
		ProjectAgentConfig:    mergeRuntimeIntoProjectAgentConfig(decodeJSONMap(row.ProjectAgentConfig), row.RuntimeID),
	}, nil
}

// TriggerMessageQuotedChainPrefixKey is the metadata field gateways
// stamp with a rendered "[Quoted message]…" prefix when an inbound is
// a reply. The prefix is prepended to TriggerMessageContent at dispatch
// time so the LLM sees the chain context, while messages.content stays
// the user's verbatim input.
const TriggerMessageQuotedChainPrefixKey = "quoted_chain_prefix"

// applyTriggerMessagePrefix prepends metadata-stashed dispatch context
// (today: the Feishu quoted-chain prefix) to the raw user text. Empty
// or missing metadata leaves the text untouched.
func applyTriggerMessagePrefix(metadata map[string]any, content string) string {
	if len(metadata) == 0 {
		return content
	}
	raw, ok := metadata[TriggerMessageQuotedChainPrefixKey].(string)
	if !ok || raw == "" {
		return content
	}
	return raw + content
}

// mergeRuntimeIntoProjectAgentConfig folds the explicit
// project_agents.runtime_id binding into the legacy
// ProjectAgentConfig.device_id slot so downstream connectors see a uniform
// view. When runtime_id is set it wins over any stale config.device_id —
// the explicit FK is the source of truth. Empty runtime_id leaves the map
// untouched.
func mergeRuntimeIntoProjectAgentConfig(cfg map[string]any, runtimeID string) map[string]any {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" {
		return cfg
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["device_id"] = runtimeID
	cfg["runtime_id"] = runtimeID
	return cfg
}

func (s *Store) GetHTTPAgentRunInvocation(ctx context.Context, runID string) (AgentRunInvocation, error) {
	invocation, err := s.GetAgentRunInvocation(ctx, runID)
	if err != nil {
		return AgentRunInvocation{}, err
	}
	if invocation.ConnectorType != "http" {
		return AgentRunInvocation{}, fmt.Errorf("%w: %s has connector_type %s", ErrInvalidHTTPConnector, runID, invocation.ConnectorType)
	}
	return invocation, nil
}

func (s *Store) ConfigureDevProjectAgentConnector(ctx context.Context, input ConfigureDevProjectAgentConnectorInput) (ConfigureDevProjectAgentConnectorResult, error) {
	connectorType := strings.TrimSpace(input.ConnectorType)
	if connectorType != "http" {
		return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: %s", ErrInvalidConnectorType, connectorType)
	}
	projectAgentID, err := uuid(input.ProjectAgentID)
	if err != nil {
		return ConfigureDevProjectAgentConnectorResult{}, err
	}

	agentConfig := map[string]any{}
	if connectorType == "http" {
		endpoint := strings.TrimSpace(input.Endpoint)
		if endpoint != "" {
			agentConfig["endpoint"] = endpoint
		}
	}
	agentConfigJSON, err := json.Marshal(agentConfig)
	if err != nil {
		return ConfigureDevProjectAgentConnectorResult{}, err
	}

	queries := sqlc.New(s.db)
	row, err := queries.ConfigureDevProjectAgentConnector(ctx, sqlc.ConfigureDevProjectAgentConnectorParams{
		ProjectAgentID: projectAgentID,
		ConnectorType:  connectorType,
		AgentConfig:    agentConfigJSON,
		Now:            timestamptz(time.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, input.ProjectAgentID)
		}
		return ConfigureDevProjectAgentConnectorResult{}, err
	}

	return ConfigureDevProjectAgentConnectorResult{
		ProjectAgentID: row.ProjectAgentID,
		ProjectID:      row.PaProjectID,
		AgentID:        row.AgentID,
		Name:           row.Name,
		Slug:           row.Slug,
		ConnectorType:  row.ConnectorType,
		AgentConfig:    decodeJSONMap(row.AgentConfig),
	}, nil
}

func (s *Store) ConfigureProjectAgentProfile(ctx context.Context, input ConfigureProjectAgentProfileInput) (ConfigureDevProjectAgentConnectorResult, error) {
	projectAgentID, err := uuid(input.ProjectAgentID)
	if err != nil {
		return ConfigureDevProjectAgentConnectorResult{}, err
	}
	config := nonNilMap(input.Config)
	if modelID := strings.TrimSpace(input.ModelID); modelID != "" {
		modelUUID, err := uuid(modelID)
		if err != nil {
			return ConfigureDevProjectAgentConnectorResult{}, err
		}
		workspaceID, err := sqlc.New(s.db).GetProjectAgentWorkspace(ctx, projectAgentID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, input.ProjectAgentID)
			}
			return ConfigureDevProjectAgentConnectorResult{}, err
		}
		workspaceUUID, err := uuid(workspaceID)
		if err != nil {
			return ConfigureDevProjectAgentConnectorResult{}, err
		}
		_ = workspaceUUID
		status, err := sqlc.New(s.db).GetModelStatus(ctx, modelUUID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
			}
			return ConfigureDevProjectAgentConnectorResult{}, err
		}
		if status != "active" {
			return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: model=%s", ErrModelDisabled, status)
		}
		config["model_id"] = modelID
	}
	if workdir := strings.TrimSpace(input.Workdir); workdir != "" {
		config["workdir"] = workdir
	}
	if systemPrompt := strings.TrimSpace(input.SystemPrompt); systemPrompt != "" {
		config["system_prompt"] = systemPrompt
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return ConfigureDevProjectAgentConnectorResult{}, err
	}
	row, err := sqlc.New(s.db).ConfigureProjectAgentProfile(ctx, sqlc.ConfigureProjectAgentProfileParams{ProjectAgentID: projectAgentID, ProjectAgentConfig: encoded, Now: timestamptz(time.Now().UTC())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfigureDevProjectAgentConnectorResult{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, input.ProjectAgentID)
		}
		return ConfigureDevProjectAgentConnectorResult{}, err
	}
	return ConfigureDevProjectAgentConnectorResult{ProjectAgentID: row.ProjectAgentID, ProjectID: row.PaProjectID, AgentID: row.AgentID, Name: row.Name, Slug: row.Slug, ConnectorType: row.ConnectorType, AgentConfig: decodeJSONMap(row.AgentConfig), ProjectAgentConfig: decodeJSONMap(row.ProjectAgentConfig)}, nil
}

func (s *Store) ClaimNextQueuedHTTPAgentRun(ctx context.Context) (ClaimHTTPAgentRunResult, error) {
	queries := sqlc.New(s.db)
	runID, err := queries.ClaimNextQueuedHTTPAgentRun(ctx, timestamptz(time.Now().UTC()))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClaimHTTPAgentRunResult{Claimed: false}, nil
		}
		return ClaimHTTPAgentRunResult{}, err
	}
	return ClaimHTTPAgentRunResult{RunID: runID, Claimed: true}, nil
}

func (s *Store) FailAgentRun(ctx context.Context, input FailAgentRunInput) error {
	now := time.Now().UTC()
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "agent_run"
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "unknown"
	}
	userFacing := mapUserFacingReason(reason)
	runID, err := uuid(input.RunID)
	if err != nil {
		return err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	run, err := queries.GetAgentRunForRead(ctx, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrUnknownAgentRun, input.RunID)
		}
		return err
	}
	metadata, err := json.Marshal(map[string]any{
		"failed_by":          source,
		"failure_reason":     reason,
		"user_facing_reason": userFacing,
	})
	if err != nil {
		return err
	}
	affected, err := queries.FailAgentRun(ctx, sqlc.FailAgentRunParams{Now: timestamptz(now), Metadata: metadata, ID: runID})
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// affected==0 means the run was already in a terminal state. The SQL
	// guard prevents overwriting; we must NOT emit a *.failed audit event
	// in that case, otherwise the failed audit count would exceed the
	// failed run count. 'failed' itself is allowed to self-overwrite.
	if affected == 0 {
		return nil
	}
	eventType := source + ".failed"
	if source == auditSourceHTTPAgent {
		eventType = auditHTTPAgentFailed
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceRuntime,
		EventType:   eventType,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     run.PaAgentID,
		TargetType:  "agent_run",
		TargetID:    run.RID,
		WorkspaceID: run.RWorkspaceID,
		ProjectID:   run.RProjectID,
		Payload: map[string]any{
			"source": source,
			"reason": reason,
		},
	})
	// Serial-queue handoff: failing a running run lets the next queued
	// sibling move forward.
	s.dispatchNextQueuedRunAfter(ctx, input.RunID)
	return nil
}

func (s *Store) RequeueFailedAgentRun(ctx context.Context, input RequeueAgentRunInput) (RequeueAgentRunResult, error) {
	now := time.Now().UTC()
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "dev_retry"
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "manual_retry"
	}
	runID, err := uuid(input.RunID)
	if err != nil {
		return RequeueAgentRunResult{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return RequeueAgentRunResult{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	metadata, err := json.Marshal(map[string]any{"requeued_by": source, "requeue_reason": reason})
	if err != nil {
		return RequeueAgentRunResult{}, err
	}
	row, err := queries.RequeueFailedAgentRun(ctx, sqlc.RequeueFailedAgentRunParams{Metadata: metadata, Now: timestamptz(now), ID: runID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			exists, existsErr := queries.AgentRunExists(ctx, runID)
			if existsErr != nil {
				return RequeueAgentRunResult{}, existsErr
			}
			if !exists {
				return RequeueAgentRunResult{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, input.RunID)
			}
			return RequeueAgentRunResult{}, fmt.Errorf("%w: %s is not failed", ErrAgentRunNotCompletable, input.RunID)
		}
		return RequeueAgentRunResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RequeueAgentRunResult{}, err
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceRuntime,
		EventType:   auditAgentRunRequeued,
		ActorType:   audit.ActorTypeSystem,
		TargetType:  "agent_run",
		TargetID:    row.ID,
		WorkspaceID: row.WorkspaceID,
		ProjectID:   row.ProjectID,
		Payload: map[string]any{
			"source": source,
			"reason": reason,
		},
	})
	return RequeueAgentRunResult{RunID: row.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID, ConversationID: row.ConversationID, ProjectAgentID: row.ProjectAgentID, Status: "queued"}, nil
}

func (s *Store) CancelAgentRun(ctx context.Context, runID, reason string) (bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "user_clicked_cancel"
	}
	runUUID, err := uuid(runID)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx, `
update agent_runs
set status = 'cancelled',
    failure_reason = $2,
    finished_at = $3,
    updated_at = $3,
    metadata = metadata || jsonb_build_object('cancel_reason', $2::text)
where id = $1::uuid
  and status not in ('completed', 'failed', 'cancelled')`, runUUID, reason, timestamptz(now))
	if err != nil {
		return false, err
	}
	transitioned := tag.RowsAffected() == 1
	if !transitioned {
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from agent_runs where id = $1::uuid)`, runUUID).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	// Serial-queue handoff: cancelling a running run lets the next queued
	// sibling move forward.
	if transitioned {
		s.dispatchNextQueuedRunAfter(ctx, runID)
	}
	return transitioned, nil
}

// SupersededRun is a lightweight descriptor of a run that was
// cancelled because a newer prompt superseded it.
type SupersededRun struct {
	ID            string
	ConnectorType string
}

// CancelRunningRunsForConversation cancels all in-flight (queued / running)
// agent_runs for the same (conversation, project_agent) pair, excluding
// excludeRunID (the new run about to start). Returns the cancelled runs
// so callers can send connector-level abort signals.
func (s *Store) CancelRunningRunsForConversation(ctx context.Context, conversationID, excludeRunID, reason string) ([]SupersededRun, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return nil, err
	}
	excludeUUID, err := uuid(excludeRunID)
	if err != nil {
		return nil, err
	}
	now := timestamptz(time.Now().UTC())
	rows, err := s.db.Query(ctx, `
UPDATE agent_runs
SET status = 'cancelled',
    failure_reason = $3,
    finished_at = $4,
    updated_at = $4,
    metadata = metadata || jsonb_build_object('cancel_reason', $3::text)
WHERE conversation_id = $1::uuid
  AND id != $2::uuid
  AND project_agent_id = (SELECT project_agent_id FROM agent_runs WHERE id = $2::uuid)
  AND status IN ('queued', 'running')
RETURNING id::text, connector_type`, convUUID, excludeUUID, reason, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupersededRun
	for rows.Next() {
		var r SupersededRun
		if err := rows.Scan(&r.ID, &r.ConnectorType); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CancelAllInflightForConversation cancels every queued / running run in a
// conversation, regardless of project_agent. Returns the cancelled rows so
// the caller can drive connector.Abort on each.
func (s *Store) CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]SupersededRun, error) {
	convUUID, err := uuid(conversationID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "user_cancel_all"
	}
	now := timestamptz(time.Now().UTC())
	rows, err := s.db.Query(ctx, `
UPDATE agent_runs
SET status = 'cancelled',
    failure_reason = $2,
    finished_at = $3,
    updated_at = $3,
    metadata = metadata || jsonb_build_object('cancel_reason', $2::text)
WHERE conversation_id = $1::uuid
  AND status IN ('queued', 'running')
RETURNING id::text, connector_type`, convUUID, reason, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupersededRun
	for rows.Next() {
		var r SupersededRun
		if err := rows.Scan(&r.ID, &r.ConnectorType); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FindConversationByExternalRef looks up the conversation id by gateway +
// external chat + thread tuple. Returns ErrUnknownConversation when none.
func (s *Store) FindConversationByExternalRef(ctx context.Context, gateway, externalChatID, externalThreadID string) (string, error) {
	gateway = strings.TrimSpace(gateway)
	externalChatID = strings.TrimSpace(externalChatID)
	externalThreadID = strings.TrimSpace(externalThreadID)
	if externalChatID == "" {
		return "", ErrUnknownConversation
	}
	// The conversations table stores platform / external_id /
	// external_thread_id as first-class columns (see migrations
	// 000001_init.sql lines 848-850) and soft-deletes via deleted_at —
	// NOT via metadata->>'gateway' / metadata->>'external_chat_id' /
	// 'archived_at'. The original revision of this query referenced an
	// 'archived_at' column that doesn't exist on prod and filtered on
	// the metadata jsonb; it errored with SQLSTATE 42703 the first time
	// a user actually hit it from the sharedbot /cancel branch
	// (2026-06-17). Rewriting against the same shape used by
	// HasFeishuThreadInboundHistory keeps the two read paths consistent
	// and hits the uk_conversations_external_active index directly.
	var id string
	err := s.db.QueryRow(ctx, `
select id::text
from conversations
where deleted_at is null
  and platform = $1
  and external_id = $2
  and coalesce(external_thread_id, '') = $3
order by created_at desc
limit 1`, gateway, externalChatID, externalThreadID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrUnknownConversation
		}
		return "", err
	}
	return id, nil
}

// FindPendingAskByChat returns the most recently updated conversation
// on the given chat that still has an open PromptForUserChoice slot.
// Used by the inbound ask-pending fast path: a free-text reply that
// lands in the chat but on a different thread than the asking
// conversation should still be delivered as the answer. Returns
// ErrUnknownConversation when no conversation in the chat has an open
// ask. Workspace / project scoping is intentionally omitted — the
// caller has already resolved the bot route and the chat_id is
// already a workspace-scoped identifier in Feishu's model.
func (s *Store) FindPendingAskByChat(ctx context.Context, gateway, externalChatID string) (conversationID string, slot PromptForUserChoiceInflightSlot, err error) {
	gateway = strings.TrimSpace(gateway)
	externalChatID = strings.TrimSpace(externalChatID)
	if externalChatID == "" {
		return "", PromptForUserChoiceInflightSlot{}, ErrUnknownConversation
	}
	var (
		id      string
		slotRaw []byte
	)
	err = s.db.QueryRow(ctx, `
select id::text,
       (metadata->'gateway_inflight'->'prompt_for_user_choice')::text
from conversations
where deleted_at is null
  and platform = $1
  and external_id = $2
  and metadata->'gateway_inflight'->'prompt_for_user_choice'->>'request_id' is not null
order by (metadata->'gateway_inflight'->'prompt_for_user_choice'->>'updated_at')::timestamptz desc nulls last,
         updated_at desc
limit 1`, gateway, externalChatID).Scan(&id, &slotRaw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", PromptForUserChoiceInflightSlot{}, ErrUnknownConversation
		}
		return "", PromptForUserChoiceInflightSlot{}, err
	}
	if len(slotRaw) > 0 {
		if jsonErr := json.Unmarshal(slotRaw, &slot); jsonErr != nil {
			return "", PromptForUserChoiceInflightSlot{}, fmt.Errorf("FindPendingAskByChat: decode slot: %w", jsonErr)
		}
	}
	return id, slot, nil
}

// HasFeishuThreadInboundHistory reports whether the bot has previously
// accepted an inbound message in the given (chat_id, thread_key) pair on
// the feishu gateway. Used by the inbound mention gate to allow follow-up
// messages in a Feishu 话题 without requiring @mention on every reply.
// Returns false (no error) when either argument is empty. Caller passes
// the thread *key* (FeishuInboundEvent.ThreadKey()), not the message_id.
func (s *Store) HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadKey string) (bool, error) {
	externalChatID = strings.TrimSpace(externalChatID)
	threadKey = strings.TrimSpace(threadKey)
	if externalChatID == "" || threadKey == "" {
		return false, nil
	}
	var exists bool
	err := s.db.QueryRow(ctx, `
select exists (
  select 1
  from conversations c
  where c.platform = 'feishu'
    and c.external_id = $1
    and c.external_thread_id = $2
    and c.deleted_at is null
)`, externalChatID, threadKey).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (s *Store) ConfigureDevConversationExternalRef(ctx context.Context, input ConfigureDevConversationExternalRefInput) (ConfigureDevConversationExternalRefResult, error) {
	conversationID, err := uuid(input.ConversationID)
	if err != nil {
		return ConfigureDevConversationExternalRefResult{}, err
	}
	gateway := strings.TrimSpace(input.Gateway)
	if gateway == "" {
		gateway = "dev"
	}
	externalChatID := strings.TrimSpace(input.ExternalChatID)
	if externalChatID == "" {
		return ConfigureDevConversationExternalRefResult{}, fmt.Errorf("%w: external_chat_id is required", ErrUnknownConversation)
	}
	externalThreadID := strings.TrimSpace(input.ExternalThreadID)
	metadata, err := json.Marshal(map[string]any{"gateway": gateway, "external_chat_id": externalChatID, "external_thread_id": externalThreadID})
	if err != nil {
		return ConfigureDevConversationExternalRefResult{}, err
	}
	// surface is hard-coded 'im' on the SQL side; pass form only.
	conversationForm := "group"
	if externalThreadID != "" {
		conversationForm = "dm"
	}
	row, err := sqlc.New(s.db).ConfigureDevConversationExternalRef(ctx, sqlc.ConfigureDevConversationExternalRefParams{
		ConversationForm: conversationForm,
		Platform:         gateway,
		ExternalID:       externalChatID,
		ExternalThreadID: externalThreadID,
		Metadata:         metadata,
		Now:              timestamptz(time.Now().UTC()),
		ID:               conversationID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConfigureDevConversationExternalRefResult{}, fmt.Errorf("%w: %s", ErrUnknownConversation, input.ConversationID)
		}
		return ConfigureDevConversationExternalRefResult{}, err
	}
	return ConfigureDevConversationExternalRefResult{ConversationID: row.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID, Platform: row.Platform, ExternalID: row.ExternalID, ExternalThreadID: row.ExternalThreadID}, nil
}

func (s *Store) CreateProjectConversation(ctx context.Context, input CreateProjectConversationInput) (ConversationRead, error) {
	queries := sqlc.New(s.db)
	projectUUID, err := uuid(input.ProjectID)
	if err != nil {
		return ConversationRead{}, err
	}
	workspaceID, err := queries.GetActiveProjectWorkspace(ctx, projectUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationRead{}, fmt.Errorf("%w: %s", ErrUnknownProject, input.ProjectID)
		}
		return ConversationRead{}, err
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return ConversationRead{}, err
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = "未命名会话"
	}
	surface := strings.TrimSpace(input.Surface)
	if surface == "" {
		surface = "web"
	}
	switch surface {
	case "web", "im", "api":
	default:
		return ConversationRead{}, fmt.Errorf("%w: invalid conversation surface: %s", ErrInvalidProjectInput, surface)
	}
	form := strings.TrimSpace(input.Form)
	if form == "" {
		switch surface {
		case "web":
			form = "thread"
		case "im":
			form = "group"
		case "api":
			form = "oneshot"
		}
	}
	switch form {
	case "thread", "group", "dm", "oneshot":
	default:
		return ConversationRead{}, fmt.Errorf("%w: invalid conversation form: %s", ErrInvalidProjectInput, form)
	}
	metadata := nonNilMap(input.Metadata)
	// Reject callers that pre-set metadata.primary_agent_id directly,
	// bypassing the project_agent validation. The binding pointer is
	// server-managed; only input.PrimaryAgentID + JOIN validation may
	// write the key.
	if _, present := metadata["primary_agent_id"]; present {
		return ConversationRead{}, fmt.Errorf("%w: metadata.primary_agent_id is reserved — set agent_id at the top level instead", ErrInvalidProjectInput)
	}
	primaryAgentID := strings.TrimSpace(input.PrimaryAgentID)
	if primaryAgentID != "" {
		paUUID, err := uuid(primaryAgentID)
		if err != nil {
			return ConversationRead{}, fmt.Errorf("%w: primary_agent_id: %w", ErrInvalidProjectInput, err)
		}
		var exists bool
		if err := s.db.QueryRow(ctx,
			`select exists(
				select 1 from project_agents pa
				join agents a on a.id = pa.agent_id
				where pa.id = $1
				  and pa.project_id = $2
				  and pa.status = 'active'
				  and pa.deleted_at is null
				  and a.status = 'active'
				  and a.deleted_at is null
			)`,
			paUUID, projectUUID,
		).Scan(&exists); err != nil {
			return ConversationRead{}, fmt.Errorf("validate primary agent: %w", err)
		}
		if !exists {
			return ConversationRead{}, fmt.Errorf("%w: project_agent_id=%s", ErrUnknownMention, primaryAgentID)
		}
		metadata["primary_agent_id"] = primaryAgentID
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return ConversationRead{}, err
	}
	row, err := queries.CreateProjectConversation(ctx, sqlc.CreateProjectConversationParams{
		ID:          mustUUID(newID()),
		WorkspaceID: workspaceUUID,
		ProjectID:   projectUUID,
		Surface:     surface,
		Form:        form,
		Title:       title,
		Metadata:    metadataJSON,
		Now:         timestamptz(time.Now().UTC()),
	})
	if err != nil {
		return ConversationRead{}, err
	}
	// Re-read so the response carries hydrated derived fields with the same
	// SQL/JOIN logic as list / detail.
	return s.GetProjectConversation(ctx, row.ID)
}

func (s *Store) ListProjectConversations(ctx context.Context, projectID string, agentID string, limit int32) ([]ConversationListItem, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	queries := sqlc.New(s.db)
	projectUUID, err := uuid(projectID)
	if err != nil {
		return nil, err
	}
	exists, err := queries.ActiveProjectExists(ctx, projectUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}
	agentFilter := strings.TrimSpace(agentID)
	if agentFilter != "" {
		// Reject malformed UUIDs explicitly so callers see a 422-style error
		// rather than the empty list a silent SQL filter would produce.
		if _, err := uuid(agentFilter); err != nil {
			return nil, fmt.Errorf("%w: agent_id: %w", ErrInvalidProjectInput, err)
		}
	}
	rows, err := queries.ListProjectConversations(ctx, sqlc.ListProjectConversationsParams{
		ProjectID: projectUUID,
		AgentID:   agentFilter,
		ItemLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	items := make([]ConversationListItem, 0, len(rows))
	for _, row := range rows {
		item := ConversationListItem{
			ConversationRead: ConversationRead{
				ID:               row.ID,
				WorkspaceID:      row.WorkspaceID,
				ProjectID:        row.ProjectID,
				Surface:          row.Surface,
				Form:             row.Form,
				Title:            row.Title,
				Status:           row.Status,
				Metadata:         decodeJSONMap(row.Metadata),
				PrimaryAgentID:   row.PrimaryAgentID,
				PrimaryAgentName: row.PrimaryAgentName,
				CreatedAt:        pgTime(row.CreatedAt),
				UpdatedAt:        pgTime(row.UpdatedAt),
			},
			MessageCount:          row.MessageCount,
			LastMessagePreview:    row.LastMessagePreview,
			LastMessageSenderType: row.LastMessageSenderType,
		}
		if row.LastMessageAt.Valid {
			t := row.LastMessageAt.Time.UTC()
			item.LastMessageAt = &t
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) GetProjectConversation(ctx context.Context, conversationID string) (ConversationRead, error) {
	queries := sqlc.New(s.db)
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return ConversationRead{}, err
	}
	row, err := queries.GetProjectConversation(ctx, conversationUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationRead{}, fmt.Errorf("%w: %s", ErrUnknownConversation, conversationID)
		}
		return ConversationRead{}, err
	}
	return ConversationRead{
		ID:               row.ID,
		WorkspaceID:      row.WorkspaceID,
		ProjectID:        row.ProjectID,
		Surface:          row.Surface,
		Form:             row.Form,
		Title:            row.Title,
		Status:           row.Status,
		Metadata:         decodeJSONMap(row.Metadata),
		PrimaryAgentID:   row.PrimaryAgentID,
		PrimaryAgentName: row.PrimaryAgentName,
		CreatedAt:        pgTime(row.CreatedAt),
		UpdatedAt:        pgTime(row.UpdatedAt),
	}, nil
}

// UpdateConversationTitle renames an active conversation. Title must be
// 1-200 runes; returns ErrUnknownConversation on missing or soft-deleted.
func (s *Store) UpdateConversationTitle(ctx context.Context, conversationID string, title string) error {
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(title)
	if trimmed == "" || len([]rune(trimmed)) > 200 {
		return fmt.Errorf("%w: title must be 1-200 characters", ErrInvalidProjectInput)
	}
	queries := sqlc.New(s.db)
	rows, err := queries.UpdateConversationTitle(ctx, sqlc.UpdateConversationTitleParams{
		Title: trimmed,
		Now:   timestamptz(time.Now().UTC()),
		ID:    conversationUUID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownConversation, conversationID)
	}
	return nil
}

// SoftDeleteConversation sets deleted_at on the conversation. A second call
// returns ErrUnknownConversation (the row is already filtered out).
func (s *Store) SoftDeleteConversation(ctx context.Context, conversationID string) error {
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return err
	}
	queries := sqlc.New(s.db)
	rows, err := queries.SoftDeleteConversation(ctx, sqlc.SoftDeleteConversationParams{
		Now: timestamptz(time.Now().UTC()),
		ID:  conversationUUID,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownConversation, conversationID)
	}
	return nil
}

func (s *Store) ListProjectEnabledAgents(ctx context.Context, projectID string) ([]ProjectAgentRead, error) {
	return s.listProjectAgents(ctx, projectID, false)
}

func (s *Store) CreateAgent(ctx context.Context, input CreateAgentInput) (CreateAgentResult, error) {
	now := time.Now().UTC()
	workspaceUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return CreateAgentResult{}, err
	}
	projectUUID, err := uuid(input.ProjectID)
	if err != nil {
		return CreateAgentResult{}, err
	}
	createdBy, err := uuid(input.CreatedBy)
	if err != nil {
		return CreateAgentResult{}, err
	}
	name := strings.TrimSpace(input.Name)
	connectorType := strings.TrimSpace(input.ConnectorType)
	if name == "" || connectorType == "" {
		return CreateAgentResult{}, ErrInvalidProjectInput
	}
	if !validConnectorType(connectorType) {
		return CreateAgentResult{}, ErrInvalidConnectorType
	}
	slug := strings.TrimSpace(input.Slug)
	explicitSlug := slug != ""
	if !explicitSlug {
		slug = generateAutoSlug("agent")
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return CreateAgentResult{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	_, err = queries.GetWorkspaceSettings(ctx, workspaceUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return CreateAgentResult{}, err
	}
	capabilities := input.Capabilities
	if !input.CapabilitiesSet {
		capabilities = nil
	}
	if explicitSlug {
		if exists, err := queries.ActiveAgentSlugExists(ctx, sqlc.ActiveAgentSlugExistsParams{WorkspaceID: workspaceUUID, Slug: slug}); err != nil {
			return CreateAgentResult{}, err
		} else if exists {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrDuplicateAgentSlug, nextSlugSuggestion(ctx, queries, workspaceUUID, slug))
		}
	}
	config, err := agentConfigJSON(input.SystemPrompt, input.DefaultModelID, capabilities, input.Runtime, connectorType, input.ProjectAgentConfig)
	if err != nil {
		return CreateAgentResult{}, err
	}
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" {
		visibility = "workspace"
	}
	if !isValidAgentVisibility(visibility) {
		return CreateAgentResult{}, fmt.Errorf("%w: %q", ErrInvalidAgentVisibility, visibility)
	}

	// runtime lives on agents.config.runtime only; project_agents.config is
	// for orthogonal per-project choices such as agent_daemon device/mode.
	encodedProjectAgentConfig, err := projectAgentConfigJSON(input.ProjectAgentConfig, connectorType)
	if err != nil {
		return CreateAgentResult{}, err
	}
	agentRow, err := createAgentWithSlugRetry(ctx, queries, sqlc.CreateAgentCRUDParams{ID: mustUUID(newID()), WorkspaceID: workspaceUUID, Name: name, Slug: slug, Description: strings.TrimSpace(input.Description), ConnectorType: connectorType, Visibility: visibility, Config: config, CreatedBy: createdBy, Now: timestamptz(now)}, explicitSlug)
	if err != nil {
		return CreateAgentResult{}, err
	}
	paRow, err := queries.CreateProjectAgentCRUD(ctx, sqlc.CreateProjectAgentCRUDParams{ID: mustUUID(newID()), WorkspaceID: workspaceUUID, ProjectID: projectUUID, AgentID: mustUUID(agentRow.ID), Config: encodedProjectAgentConfig, CreatedBy: createdBy, Now: timestamptz(now)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrUnknownProject, input.ProjectID)
		}
		return CreateAgentResult{}, err
	}
	initialCapabilities := make([]AgentCapabilityRead, 0, len(input.InitialCapabilities))
	seenInitialCapabilities := map[string]bool{}
	for _, requested := range input.InitialCapabilities {
		versionID := strings.TrimSpace(requested.CapabilityVersionID)
		if versionID == "" {
			return CreateAgentResult{}, fmt.Errorf("%w: empty capability_version_id", ErrInvalidProjectInput)
		}
		versionUUID, err := uuid(versionID)
		if err != nil {
			return CreateAgentResult{}, fmt.Errorf("%w: invalid capability_version_id", ErrInvalidProjectInput)
		}
		version, err := queries.GetCapabilityVersion(ctx, versionUUID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrUnknownCapabilityVersion, versionID)
			}
			return CreateAgentResult{}, err
		}
		capability, err := queries.GetCapability(ctx, mustUUID(version.CapabilityID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrUnknownCapability, version.CapabilityID)
			}
			return CreateAgentResult{}, err
		}
		if capability.WorkspaceID != input.WorkspaceID && (capability.Visibility != "public" || capability.DeprecatedAt.Valid || capability.Status != "active") {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrMarketplaceCapabilityUnavailable, version.CapabilityID)
		}
		if seenInitialCapabilities[version.CapabilityID] {
			return CreateAgentResult{}, fmt.Errorf("%w: duplicate initial capability %s", ErrInvalidProjectInput, version.CapabilityID)
		}
		seenInitialCapabilities[version.CapabilityID] = true
		configuration, err := json.Marshal(nonNilMap(requested.Configuration))
		if err != nil {
			return CreateAgentResult{}, err
		}
		row, err := queries.CreateAgentCapability(ctx, sqlc.CreateAgentCapabilityParams{
			ID:                  mustUUID(newID()),
			ProjectAgentID:      mustUUID(paRow.ID),
			CapabilityID:        mustUUID(version.CapabilityID),
			CapabilityVersionID: versionUUID,
			Enabled:             true,
			Configuration:       configuration,
			PinningMode:         normalizePinningMode(requested.PinningMode),
			Now:                 timestamptz(now),
		})
		if err != nil {
			return CreateAgentResult{}, err
		}
		initialCapabilities = append(initialCapabilities, agentCapabilityFromCreateRow(row))
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateAgentResult{}, err
	}
	agent := agentSummaryFromRow(agentRow.ID, agentRow.WorkspaceID, agentRow.Name, agentRow.Slug, agentRow.Description, agentRow.ConnectorType, agentRow.Status, agentRow.Config, agentRow.CreatedAt, agentRow.UpdatedAt)
	projectAgent := projectAgentSummaryFromRow(paRow.ID, paRow.WorkspaceID, paRow.ProjectID, paRow.AgentID, paRow.Status, paRow.Config, paRow.CreatedAt, paRow.UpdatedAt)
	s.emitAgentAudit(now, input.CreatedBy, auditAgentCreated, "agent", agent.ID, agent.WorkspaceID, "", map[string]any{"name": agent.Name, "slug": agent.Slug, "connector_type": agent.ConnectorType, "default_model_id": input.DefaultModelID, "visibility": visibility})
	attachedMeta := map[string]any{"agent_id": agent.ID, "project_id": projectAgent.ProjectID}
	if runtime := strings.TrimSpace(input.Runtime); runtime != "" {
		attachedMeta["runtime"] = runtime
	}
	s.emitAgentAudit(now, input.CreatedBy, auditProjectAgentAttached, "project_agent", projectAgent.ID, projectAgent.WorkspaceID, projectAgent.ProjectID, attachedMeta)
	return CreateAgentResult{Agent: agent, ProjectAgent: projectAgent, InitialCapabilities: initialCapabilities}, nil
}

func (s *Store) UpdateAgent(ctx context.Context, input UpdateAgentInput) (AgentSummary, []string, error) {
	now := time.Now().UTC()
	agentUUID, err := uuid(input.AgentID)
	if err != nil {
		return AgentSummary{}, nil, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return AgentSummary{}, nil, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	current, err := queries.GetAgentForUpdate(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentSummary{}, nil, fmt.Errorf("%w: %s", ErrUnknownAgent, input.AgentID)
		}
		return AgentSummary{}, nil, err
	}
	name, description, connectorType := current.Name, current.Description, current.ConnectorType
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	if input.ConnectorType != nil {
		connectorType = strings.TrimSpace(*input.ConnectorType)
	}
	if name == "" {
		return AgentSummary{}, nil, ErrInvalidProjectInput
	}
	if !validConnectorType(connectorType) {
		return AgentSummary{}, nil, ErrInvalidConnectorType
	}
	config := decodeJSONMap(current.Config)
	if input.SystemPrompt != nil {
		config["system_prompt"] = strings.TrimSpace(*input.SystemPrompt)
	}
	if input.DefaultModelID != nil {
		config["default_model_id"] = strings.TrimSpace(*input.DefaultModelID)
	}
	if input.CapabilitiesSet {
		caps := normalizeStringSlice(input.Capabilities)
		config["capabilities"] = caps
	}
	// Cherry-pick credential bindings from input.Config. Wholesale-merging
	// input.Config would race the dedicated setters above (e.g. a stale
	// default_model_id in the FE scratch could clobber DefaultModelID).
	//
	// JSON `null` decodes to a nil interface; JSON `{}` decodes to a
	// non-nil but empty map. Both mean "clear" from the FE perspective
	// (user dropped the last shared pick back to personal), so delete
	// the stored key in either shape.
	if input.ConfigSet {
		for _, k := range []string{"credential_bindings", "model_credential_binding"} {
			v, ok := input.Config[k]
			if !ok {
				continue
			}
			if isEmptyBindingValue(v) {
				delete(config, k)
			} else {
				config[k] = cloneBindingValue(v)
			}
		}
	}
	// Orphan binding cleanup: when capability list is explicitly set,
	// drop credential_bindings entries whose kind is no longer required
	// by any remaining capability. The runtime resolver only asks for
	// kinds the active capabilities declare, so orphans would be inert,
	// but pruning keeps agent_config tidy and matches what the UI shows.
	if input.CapabilitiesSet {
		needed, err := s.requiredKindsForCapabilities(ctx, queries, current.WorkspaceID, normalizeStringSlice(input.Capabilities))
		if err != nil {
			return AgentSummary{}, nil, err
		}
		if bindings, ok := config["credential_bindings"].(map[string]any); ok {
			for kind := range bindings {
				if !needed[kind] {
					delete(bindings, kind)
				}
			}
			if len(bindings) == 0 {
				delete(config, "credential_bindings")
			} else {
				config["credential_bindings"] = bindings
			}
		}
	}
	encoded, err := json.Marshal(nonNilMap(config))
	if err != nil {
		return AgentSummary{}, nil, err
	}
	row, err := queries.UpdateAgentCRUD(ctx, sqlc.UpdateAgentCRUDParams{ID: agentUUID, Name: name, Description: description, ConnectorType: connectorType, Config: encoded, Now: timestamptz(now)})
	if err != nil {
		return AgentSummary{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSummary{}, nil, err
	}
	agent := agentSummaryFromRow(row.ID, row.WorkspaceID, row.Name, row.Slug, row.Description, row.ConnectorType, row.Status, row.Config, row.CreatedAt, row.UpdatedAt)
	agent.Visibility = current.Visibility
	changed := changedAgentFields(current, agent, input)
	s.emitAgentAudit(now, input.ActorID, auditAgentUpdated, "agent", agent.ID, agent.WorkspaceID, "", map[string]any{"changed_fields": changed})
	return agent, changed, nil
}

// isEmptyBindingValue reports whether a cherry-picked binding value from
// the request body means "clear the stored key". Both JSON null (nil
// interface) and JSON {} (empty map) carry that intent — the FE uses {}
// to signal "user dropped the last shared pick back to personal".
func isEmptyBindingValue(v any) bool {
	if v == nil {
		return true
	}
	if m, ok := v.(map[string]any); ok {
		return len(m) == 0
	}
	return false
}

// cloneBindingValue returns a shallow copy of a binding-shaped value so
// the store never mutates a map it received from the HTTP handler. Today
// the only caller is the orphan-binding cleanup in UpdateAgent, which
// deletes entries from `credential_bindings`; without this copy that
// deletion would be visible on the handler's req.Config too.
func cloneBindingValue(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	return cloneAnyMap(m)
}

// cloneAnyMap is a shallow copy of a JSON object map. Nested maps share
// references with the original — callers that mutate at a deeper level
// must clone the level they touch.
func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// requiredKindsForCapabilities returns the set of credential `kind` codes
// declared as required by ANY of the named capabilities' latest versions
// in the workspace. Used by UpdateAgent to prune orphan credential_bindings
// when the user un-checks a capability in the edit dialog.
//
// Capabilities not found in the workspace (e.g. removed by another admin
// between FE render and submit) are silently skipped — their bindings will
// be pruned too, which matches the user's intent.
func (s *Store) requiredKindsForCapabilities(ctx context.Context, queries *sqlc.Queries, workspaceID string, capabilityNames []string) (map[string]bool, error) {
	needed := map[string]bool{}
	if len(capabilityNames) == 0 {
		return needed, nil
	}
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	for _, name := range capabilityNames {
		row, err := queries.GetCapabilityByName(ctx, sqlc.GetCapabilityByNameParams{WorkspaceID: wsUUID, Name: name})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		for _, rc := range decodeRequiredCredentials(row.RequiredCredentials) {
			needed[rc.Kind] = true
		}
	}
	return needed, nil
}

func (s *Store) DeleteProjectAgent(ctx context.Context, projectAgentID string, actorID string) (ProjectAgentSummary, error) {
	now := time.Now().UTC()
	paUUID, err := uuid(projectAgentID)
	if err != nil {
		return ProjectAgentSummary{}, err
	}
	row, err := sqlc.New(s.db).SoftDeleteProjectAgentCRUD(ctx, sqlc.SoftDeleteProjectAgentCRUDParams{ID: paUUID, Now: timestamptz(now)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentSummary{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
		}
		return ProjectAgentSummary{}, err
	}
	pa := projectAgentSummaryFromRow(row.ID, row.WorkspaceID, row.ProjectID, row.AgentID, row.Status, row.Config, row.CreatedAt, row.UpdatedAt)
	s.emitAgentAudit(now, actorID, auditProjectAgentDetached, "project_agent", pa.ID, pa.WorkspaceID, pa.ProjectID, map[string]any{"agent_id": pa.AgentID, "project_id": pa.ProjectID})
	return pa, nil
}

// ListProjectAgentsByAgentID returns all non-deleted project_agents linked
// to the given agent.
func (s *Store) ListProjectAgentsByAgentID(ctx context.Context, agentID string) ([]ProjectAgentSummary, error) {
	agentUUID, err := uuid(agentID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
SELECT id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at
FROM project_agents
WHERE agent_id = $1::uuid AND deleted_at IS NULL
ORDER BY created_at`, agentUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectAgentSummary
	for rows.Next() {
		var pa ProjectAgentSummary
		var cfgJSON []byte
		if err := rows.Scan(&pa.ID, &pa.WorkspaceID, &pa.ProjectID, &pa.AgentID, &pa.Status, &cfgJSON, &pa.CreatedAt, &pa.UpdatedAt); err != nil {
			return out, err
		}
		if len(cfgJSON) > 0 {
			_ = json.Unmarshal(cfgJSON, &pa.Config)
		}
		out = append(out, pa)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgent(ctx context.Context, agentID string, actorID string) (DeleteAgentResult, int64, error) {
	now := time.Now().UTC()
	agentUUID, err := uuid(agentID)
	if err != nil {
		return DeleteAgentResult{}, 0, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return DeleteAgentResult{}, 0, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	current, err := queries.GetAgentForUpdate(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DeleteAgentResult{}, 0, fmt.Errorf("%w: %s", ErrUnknownAgent, agentID)
		}
		return DeleteAgentResult{}, 0, err
	}
	runCount, err := queries.CountInFlightRunsByAgent(ctx, agentUUID)
	if err != nil {
		return DeleteAgentResult{}, 0, err
	}
	if runCount > 0 {
		return DeleteAgentResult{}, runCount, ErrInFlightAgentRuns
	}
	detachedRows, err := queries.SoftDeleteProjectAgentsByAgent(ctx, sqlc.SoftDeleteProjectAgentsByAgentParams{AgentID: agentUUID, Now: timestamptz(now)})
	if err != nil {
		return DeleteAgentResult{}, 0, err
	}
	row, err := queries.SoftDeleteAgentCRUD(ctx, sqlc.SoftDeleteAgentCRUDParams{ID: agentUUID, Now: timestamptz(now)})
	if err != nil {
		return DeleteAgentResult{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeleteAgentResult{}, 0, err
	}
	agent := agentSummaryFromRow(row.ID, row.WorkspaceID, row.Name, row.Slug, row.Description, row.ConnectorType, row.Status, row.Config, row.CreatedAt, row.UpdatedAt)
	detached := make([]string, 0, len(detachedRows))
	for _, pa := range detachedRows {
		detached = append(detached, pa.ID)
	}
	s.emitAgentAudit(now, actorID, auditAgentDeleted, "agent", current.ID, current.WorkspaceID, "", map[string]any{"name": current.Name, "slug": current.Slug})
	for _, pa := range detachedRows {
		s.emitAgentAudit(now, actorID, auditProjectAgentDetached, "project_agent", pa.ID, pa.WorkspaceID, pa.ProjectID, map[string]any{"agent_id": pa.AgentID, "project_id": pa.ProjectID})
	}
	return DeleteAgentResult{Agent: agent, DetachedProjectAgentIDs: detached}, 0, nil
}

func (s *Store) ListProjectAgentsForAdmin(ctx context.Context, projectID string) ([]ProjectAgentRead, error) {
	return s.listProjectAgents(ctx, projectID, true)
}

func (s *Store) listProjectAgents(ctx context.Context, projectID string, includeDisabled bool) ([]ProjectAgentRead, error) {
	queries := sqlc.New(s.db)
	projectUUID, err := uuid(projectID)
	if err != nil {
		return nil, err
	}

	exists, err := queries.ActiveProjectExists(ctx, projectUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}

	agents := make([]ProjectAgentRead, 0)
	if includeDisabled {
		rows, err := queries.ListProjectAgentsAdmin(ctx, projectUUID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			agents = append(agents, ProjectAgentRead{
				ProjectAgentID:    row.ProjectAgentID,
				ProjectID:         row.ProjectID,
				AgentID:           row.AgentID,
				Name:              row.Name,
				Slug:              row.Slug,
				Description:       row.Description,
				ConnectorType:     row.ConnectorType,
				Status:            row.Status,
				Runtime:           runtimePtr(row.Runtime),
				Config:            decodeJSONMap(row.Config),
				AgentConfig:       decodeJSONMap(row.AgentConfig),
				Visibility:        row.Visibility,
				CreatedByUserID:   row.CreatedByUserID,
				CreatedByName:     row.CreatedByName,
				EnabledAt:         pgTime(row.EnabledAt),
				RuntimeID:         row.RuntimeID,
				RuntimeName:       row.RuntimeName,
				RuntimeKind:       row.RuntimeKind,
				RuntimeLiveness:   row.RuntimeLiveness,
				SandboxExternalID: row.SandboxExternalID,
				SandboxStatus:     row.SandboxStatus,
			})
		}
		return agents, nil
	}
	rows, err := queries.ListProjectEnabledAgents(ctx, projectUUID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		agents = append(agents, ProjectAgentRead{
			ProjectAgentID:    row.ProjectAgentID,
			ProjectID:         row.ProjectID,
			AgentID:           row.AgentID,
			Name:              row.Name,
			Slug:              row.Slug,
			Description:       row.Description,
			ConnectorType:     row.ConnectorType,
			Status:            row.Status,
			Runtime:           runtimePtr(row.Runtime),
			Config:            decodeJSONMap(row.Config),
			AgentConfig:       decodeJSONMap(row.AgentConfig),
			Visibility:        row.Visibility,
			CreatedByUserID:   row.CreatedByUserID,
			CreatedByName:     row.CreatedByName,
			EnabledAt:         pgTime(row.EnabledAt),
			RuntimeID:         row.RuntimeID,
			RuntimeName:       row.RuntimeName,
			RuntimeKind:       row.RuntimeKind,
			RuntimeLiveness:   row.RuntimeLiveness,
			SandboxExternalID: row.SandboxExternalID,
			SandboxStatus:     row.SandboxStatus,
		})
	}
	return agents, nil
}

func (s *Store) GetConversationTimeline(ctx context.Context, conversationID string, limit int32) (ConversationTimelineRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	queries := sqlc.New(s.db)
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return ConversationTimelineRead{}, err
	}

	exists, err := queries.ActiveConversationExists(ctx, conversationUUID)
	if err != nil {
		return ConversationTimelineRead{}, err
	}
	if !exists {
		return ConversationTimelineRead{}, fmt.Errorf("%w: %s", ErrUnknownConversationForRead, conversationID)
	}

	messageRows, err := queries.ListConversationMessages(ctx, sqlc.ListConversationMessagesParams{ConversationID: conversationUUID, ItemLimit: limit})
	if err != nil {
		return ConversationTimelineRead{}, err
	}
	runRows, err := queries.ListConversationAgentRuns(ctx, sqlc.ListConversationAgentRunsParams{ConversationID: conversationUUID, ItemLimit: limit})
	if err != nil {
		return ConversationTimelineRead{}, err
	}

	runsByTrigger := make(map[string][]AgentRunBriefRead)
	runs := make([]AgentRunBriefRead, 0, len(runRows))
	for _, row := range runRows {
		run := agentRunBriefFromConversationRow(row)
		runs = append(runs, run)
		if run.TriggerMessageID != "" {
			runsByTrigger[run.TriggerMessageID] = append(runsByTrigger[run.TriggerMessageID], run)
		}
	}

	// Batch-fetch tool steps for all runs and attach to each run.
	stepsByRun, err := s.fetchToolStepsForRuns(ctx, queries, runs)
	if err != nil {
		return ConversationTimelineRead{}, err
	}
	for i := range runs {
		runs[i].Steps = stepsByRun[runs[i].ID]
		// Position-lookup failure is non-fatal — UI falls back to a bare "排队中".
		if runs[i].Status == "queued" {
			if pos, posErr := s.QueuePositionForRun(ctx, runs[i].ID); posErr == nil {
				runs[i].QueuePosition = pos
			}
		}
	}
	runsByTrigger = make(map[string][]AgentRunBriefRead)
	for _, run := range runs {
		if run.TriggerMessageID != "" {
			runsByTrigger[run.TriggerMessageID] = append(runsByTrigger[run.TriggerMessageID], run)
		}
	}

	messages := make([]MessageRead, 0, len(messageRows))
	for _, row := range messageRows {
		message := messageFromConversationRow(row)
		message.Runs = runsByTrigger[message.ID]
		messages = append(messages, message)
	}

	return ConversationTimelineRead{ConversationID: conversationID, Messages: messages, AgentRuns: runs}, nil
}

// fetchToolStepsForRuns batch-loads tool.call / tool.result events for the
// given runs and returns them grouped by run ID.
func (s *Store) fetchToolStepsForRuns(ctx context.Context, queries *sqlc.Queries, runs []AgentRunBriefRead) (map[string][]ToolStepRead, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	ids := make([]pgtype.UUID, 0, len(runs))
	for _, r := range runs {
		u, err := uuid(r.ID)
		if err != nil {
			continue
		}
		ids = append(ids, u)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := queries.ListToolEventsForRuns(ctx, ids)
	if err != nil {
		return nil, err
	}
	return buildToolSteps(rows), nil
}

// buildToolSteps pairs tool.call and tool.result events by payload.id into
// ToolStepRead slices grouped by agent_run_id.
func buildToolSteps(rows []sqlc.ListToolEventsForRunsRow) map[string][]ToolStepRead {
	type pendingStep struct {
		index int
		runID string
	}
	result := make(map[string][]ToolStepRead)
	pending := make(map[string]pendingStep)

	for _, row := range rows {
		// Persisted payload shape is flat
		// ({id, name, stage, args, result, sequence}),
		// NOT wrapped under a "tool" key like the SSE wire shape.
		payload := decodeJSONMap(row.Payload)
		callID, _ := payload["id"].(string)
		name, _ := payload["name"].(string)
		stage, _ := payload["stage"].(string)

		switch row.EventKind {
		case "tool.call":
			step := ToolStepRead{
				ToolCallID: callID,
				Name:       name,
				Status:     "running",
				OccurredAt: pgTime(row.OccurredAt),
			}
			if args := mapFromPayload(payload, "args"); len(args) > 0 {
				step.Args = args
			}
			steps := result[row.AgentRunID]
			result[row.AgentRunID] = append(steps, step)
			if callID != "" {
				pending[callID] = pendingStep{index: len(result[row.AgentRunID]) - 1, runID: row.AgentRunID}
			}

		case "tool.result":
			if callID != "" {
				if p, ok := pending[callID]; ok {
					result[p.runID][p.index].Status = "completed"
					if res := mapFromPayload(payload, "result"); len(res) > 0 {
						result[p.runID][p.index].Result = res
					}
					delete(pending, callID)
					continue
				}
			}
			// Orphan tool.result — create a standalone completed step.
			if stage == "after" || stage == "" {
				step := ToolStepRead{
					ToolCallID: callID,
					Name:       name,
					Status:     "completed",
					OccurredAt: pgTime(row.OccurredAt),
				}
				result[row.AgentRunID] = append(result[row.AgentRunID], step)
			}
		}
	}
	return result
}

func mapFromPayload(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	v, ok := parent[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func (s *Store) MarkGatewayOutboundDelivered(ctx context.Context, input MarkGatewayOutboundDeliveredInput) (MarkGatewayOutboundDeliveredResult, error) {
	messageID, err := uuid(input.MessageID)
	if err != nil {
		return MarkGatewayOutboundDeliveredResult{}, err
	}
	// DeliveryID is accepted for back-compat; the inflight slot owns the
	// live external_msg_id.
	_ = input.DeliveryID
	row, err := sqlc.New(s.db).MarkGatewayOutboundDelivered(ctx, sqlc.MarkGatewayOutboundDeliveredParams{Now: timestamptz(time.Now().UTC()), MessageID: messageID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MarkGatewayOutboundDeliveredResult{}, fmt.Errorf("%w: %s", ErrUnknownMessage, input.MessageID)
		}
		return MarkGatewayOutboundDeliveredResult{}, err
	}
	return MarkGatewayOutboundDeliveredResult{MessageID: row.ID, Metadata: decodeJSONMap(row.Metadata)}, nil
}

func (s *Store) GetAgentRun(ctx context.Context, runID string) (AgentRunDetailRead, error) {
	queries := sqlc.New(s.db)
	runUUID, err := uuid(runID)
	if err != nil {
		return AgentRunDetailRead{}, err
	}

	row, err := queries.GetAgentRunForRead(ctx, runUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentRunDetailRead{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		return AgentRunDetailRead{}, err
	}

	runMetadata := decodeJSONMap(row.Metadata)
	projectAgentConfig := decodeJSONMap(row.ProjectAgentConfig)
	agentConfig := decodeJSONMap(row.AgentConfig)
	bindingMetadata := decodeJSONMap(row.BindingMetadata)
	runtimeConfig := decodeJSONMap(row.RuntimeConfig)

	detail := AgentRunDetailRead{
		AgentRunBriefRead: AgentRunBriefRead{
			ID:               row.RID,
			WorkspaceID:      row.RWorkspaceID,
			ProjectID:        row.RProjectID,
			ConversationID:   row.RConversationID,
			TriggerMessageID: row.TriggerMessageID,
			OutputMessageID:  row.OutputMessageID,
			ProjectAgentID:   row.RProjectAgentID,
			AgentID:          row.PaAgentID,
			AgentName:        row.AgentName,
			AgentSlug:        row.AgentSlug,
			ConnectorType:    row.ConnectorType,
			Status:           row.Status,
			CreatedAt:        pgTime(row.CreatedAt),
			StartedAt:        pgOptionalTime(row.StartedAt),
			FinishedAt:       pgOptionalTime(row.FinishedAt),
		},
		RequestedByType: row.RequestedByType,
		RequestedByID:   row.RequestedByID,
		ExternalRunID:   row.ExternalRunID,
		Metadata:        runMetadata,
		UpdatedAt:       pgTime(row.UpdatedAt),
		Artifacts:       []ArtifactRead{},
		Usage:           []UsageLogRead{},
		Events:          []AgentRunEventRead{},
		Runtime:         agentRunRuntimeReadFromRow(row, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig),
	}
	if transcript, ok := detail.Metadata["transcript"].(string); ok {
		detail.Transcript = transcript
	}
	if detail.Status == "failed" {
		detail.UserFacingReason = userFacingReasonFromMetadata(detail.Metadata)
	}

	if detail.OutputMessageID != "" {
		messageRow, err := queries.GetOutputMessageByRunID(ctx, runUUID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return AgentRunDetailRead{}, err
		}
		if err == nil {
			message := messageFromOutputRow(messageRow)
			detail.OutputMessage = &message
		}
	}

	artifactRows, err := queries.ListAgentRunArtifacts(ctx, sqlc.ListAgentRunArtifactsParams{RunID: runUUID, ProjectID: mustUUID(detail.ProjectID)})
	if err != nil {
		return AgentRunDetailRead{}, err
	}
	for _, artifact := range artifactRows {
		detail.Artifacts = append(detail.Artifacts, ArtifactRead{
			ID:         artifact.ID,
			AgentRunID: artifact.AgentRunID,
			Name:       artifact.Name,
			Medium:     artifact.Medium,
			Kind:       artifact.Kind,
			URI:        artifact.Uri,
			Visibility: artifact.Visibility,
			Metadata:   decodeJSONMap(artifact.Metadata),
			CreatedAt:  pgTime(artifact.CreatedAt),
		})
	}

	usageRows, err := queries.ListUsageLogsByRun(ctx, sqlc.ListUsageLogsByRunParams{AgentRunID: runUUID, ProjectID: mustUUID(detail.ProjectID), ItemLimit: defaultReadLimit})
	if err != nil {
		return AgentRunDetailRead{}, err
	}
	for _, usage := range usageRows {
		detail.Usage = append(detail.Usage, usageLogFromRunRow(usage))
	}

	eventRows, err := queries.ListAgentRunEventsByRun(ctx, sqlc.ListAgentRunEventsByRunParams{AgentRunID: runUUID, ProjectID: mustUUID(detail.ProjectID), AfterSequence: 0})
	if err != nil {
		return AgentRunDetailRead{}, err
	}
	detail.Events = make([]AgentRunEventRead, 0, len(eventRows))
	for _, ev := range eventRows {
		detail.Events = append(detail.Events, agentRunEventFromRow(ev.ID, ev.WorkspaceID, ev.ProjectID, ev.AgentRunID, ev.Sequence, ev.EventKind, ev.Payload, ev.OccurredAt, ev.CreatedAt))
	}

	return detail, nil
}

func agentRunRuntimeReadFromRow(row sqlc.GetAgentRunForReadRow, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig map[string]any) *AgentRunRuntimeRead {
	connectorType := strings.TrimSpace(row.ConnectorType)
	runtime := AgentRunRuntimeRead{
		ID:               strings.TrimSpace(row.RuntimeID),
		Name:             strings.TrimSpace(row.RuntimeName),
		Type:             strings.TrimSpace(row.RuntimeType),
		Provider:         strings.TrimSpace(row.RuntimeProvider),
		ConnectorType:    connectorType,
		AgentKind:        resolveAgentRunRuntimeAgentKind(connectorType, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig),
		RuntimeMode:      resolveAgentRunRuntimeMode(row, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig),
		DeviceID:         resolveAgentRunRuntimeDeviceID(row, runMetadata, projectAgentConfig, bindingMetadata, runtimeConfig),
		SandboxID:        firstStringForKeys([]string{"sandbox_id", "e2b_sandbox_id", "parsar.sandbox_id"}, runMetadata, bindingMetadata, runtimeConfig, projectAgentConfig, agentConfig),
		ManagedModelID:   resolveAgentRunManagedModelID(runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig),
		Capabilities:     boolMapFromValue(runtimeConfig["daemon_capabilities"]),
		Liveness:         strings.TrimSpace(row.RuntimeLiveness),
		Hostname:         strings.TrimSpace(row.RuntimeHostname),
		Version:          strings.TrimSpace(row.RuntimeVersion),
		LastHeartbeatAt:  pgOptionalTime(row.LastHeartbeatAt),
		WorkingDirectory: resolveAgentRunWorkingDirectory(row, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig),
	}
	mergeAgentRunRuntimeSnapshot(&runtime, agentRunRuntimeReadFromMetadataSnapshot(runMetadata))
	if runtime.ExecutionPlace == "" {
		runtime.ExecutionPlace = deriveAgentRunExecutionPlace(runtime)
	}
	if runtime.GovernanceMode == "" {
		runtime.GovernanceMode = deriveAgentRunGovernanceMode(runtime)
	}
	if !agentRunRuntimeReadHasContent(runtime) {
		return nil
	}
	return &runtime
}

func agentRunRuntimeReadFromMetadataSnapshot(runMetadata map[string]any) *AgentRunRuntimeRead {
	if runMetadata == nil {
		return nil
	}
	raw, ok := runMetadata[agentRunExecutionSnapshotKey]
	if !ok || raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var runtime AgentRunRuntimeRead
	if err := json.Unmarshal(encoded, &runtime); err != nil {
		return nil
	}
	if !agentRunRuntimeReadHasContent(runtime) {
		return nil
	}
	return &runtime
}

func mergeAgentRunRuntimeSnapshot(runtime *AgentRunRuntimeRead, snapshot *AgentRunRuntimeRead) {
	if runtime == nil || snapshot == nil {
		return
	}
	if snapshot.ID != "" {
		runtime.ID = snapshot.ID
	}
	if snapshot.Name != "" {
		runtime.Name = snapshot.Name
	}
	if snapshot.Type != "" {
		runtime.Type = snapshot.Type
	}
	if snapshot.Provider != "" {
		runtime.Provider = snapshot.Provider
	}
	if snapshot.ConnectorType != "" {
		runtime.ConnectorType = snapshot.ConnectorType
	}
	if snapshot.AgentKind != "" {
		runtime.AgentKind = snapshot.AgentKind
	}
	if snapshot.RuntimeMode != "" {
		runtime.RuntimeMode = snapshot.RuntimeMode
	}
	if snapshot.ExecutionPlace != "" {
		runtime.ExecutionPlace = snapshot.ExecutionPlace
	}
	if snapshot.GovernanceMode != "" {
		runtime.GovernanceMode = snapshot.GovernanceMode
	}
	if snapshot.DeviceID != "" {
		runtime.DeviceID = snapshot.DeviceID
	}
	if snapshot.SandboxID != "" {
		runtime.SandboxID = snapshot.SandboxID
	}
	if snapshot.ManagedModelID != "" {
		runtime.ManagedModelID = snapshot.ManagedModelID
	}
	if len(snapshot.Capabilities) > 0 {
		runtime.Capabilities = cloneBoolMap(snapshot.Capabilities)
	}
	if snapshot.Liveness != "" {
		runtime.Liveness = snapshot.Liveness
	}
	if snapshot.Hostname != "" {
		runtime.Hostname = snapshot.Hostname
	}
	if snapshot.Version != "" {
		runtime.Version = snapshot.Version
	}
	if snapshot.LastHeartbeatAt != nil {
		runtime.LastHeartbeatAt = snapshot.LastHeartbeatAt
	}
	if snapshot.WorkingDirectory != "" {
		runtime.WorkingDirectory = snapshot.WorkingDirectory
	}
	if snapshot.CapturedAt != nil {
		runtime.CapturedAt = snapshot.CapturedAt
	}
}

func agentRunRuntimeReadHasContent(runtime AgentRunRuntimeRead) bool {
	if runtime.ConnectorType == "agent_daemon" {
		return true
	}
	for _, value := range []string{
		runtime.ID,
		runtime.Name,
		runtime.Type,
		runtime.Provider,
		runtime.AgentKind,
		runtime.RuntimeMode,
		runtime.ExecutionPlace,
		runtime.GovernanceMode,
		runtime.DeviceID,
		runtime.SandboxID,
		runtime.ManagedModelID,
		runtime.Liveness,
		runtime.Hostname,
		runtime.Version,
		runtime.WorkingDirectory,
	} {
		if value != "" {
			return true
		}
	}
	return runtime.LastHeartbeatAt != nil || runtime.CapturedAt != nil || len(runtime.Capabilities) > 0
}

func deriveAgentRunRuntimeMode(runtime AgentRunRuntimeRead) string {
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if strings.Contains(provider, "sandbox") || runtime.Type == RuntimeTypeSandbox {
		return "sandbox"
	}
	if runtime.Type == RuntimeTypeExternal || provider == RuntimeProviderHTTPAgent || runtime.ConnectorType == "http" {
		return "external"
	}
	if runtime.ConnectorType == "agent_daemon" || runtime.Type == RuntimeTypeAgentDaemon || runtime.DeviceID != "" {
		return "local"
	}
	return ""
}

func deriveAgentRunExecutionPlace(runtime AgentRunRuntimeRead) string {
	switch strings.ToLower(strings.TrimSpace(runtime.RuntimeMode)) {
	case "sandbox", "cloud_sandbox":
		return "cloud_sandbox"
	case "local", "local_device":
		return "local_device"
	case "external", "external_agent":
		return "external_agent"
	}
	provider := strings.ToLower(strings.TrimSpace(runtime.Provider))
	if strings.Contains(provider, "sandbox") || runtime.Type == RuntimeTypeSandbox {
		return "cloud_sandbox"
	}
	if runtime.Type == RuntimeTypeExternal || provider == RuntimeProviderHTTPAgent || runtime.ConnectorType == "http" {
		return "external_agent"
	}
	if runtime.ConnectorType == "agent_daemon" || runtime.Type == RuntimeTypeAgentDaemon || runtime.DeviceID != "" {
		return "local_device"
	}
	return ""
}

func deriveAgentRunGovernanceMode(runtime AgentRunRuntimeRead) string {
	switch deriveAgentRunExecutionPlace(runtime) {
	case "cloud_sandbox":
		return "managed"
	case "local_device":
		return "external_byo"
	case "external_agent":
		return "external"
	default:
		return ""
	}
}

func resolveAgentRunRuntimeAgentKind(connectorType string, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig map[string]any) string {
	if v := firstStringForKeys([]string{"agent_kind"}, runMetadata, projectAgentConfig, bindingMetadata, agentConfig, runtimeConfig); v != "" {
		return v
	}
	if connectorType == "agent_daemon" {
		return "claude_code"
	}
	return ""
}

func resolveAgentRunRuntimeMode(row sqlc.GetAgentRunForReadRow, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig map[string]any) string {
	if v := firstStringForKeys([]string{"runtime_mode", "daemon_mode"}, runMetadata, projectAgentConfig, bindingMetadata, runtimeConfig, agentConfig); v != "" {
		return v
	}
	if strings.TrimSpace(row.ConnectorType) != "agent_daemon" {
		return ""
	}
	provider := strings.ToLower(strings.TrimSpace(row.RuntimeProvider))
	if strings.Contains(provider, "sandbox") || strings.EqualFold(stringFromMap(runtimeConfig, "created_by"), "sandbox_provider") || stringFromMap(runtimeConfig, "sandbox_kind") != "" {
		return "sandbox"
	}
	if firstStringForKeys([]string{"device_id"}, projectAgentConfig, runMetadata, bindingMetadata, runtimeConfig) != "" || strings.TrimSpace(row.BoundDeviceID) != "" || provider == "agent_daemon" || strings.Contains(provider, "local") {
		return "local"
	}
	return ""
}

func resolveAgentRunRuntimeDeviceID(row sqlc.GetAgentRunForReadRow, runMetadata, projectAgentConfig, bindingMetadata, runtimeConfig map[string]any) string {
	if v := firstStringForKeys([]string{"device_id", "runtime_id"}, runMetadata); v != "" {
		return v
	}
	if strings.TrimSpace(row.RuntimeID) != "" && (strings.TrimSpace(row.ConnectorType) == "agent_daemon" || strings.TrimSpace(row.RuntimeType) == "agent_daemon") {
		return strings.TrimSpace(row.RuntimeID)
	}
	if v := firstStringForKeys([]string{"device_id"}, projectAgentConfig, bindingMetadata, runtimeConfig); v != "" {
		return v
	}
	return strings.TrimSpace(row.BoundDeviceID)
}

func resolveAgentRunWorkingDirectory(row sqlc.GetAgentRunForReadRow, runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig map[string]any) string {
	if wd := strings.TrimSpace(row.WorkingDirectory); wd != "" {
		return wd
	}
	return firstStringForKeys([]string{"working_directory", "work_dir", "workdir"}, runMetadata, projectAgentConfig, bindingMetadata, agentConfig, runtimeConfig)
}

func resolveAgentRunManagedModelID(runMetadata, projectAgentConfig, agentConfig, bindingMetadata, runtimeConfig map[string]any) string {
	if v := firstStringForKeys([]string{"managed_model_id"}, runMetadata, bindingMetadata, runtimeConfig); v != "" {
		return v
	}
	return firstStringForKeys([]string{"model_id", "default_model_id"}, runMetadata, projectAgentConfig, agentConfig)
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func boolMapFromValue(value any) map[string]bool {
	switch typed := value.(type) {
	case map[string]bool:
		return cloneBoolMap(typed)
	case map[string]any:
		out := map[string]bool{}
		for key, raw := range typed {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			switch v := raw.(type) {
			case bool:
				out[key] = v
			case string:
				trimmed := strings.ToLower(strings.TrimSpace(v))
				if trimmed == "true" || trimmed == "false" {
					out[key] = trimmed == "true"
				}
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func firstStringForKeys(keys []string, maps ...map[string]any) string {
	for _, values := range maps {
		for _, key := range keys {
			if v := stringFromMap(values, key); v != "" {
				return v
			}
		}
	}
	return ""
}

// ListProjectAgentRunsResult bundles a page of agent_run rows with the
// total row count under the same filter.
type ListProjectAgentRunsResult struct {
	Runs  []AgentRunBriefRead
	Total int64
}

// ListProjectAgentRuns returns a page of agent runs for an active project,
// newest first. `statuses` is an OR filter (nil/empty means no filter).
func (s *Store) ListProjectAgentRuns(ctx context.Context, projectID string, statuses []string, limit, offset int32) (ListProjectAgentRunsResult, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if offset < 0 {
		offset = 0
	}
	queries := sqlc.New(s.db)
	projectUUID, err := uuid(projectID)
	if err != nil {
		return ListProjectAgentRunsResult{}, err
	}

	exists, err := queries.ActiveProjectExists(ctx, projectUUID)
	if err != nil {
		return ListProjectAgentRunsResult{}, err
	}
	if !exists {
		return ListProjectAgentRunsResult{}, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}

	// sqlc + pgx/v5 treat a nil []string as NULL, which would break
	// `cardinality(NULL::text[]) = 0`. Normalise to an empty slice so
	// the "no filter" branch always evaluates to 0 cardinality.
	if statuses == nil {
		statuses = []string{}
	}

	rows, err := queries.ListProjectAgentRunsPage(ctx, sqlc.ListProjectAgentRunsPageParams{
		ProjectID:  projectUUID,
		Statuses:   statuses,
		ItemOffset: offset,
		ItemLimit:  limit,
	})
	if err != nil {
		return ListProjectAgentRunsResult{}, err
	}
	total, err := queries.CountProjectAgentRuns(ctx, sqlc.CountProjectAgentRunsParams{
		ProjectID: projectUUID,
		Statuses:  statuses,
	})
	if err != nil {
		return ListProjectAgentRunsResult{}, err
	}

	runs := make([]AgentRunBriefRead, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, agentRunBriefFromProjectPageRow(row))
	}
	return ListProjectAgentRunsResult{Runs: runs, Total: total}, nil
}

// AgentMetricsRead aggregates run history. SuccessRate is computed against
// (Completed + Failed); queued rows don't count. AvgDurationMs averages
// completed runs only.
type AgentMetricsRead struct {
	WindowDays     int32   `json:"window_days"`
	CompletedCount int64   `json:"completed_count"`
	FailedCount    int64   `json:"failed_count"`
	SuccessRate    float64 `json:"success_rate"`
	AvgDurationMs  float64 `json:"avg_duration_ms"`
}

// GetProjectAgentMetrics aggregates one project_agent's runs over the last
// windowDays (defaults to 30). Returns zeros (not an error) when there are
// no runs in window.
func (s *Store) GetProjectAgentMetrics(ctx context.Context, projectID, projectAgentID string, windowDays int32) (AgentMetricsRead, error) {
	if windowDays <= 0 {
		windowDays = 30
	}
	projectUUID, err := uuid(projectID)
	if err != nil {
		return AgentMetricsRead{}, err
	}
	agentUUID, err := uuid(projectAgentID)
	if err != nil {
		return AgentMetricsRead{}, err
	}
	queries := sqlc.New(s.db)
	exists, err := queries.ActiveProjectExists(ctx, projectUUID)
	if err != nil {
		return AgentMetricsRead{}, err
	}
	if !exists {
		return AgentMetricsRead{}, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}

	row, err := queries.GetProjectAgentMetrics(ctx, sqlc.GetProjectAgentMetricsParams{
		ProjectAgentID: agentUUID,
		ProjectID:      projectUUID,
		WindowDays:     windowDays,
	})
	if err != nil {
		return AgentMetricsRead{}, err
	}
	out := AgentMetricsRead{
		WindowDays:     windowDays,
		CompletedCount: row.CompletedCount,
		FailedCount:    row.FailedCount,
		AvgDurationMs:  row.AvgDurationMs,
	}
	if row.TotalCount > 0 {
		out.SuccessRate = float64(row.CompletedCount) / float64(row.TotalCount)
	}
	return out, nil
}

// ListWorkspaceMembers returns active workspace memberships joined with the
// user record. Empty list on unknown / deleted workspace (we don't 404 at
// this layer).
func (s *Store) ListWorkspaceMembers(ctx context.Context, workspaceID string, limit int32) ([]WorkspaceMemberRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	rows, err := queries.ListWorkspaceMembers(ctx, sqlc.ListWorkspaceMembersParams{WorkspaceID: workspaceUUID, ItemLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceMemberRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, WorkspaceMemberRead{
			ID:          row.ID,
			WorkspaceID: row.WorkspaceID,
			UserID:      row.UserID,
			Role:        row.Role,
			UserEmail:   row.UserEmail,
			UserName:    row.UserName,
			UserStatus:  row.UserStatus,
			CreatedAt:   pgTime(row.CreatedAt),
			UpdatedAt:   pgTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// ListActiveWorkspaceOwnerNames returns display names of active owners,
// earliest membership first. Returns names only (no user_id / email) —
// the consumer is a Feishu card shown to unauthenticated senders.
// Returns nil + nil on unknown / soft-deleted workspace.
func (s *Store) ListActiveWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	if limit <= 0 {
		limit = 5
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	names, err := queries.ListActiveWorkspaceOwnerNames(ctx, sqlc.ListActiveWorkspaceOwnerNamesParams{
		WorkspaceID: workspaceUUID,
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// GetWorkspaceVisibility returns "public" or "private" for a workspace.
// Returns ErrUnknownWorkspace when the workspace doesn't exist or was
// soft-deleted.
func (s *Store) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return "", err
	}
	queries := sqlc.New(s.db)
	vis, err := queries.GetWorkspaceVisibilityByID(ctx, workspaceUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrUnknownWorkspace
		}
		return "", err
	}
	return vis, nil
}

// ListUserWorkspaces returns the active workspaces the given user belongs
// to. No existence check on user — unknown userID returns [].
func (s *Store) ListUserWorkspaces(ctx context.Context, userID string, limit int32) ([]UserWorkspaceRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	rows, err := queries.ListUserWorkspaces(ctx, sqlc.ListUserWorkspacesParams{UserID: userUUID, ItemLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]UserWorkspaceRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserWorkspaceRead{
			ID:         row.ID,
			Name:       row.Name,
			Slug:       row.Slug,
			Visibility: row.Visibility,
			Role:       row.Role,
			CreatedAt:  pgTime(row.CreatedAt),
			UpdatedAt:  pgTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// ListAllActiveWorkspaces returns every non-deleted workspace with
// role='owner'. Reserved for platform admins — the /me/workspaces
// handler routes here when auth.IsPlatformAdmin(caller) is true.
func (s *Store) ListAllActiveWorkspaces(ctx context.Context, limit int32) ([]UserWorkspaceRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	queries := sqlc.New(s.db)
	rows, err := queries.ListAllActiveWorkspaces(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]UserWorkspaceRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, UserWorkspaceRead{
			ID:         row.ID,
			Name:       row.Name,
			Slug:       row.Slug,
			Visibility: row.Visibility,
			Role:       row.Role,
			CreatedAt:  pgTime(row.CreatedAt),
			UpdatedAt:  pgTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// 工作区主动申请加入(self-service join request):申请、批准、拒绝都是
// workspace_members 行的 status 状态机过渡,不另建独立表。RBAC 在 handler 层做。

type DiscoverableWorkspaceRead struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Slug              string    `json:"slug"`
	Visibility        string    `json:"visibility"`
	MemberCount       int64     `json:"member_count"`
	HasPendingRequest bool      `json:"has_pending_request"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type PendingJoinRequestRead struct {
	ID            string    `json:"id"`
	WorkspaceID   string    `json:"workspace_id"`
	UserID        string    `json:"user_id"`
	UserEmail     string    `json:"user_email"`
	UserName      string    `json:"user_name"`
	RequestReason string    `json:"request_reason"`
	RequestedAt   time.Time `json:"requested_at"`
}

type RequestJoinWorkspaceInput struct {
	WorkspaceID string
	UserID      string
	Reason      string
	Now         time.Time
}

type RequestJoinWorkspaceResult struct {
	Request PendingJoinRequestRead
	Already bool // true: 用户在此工作区已有 active/pending 行, Request 字段携带 user_id + workspace_id 但不是新提交
}

type ReviewJoinRequestInput struct {
	WorkspaceID string
	RequestID   string
	ReviewerID  string
	Now         time.Time
}

// ListDiscoverableWorkspacesInput drives the paginated discover endpoint.
// Search 为空字符串时跳过模糊匹配。
type ListDiscoverableWorkspacesInput struct {
	UserID string
	Search string
	Limit  int32
	Offset int32
}

type ListDiscoverableWorkspacesResult struct {
	Workspaces []DiscoverableWorkspaceRead
	Total      int64
}

// ListDiscoverableWorkspaces returns public workspaces the given user can
// request to join. Private workspaces are never enumerated — listing them
// would leak tenant existence.
func (s *Store) ListDiscoverableWorkspaces(ctx context.Context, input ListDiscoverableWorkspacesInput) (ListDiscoverableWorkspacesResult, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	offset := input.Offset
	if offset < 0 {
		offset = 0
	}
	search := strings.TrimSpace(input.Search)

	userUUID, err := uuid(input.UserID)
	if err != nil {
		return ListDiscoverableWorkspacesResult{}, err
	}
	q := sqlc.New(s.db)
	rows, err := q.ListDiscoverableWorkspaces(ctx, sqlc.ListDiscoverableWorkspacesParams{
		UserID:     userUUID,
		SearchQ:    search,
		ItemLimit:  limit,
		ItemOffset: offset,
	})
	if err != nil {
		return ListDiscoverableWorkspacesResult{}, err
	}
	total, err := q.CountDiscoverableWorkspaces(ctx, sqlc.CountDiscoverableWorkspacesParams{
		UserID:  userUUID,
		SearchQ: search,
	})
	if err != nil {
		return ListDiscoverableWorkspacesResult{}, err
	}
	out := make([]DiscoverableWorkspaceRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, DiscoverableWorkspaceRead{
			ID:                row.ID,
			Name:              row.Name,
			Slug:              row.Slug,
			Visibility:        row.Visibility,
			MemberCount:       row.MemberCount,
			HasPendingRequest: row.HasPendingRequest,
			CreatedAt:         pgTime(row.CreatedAt),
			UpdatedAt:         pgTime(row.UpdatedAt),
		})
	}
	return ListDiscoverableWorkspacesResult{
		Workspaces: out,
		Total:      total,
	}, nil
}

// ListPendingJoinRequests returns the pending requests for the given
// workspace. Handler is responsible for owner/admin RBAC before calling.
func (s *Store) ListPendingJoinRequests(ctx context.Context, workspaceID string) ([]PendingJoinRequestRead, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.db)
	rows, err := q.ListPendingJoinRequests(ctx, wsUUID)
	if err != nil {
		return nil, err
	}
	out := make([]PendingJoinRequestRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingJoinRequestRead{
			ID:            row.ID,
			WorkspaceID:   row.WorkspaceID,
			UserID:        row.UserID,
			UserEmail:     row.UserEmail,
			UserName:      row.UserName,
			RequestReason: row.RequestReason,
			RequestedAt:   pgTime(row.RequestedAt),
		})
	}
	return out, nil
}

func (s *Store) CountPendingJoinRequests(ctx context.Context, workspaceID string) (int64, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return 0, err
	}
	q := sqlc.New(s.db)
	return q.CountPendingJoinRequests(ctx, wsUUID)
}

// RequestJoinWorkspace 提交申请。Already=true 表示用户在此 workspace 已经
// 有 active 或 pending 行 —— handler 据此返回 409。Rejected 行允许再申请,
// 先清掉旧 rejected 再插新 pending。
// 失败:workspace 不存在或非 public → ErrUnknownWorkspace (404 防枚举)。
func (s *Store) RequestJoinWorkspace(ctx context.Context, input RequestJoinWorkspaceInput) (RequestJoinWorkspaceResult, error) {
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return RequestJoinWorkspaceResult{}, err
	}
	userUUID, err := uuid(input.UserID)
	if err != nil {
		return RequestJoinWorkspaceResult{}, err
	}
	reason := strings.TrimSpace(input.Reason)

	beginner, ok := s.db.(txBeginner)
	if !ok {
		return RequestJoinWorkspaceResult{}, fmt.Errorf("backing pool does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return RequestJoinWorkspaceResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	// 私有 workspace 不暴露存在性 —— 不存在 / 非 public 一律 ErrNoRows。
	wsRow, err := q.GetDiscoverableWorkspaceForJoin(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RequestJoinWorkspaceResult{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return RequestJoinWorkspaceResult{}, err
	}

	// rejected 不算 —— 允许被拒后再申请。
	current, err := q.GetWorkspaceMembershipForUser(ctx, sqlc.GetWorkspaceMembershipForUserParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return RequestJoinWorkspaceResult{}, err
	}
	hasExisting := err == nil

	userRow, err := q.GetUserByID(ctx, userUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RequestJoinWorkspaceResult{}, fmt.Errorf("%w: %s", ErrUnknownUser, input.UserID)
		}
		return RequestJoinWorkspaceResult{}, err
	}

	if hasExisting {
		return RequestJoinWorkspaceResult{
			Request: PendingJoinRequestRead{
				ID:            current.ID,
				WorkspaceID:   wsRow.ID,
				UserID:        input.UserID,
				UserEmail:     userRow.Email,
				UserName:      userRow.Name,
				RequestReason: "",
				RequestedAt:   input.Now,
			},
			Already: true,
		}, nil
	}

	// 清掉旧 rejected 行让 AddWorkspaceMember 走 insert 分支。
	if _, err := q.SoftDeleteRejectedJoinRequest(ctx, sqlc.SoftDeleteRejectedJoinRequestParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Now:         timestamptz(input.Now),
	}); err != nil {
		return RequestJoinWorkspaceResult{}, err
	}

	memberRow, err := q.AddWorkspaceMember(ctx, sqlc.AddWorkspaceMemberParams{
		ID:            mustUUID(newID()),
		WorkspaceID:   wsUUID,
		UserID:        userUUID,
		Role:          "member", // 通过后默认 member 角色;owner 可后续调整
		Status:        memberStatusPending,
		RequestReason: reason,
		Now:           timestamptz(input.Now),
	})
	if err != nil {
		return RequestJoinWorkspaceResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RequestJoinWorkspaceResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceJoinRequested,
		ActorType:   audit.ActorTypeUser,
		ActorID:     input.UserID,
		TargetType:  "workspace_member",
		TargetID:    memberRow.ID,
		WorkspaceID: wsRow.ID,
		Payload: map[string]any{
			"reason": reason,
		},
	})

	return RequestJoinWorkspaceResult{
		Request: PendingJoinRequestRead{
			ID:            memberRow.ID,
			WorkspaceID:   memberRow.WorkspaceID,
			UserID:        memberRow.UserID,
			UserEmail:     userRow.Email,
			UserName:      userRow.Name,
			RequestReason: reason,
			RequestedAt:   pgTime(memberRow.CreatedAt),
		},
		Already: false,
	}, nil
}

// ApproveJoinRequest 同意。WHERE status='pending' 保证双 admin 并发只有一个
// 生效;0 affected rows 时返回 ErrJoinRequestAlreadyHandled,handler 返回 409。
func (s *Store) ApproveJoinRequest(ctx context.Context, input ReviewJoinRequestInput) (WorkspaceMemberRead, error) {
	return s.reviewJoinRequest(ctx, input, true)
}

// RejectJoinRequest 拒绝。row 保留(status=rejected),申请人可再次发起。
func (s *Store) RejectJoinRequest(ctx context.Context, input ReviewJoinRequestInput) (WorkspaceMemberRead, error) {
	return s.reviewJoinRequest(ctx, input, false)
}

// WithdrawOwnJoinRequest 申请人自助撤回自己的 pending 申请。
// 双锁 (workspace_id, user_id) + status='pending';0 affected rows 表示
// row 已被 admin 处理或不存在 —— 返回 ErrJoinRequestAlreadyHandled。
func (s *Store) WithdrawOwnJoinRequest(ctx context.Context, workspaceID, userID string, now time.Time) error {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return err
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return err
	}
	q := sqlc.New(s.db)
	affected, err := q.WithdrawOwnPendingJoinRequest(ctx, sqlc.WithdrawOwnPendingJoinRequestParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Now:         timestamptz(now),
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrJoinRequestAlreadyHandled
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceJoinWithdrawn,
		ActorType:   audit.ActorTypeUser,
		ActorID:     userID,
		TargetType:  "workspace_member",
		TargetID:    "", // sqlc UPDATE 不返回 id; 申请人 + workspace 一对即可定位审计
		WorkspaceID: workspaceID,
		Payload: map[string]any{
			"user_id": userID,
		},
	})
	return nil
}

func (s *Store) reviewJoinRequest(ctx context.Context, input ReviewJoinRequestInput, approve bool) (WorkspaceMemberRead, error) {
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	reqUUID, err := uuid(input.RequestID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	reviewerUUID, err := uuid(input.ReviewerID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}

	beginner, ok := s.db.(txBeginner)
	if !ok {
		return WorkspaceMemberRead{}, fmt.Errorf("backing pool does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	var (
		memberID, memberWsID, memberUserID, role string
	)
	if approve {
		row, err := q.ApproveJoinRequest(ctx, sqlc.ApproveJoinRequestParams{
			ID:          reqUUID,
			WorkspaceID: wsUUID,
			ReviewedBy:  reviewerUUID,
			Now:         timestamptz(input.Now),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return WorkspaceMemberRead{}, ErrJoinRequestAlreadyHandled
			}
			return WorkspaceMemberRead{}, err
		}
		memberID, memberWsID, memberUserID, role = row.ID, row.WorkspaceID, row.UserID, row.Role
	} else {
		row, err := q.RejectJoinRequest(ctx, sqlc.RejectJoinRequestParams{
			ID:          reqUUID,
			WorkspaceID: wsUUID,
			ReviewedBy:  reviewerUUID,
			Now:         timestamptz(input.Now),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return WorkspaceMemberRead{}, ErrJoinRequestAlreadyHandled
			}
			return WorkspaceMemberRead{}, err
		}
		memberID, memberWsID, memberUserID, role = row.ID, row.WorkspaceID, row.UserID, row.Role
	}

	userUUID, err := uuid(memberUserID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	userRow, err := q.GetUserByID(ctx, userUUID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WorkspaceMemberRead{}, err
	}

	evt := auditWorkspaceJoinApproved
	if !approve {
		evt = auditWorkspaceJoinRejected
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   evt,
		ActorType:   audit.ActorTypeUser,
		ActorID:     input.ReviewerID,
		TargetType:  "workspace_member",
		TargetID:    memberID,
		WorkspaceID: memberWsID,
		Payload: map[string]any{
			"user_id": memberUserID,
		},
	})

	return WorkspaceMemberRead{
		ID:          memberID,
		WorkspaceID: memberWsID,
		UserID:      memberUserID,
		Role:        role,
		UserEmail:   userRow.Email,
		UserName:    userRow.Name,
		UserStatus:  userRow.Status,
		CreatedAt:   input.Now, // approved/rejected 时间也是更新的 updated_at,直接给现在足够
		UpdatedAt:   input.Now,
	}, nil
}

// GetUserByID returns the Parsar user profile plus the latest avatar_url
// exposed by an auth provider identity, when present. The users table keeps
// only core account fields; provider profile extras remain metadata.
func (s *Store) GetUserByID(ctx context.Context, userID string) (UserRead, error) {
	userUUID, err := uuid(userID)
	if err != nil {
		return UserRead{}, err
	}
	queries := sqlc.New(s.db)
	row, err := queries.GetUserByID(ctx, userUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRead{}, fmt.Errorf("%w: %s", ErrUnknownUser, userID)
		}
		return UserRead{}, err
	}
	avatarURL, err := latestAuthIdentityAvatarURL(ctx, s.db, userUUID)
	if err != nil {
		return UserRead{}, err
	}
	return UserRead{
		ID:        row.ID,
		Email:     row.Email,
		Name:      row.Name,
		Status:    row.Status,
		AvatarURL: avatarURL,
		CreatedAt: pgTime(row.CreatedAt),
		UpdatedAt: pgTime(row.UpdatedAt),
	}, nil
}

func latestAuthIdentityAvatarURL(ctx context.Context, db sqlc.DBTX, userID pgtype.UUID) (string, error) {
	const query = `
select coalesce(metadata->>'avatar_url', '')
from auth_identities
where user_id = $1::uuid
order by updated_at desc
limit 1`
	var avatarURL string
	err := db.QueryRow(ctx, query, userID).Scan(&avatarURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return avatarURL, nil
}

// SearchUsersInput drives the platform-wide user picker. ExcludeWorkspaceID
// hides users already active in that workspace. Soft-deleted and non-active
// users are always filtered out.
type SearchUsersInput struct {
	Query              string
	ExcludeWorkspaceID string
	Limit              int32
}

type SearchUsersResultItem struct {
	ID        string
	Email     string
	Name      string
	AvatarURL string
	Status    string
}

func (s *Store) SearchUsers(ctx context.Context, in SearchUsersInput) ([]SearchUsersResultItem, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	// Malformed UUID treated as "no filter" rather than erroring — the picker is read-only.
	var excludeWS pgtype.UUID
	if id, err := uuid(in.ExcludeWorkspaceID); err == nil {
		excludeWS = id
	}

	// $1 = like-pattern, $2 = exact match for ORDER BY tie-breakers,
	// $3 = exclude_workspace_id, $4 = limit.
	const query = `
SELECT u.id::text,
       u.email,
       u.name,
       u.status,
       COALESCE(ai.metadata->>'avatar_url', '') AS avatar_url
FROM users u
LEFT JOIN LATERAL (
  SELECT metadata
  FROM auth_identities
  WHERE user_id = u.id
  ORDER BY updated_at DESC
  LIMIT 1
) ai ON true
WHERE u.deleted_at IS NULL
  AND u.status = 'active'
  AND (u.email ILIKE $1 OR u.name ILIKE $1)
  AND ($3::uuid IS NULL OR NOT EXISTS (
    SELECT 1 FROM workspace_members wm
    WHERE wm.workspace_id = $3::uuid
      AND wm.user_id = u.id
      AND wm.deleted_at IS NULL))
ORDER BY (u.email = $2) DESC,
         (u.name = $2) DESC,
         u.name ASC,
         u.email ASC
LIMIT $4`

	likePattern := "%" + escapeLikePattern(q) + "%"

	rows, err := s.db.Query(ctx, query, likePattern, q, excludeWS, limit)
	if err != nil {
		return nil, fmt.Errorf("search users: %w", err)
	}
	defer rows.Close()

	out := make([]SearchUsersResultItem, 0, limit)
	for rows.Next() {
		var item SearchUsersResultItem
		if err := rows.Scan(&item.ID, &item.Email, &item.Name, &item.Status, &item.AvatarURL); err != nil {
			return nil, fmt.Errorf("search users scan: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search users iterate: %w", err)
	}
	return out, nil
}

// escapeLikePattern escapes %, _, and \ so a user-typed substring stays literal.
func escapeLikePattern(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// ListWorkspaceProjects returns active projects the caller can open. Any
// active workspace member sees every project in that workspace. The join
// on workspace_members doubles as an active-membership gate, so a
// non-member or soft-deleted user gets the empty list. Returns
// ErrUnknownWorkspace on unknown workspace.
func (s *Store) ListWorkspaceProjects(ctx context.Context, workspaceID, userID string, limit int32) ([]WorkspaceProjectRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	exists, err := queries.ActiveWorkspaceExists(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
	}
	rows, err := queries.ListWorkspaceProjects(ctx, sqlc.ListWorkspaceProjectsParams{
		UserID:      userUUID,
		WorkspaceID: workspaceUUID,
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceProjectRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, WorkspaceProjectRead{
			ID:          row.ID,
			WorkspaceID: row.WorkspaceID,
			Name:        row.Name,
			Slug:        row.Slug,
			Description: row.Description,
			Status:      row.Status,
			CreatedAt:   pgTime(row.CreatedAt),
			UpdatedAt:   pgTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// ListWorkspaceProjectsForAdmin is the platform-admin twin of
// ListWorkspaceProjects: no workspace_members gate, the caller is
// trusted by handler-level auth.IsPlatformAdmin.
func (s *Store) ListWorkspaceProjectsForAdmin(ctx context.Context, workspaceID string, limit int32) ([]WorkspaceProjectRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	queries := sqlc.New(s.db)
	exists, err := queries.ActiveWorkspaceExists(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
	}
	rows, err := queries.ListWorkspaceProjectsAdmin(ctx, sqlc.ListWorkspaceProjectsAdminParams{
		WorkspaceID: workspaceUUID,
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceProjectRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, WorkspaceProjectRead{
			ID:          row.ID,
			WorkspaceID: row.WorkspaceID,
			Name:        row.Name,
			Slug:        row.Slug,
			Description: row.Description,
			Status:      row.Status,
			CreatedAt:   pgTime(row.CreatedAt),
			UpdatedAt:   pgTime(row.UpdatedAt),
		})
	}
	return out, nil
}

// Workspace + Project CRUD. All six writes emit audit events. Slug is
// system-generated (`workspace-<12hex>` / `project-<12hex>`) and permanent —
// it doubles as a stable external identifier. Empty Name → ErrInvalid*Input.

type CreateWorkspaceInput struct {
	Name       string
	Visibility string // "public" | "private"; 空字符串 → "private"
	CreatedBy  string
	Now        time.Time
}

type CreateWorkspaceResult struct {
	Workspace UserWorkspaceRead
	Member    WorkspaceMemberRead
}

type UpdateWorkspaceInput struct {
	WorkspaceID string
	Name        *string // nil = leave unchanged
	Visibility  *string // nil = leave unchanged
	ActorID     string
	Now         time.Time
}

type ArchiveWorkspaceInput struct {
	WorkspaceID string
	ActorID     string
	Now         time.Time
}

type CreateProjectInput struct {
	WorkspaceID string
	Name        string
	Description string
	CreatedBy   string
	Now         time.Time
}

type CreateProjectResult struct {
	Project WorkspaceProjectRead
}

type UpdateProjectInput struct {
	ProjectID   string
	Name        *string // nil = leave unchanged
	Description *string // nil = leave unchanged
	ActorID     string
	Now         time.Time
}

type ArchiveProjectInput struct {
	ProjectID string
	ActorID   string
	Now       time.Time
}

// generateAutoSlug returns `<prefix>-<12 hex chars>`. 48 bits of entropy —
// caller still retries on collision around the DB insert.
func generateAutoSlug(prefix string) string {
	return prefix + "-" + generateSlugSuffix(6)
}

func generateSlugSuffix(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// autoSlugMaxAttempts caps slug re-rolls on collision. Five gives ≈1-in-10^45
// odds of end-to-end failure; in practice the first attempt almost always wins.
const autoSlugMaxAttempts = 5

func (s *Store) CreateWorkspace(ctx context.Context, input CreateWorkspaceInput) (CreateWorkspaceResult, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreateWorkspaceResult{}, fmt.Errorf("%w: name is required", ErrInvalidWorkspaceInput)
	}
	createdBy, err := uuid(input.CreatedBy)
	if err != nil {
		return CreateWorkspaceResult{}, fmt.Errorf("%w: invalid created_by: %v", ErrInvalidWorkspaceInput, err)
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return CreateWorkspaceResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	var slug string
	for attempt := 0; attempt < autoSlugMaxAttempts; attempt++ {
		candidate := generateAutoSlug("workspace")
		exists, err := q.WorkspaceSlugExists(ctx, candidate)
		if err != nil {
			return CreateWorkspaceResult{}, err
		}
		if !exists {
			slug = candidate
			break
		}
	}
	if slug == "" {
		return CreateWorkspaceResult{}, fmt.Errorf("%w: could not generate unique slug after %d attempts", ErrDuplicateWorkspaceSlug, autoSlugMaxAttempts)
	}

	wsID := newID()
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" {
		visibility = workspaceVisibilityPrivate
	}
	if visibility != workspaceVisibilityPublic && visibility != workspaceVisibilityPrivate {
		return CreateWorkspaceResult{}, fmt.Errorf("%w: invalid visibility %q", ErrInvalidWorkspaceInput, visibility)
	}
	wsRow, err := q.CreateWorkspace(ctx, sqlc.CreateWorkspaceParams{
		ID:         mustUUID(wsID),
		Name:       name,
		Slug:       slug,
		Visibility: visibility,
		CreatedBy:  createdBy,
		Now:        timestamptz(input.Now),
	})
	if err != nil {
		return CreateWorkspaceResult{}, err
	}

	memberRow, err := q.AddWorkspaceMember(ctx, sqlc.AddWorkspaceMemberParams{
		ID:            mustUUID(newID()),
		WorkspaceID:   mustUUID(wsRow.ID),
		UserID:        createdBy,
		Role:          memberRoleOwner,
		Status:        memberStatusActive, // owner 永远直接 active,不走审批
		RequestReason: "",
		Now:           timestamptz(input.Now),
	})
	if err != nil {
		return CreateWorkspaceResult{}, err
	}

	creator, err := q.GetUserByID(ctx, createdBy)
	if err != nil {
		return CreateWorkspaceResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateWorkspaceResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceCreated,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.CreatedBy,
		TargetType:  "workspace",
		TargetID:    wsRow.ID,
		WorkspaceID: wsRow.ID,
		Payload: map[string]any{
			"source": auditSourceDevWorkspaceWrite,
			"name":   wsRow.Name,
			"slug":   wsRow.Slug,
		},
	})

	return CreateWorkspaceResult{
		Workspace: UserWorkspaceRead{
			ID:         wsRow.ID,
			Name:       wsRow.Name,
			Slug:       wsRow.Slug,
			Visibility: wsRow.Visibility,
			Role:       memberRoleOwner,
			CreatedAt:  pgTime(wsRow.CreatedAt),
			UpdatedAt:  pgTime(wsRow.UpdatedAt),
		},
		Member: WorkspaceMemberRead{
			ID:          memberRow.ID,
			WorkspaceID: memberRow.WorkspaceID,
			UserID:      memberRow.UserID,
			Role:        memberRow.Role,
			UserEmail:   creator.Email,
			UserName:    creator.Name,
			UserStatus:  creator.Status,
			CreatedAt:   pgTime(memberRow.CreatedAt),
			UpdatedAt:   pgTime(memberRow.UpdatedAt),
		},
	}, nil
}

func (s *Store) UpdateWorkspace(ctx context.Context, input UpdateWorkspaceInput) (UserWorkspaceRead, error) {
	if input.Name == nil && input.Visibility == nil {
		return UserWorkspaceRead{}, fmt.Errorf("%w: at least one of name / visibility must be supplied", ErrInvalidWorkspaceInput)
	}
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return UserWorkspaceRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return UserWorkspaceRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	currentRow, err := q.GetActiveWorkspaceByID(ctx, wsUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserWorkspaceRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return UserWorkspaceRead{}, err
	}

	newName := currentRow.Name
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" {
			return UserWorkspaceRead{}, fmt.Errorf("%w: name must not be empty", ErrInvalidWorkspaceInput)
		}
		newName = trimmed
	}

	newVisibility := currentRow.Visibility
	if input.Visibility != nil {
		v := strings.TrimSpace(*input.Visibility)
		if !IsValidWorkspaceVisibility(v) {
			return UserWorkspaceRead{}, fmt.Errorf("%w: invalid visibility %q", ErrInvalidWorkspaceInput, v)
		}
		newVisibility = v
	}

	row, err := q.UpdateWorkspace(ctx, sqlc.UpdateWorkspaceParams{
		ID:         wsUUID,
		Name:       newName,
		Slug:       currentRow.Slug,
		Visibility: newVisibility,
		Now:        timestamptz(input.Now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserWorkspaceRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return UserWorkspaceRead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return UserWorkspaceRead{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceUpdated,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.ActorID,
		TargetType:  "workspace",
		TargetID:    row.ID,
		WorkspaceID: row.ID,
		Payload: map[string]any{
			"source":   auditSourceDevWorkspaceWrite,
			"old_name": currentRow.Name,
			"new_name": row.Name,
		},
	})

	// Role unknown without an explicit caller — leave blank; the UI
	// re-reads `/api/v1/me/workspaces` to pick up the role anyway.
	return UserWorkspaceRead{
		ID:         row.ID,
		Name:       row.Name,
		Slug:       row.Slug,
		Visibility: row.Visibility,
		CreatedAt:  pgTime(row.CreatedAt),
		UpdatedAt:  pgTime(row.UpdatedAt),
	}, nil
}

func (s *Store) ArchiveWorkspace(ctx context.Context, input ArchiveWorkspaceInput) (UserWorkspaceRead, error) {
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return UserWorkspaceRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return UserWorkspaceRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)
	dependents, err := q.HasMarketplaceDependentsForWorkspace(ctx, wsUUID)
	if err != nil {
		return UserWorkspaceRead{}, err
	}
	if dependents {
		return UserWorkspaceRead{}, fmt.Errorf("%w: %s", ErrMarketplaceDependents, input.WorkspaceID)
	}

	row, err := q.ArchiveWorkspace(ctx, sqlc.ArchiveWorkspaceParams{
		ID:  wsUUID,
		Now: timestamptz(input.Now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserWorkspaceRead{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return UserWorkspaceRead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return UserWorkspaceRead{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceArchived,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.ActorID,
		TargetType:  "workspace",
		TargetID:    row.ID,
		WorkspaceID: row.ID,
		Payload: map[string]any{
			"source": auditSourceDevWorkspaceWrite,
			"name":   row.Name,
			"slug":   row.Slug,
		},
	})

	return UserWorkspaceRead{
		ID:        row.ID,
		Name:      row.Name,
		Slug:      row.Slug,
		CreatedAt: pgTime(row.CreatedAt),
		UpdatedAt: pgTime(row.UpdatedAt),
	}, nil
}

func (s *Store) CreateProject(ctx context.Context, input CreateProjectInput) (CreateProjectResult, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreateProjectResult{}, fmt.Errorf("%w: name is required", ErrInvalidProjectInput)
	}
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return CreateProjectResult{}, err
	}
	createdBy, err := uuid(input.CreatedBy)
	if err != nil {
		return CreateProjectResult{}, fmt.Errorf("%w: invalid created_by: %v", ErrInvalidProjectInput, err)
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return CreateProjectResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	wsExists, err := q.ActiveWorkspaceExists(ctx, wsUUID)
	if err != nil {
		return CreateProjectResult{}, err
	}
	if !wsExists {
		return CreateProjectResult{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
	}

	// Project slug uniqueness is workspace-scoped (partial unique index).
	var slug string
	for attempt := 0; attempt < autoSlugMaxAttempts; attempt++ {
		candidate := generateAutoSlug("project")
		exists, err := q.ProjectSlugExistsInWorkspace(ctx, sqlc.ProjectSlugExistsInWorkspaceParams{
			WorkspaceID: wsUUID,
			Slug:        candidate,
		})
		if err != nil {
			return CreateProjectResult{}, err
		}
		if !exists {
			slug = candidate
			break
		}
	}
	if slug == "" {
		return CreateProjectResult{}, fmt.Errorf("%w: could not generate unique slug after %d attempts", ErrDuplicateProjectSlug, autoSlugMaxAttempts)
	}

	projID := newID()
	projRow, err := q.CreateProject(ctx, sqlc.CreateProjectParams{
		ID:          mustUUID(projID),
		WorkspaceID: wsUUID,
		Name:        name,
		Slug:        slug,
		Description: input.Description,
		CreatedBy:   createdBy,
		Now:         timestamptz(input.Now),
	})
	if err != nil {
		return CreateProjectResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return CreateProjectResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditProjectCreated,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.CreatedBy,
		TargetType:  "project",
		TargetID:    projRow.ID,
		WorkspaceID: projRow.WorkspaceID,
		ProjectID:   projRow.ID,
		Payload: map[string]any{
			"source":      auditSourceDevProjectWrite,
			"name":        projRow.Name,
			"slug":        projRow.Slug,
			"description": projRow.Description,
		},
	})

	return CreateProjectResult{
		Project: WorkspaceProjectRead{
			ID:          projRow.ID,
			WorkspaceID: projRow.WorkspaceID,
			Name:        projRow.Name,
			Slug:        projRow.Slug,
			Description: projRow.Description,
			Status:      projRow.Status,
			CreatedAt:   pgTime(projRow.CreatedAt),
			UpdatedAt:   pgTime(projRow.UpdatedAt),
		},
	}, nil
}

func (s *Store) UpdateProject(ctx context.Context, input UpdateProjectInput) (WorkspaceProjectRead, error) {
	if input.Name == nil && input.Description == nil {
		return WorkspaceProjectRead{}, fmt.Errorf("%w: at least one of name / description must be set", ErrInvalidProjectInput)
	}
	projUUID, err := uuid(input.ProjectID)
	if err != nil {
		return WorkspaceProjectRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return WorkspaceProjectRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	current, err := q.GetActiveProjectByID(ctx, projUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceProjectRead{}, fmt.Errorf("%w: %s", ErrUnknownProject, input.ProjectID)
		}
		return WorkspaceProjectRead{}, err
	}

	newName := current.Name
	if input.Name != nil {
		trimmed := strings.TrimSpace(*input.Name)
		if trimmed == "" {
			return WorkspaceProjectRead{}, fmt.Errorf("%w: name must not be empty", ErrInvalidProjectInput)
		}
		newName = trimmed
	}
	newDesc := current.Description
	if input.Description != nil {
		newDesc = *input.Description
	}

	row, err := q.UpdateProject(ctx, sqlc.UpdateProjectParams{
		ID:          projUUID,
		Name:        newName,
		Slug:        current.Slug,
		Description: newDesc,
		Now:         timestamptz(input.Now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceProjectRead{}, fmt.Errorf("%w: %s", ErrUnknownProject, input.ProjectID)
		}
		return WorkspaceProjectRead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WorkspaceProjectRead{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditProjectUpdated,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.ActorID,
		TargetType:  "project",
		TargetID:    row.ID,
		WorkspaceID: row.WorkspaceID,
		ProjectID:   row.ID,
		Payload: map[string]any{
			"source":          auditSourceDevProjectWrite,
			"old_name":        current.Name,
			"new_name":        row.Name,
			"old_description": current.Description,
			"new_description": row.Description,
		},
	})

	return WorkspaceProjectRead{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		Name:        row.Name,
		Slug:        row.Slug,
		Description: row.Description,
		Status:      row.Status,
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}, nil
}

func (s *Store) ArchiveProject(ctx context.Context, input ArchiveProjectInput) (WorkspaceProjectRead, error) {
	projUUID, err := uuid(input.ProjectID)
	if err != nil {
		return WorkspaceProjectRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return WorkspaceProjectRead{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	row, err := q.ArchiveProject(ctx, sqlc.ArchiveProjectParams{
		ID:  projUUID,
		Now: timestamptz(input.Now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceProjectRead{}, fmt.Errorf("%w: %s", ErrUnknownProject, input.ProjectID)
		}
		return WorkspaceProjectRead{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return WorkspaceProjectRead{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditProjectArchived,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     input.ActorID,
		TargetType:  "project",
		TargetID:    row.ID,
		WorkspaceID: row.WorkspaceID,
		ProjectID:   row.ID,
		Payload: map[string]any{
			"source": auditSourceDevProjectWrite,
			"name":   row.Name,
			"slug":   row.Slug,
		},
	})

	return WorkspaceProjectRead{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		Name:        row.Name,
		Slug:        row.Slug,
		Description: row.Description,
		Status:      row.Status,
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}, nil
}

// AddWorkspaceMember atomically upserts the user by email and inserts /
// revives the (workspace_id, user_id) membership at the requested role.
func (s *Store) AddWorkspaceMember(ctx context.Context, input AddWorkspaceMemberInput) (AddWorkspaceMemberResult, error) {
	if !IsValidMemberRole(input.Role) {
		return AddWorkspaceMemberResult{}, fmt.Errorf("%w: %s", ErrInvalidMemberRole, input.Role)
	}
	email := strings.TrimSpace(input.Email)
	name := strings.TrimSpace(input.Name)
	if email == "" {
		return AddWorkspaceMemberResult{}, fmt.Errorf("email is required")
	}
	if name == "" {
		if at := strings.Index(email, "@"); at > 0 {
			name = email[:at]
		} else {
			name = email
		}
	}
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	beginner, ok := s.db.(txBeginner)
	if !ok {
		return AddWorkspaceMemberResult{}, fmt.Errorf("backing pool does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New(tx)

	userRow, err := q.UpsertUserByEmail(ctx, sqlc.UpsertUserByEmailParams{
		ID:    mustUUID(newID()),
		Email: email,
		Name:  name,
		Now:   timestamptz(input.Now),
	})
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	memberRow, err := q.AddWorkspaceMember(ctx, sqlc.AddWorkspaceMemberParams{
		ID:            mustUUID(newID()),
		WorkspaceID:   wsUUID,
		UserID:        mustUUID(userRow.ID),
		Role:          input.Role,
		Status:        memberStatusActive, // owner/admin 显式添加成员永远 active
		RequestReason: "",
		Now:           timestamptz(input.Now),
	})
	if err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return AddWorkspaceMemberResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  input.Now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceMemberAdded,
		ActorType:   audit.ActorTypeSystem,
		TargetType:  "workspace_member",
		TargetID:    memberRow.ID,
		WorkspaceID: input.WorkspaceID,
		Payload: map[string]any{
			"source":       auditSourceDevMemberWrite,
			"user_id":      userRow.ID,
			"user_email":   userRow.Email,
			"role":         input.Role,
			"user_created": userRow.Created,
		},
	})

	return AddWorkspaceMemberResult{
		Member: WorkspaceMemberRead{
			ID:          memberRow.ID,
			WorkspaceID: memberRow.WorkspaceID,
			UserID:      memberRow.UserID,
			Role:        memberRow.Role,
			UserEmail:   userRow.Email,
			UserName:    userRow.Name,
			UserStatus:  userRow.Status,
			CreatedAt:   pgTime(memberRow.CreatedAt),
			UpdatedAt:   pgTime(memberRow.UpdatedAt),
		},
		UserCreated: userRow.Created,
	}, nil
}

// UpdateWorkspaceMemberRole flips an existing member's role. Returns
// ErrUnknownWorkspaceMember when no active row exists.
func (s *Store) UpdateWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string, role string, now time.Time) (WorkspaceMemberRead, error) {
	if !IsValidMemberRole(role) {
		return WorkspaceMemberRead{}, fmt.Errorf("%w: %s", ErrInvalidMemberRole, role)
	}
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	q := sqlc.New(s.db)
	row, err := q.UpdateWorkspaceMemberRole(ctx, sqlc.UpdateWorkspaceMemberRoleParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Role:        role,
		Now:         timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceMemberRead{}, fmt.Errorf("%w: %s/%s", ErrUnknownWorkspaceMember, workspaceID, userID)
		}
		return WorkspaceMemberRead{}, err
	}
	user, err := q.GetUserByID(ctx, userUUID)
	if err != nil {
		return WorkspaceMemberRead{}, err
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceMemberRoleUpdated,
		ActorType:   audit.ActorTypeSystem,
		TargetType:  "workspace_member",
		TargetID:    row.ID,
		WorkspaceID: workspaceID,
		Payload: map[string]any{
			"source":     auditSourceDevMemberWrite,
			"user_id":    userID,
			"user_email": user.Email,
			"new_role":   role,
		},
	})
	return WorkspaceMemberRead{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		UserID:      row.UserID,
		Role:        row.Role,
		UserEmail:   user.Email,
		UserName:    user.Name,
		UserStatus:  user.Status,
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}, nil
}

// RemoveWorkspaceMember soft-deletes the workspace membership.
func (s *Store) RemoveWorkspaceMember(ctx context.Context, workspaceID string, userID string, now time.Time) (RemoveWorkspaceMemberResult, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return RemoveWorkspaceMemberResult{}, err
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return RemoveWorkspaceMemberResult{}, err
	}

	beginner, ok := s.db.(txBeginner)
	if !ok {
		return RemoveWorkspaceMemberResult{}, fmt.Errorf("backing pool does not support transactions")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return RemoveWorkspaceMemberResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := sqlc.New(tx)

	memberRow, err := q.SoftDeleteWorkspaceMember(ctx, sqlc.SoftDeleteWorkspaceMemberParams{
		WorkspaceID: wsUUID,
		UserID:      userUUID,
		Now:         timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RemoveWorkspaceMemberResult{}, fmt.Errorf("%w: %s/%s", ErrUnknownWorkspaceMember, workspaceID, userID)
		}
		return RemoveWorkspaceMemberResult{}, err
	}

	user, err := q.GetUserByID(ctx, userUUID)
	if err != nil {
		return RemoveWorkspaceMemberResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RemoveWorkspaceMemberResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   auditWorkspaceMemberRemoved,
		ActorType:   audit.ActorTypeSystem,
		TargetType:  "workspace_member",
		TargetID:    memberRow.ID,
		WorkspaceID: workspaceID,
		Payload: map[string]any{
			"source":       "dev_member_write",
			"user_id":      userID,
			"user_email":   user.Email,
			"removed_role": memberRow.Role,
		},
	})

	return RemoveWorkspaceMemberResult{
		Member: WorkspaceMemberRead{
			ID:          memberRow.ID,
			WorkspaceID: memberRow.WorkspaceID,
			UserID:      memberRow.UserID,
			Role:        memberRow.Role,
			UserEmail:   user.Email,
			UserName:    user.Name,
			UserStatus:  user.Status,
			CreatedAt:   pgTime(memberRow.CreatedAt),
			UpdatedAt:   pgTime(memberRow.UpdatedAt),
		},
	}, nil
}

// ListAuditRecords reads the audit_records table. All filters are optional
// (empty / zero = skip). Newest-first by (occurred_at, id). When ProjectID
// is set, unknown IDs return ErrUnknownProject instead of an empty list.
func (s *Store) ListAuditRecords(ctx context.Context, f ListAuditRecordsFilter, limit int32) ([]AuditRecordRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	queries := sqlc.New(s.db)

	params := sqlc.ListAuditRecordsParams{
		WorkspaceID: nullableUUID(f.WorkspaceID),
		ProjectID:   nullableUUID(f.ProjectID),
		Source:      strings.TrimSpace(f.Source),
		EventType:   strings.TrimSpace(f.EventType),
		ActorID:     nullableUUID(f.ActorID),
		TargetType:  strings.TrimSpace(f.TargetType),
		TargetID:    nullableUUID(f.TargetID),
		ItemLimit:   limit,
	}
	if !f.Since.IsZero() {
		params.Since = pgtype.Timestamptz{Time: f.Since, Valid: true}
	}
	if !f.Until.IsZero() {
		params.Until = pgtype.Timestamptz{Time: f.Until, Valid: true}
	}

	if f.ProjectID != "" {
		projectUUID, err := uuid(f.ProjectID)
		if err != nil {
			return nil, err
		}
		exists, err := queries.ActiveProjectExists(ctx, projectUUID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrUnknownProject, f.ProjectID)
		}
	}

	rows, err := queries.ListAuditRecords(ctx, params)
	if err != nil {
		return nil, err
	}

	out := make([]AuditRecordRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, auditRecordFromRow(row))
	}
	return out, nil
}

func auditRecordFromRow(row sqlc.ListAuditRecordsRow) AuditRecordRead {
	return AuditRecordRead{
		ID:          row.ID,
		OccurredAt:  pgTime(row.OccurredAt),
		Source:      row.Source,
		EventType:   row.EventType,
		ActorType:   row.ActorType,
		ActorID:     pgUUIDString(row.ActorID),
		TargetType:  row.TargetType,
		TargetID:    pgUUIDString(row.TargetID),
		WorkspaceID: pgUUIDString(row.WorkspaceID),
		ProjectID:   pgUUIDString(row.ProjectID),
		Payload:     decodeJSONMap(row.Payload),
	}
}

// pgUUIDString formats a pgtype.UUID as the canonical
// 8-4-4-4-12 hex form. Returns "" when the UUID is SQL NULL.
func pgUUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return guuid.UUID(id.Bytes).String()
}

func (s *Store) ListProjectUsageLogs(ctx context.Context, projectID string, agentRunID string, limit int32) ([]UsageLogRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	queries := sqlc.New(s.db)
	projectUUID, err := uuid(projectID)
	if err != nil {
		return nil, err
	}

	exists, err := queries.ActiveProjectExists(ctx, projectUUID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownProject, projectID)
	}

	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		rows, err := queries.ListProjectUsageLogs(ctx, sqlc.ListProjectUsageLogsParams{ProjectID: projectUUID, ItemLimit: limit})
		if err != nil {
			return nil, err
		}
		usage := make([]UsageLogRead, 0, len(rows))
		for _, row := range rows {
			usage = append(usage, usageLogFromProjectRow(row))
		}
		return usage, nil
	}

	runUUID, err := uuid(agentRunID)
	if err != nil {
		return nil, err
	}
	rows, err := queries.ListProjectUsageLogsByRun(ctx, sqlc.ListProjectUsageLogsByRunParams{ProjectID: projectUUID, AgentRunID: runUUID, ItemLimit: limit})
	if err != nil {
		return nil, err
	}
	usage := make([]UsageLogRead, 0, len(rows))
	for _, row := range rows {
		usage = append(usage, usageLogFromProjectRunRow(row))
	}
	return usage, nil
}

func (s *Store) CreateSecret(ctx context.Context, input CreateSecretInput, encryptedPayload []byte) (SecretRead, error) {
	now := time.Now().UTC()
	// Secrets are org-global; WorkspaceID accepted for caller-compat only.
	_ = input.WorkspaceID
	createdBy := nullableUUID(input.CreatedBy)
	metaPayload := map[string]any{"masked": strings.TrimSpace(input.Masked)}
	if code := strings.TrimSpace(input.CredentialKindCode); code != "" {
		metaPayload["credential_kind_code"] = code
	}
	metadata, err := json.Marshal(metaPayload)
	if err != nil {
		return SecretRead{}, err
	}
	row, err := sqlc.New(s.db).CreateSecret(ctx, sqlc.CreateSecretParams{
		ID:               mustUUID(newID()),
		Slug:             generateAutoSlug("secret"),
		Name:             strings.TrimSpace(input.Name),
		Kind:             secretKind(input.Kind),
		Provider:         strings.TrimSpace(input.Provider),
		AuthType:         strings.TrimSpace(input.AuthType),
		EncryptedPayload: encryptedPayload,
		KeyVersion:       "v1",
		Metadata:         metadata,
		CreatedBy:        createdBy,
		Now:              timestamptz(now),
	})
	if err != nil {
		return SecretRead{}, err
	}
	read := secretReadFromCreateRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditSecretCreated,
		ActorType:  audit.ActorTypeSystem,
		ActorID:    input.CreatedBy,
		TargetType: "secret",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source":    auditSourceDevSecretWrite,
			"name":      read.Name,
			"slug":      read.Slug,
			"kind":      read.Kind,
			"provider":  read.Provider,
			"auth_type": read.AuthType,
		},
	})

	return read, nil
}

// ListSecrets returns active secrets in the org-global catalog.
// workspaceID is accepted for caller-compat only and ignored.
func (s *Store) ListSecrets(ctx context.Context, workspaceID string, limit int32) ([]SecretRead, error) {
	return s.ListSecretsByKind(ctx, "", limit)
}

func (s *Store) ListSecretsByKind(ctx context.Context, kindFilter string, limit int32) ([]SecretRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListSecrets(ctx, sqlc.ListSecretsParams{KindFilter: strings.TrimSpace(kindFilter), ItemLimit: limit})
	if err != nil {
		return nil, err
	}
	secrets := make([]SecretRead, 0, len(rows))
	for _, row := range rows {
		secrets = append(secrets, secretReadFromListRow(row))
	}
	return secrets, nil
}

func (s *Store) DisableSecret(ctx context.Context, workspaceID string, secretID string) (SecretRead, error) {
	now := time.Now().UTC()
	_ = workspaceID
	secretUUID, err := uuid(secretID)
	if err != nil {
		return SecretRead{}, err
	}
	row, err := sqlc.New(s.db).DisableSecret(ctx, sqlc.DisableSecretParams{ID: secretUUID, Now: timestamptz(now)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SecretRead{}, fmt.Errorf("%w: %s", ErrUnknownSecret, secretID)
		}
		return SecretRead{}, err
	}
	read := secretReadFromDisableRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditSecretDisabled,
		ActorType:  audit.ActorTypeSystem,
		TargetType: "secret",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source": auditSourceDevSecretWrite,
			"name":   read.Name,
			"slug":   read.Slug,
			"status": read.Status,
		},
	})

	return read, nil
}

func (s *Store) GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (SecretPayload, error) {
	_ = workspaceID
	secretUUID, err := uuid(secretID)
	if err != nil {
		return SecretPayload{}, err
	}
	row, err := sqlc.New(s.db).GetSecretPayload(ctx, secretUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SecretPayload{}, fmt.Errorf("%w: %s", ErrUnknownSecret, secretID)
		}
		return SecretPayload{}, err
	}
	read := secretReadFromSecretRow(row)
	return SecretPayload{SecretRead: read, EncryptedPayload: row.EncryptedPayload}, nil
}

func (s *Store) CreateModel(ctx context.Context, input CreateModelInput) (ModelRead, error) {
	now := time.Now().UTC()
	mode := strings.TrimSpace(input.CredentialMode)
	if mode != "inline_secret" && mode != "credential_ref" {
		return ModelRead{}, fmt.Errorf("create model: invalid credential_mode %q", mode)
	}
	if mode == "credential_ref" && strings.TrimSpace(input.CredentialKindCode) == "" {
		return ModelRead{}, fmt.Errorf("create model: credential_ref mode requires credential_kind_code")
	}
	config, err := json.Marshal(nonNilMap(input.Config))
	if err != nil {
		return ModelRead{}, err
	}

	queries := sqlc.New(s.db)
	var row sqlc.CreateModelRow
	slug := generateAutoSlug("model")
	for attempt := 0; attempt < autoSlugMaxAttempts; attempt++ {
		row, err = queries.CreateModel(ctx, sqlc.CreateModelParams{
			ID:                 mustUUID(newID()),
			Slug:               slug,
			Name:               strings.TrimSpace(input.Name),
			ProviderType:       strings.TrimSpace(input.ProviderType),
			Adapter:            strings.TrimSpace(input.Adapter),
			BaseUrl:            strings.TrimSpace(input.BaseURL),
			ModelKey:           strings.TrimSpace(input.ModelKey),
			CredentialMode:     mode,
			SecretID:           strings.TrimSpace(input.SecretID),
			CredentialKindCode: strings.TrimSpace(input.CredentialKindCode),
			Config:             config,
			CreatedBy:          nullableUUID(input.CreatedBy),
			Now:                timestamptz(now),
		})
		if err == nil {
			break
		}
		if !isUniqueViolation(err) {
			return ModelRead{}, err
		}
		slug = generateAutoSlug("model")
	}
	if err != nil {
		return ModelRead{}, fmt.Errorf("create model: could not generate unique slug after %d attempts", autoSlugMaxAttempts)
	}
	read := modelReadFromCreateRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditModelCreated,
		ActorType:  audit.ActorTypeSystem,
		ActorID:    input.CreatedBy,
		TargetType: "model",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source":          auditSourceDevModelRegistryWrite,
			"slug":            read.Slug,
			"name":            read.Name,
			"provider_type":   read.ProviderType,
			"adapter":         read.Adapter,
			"credential_mode": read.CredentialMode,
			"model_key":       read.ModelKey,
		},
	})

	return read, nil
}

// UpdateModel rewrites the editable fields. credential_mode / provider_type / adapter
// are NOT editable; create a new model to change them.
func (s *Store) UpdateModel(ctx context.Context, input UpdateModelInput) (ModelRead, error) {
	now := time.Now().UTC()
	modelUUID, err := uuid(input.ModelID)
	if err != nil {
		return ModelRead{}, err
	}
	config, err := json.Marshal(nonNilMap(input.Config))
	if err != nil {
		return ModelRead{}, err
	}
	row, err := sqlc.New(s.db).UpdateModel(ctx, sqlc.UpdateModelParams{
		ID:                 modelUUID,
		Name:               strings.TrimSpace(input.Name),
		ModelKey:           strings.TrimSpace(input.ModelKey),
		BaseUrl:            strings.TrimSpace(input.BaseURL),
		SecretID:           strings.TrimSpace(input.SecretID),
		CredentialKindCode: strings.TrimSpace(input.CredentialKindCode),
		Config:             config,
		Now:                timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelRead{}, fmt.Errorf("%w: %s", ErrUnknownModel, input.ModelID)
		}
		return ModelRead{}, err
	}
	read := modelReadFromUpdateRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditModelUpdated,
		ActorType:  audit.ActorTypeSystem,
		TargetType: "model",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source":    auditSourceDevModelRegistryWrite,
			"slug":      read.Slug,
			"name":      read.Name,
			"model_key": read.ModelKey,
		},
	})

	return read, nil
}

func (s *Store) DisableModel(ctx context.Context, workspaceID string, modelID string) (ModelRead, error) {
	now := time.Now().UTC()
	_ = workspaceID
	modelUUID, err := uuid(modelID)
	if err != nil {
		return ModelRead{}, err
	}
	row, err := sqlc.New(s.db).DisableModel(ctx, sqlc.DisableModelParams{ID: modelUUID, Now: timestamptz(now)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelRead{}, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
		}
		return ModelRead{}, err
	}
	read := modelReadFromDisableRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditModelDisabled,
		ActorType:  audit.ActorTypeSystem,
		TargetType: "model",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source":    auditSourceDevModelRegistryWrite,
			"slug":      read.Slug,
			"name":      read.Name,
			"model_key": read.ModelKey,
			"status":    read.Status,
		},
	})

	return read, nil
}

// SoftDeleteModel removes a shared model from the org catalog. In-flight
// agent sessions already holding a resolved ModelRuntime continue to run;
// new requests for this model will fail with ErrUnknownModel.
func (s *Store) SoftDeleteModel(ctx context.Context, modelID, actorID string) error {
	now := time.Now().UTC()
	modelUUID, err := uuid(modelID)
	if err != nil {
		return err
	}
	rows, err := sqlc.New(s.db).SoftDeleteModel(ctx, sqlc.SoftDeleteModelParams{ID: modelUUID, Now: timestamptz(now)})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditModelDeleted,
		ActorType:  audit.ActorTypeSystem,
		ActorID:    actorID,
		TargetType: "model",
		TargetID:   modelID,
		Payload:    map[string]any{"source": auditSourceDevModelRegistryWrite},
	})
	return nil
}

type ProjectAgentStatusRead struct {
	ProjectAgentID string         `json:"project_agent_id"`
	WorkspaceID    string         `json:"workspace_id"`
	ProjectID      string         `json:"project_id"`
	AgentID        string         `json:"agent_id"`
	AgentName      string         `json:"agent_name"`
	AgentSlug      string         `json:"agent_slug"`
	ConnectorType  string         `json:"connector_type"`
	Status         string         `json:"status"`
	Config         map[string]any `json:"config"`
	CreatedBy      string         `json:"created_by,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func (s *Store) DisableProjectAgent(ctx context.Context, projectAgentID string) (ProjectAgentStatusRead, error) {
	return s.setProjectAgentStatus(ctx, projectAgentID, "disabled", auditProjectAgentDisabled)
}

func (s *Store) EnableProjectAgent(ctx context.Context, projectAgentID string) (ProjectAgentStatusRead, error) {
	return s.setProjectAgentStatus(ctx, projectAgentID, "active", auditProjectAgentEnabled)
}

func (s *Store) GetProjectAgentDetail(ctx context.Context, projectAgentID string) (ProjectAgentStatusRead, error) {
	paUUID, err := uuid(projectAgentID)
	if err != nil {
		return ProjectAgentStatusRead{}, err
	}
	row, err := sqlc.New(s.db).GetProjectAgentDetailForRead(ctx, paUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
		}
		return ProjectAgentStatusRead{}, err
	}
	read := ProjectAgentStatusRead{
		ProjectAgentID: row.ID,
		WorkspaceID:    row.WorkspaceID,
		ProjectID:      row.ProjectID,
		AgentID:        row.AgentID,
		AgentName:      row.AgentName,
		AgentSlug:      row.AgentSlug,
		ConnectorType:  row.ConnectorType,
		Status:         row.Status,
		Config:         decodeJSONMap(row.Config),
		CreatedBy:      row.CreatedBy,
		CreatedAt:      pgTime(row.CreatedAt),
		UpdatedAt:      pgTime(row.UpdatedAt),
	}
	return read, nil
}

func (s *Store) setProjectAgentStatus(ctx context.Context, projectAgentID, targetStatus, eventType string) (ProjectAgentStatusRead, error) {
	now := time.Now().UTC()
	paUUID, err := uuid(projectAgentID)
	if err != nil {
		return ProjectAgentStatusRead{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ProjectAgentStatusRead{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	detail, err := queries.GetProjectAgentDetailForRead(ctx, paUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
		}
		return ProjectAgentStatusRead{}, err
	}

	var (
		updatedID, updatedWS, updatedProject, updatedAgent, updatedStatus string
		updatedConfig                                                     []byte
		updatedCreatedAt, updatedUpdatedAt                                pgtype.Timestamptz
	)
	switch targetStatus {
	case "disabled":
		row, err := queries.DisableProjectAgent(ctx, sqlc.DisableProjectAgentParams{ID: paUUID, Now: timestamptz(now)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ProjectAgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
			}
			return ProjectAgentStatusRead{}, err
		}
		updatedID, updatedWS, updatedProject, updatedAgent, updatedStatus = row.ID, row.WorkspaceID, row.ProjectID, row.AgentID, row.Status
		updatedConfig, updatedCreatedAt, updatedUpdatedAt = row.Config, row.CreatedAt, row.UpdatedAt
	case "active":
		row, err := queries.EnableProjectAgent(ctx, sqlc.EnableProjectAgentParams{ID: paUUID, Now: timestamptz(now)})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ProjectAgentStatusRead{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
			}
			return ProjectAgentStatusRead{}, err
		}
		updatedID, updatedWS, updatedProject, updatedAgent, updatedStatus = row.ID, row.WorkspaceID, row.ProjectID, row.AgentID, row.Status
		updatedConfig, updatedCreatedAt, updatedUpdatedAt = row.Config, row.CreatedAt, row.UpdatedAt
	default:
		return ProjectAgentStatusRead{}, fmt.Errorf("invalid project agent target status: %s", targetStatus)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProjectAgentStatusRead{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   eventType,
		ActorType:   audit.ActorTypeSystem,
		ActorID:     updatedAgent,
		TargetType:  "project_agent",
		TargetID:    updatedID,
		WorkspaceID: updatedWS,
		ProjectID:   updatedProject,
		Payload: map[string]any{
			"agent_slug": detail.AgentSlug,
			"agent_name": detail.AgentName,
			"prev":       detail.Status,
			"next":       updatedStatus,
		},
	})

	return ProjectAgentStatusRead{
		ProjectAgentID: updatedID,
		WorkspaceID:    updatedWS,
		ProjectID:      updatedProject,
		AgentID:        updatedAgent,
		AgentName:      detail.AgentName,
		AgentSlug:      detail.AgentSlug,
		ConnectorType:  detail.ConnectorType,
		Status:         updatedStatus,
		Config:         decodeJSONMap(updatedConfig),
		CreatedAt:      pgTime(updatedCreatedAt),
		UpdatedAt:      pgTime(updatedUpdatedAt),
	}, nil
}

// ResolveModelRuntime returns the flattened runtime view for a shared model.
// workspaceID is accepted for caller-compat — models are org-global.
//
// Returns metadata only: provider/model/base_url/credential_mode/credential_kind_code.
// For inline_secret models the joined secret payload is already on the row.
// For credential_ref models the caller decides whether to use a shared
// workspace secret (via GetSecretPayload) or to call ResolveModelRuntimeForUser
// to pick up a per-user credential.
func (s *Store) ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (ModelRuntime, error) {
	_ = workspaceID
	modelUUID, err := uuid(modelID)
	if err != nil {
		return ModelRuntime{}, err
	}
	row, err := sqlc.New(s.db).ResolveModelRuntime(ctx, modelUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelRuntime{}, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
		}
		return ModelRuntime{}, err
	}
	return modelRuntimeFromRow(row), nil
}

// ResolveModelRuntimeForUser resolves a model AND attaches the per-user
// user_credentials row for credential_ref-mode models. Pass a non-empty
// userID — callers that do not have one should use ResolveModelRuntime
// and decide on a shared path.
func (s *Store) ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (ModelRuntime, error) {
	modelUUID, err := uuid(modelID)
	if err != nil {
		return ModelRuntime{}, err
	}
	row, err := sqlc.New(s.db).ResolveModelRuntime(ctx, modelUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelRuntime{}, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
		}
		return ModelRuntime{}, err
	}
	mr := modelRuntimeFromRow(row)
	// inline_secret 模式: row.SecretEncryptedPayload 已经 join 进来,直接返回
	// credential_ref 模式: 按 caller user_id + kind 拿 user_credentials;
	// 错误路径仍返回已 resolve 的 mr (CredentialMode + CredentialKindCode 已填充),
	// 让 connector 能据 kind emit credential-form 引导卡片。
	if mr.CredentialMode == "credential_ref" {
		if strings.TrimSpace(userID) == "" {
			return mr, fmt.Errorf("%w: model %s requires user-scoped credential but no caller user_id provided", ErrModelDisabled, modelID)
		}
		userUUID, err := uuid(userID)
		if err != nil {
			return mr, err
		}
		cred, err := sqlc.New(s.db).GetUserCredentialByUserKind(ctx, sqlc.GetUserCredentialByUserKindParams{
			UserID: userUUID,
			Kind:   strings.TrimSpace(mr.CredentialKindCode),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return mr, fmt.Errorf("%w: user has not configured credential for kind %q (required by model %s)", ErrModelDisabled, mr.CredentialKindCode, modelID)
			}
			return mr, err
		}
		mr.EncryptedPayload = cred.Ciphertext
	}
	return mr, nil
}

// ListModels returns the org-global shared model catalog.
// workspaceID is accepted for caller-compat — it is ignored.
func (s *Store) ListModels(ctx context.Context, workspaceID string, limit int32) ([]ModelRead, error) {
	_ = workspaceID
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListModels(ctx, limit)
	if err != nil {
		return nil, err
	}
	models := make([]ModelRead, 0, len(rows))
	for _, row := range rows {
		models = append(models, modelReadFromListRow(row))
	}
	return models, nil
}

// GetModel returns a single shared model by id.
func (s *Store) GetModel(ctx context.Context, modelID string) (ModelRead, error) {
	modelUUID, err := uuid(modelID)
	if err != nil {
		return ModelRead{}, err
	}
	row, err := sqlc.New(s.db).GetModel(ctx, modelUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ModelRead{}, fmt.Errorf("%w: %s", ErrUnknownModel, modelID)
		}
		return ModelRead{}, err
	}
	return modelReadFromGetRow(row), nil
}

const agentRunExecutionSnapshotKey = "execution_snapshot"

func (s *Store) RecordAgentRunExecutionSnapshot(ctx context.Context, input RecordAgentRunExecutionSnapshotInput) error {
	runID, err := uuid(input.RunID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()

	runtimeID := strings.TrimSpace(input.RuntimeID)
	deviceID := strings.TrimSpace(input.DeviceID)
	if runtimeID == "" {
		runtimeID = deviceID
	}

	runtime := AgentRunRuntimeRead{
		ID:               runtimeID,
		ConnectorType:    strings.TrimSpace(input.ConnectorType),
		AgentKind:        strings.TrimSpace(input.AgentKind),
		RuntimeMode:      strings.TrimSpace(input.RuntimeMode),
		DeviceID:         deviceID,
		SandboxID:        strings.TrimSpace(input.SandboxID),
		ManagedModelID:   strings.TrimSpace(input.ManagedModelID),
		Capabilities:     cloneBoolMap(input.Capabilities),
		WorkingDirectory: strings.TrimSpace(input.WorkingDirectory),
		CapturedAt:       &now,
	}

	runtimeUUID := pgtype.UUID{}
	if runtimeID != "" {
		parsedRuntimeID, err := uuid(runtimeID)
		if err != nil {
			return fmt.Errorf("agent run execution snapshot: invalid runtime id %q: %w", runtimeID, err)
		}
		runtimeUUID = parsedRuntimeID
	}
	if runtimeUUID.Valid {
		var lastHeartbeat pgtype.Timestamptz
		var runtimeConfigRaw []byte
		err := s.db.QueryRow(ctx, `
select id::text, name, type, provider, liveness, hostname, version, last_heartbeat_at, config
from runtimes
where id = $1::uuid
  and deleted_at is null`, runtimeUUID).Scan(
			&runtime.ID,
			&runtime.Name,
			&runtime.Type,
			&runtime.Provider,
			&runtime.Liveness,
			&runtime.Hostname,
			&runtime.Version,
			&lastHeartbeat,
			&runtimeConfigRaw,
		)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("agent run execution snapshot: load runtime: %w", err)
		}
		if err == nil {
			runtime.LastHeartbeatAt = pgOptionalTime(lastHeartbeat)
			runtimeConfig := decodeJSONMap(runtimeConfigRaw)
			if len(runtime.Capabilities) == 0 {
				runtime.Capabilities = boolMapFromValue(runtimeConfig["daemon_capabilities"])
			}
			if runtime.SandboxID == "" {
				runtime.SandboxID = firstStringForKeys([]string{"sandbox_id", "e2b_sandbox_id", "parsar.sandbox_id"}, runtimeConfig)
			}
		}
	}

	if runtime.DeviceID == "" && (runtime.ConnectorType == "agent_daemon" || runtime.Type == RuntimeTypeAgentDaemon) {
		runtime.DeviceID = runtime.ID
	}
	if runtime.RuntimeMode == "" {
		runtime.RuntimeMode = deriveAgentRunRuntimeMode(runtime)
	}
	runtime.ExecutionPlace = deriveAgentRunExecutionPlace(runtime)
	runtime.GovernanceMode = deriveAgentRunGovernanceMode(runtime)

	patch, err := json.Marshal(map[string]any{agentRunExecutionSnapshotKey: runtime})
	if err != nil {
		return fmt.Errorf("agent run execution snapshot: marshal metadata: %w", err)
	}

	commandTag, err := s.db.Exec(ctx, `
update agent_runs
set runtime_id = coalesce($2::uuid, runtime_id),
    working_directory = case when $3::text <> '' then $3::text else working_directory end,
    metadata = metadata || $4::jsonb,
    updated_at = $5
where id = $1::uuid`, runID, runtimeUUID, runtime.WorkingDirectory, patch, timestamptz(now))
	if err != nil {
		return fmt.Errorf("agent run execution snapshot: update run: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrUnknownAgentRun, input.RunID)
	}
	return nil
}

func (s *Store) MarkAgentRunRunning(ctx context.Context, runID string, conversationID string) (MarkAgentRunRunningResult, error) {
	now := time.Now().UTC()
	runUUID, err := uuid(runID)
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	conversationUUID, err := uuid(conversationID)
	if err != nil {
		return MarkAgentRunRunningResult{}, err
	}
	// NOT EXISTS clause enforces at most one running run per (conversation, project_agent).
	// If a sibling is already running, UPDATE matches 0 rows and we return
	// ErrAgentRunBlockedByQueue. Slow-path defender behind HasInflightRunForConversationAgent's
	// fast check; closes the race between two messages arriving before either marks-running.
	row := s.db.QueryRow(ctx, `
update agent_runs
set status = 'running',
    started_at = coalesce(started_at, $3),
    updated_at = $3,
    metadata = metadata || jsonb_build_object('started_by', 'conversation_stream')
where id = $1::uuid
  and conversation_id = $2::uuid
  and status = 'queued'
  and not exists (
    select 1 from agent_runs r2
    where r2.conversation_id = $2::uuid
      and r2.project_agent_id = (select project_agent_id from agent_runs where id = $1::uuid)
      and r2.status = 'running'
      and r2.id != $1::uuid
  )
returning id::text, workspace_id::text, project_id::text, conversation_id::text, status, started_at`, runUUID, conversationUUID, timestamptz(now))
	var result MarkAgentRunRunningResult
	var startedAt pgtype.Timestamptz
	if err := row.Scan(&result.RunID, &result.WorkspaceID, &result.ProjectID, &result.ConversationID, &result.Status, &startedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			exists, existsErr := sqlc.New(s.db).AgentRunExists(ctx, runUUID)
			if existsErr != nil {
				return MarkAgentRunRunningResult{}, existsErr
			}
			if !exists {
				return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
			}
			// Disambiguate: queued in THIS conversation (blocked by sibling)
			// vs past-queued or foreign-conversation (not startable). The
			// conversation_id predicate keeps a wrong-conversation run — queued
			// but not ours to start — off the blocked-by-queue branch.
			var status string
			if err := s.db.QueryRow(ctx,
				`select status from agent_runs where id = $1::uuid and conversation_id = $2::uuid`,
				runUUID, conversationUUID).Scan(&status); err == nil && status == "queued" {
				return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrAgentRunBlockedByQueue, runID)
			}
			return MarkAgentRunRunningResult{}, fmt.Errorf("%w: %s", ErrAgentRunNotStartable, runID)
		}
		return MarkAgentRunRunningResult{}, err
	}
	result.StartedAt = pgTime(startedAt)
	return result, nil
}

// HasInflightRunForConversationAgent reports whether the (conversation, project_agent)
// tuple identified by runID has any sibling run in 'running' state. Fast-path check;
// MarkAgentRunRunning's NOT EXISTS guard closes the race window.
func (s *Store) HasInflightRunForConversationAgent(ctx context.Context, runID string) (bool, error) {
	runUUID, err := uuid(runID)
	if err != nil {
		return false, err
	}
	var inflight bool
	if err := s.db.QueryRow(ctx, `
select exists (
  select 1 from agent_runs r2
  where r2.conversation_id = (select conversation_id from agent_runs where id = $1::uuid)
    and r2.project_agent_id = (select project_agent_id from agent_runs where id = $1::uuid)
    and r2.status = 'running'
    and r2.id != $1::uuid
)`, runUUID).Scan(&inflight); err != nil {
		return false, err
	}
	return inflight, nil
}

// QueuePositionForRun returns the 1-indexed position of runID inside the queued-only
// segment of its (conversation_id, project_agent_id) lane. Excludes the running
// lane-holder ("currently being served", not "ahead of you"), so running self → 1
// and queued self with no queued siblings ahead → 1.
//
// Returns ErrUnknownAgentRun when runID does not exist.
func (s *Store) QueuePositionForRun(ctx context.Context, runID string) (int, error) {
	runUUID, err := uuid(runID)
	if err != nil {
		return 0, err
	}
	// Load target first so we can distinguish "row not found" from "lane empty
	// besides target" — the queued-only COUNT below cannot tell them apart.
	var (
		conversationID, projectAgentID string
		targetStatus                   string
		targetCreatedAt                pgtype.Timestamptz
	)
	err = s.db.QueryRow(ctx, `
		select conversation_id::text, project_agent_id::text, status, created_at
		from agent_runs
		where id = $1::uuid
	`, runUUID).Scan(&conversationID, &projectAgentID, &targetStatus, &targetCreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		return 0, err
	}
	if targetStatus == "running" {
		return 1, nil
	}
	// Queued target: count queued siblings ahead of (and including) self.
	var position int
	err = s.db.QueryRow(ctx, `
		select count(*)::int
		from agent_runs r
		where r.conversation_id = $1::uuid
		  and r.project_agent_id = $2::uuid
		  and r.status = 'queued'
		  and r.created_at <= $3::timestamptz
	`, mustUUID(conversationID), mustUUID(projectAgentID), targetCreatedAt).Scan(&position)
	if err != nil {
		return 0, err
	}
	if position < 1 {
		// Defensive: a queued target must at least see itself. Anything
		// less means the target was deleted between the two queries.
		return 0, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
	}
	return position, nil
}

// DequeuedRun is the dispatch descriptor returned by DequeueNextRunForConversationAgent
// for the run terminator to forward to the streaming dispatcher.
type DequeuedRun struct {
	RunID          string
	ConversationID string
	ConnectorType  string
}

// DequeueNextRunForConversationAgent finds the oldest queued run for
// the same (conversation, project_agent) as finishedRunID and returns
// its dispatch descriptor. Returns (nil, nil) when no queued sibling
// exists. FOR UPDATE SKIP LOCKED prevents two concurrent terminators
// from grabbing the same queued run; the eventual NOT EXISTS guard
// inside MarkAgentRunRunning is the final backstop.
func (s *Store) DequeueNextRunForConversationAgent(ctx context.Context, finishedRunID string) (*DequeuedRun, error) {
	finishedUUID, err := uuid(finishedRunID)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRow(ctx, `
select next_run.id::text, next_run.conversation_id::text, next_run.connector_type
from agent_runs finished
join lateral (
  select id, conversation_id, connector_type
  from agent_runs
  where conversation_id = finished.conversation_id
    and project_agent_id = finished.project_agent_id
    and status = 'queued'
  order by created_at asc
  limit 1
  for update skip locked
) as next_run on true
where finished.id = $1::uuid`, finishedUUID)
	var out DequeuedRun
	if err := row.Scan(&out.RunID, &out.ConversationID, &out.ConnectorType); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// GetAgentRunStatusAndStartedAt returns status and started_at — agent_daemon uses these
// to skip session id writeback on cancel/interrupt and to feed CAS against the binding's
// session_updated_at.
func (s *Store) GetAgentRunStatusAndStartedAt(ctx context.Context, runID string) (string, time.Time, error) {
	runUUID, err := uuid(runID)
	if err != nil {
		return "", time.Time{}, err
	}
	var status string
	var startedAt pgtype.Timestamptz
	err = s.db.QueryRow(ctx,
		`select status, started_at from agent_runs where id = $1::uuid`, runUUID).Scan(&status, &startedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", time.Time{}, fmt.Errorf("%w: %s", ErrUnknownAgentRun, runID)
		}
		return "", time.Time{}, err
	}
	return status, pgTime(startedAt), nil
}

func (s *Store) SendAssistantMessageFromRun(ctx context.Context, input SendAssistantMessageFromRunInput) (CompleteAgentRunResult, error) {
	return s.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:      input.RunID,
		Source:     input.Source,
		Content:    input.Content,
		Transcript: input.Transcript,
		Usage:      input.Usage,
	})
}

func (s *Store) CompleteAgentRun(ctx context.Context, input CompleteAgentRunInput) (CompleteAgentRunResult, error) {
	now := time.Now().UTC()
	source := completionSource(input.Source)
	result := CompleteAgentRunResult{
		RunID:      input.RunID,
		MessageID:  newID(),
		Status:     "completed",
		StartedAt:  now,
		FinishedAt: now,
	}
	content := strings.TrimSpace(input.Content)
	if content == "" {
		content = "Runtime completed this run with no output."
	}
	transcript := strings.TrimSpace(input.Transcript)
	// Always sanitize what we persist into the conversation, even when the producer
	// claims clean — protects against ANSI/build noise leaking into chat.
	messageContent := sanitizeAgentMessage(content)
	if messageContent == "" {
		messageContent = content
	}
	if transcript == "" && messageContent != content {
		// Preserve the noisy original as transcript only when sanitation removed something.
		transcript = content
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	runID, err := uuid(input.RunID)
	if err != nil {
		return result, err
	}
	messageID, err := uuid(result.MessageID)
	if err != nil {
		return result, err
	}

	run, err := queries.GetCompletableAgentRunForUpdate(ctx, runID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			exists, existsErr := queries.AgentRunExists(ctx, runID)
			if existsErr != nil {
				return result, existsErr
			}
			if exists {
				return result, fmt.Errorf("%w: %s", ErrInvalidProjectAgent, input.RunID)
			}
			return result, fmt.Errorf("%w: %s", ErrUnknownAgentRun, input.RunID)
		}
		return result, err
	}

	if run.Status != "queued" && run.Status != "running" {
		return result, fmt.Errorf("%w: %s has status %s", ErrAgentRunNotCompletable, input.RunID, run.Status)
	}
	result.RunID = run.RID
	result.WorkspaceID = run.RWorkspaceID
	result.ProjectID = run.RProjectID
	result.ConversationID = run.RConversationID
	result.ProjectAgentID = run.RProjectAgentID
	result.AgentID = run.PaAgentID
	result.Usage = normalizeUsageLog(input.Usage, result.WorkspaceID, result.ProjectID, result.RunID, now, source)
	if run.StartedAt.Valid {
		result.StartedAt = run.StartedAt.Time.UTC()
	}
	mentions := mentionPattern.FindAllString(content, -1)
	mentionedAgents, skippedMentions, err := resolveChildAgentMentions(ctx, queries, run, mentions)
	if err != nil {
		return result, err
	}
	result.SkippedMentions = skippedMentions

	metadata, err := json.Marshal(map[string]any{
		"source":           source,
		"run_id":           input.RunID,
		"mentions":         mentions,
		"skipped_mentions": result.SkippedMentions,
	})
	if err != nil {
		return result, err
	}

	if err := queries.CreateMessage(ctx, sqlc.CreateMessageParams{
		ID:             messageID,
		WorkspaceID:    mustUUID(result.WorkspaceID),
		ProjectID:      mustUUID(result.ProjectID),
		ConversationID: mustUUID(result.ConversationID),
		SenderType:     "agent",
		SenderID:       mustUUID(result.AgentID),
		Content:        messageContent,
		Metadata:       metadata,
		Now:            timestamptz(now),
	}); err != nil {
		return result, err
	}
	if transcript != "" {
		if err := queries.AppendAgentRunMetadata(ctx, sqlc.AppendAgentRunMetadataParams{
			ID:    runID,
			Patch: mustJSONBytes(map[string]any{"transcript": transcript, "transcript_source": source}),
			Now:   timestamptz(now),
		}); err != nil {
			return result, err
		}
	}
	pendingAudit := []audit.Event{{
		OccurredAt:  now,
		Source:      audit.SourceRuntime,
		EventType:   completionAuditEvent(source),
		ActorType:   audit.ActorTypeAgent,
		ActorID:     result.AgentID,
		TargetType:  "agent_run",
		TargetID:    result.RunID,
		WorkspaceID: result.WorkspaceID,
		ProjectID:   result.ProjectID,
		Payload: map[string]any{
			"source":            source,
			"source_event_id":   result.MessageID,
			"output_message_id": result.MessageID,
			"child_run_count":   len(mentionedAgents),
			"skipped_count":     len(result.SkippedMentions),
		},
	}}

	if err := queries.CompleteAgentRun(ctx, sqlc.CompleteAgentRunParams{
		CompletedBy:     source,
		OutputMessageID: messageID,
		Now:             timestamptz(now),
		ID:              runID,
	}); err != nil {
		return result, err
	}
	usageRaw, err := json.Marshal(result.Usage.Raw)
	if err != nil {
		return result, err
	}
	if err := queries.CreateUsageLog(ctx, sqlc.CreateUsageLogParams{
		ID:           mustUUID(result.Usage.ID),
		WorkspaceID:  mustUUID(result.WorkspaceID),
		ProjectID:    mustUUID(result.ProjectID),
		AgentRunID:   runID,
		Provider:     result.Usage.Provider,
		Model:        result.Usage.Model,
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		CostUsd:      numeric(result.Usage.CostUSD),
		Raw:          usageRaw,
		Now:          timestamptz(now),
	}); err != nil {
		return result, err
	}
	var pendingStreaming []StreamingDispatchInput
	for _, agent := range mentionedAgents {
		childRunID := newID()
		runMetadata, err := json.Marshal(map[string]any{
			"source":  source + "_agent_mention",
			"mention": "@" + agent.name,
		})
		if err != nil {
			return result, err
		}

		if err := queries.CreateChildAgentRun(ctx, sqlc.CreateChildAgentRunParams{
			ID:               mustUUID(childRunID),
			WorkspaceID:      mustUUID(result.WorkspaceID),
			ProjectID:        mustUUID(result.ProjectID),
			ConversationID:   mustUUID(result.ConversationID),
			TriggerMessageID: messageID,
			RequestedByID:    mustUUID(result.AgentID),
			ProjectAgentID:   mustUUID(agent.projectAgentID),
			ConnectorType:    agent.connectorType,
			Metadata:         runMetadata,
			Now:              timestamptz(now),
		}); err != nil {
			return result, err
		}
		pendingAudit = append(pendingAudit, audit.Event{
			OccurredAt:  now,
			Source:      audit.SourceRuntime,
			EventType:   auditAgentToAgentChildRunCreated,
			ActorType:   audit.ActorTypeAgent,
			ActorID:     result.AgentID,
			TargetType:  "agent_run",
			TargetID:    childRunID,
			WorkspaceID: result.WorkspaceID,
			ProjectID:   result.ProjectID,
			Payload: map[string]any{
				"source":            source,
				"source_event_id":   result.MessageID,
				"output_message_id": result.MessageID,
				"project_agent_id":  agent.projectAgentID,
			},
		})
		result.ChildRunIDs = append(result.ChildRunIDs, childRunID)
		// agent_daemon needs StreamingDispatcher to flip queued → running and
		// push the prompt; otherwise child runs sit at status=queued forever.
		switch {
		case connectorNeedsStreamingDispatch(agent.connectorType):
			pendingStreaming = append(pendingStreaming, StreamingDispatchInput{
				RunID:          childRunID,
				ConversationID: result.ConversationID,
				ConnectorType:  agent.connectorType,
			})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	s.dispatchPendingStreaming(ctx, pendingStreaming)
	for _, ev := range pendingAudit {
		s.emitAuditEvent(ev)
	}
	// Serial-queue handoff: if a queued sibling is waiting on the
	// same (conversation, project_agent), dispatch it now.
	s.dispatchNextQueuedRunAfter(ctx, result.RunID)
	return result, nil
}

func (s *Store) SendUserMessageToConversation(ctx context.Context, input SendUserMessageToConversationInput) (SendUserMessageToConversationResult, error) {
	var result SendUserMessageToConversationResult
	now := time.Now().UTC()
	conversationID, err := uuid(input.ConversationID)
	if err != nil {
		return result, err
	}
	userID, err := uuid(input.UserID)
	if err != nil {
		return result, err
	}
	content := strings.TrimSpace(input.Content)
	if content == "" || len(content) > 32000 {
		return result, ErrInvalidProjectInput
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	conversation, err := queries.GetProjectConversation(ctx, conversationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, fmt.Errorf("%w: %s", ErrUnknownConversation, input.ConversationID)
		}
		return result, err
	}

	mentionNames := mentionPattern.FindAllString(content, -1)
	if len(input.MentionedAgentIDs) > 0 {
		mentionNames = nil
	}
	// 1v1 fallback: when no @-mention and no explicit MentionedAgentIDs, route to the
	// conversation's bound primary_agent so a typed message reaches it (ChatGPT-style).
	implicitPrimary := ""
	if len(mentionNames) == 0 && len(input.MentionedAgentIDs) == 0 {
		implicitPrimary = strings.TrimSpace(conversation.PrimaryAgentID)
	}
	mentionedAgents := make([]mentionedProjectAgent, 0, len(input.MentionedAgentIDs)+len(mentionNames)+1)
	seenAgents := map[string]struct{}{}
	for _, mention := range mentionNames {
		agent, err := queries.GetActiveMentionedProjectAgent(ctx, sqlc.GetActiveMentionedProjectAgentParams{ProjectID: mustUUID(conversation.ProjectID), MentionName: strings.TrimPrefix(mention, "@")})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return result, err
		}
		if _, ok := seenAgents[agent.ProjectAgentID]; ok {
			continue
		}
		seenAgents[agent.ProjectAgentID] = struct{}{}
		mentionedAgents = append(mentionedAgents, mentionedProjectAgent{projectAgentID: agent.ProjectAgentID, agentID: agent.AgentID, name: agent.Name, slug: agent.Slug, connectorType: agent.ConnectorType})
	}
	for _, projectAgentID := range input.MentionedAgentIDs {
		trimmedID := strings.TrimSpace(projectAgentID)
		projectAgentUUID, err := uuid(trimmedID)
		if err != nil {
			return result, fmt.Errorf("%w: %s", ErrUnknownMention, projectAgentID)
		}
		row := tx.QueryRow(ctx, `select pa.id::text, a.id::text, a.name, a.slug, a.connector_type from project_agents pa join agents a on a.id = pa.agent_id where (pa.id = $1 or a.id = $1) and pa.project_id = $2 and pa.status = 'active' and pa.deleted_at is null and a.status = 'active' and a.deleted_at is null`, projectAgentUUID, mustUUID(conversation.ProjectID))
		var agent mentionedProjectAgent
		if err := row.Scan(&agent.projectAgentID, &agent.agentID, &agent.name, &agent.slug, &agent.connectorType); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result, fmt.Errorf("%w: %s", ErrUnknownMention, projectAgentID)
			}
			return result, err
		}
		if _, ok := seenAgents[agent.projectAgentID]; ok {
			continue
		}
		seenAgents[agent.projectAgentID] = struct{}{}
		mentionedAgents = append(mentionedAgents, agent)
	}
	// Implicit primary_agent fallback: must be active, otherwise silently drop to "no run
	// dispatched" so the user message still lands and the UI shows the bound-agent-disabled state.
	if implicitPrimary != "" {
		projectAgentUUID, err := uuid(implicitPrimary)
		if err == nil {
			row := tx.QueryRow(ctx, `select pa.id::text, a.id::text, a.name, a.slug, a.connector_type from project_agents pa join agents a on a.id = pa.agent_id where pa.id = $1 and pa.project_id = $2 and pa.status = 'active' and pa.deleted_at is null and a.status = 'active' and a.deleted_at is null`, projectAgentUUID, mustUUID(conversation.ProjectID))
			var agent mentionedProjectAgent
			if scanErr := row.Scan(&agent.projectAgentID, &agent.agentID, &agent.name, &agent.slug, &agent.connectorType); scanErr == nil {
				if _, ok := seenAgents[agent.projectAgentID]; !ok {
					seenAgents[agent.projectAgentID] = struct{}{}
					mentionedAgents = append(mentionedAgents, agent)
				}
			} else if !errors.Is(scanErr, pgx.ErrNoRows) {
				return result, scanErr
			}
		}
	}
	metadataMap := map[string]any{"source": "web"}
	metadata, err := json.Marshal(metadataMap)
	if err != nil {
		return result, err
	}
	messageID := newID()
	messageUUID := mustUUID(messageID)
	if err := queries.CreateMessage(ctx, sqlc.CreateMessageParams{ID: messageUUID, WorkspaceID: mustUUID(conversation.WorkspaceID), ProjectID: mustUUID(conversation.ProjectID), ConversationID: mustUUID(conversation.ID), SenderType: "user", SenderID: userID, Content: content, Metadata: metadata, Now: timestamptz(now)}); err != nil {
		return result, err
	}
	result.Message = MessageRead{ID: messageID, WorkspaceID: conversation.WorkspaceID, ProjectID: conversation.ProjectID, ConversationID: conversation.ID, SenderType: "user", SenderID: input.UserID, Kind: "message", ContentFormat: "text", Content: content, Metadata: metadataMap, CreatedAt: now}

	pendingAudit := []audit.Event{{OccurredAt: now, Source: audit.SourceRuntime, EventType: auditUserMessageSent, ActorType: audit.ActorTypeUser, ActorID: input.UserID, TargetType: "message", TargetID: messageID, WorkspaceID: conversation.WorkspaceID, ProjectID: conversation.ProjectID, Payload: map[string]any{"conversation_id": conversation.ID, "mentioned_count": len(mentionedAgents)}}}
	var pendingStreaming []StreamingDispatchInput
	for _, agent := range mentionedAgents {
		runID := newID()
		runMetadata, err := json.Marshal(map[string]any{"source": "web", "mention": "@" + agent.name})
		if err != nil {
			return result, err
		}
		if err := queries.CreateAgentRun(ctx, sqlc.CreateAgentRunParams{ID: mustUUID(runID), WorkspaceID: mustUUID(conversation.WorkspaceID), ProjectID: mustUUID(conversation.ProjectID), ConversationID: mustUUID(conversation.ID), TriggerMessageID: messageUUID, TriggerChannel: "web", RequestedByID: userID, ProjectAgentID: mustUUID(agent.projectAgentID), ConnectorType: agent.connectorType, Metadata: runMetadata, Now: timestamptz(now)}); err != nil {
			return result, err
		}
		result.RunIDs = append(result.RunIDs, runID)
		pendingAudit = append(pendingAudit, audit.Event{OccurredAt: now, Source: audit.SourceRuntime, EventType: auditAgentRunCreated, ActorType: audit.ActorTypeUser, ActorID: input.UserID, TargetType: "agent_run", TargetID: runID, WorkspaceID: conversation.WorkspaceID, ProjectID: conversation.ProjectID, Payload: map[string]any{"source": "web", "trigger_message_id": messageID, "project_agent_id": agent.projectAgentID}})
		// agent_daemon needs StreamingDispatcher to flip queued → running and
		// push the prompt; otherwise the run sits at queued forever.
		switch {
		case connectorNeedsStreamingDispatch(agent.connectorType):
			pendingStreaming = append(pendingStreaming, StreamingDispatchInput{RunID: runID, ConversationID: conversation.ID, ConnectorType: agent.connectorType})
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	s.dispatchPendingStreaming(ctx, pendingStreaming)
	for _, ev := range pendingAudit {
		s.emitAuditEvent(ev)
	}
	return result, nil
}

func (s *Store) CreateInboundIMMessage(ctx context.Context, input CreateInboundIMMessageInput) (CreateInboundIMMessageResult, error) {
	now := time.Now().UTC()
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = auditSourceIM
	}
	gateway := strings.TrimSpace(input.Gateway)
	result := CreateInboundIMMessageResult{
		MessageID: newID(),
		Mentions:  append([]string(nil), input.Mentions...),
		CreatedAt: now,
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	targetAgentID := strings.TrimSpace(input.TargetAgentID)
	targetMode := targetAgentID != ""
	var targetAgent mentionedProjectAgent
	var conversation struct {
		ID          string
		WorkspaceID string
		ProjectID   string
	}

	if targetMode {
		targetUUID, err := uuid(targetAgentID)
		if err != nil {
			return result, fmt.Errorf("%w: target_agent_id: %w", ErrInvalidProjectInput, err)
		}
		var targetWorkspaceID, targetProjectID string
		row := tx.QueryRow(ctx, `
			select pa.id::text, pa.workspace_id::text, pa.project_id::text,
			       a.id::text, a.name, a.slug, a.connector_type
			from project_agents pa
			join projects p on p.id = pa.project_id
			join agents a on a.id = pa.agent_id
			where a.id = $1
			  and pa.status = 'active'
			  and pa.deleted_at is null
			  and p.status = 'active'
			  and p.deleted_at is null
			  and a.status = 'active'
			  and a.deleted_at is null
			order by pa.created_at asc, pa.id asc
			limit 1
		`, targetUUID)
		if err := row.Scan(&targetAgent.projectAgentID, &targetWorkspaceID, &targetProjectID, &targetAgent.agentID, &targetAgent.name, &targetAgent.slug, &targetAgent.connectorType); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result, fmt.Errorf("%w: target_agent_id=%s", ErrUnknownMention, targetAgentID)
			}
			return result, err
		}
		result.Mentions = []string{"@" + targetAgent.name}

		externalChatID := strings.TrimSpace(input.ExternalChatID)
		if externalChatID == "" {
			return result, fmt.Errorf("%w: external_chat_id is required for targeted gateway inbound", ErrUnknownConversation)
		}
		externalThreadID := strings.TrimSpace(input.ExternalThreadID)
		platform := gateway
		if platform == "" {
			platform = "gateway"
		}
		err = tx.QueryRow(ctx, `
			select id::text, workspace_id::text, project_id::text
			from conversations
			where workspace_id = $1
			  and project_id = $2
			  and platform = $3
			  and external_id = $4
			  and external_thread_id = $5
			  and status = 'active'
			  and deleted_at is null
			order by created_at asc, id asc
			limit 1
		`, mustUUID(targetWorkspaceID), mustUUID(targetProjectID), platform, externalChatID, externalThreadID).Scan(&conversation.ID, &conversation.WorkspaceID, &conversation.ProjectID)
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return result, err
			}
			conversation.ID = newID()
			conversation.WorkspaceID = targetWorkspaceID
			conversation.ProjectID = targetProjectID
			form := normalizeIMConversationForm(input.ConversationForm)
			title := strings.TrimSpace(input.ConversationTitle)
			if title == "" {
				title = fmt.Sprintf("Feishu %s", externalChatID)
			}
			convMetadata, err := json.Marshal(map[string]any{
				"primary_agent_id": targetAgent.projectAgentID,
				"source":           source,
				"gateway":          gateway,
			})
			if err != nil {
				return result, err
			}
			if _, err := tx.Exec(ctx, `
				insert into conversations(
				  id, workspace_id, project_id, surface, form, title,
				  platform, external_id, external_thread_id, source_app_id,
				  status, metadata, created_at, updated_at
				) values ($1::uuid, $2::uuid, $3::uuid, 'im', $4, $5, $6, $7, $8, $9, 'active', $10::jsonb, $11, $11)
			`, mustUUID(conversation.ID), mustUUID(targetWorkspaceID), mustUUID(targetProjectID), form, title, platform, externalChatID, externalThreadID, strings.TrimSpace(input.SourceAppID), convMetadata, timestamptz(now)); err != nil {
				return result, err
			}
		} else if _, err := tx.Exec(ctx, `
			update conversations
			set source_app_id = coalesce(nullif($2, ''), source_app_id),
			    metadata = metadata || jsonb_build_object('primary_agent_id', $3::text),
			    updated_at = $4
			where id = $1::uuid
		`, mustUUID(conversation.ID), strings.TrimSpace(input.SourceAppID), targetAgent.projectAgentID, timestamptz(now)); err != nil {
			return result, err
		}
	} else {
		conversationKey := strings.TrimSpace(input.ConversationTitle)
		if strings.TrimSpace(input.ExternalChatID) != "" {
			conversationKey = strings.TrimSpace(input.ExternalChatID)
		}
		row, err := queries.GetActiveConversationByTitle(ctx, conversationKey)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result, fmt.Errorf("%w: %s", ErrUnknownConversation, input.ConversationTitle)
			}
			return result, err
		}
		conversation = struct {
			ID          string
			WorkspaceID string
			ProjectID   string
		}{ID: row.ID, WorkspaceID: row.WorkspaceID, ProjectID: row.ProjectID}
	}
	result.WorkspaceID = conversation.WorkspaceID
	result.ProjectID = conversation.ProjectID
	result.ConversationID = conversation.ID

	if source == auditSourceGateway && gateway != "" && strings.TrimSpace(input.ExternalMessageID) != "" {
		existing, lookupErr := queries.GetGatewayInboundMessageByExternalID(ctx, sqlc.GetGatewayInboundMessageByExternalIDParams{
			ConversationID:    mustUUID(conversation.ID),
			Gateway:           gateway,
			ExternalMessageID: strings.TrimSpace(input.ExternalMessageID),
		})
		if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
			return result, lookupErr
		}
		if lookupErr == nil {
			runRows, err := queries.ListAgentRunsByTriggerMessage(ctx, mustUUID(existing.MID))
			if err != nil {
				return result, err
			}
			result.MessageID = existing.MID
			result.RunIDs = append(result.RunIDs[:0], runRows...)
			result.CreatedAt = pgTime(existing.CreatedAt)
			return result, nil
		}
	}

	senderID := ""
	// Internal re-enqueue callers (credential-form submit) pre-resolve
	// the user_id and pass it on InitiatorUserID, bypassing the gateway-
	// subject lookup that would otherwise force them to translate
	// open_id → union_id via a Feishu API round-trip just to populate
	// ExternalUserID. Trust the value verbatim — the field is internal-
	// caller-only and callers responsible for setting it are responsible
	// for sourcing it from a trustworthy place (a previously-resolved
	// inbound row, in the form-submit case).
	if id := strings.TrimSpace(input.InitiatorUserID); id != "" {
		senderID = id
	}
	if senderID == "" && gateway != "" && strings.TrimSpace(input.ExternalUserID) != "" {
		if id, lookupErr := queries.GetActiveUserIDByGatewaySubject(ctx, sqlc.GetActiveUserIDByGatewaySubjectParams{Provider: gateway, Subject: strings.TrimSpace(input.ExternalUserID)}); lookupErr == nil {
			senderID = id
		} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return result, lookupErr
		}
	}
	if senderID == "" && !targetMode {
		var lookupErr error
		senderID, lookupErr = queries.GetActiveUserIDByEmail(ctx, input.SenderEmail)
		if lookupErr != nil {
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return result, fmt.Errorf("%w: %s", ErrUnknownSender, input.SenderEmail)
			}
			return result, lookupErr
		}
	}

	mentionedAgents := make([]mentionedProjectAgent, 0, len(input.Mentions)+1)
	if targetMode {
		mentionedAgents = append(mentionedAgents, targetAgent)
	}
	if !targetMode {
		seenMentions := make(map[string]struct{}, len(input.Mentions))
		for _, mention := range input.Mentions {
			if _, ok := seenMentions[mention]; ok {
				continue
			}
			seenMentions[mention] = struct{}{}

			agent, err := queries.GetActiveMentionedProjectAgent(ctx, sqlc.GetActiveMentionedProjectAgentParams{
				ProjectID:   mustUUID(conversation.ProjectID),
				MentionName: strings.TrimPrefix(mention, "@"),
			})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return result, fmt.Errorf("%w: %s", ErrUnknownMention, mention)
				}
				return result, err
			}
			mentionedAgents = append(mentionedAgents, mentionedProjectAgent{
				projectAgentID: agent.ProjectAgentID,
				agentID:        agent.AgentID,
				name:           agent.Name,
				slug:           agent.Slug,
				connectorType:  agent.ConnectorType,
			})
		}
	}

	messageMetadata := map[string]any{
		"source":   source,
		"mentions": input.Mentions,
	}
	if gateway != "" {
		messageMetadata["gateway"] = gateway
	}
	if strings.TrimSpace(input.ExternalUserID) != "" {
		messageMetadata["external_user_id"] = strings.TrimSpace(input.ExternalUserID)
	}
	// sender_open_id is the per-app Feishu open_id, captured here because the
	// credential-form submit callback envelope only exposes callback.Operator.OpenID.
	// Independent of ExternalUserID (which may be the union_id).
	if strings.TrimSpace(input.SenderOpenID) != "" {
		messageMetadata["sender_open_id"] = strings.TrimSpace(input.SenderOpenID)
	}
	if strings.TrimSpace(input.ExternalChatID) != "" {
		messageMetadata["external_chat_id"] = strings.TrimSpace(input.ExternalChatID)
	}
	if strings.TrimSpace(input.ExternalThreadID) != "" {
		messageMetadata["external_thread_id"] = strings.TrimSpace(input.ExternalThreadID)
	}
	if strings.TrimSpace(input.ExternalMessageID) != "" {
		messageMetadata["external_message_id"] = strings.TrimSpace(input.ExternalMessageID)
	}
	if strings.TrimSpace(input.TargetAgentID) != "" {
		messageMetadata["target_agent_id"] = strings.TrimSpace(input.TargetAgentID)
	}
	if strings.TrimSpace(input.SourceAppID) != "" {
		messageMetadata["source_app_id"] = strings.TrimSpace(input.SourceAppID)
	}
	for key, value := range input.Metadata {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		messageMetadata[key] = value
	}
	metadata, err := json.Marshal(messageMetadata)
	if err != nil {
		return result, err
	}

	messageID := mustUUID(result.MessageID)
	senderType := "user"
	senderUUID := pgtype.UUID{}
	if senderID != "" {
		senderUUID = mustUUID(senderID)
	} else {
		senderType = "external"
	}
	if _, err := tx.Exec(ctx, `
		insert into messages(
		  id, workspace_id, project_id, conversation_id,
		  sender_type, sender_id, kind, content_format, visibility, content, metadata,
		  created_at, updated_at
		) values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6::uuid, 'message', 'text', 'project', $7, $8::jsonb, $9, $9)
	`, messageID, mustUUID(conversation.WorkspaceID), mustUUID(conversation.ProjectID), mustUUID(conversation.ID), senderType, senderUUID, input.Text, metadata, timestamptz(now)); err != nil {
		return result, err
	}
	messageAuditMetadata := map[string]any{
		"source":          source,
		"source_event_id": result.MessageID,
		"conversation_id": conversation.ID,
		"mention_count":   len(input.Mentions),
	}
	if gateway != "" {
		messageAuditMetadata["gateway"] = gateway
	}
	if strings.TrimSpace(input.ExternalChatID) != "" {
		messageAuditMetadata["external_chat_id"] = strings.TrimSpace(input.ExternalChatID)
	}
	if strings.TrimSpace(input.ExternalMessageID) != "" {
		messageAuditMetadata["external_message_id"] = strings.TrimSpace(input.ExternalMessageID)
	}
	auditActorType := audit.ActorTypeUser
	auditActorID := senderID
	if senderType == "external" {
		auditActorType = audit.ActorTypeExternal
		auditActorID = strings.TrimSpace(input.ExternalUserID)
	}
	pendingAudit := []audit.Event{{
		OccurredAt:  now,
		Source:      audit.SourceRuntime,
		EventType:   auditIMMessageCreated,
		ActorType:   auditActorType,
		ActorID:     auditActorID,
		TargetType:  "message",
		TargetID:    result.MessageID,
		WorkspaceID: conversation.WorkspaceID,
		ProjectID:   conversation.ProjectID,
		Payload:     cloneAuditPayload(messageAuditMetadata),
	}}

	var pendingStreaming []StreamingDispatchInput
	for _, agent := range mentionedAgents {
		runID := newID()
		runMetadataMap := map[string]any{
			"source":  source,
			"mention": "@" + agent.name,
		}
		if gateway != "" {
			runMetadataMap["gateway"] = gateway
		}
		if strings.TrimSpace(input.ExternalChatID) != "" {
			runMetadataMap["external_chat_id"] = strings.TrimSpace(input.ExternalChatID)
		}
		if strings.TrimSpace(input.ExternalMessageID) != "" {
			runMetadataMap["external_message_id"] = strings.TrimSpace(input.ExternalMessageID)
		}
		if strings.TrimSpace(input.SourceAppID) != "" {
			runMetadataMap["source_app_id"] = strings.TrimSpace(input.SourceAppID)
		}
		runMetadata, err := json.Marshal(runMetadataMap)
		if err != nil {
			return result, err
		}

		requestedByType := senderType
		requestedByUUID := senderUUID
		if _, err := tx.Exec(ctx, `
			insert into agent_runs(
			  id, workspace_id, project_id, conversation_id,
			  trigger_message_id, trigger_source, trigger_channel, requested_by_type, requested_by_id,
			  project_agent_id, connector_type, status, visibility, metadata,
			  created_at, updated_at
			) values ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'message', 'im', $6, $7::uuid, $8::uuid, $9, 'queued', 'project', $10::jsonb, $11, $11)
		`, mustUUID(runID), mustUUID(conversation.WorkspaceID), mustUUID(conversation.ProjectID), mustUUID(conversation.ID), messageID, requestedByType, requestedByUUID, mustUUID(agent.projectAgentID), agent.connectorType, runMetadata, timestamptz(now)); err != nil {
			return result, err
		}
		runAuditMetadata := map[string]any{
			"source":             source,
			"source_event_id":    result.MessageID,
			"trigger_message_id": result.MessageID,
			"project_agent_id":   agent.projectAgentID,
		}
		if gateway != "" {
			runAuditMetadata["gateway"] = gateway
		}
		pendingAudit = append(pendingAudit, audit.Event{
			OccurredAt:  now,
			Source:      audit.SourceRuntime,
			EventType:   auditAgentRunCreated,
			ActorType:   auditActorType,
			ActorID:     auditActorID,
			TargetType:  "agent_run",
			TargetID:    runID,
			WorkspaceID: conversation.WorkspaceID,
			ProjectID:   conversation.ProjectID,
			Payload:     cloneAuditPayload(runAuditMetadata),
		})
		result.RunIDs = append(result.RunIDs, runID)
		// agent_daemon needs StreamingDispatcher to flip queued → running and
		// push the prompt; otherwise IM-triggered daemon runs never receive it.
		switch {
		case connectorNeedsStreamingDispatch(agent.connectorType):
			pendingStreaming = append(pendingStreaming, StreamingDispatchInput{RunID: runID, ConversationID: conversation.ID, ConnectorType: agent.connectorType})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	s.dispatchPendingStreaming(ctx, pendingStreaming)
	for _, ev := range pendingAudit {
		s.emitAuditEvent(ev)
	}
	return result, nil
}

func normalizeIMConversationForm(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "dm", "p2p", "private":
		return "dm"
	case "group", "chat", "":
		return "group"
	default:
		return "group"
	}
}

// cloneAuditPayload makes a shallow copy of an audit payload so the
// caller can mutate the original (commonly the audit metadata map)
// without changing the value the ingester later serializes.
func cloneAuditPayload(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func DefaultDevFixtureIDs() DevFixtureIDs {
	return DevFixtureIDs{
		UserID:                "00000000-0000-0000-0000-000000000001",
		FeishuAuthIdentityID:  "00000000-0000-0000-0000-000000000013",
		WorkspaceID:           "00000000-0000-0000-0000-000000000002",
		WorkspaceMemberID:     "00000000-0000-0000-0000-000000000003",
		ProjectID:             "00000000-0000-0000-0000-000000000004",
		ProductAgentID:        "00000000-0000-0000-0000-000000000006",
		BackendAgentID:        "00000000-0000-0000-0000-000000000007",
		TestAgentID:           "00000000-0000-0000-0000-000000000008",
		ProductProjectAgentID: "00000000-0000-0000-0000-000000000009",
		BackendProjectAgentID: "00000000-0000-0000-0000-000000000010",
		TestProjectAgentID:    "00000000-0000-0000-0000-000000000011",
		ConversationID:        "00000000-0000-0000-0000-000000000012",
	}
}

func (s *Store) SeedDevFixture(ctx context.Context) (DevSeedResult, error) {
	return s.InsertDevFixture(ctx, DefaultDevFixtureIDs())
}

func (s *Store) InsertDevFixture(ctx context.Context, ids DevFixtureIDs) (DevSeedResult, error) {
	now := time.Now().UTC()
	var result DevSeedResult

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)

	credentialKindRows, err := seedBuiltInCredentialKinds(ctx, tx)
	if err != nil {
		return result, err
	}
	result.CredentialKinds += credentialKindRows

	userRows, err := queries.CreateDevUser(ctx, sqlc.CreateDevUserParams{ID: mustUUID(ids.UserID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.Users += userRows
	userID, err := queries.GetActiveUserIDByEmail(ctx, "admin@example.com")
	if err != nil {
		return result, err
	}
	identityRows, err := queries.CreateDevFeishuAuthIdentity(ctx, sqlc.CreateDevFeishuAuthIdentityParams{ID: mustUUID(ids.FeishuAuthIdentityID), UserID: mustUUID(userID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.AuthIdentities += identityRows

	workspaceRows, err := queries.CreateDevWorkspace(ctx, sqlc.CreateDevWorkspaceParams{ID: mustUUID(ids.WorkspaceID), CreatedBy: mustUUID(userID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.Workspaces += workspaceRows
	workspaceID, err := queries.GetActiveWorkspaceIDBySlug(ctx, "demo")
	if err != nil {
		return result, err
	}

	workspaceMemberRows, err := queries.CreateDevWorkspaceMember(ctx, sqlc.CreateDevWorkspaceMemberParams{ID: mustUUID(ids.WorkspaceMemberID), WorkspaceID: mustUUID(workspaceID), UserID: mustUUID(userID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.WorkspaceMembers += workspaceMemberRows

	projectRows, err := queries.CreateDevProject(ctx, sqlc.CreateDevProjectParams{ID: mustUUID(ids.ProjectID), WorkspaceID: mustUUID(workspaceID), CreatedBy: mustUUID(userID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.Projects += projectRows
	projectID, err := queries.GetActiveProjectIDBySlug(ctx, sqlc.GetActiveProjectIDBySlugParams{WorkspaceID: mustUUID(workspaceID), Slug: "demo-project"})
	if err != nil {
		return result, err
	}

	agents := []struct {
		id          string
		name        string
		slug        string
		description string
		config      string
	}{
		{
			id:          ids.ProductAgentID,
			name:        "产品Agent",
			slug:        "product-agent",
			description: "产品视角评估需求和范围",
			config:      `{"profile":{"skills":["prd-review","scope"]}}`,
		},
		{
			id:          ids.BackendAgentID,
			name:        "后端Agent",
			slug:        "backend-agent",
			description: "后端视角评估架构和数据模型",
			config:      `{"profile":{"skills":["go","postgres","api"]}}`,
		},
		{
			id:          ids.TestAgentID,
			name:        "测试Agent",
			slug:        "test-agent",
			description: "测试视角补充验收和反例",
			config:      `{"profile":{"skills":["e2e","regression"]}}`,
		},
	}

	for _, agent := range agents {
		agentRows, err := queries.CreateDevAgent(ctx, sqlc.CreateDevAgentParams{
			ID:          mustUUID(agent.id),
			WorkspaceID: mustUUID(workspaceID),
			Name:        agent.name,
			Slug:        agent.slug,
			Description: agent.description,
			Config:      []byte(agent.config),
			CreatedBy:   mustUUID(userID),
			Now:         timestamptz(now),
		})
		if err != nil {
			return result, err
		}
		result.Agents += agentRows
	}

	projectAgents := []struct {
		id        string
		agentSlug string
	}{
		{id: ids.ProductProjectAgentID, agentSlug: "product-agent"},
		{id: ids.BackendProjectAgentID, agentSlug: "backend-agent"},
		{id: ids.TestProjectAgentID, agentSlug: "test-agent"},
	}

	for _, projectAgent := range projectAgents {
		agentID, err := queries.GetActiveAgentIDBySlug(ctx, sqlc.GetActiveAgentIDBySlugParams{WorkspaceID: mustUUID(workspaceID), Slug: projectAgent.agentSlug})
		if err != nil {
			return result, err
		}

		projectAgentRows, err := queries.CreateDevProjectAgent(ctx, sqlc.CreateDevProjectAgentParams{
			ID:          mustUUID(projectAgent.id),
			WorkspaceID: mustUUID(workspaceID),
			ProjectID:   mustUUID(projectID),
			AgentID:     mustUUID(agentID),
			CreatedBy:   mustUUID(userID),
			Now:         timestamptz(now),
		})
		if err != nil {
			return result, err
		}
		result.ProjectAgents += projectAgentRows

		activatedRows, err := queries.ActivateDevProjectAgent(ctx, sqlc.ActivateDevProjectAgentParams{
			ProjectID: mustUUID(projectID),
			AgentID:   mustUUID(agentID),
			Now:       timestamptz(now),
		})
		if err != nil {
			return result, err
		}
		result.ProjectAgents += activatedRows
	}

	conversationRows, err := queries.CreateDevConversation(ctx, sqlc.CreateDevConversationParams{ID: mustUUID(ids.ConversationID), WorkspaceID: mustUUID(workspaceID), ProjectID: mustUUID(projectID), Now: timestamptz(now)})
	if err != nil {
		return result, err
	}
	result.Conversations += conversationRows

	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type txBeginnerFunc func(context.Context) (pgx.Tx, error)

func (f txBeginnerFunc) Begin(ctx context.Context) (pgx.Tx, error) {
	return f(ctx)
}

type mentionedProjectAgent struct {
	projectAgentID string
	agentID        string
	name           string
	slug           string
	connectorType  string
}

var mentionPattern = regexp.MustCompile(`@[\p{Han}A-Za-z0-9_-]+`)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
var buildLinePattern = regexp.MustCompile(`(?m)^>\s*build.*$`)
var shellPromptLinePattern = regexp.MustCompile(`(?m)^\$\s.*$`)

// sanitizeAgentMessage strips ANSI escapes and shell/build preambles so the message
// stored in the conversation never carries terminal noise. Raw output is preserved
// as the run transcript.
func sanitizeAgentMessage(content string) string {
	cleaned := ansiEscapePattern.ReplaceAllString(content, "")
	cleaned = buildLinePattern.ReplaceAllString(cleaned, "")
	cleaned = shellPromptLinePattern.ReplaceAllString(cleaned, "")
	// collapse trailing whitespace and stray blank lines without merging real
	// paragraph breaks.
	lines := strings.Split(cleaned, "\n")
	trimmed := make([]string, 0, len(lines))
	previousBlank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" {
			if previousBlank {
				continue
			}
			previousBlank = true
		} else {
			previousBlank = false
		}
		trimmed = append(trimmed, line)
	}
	return strings.TrimSpace(strings.Join(trimmed, "\n"))
}

func beginTx(ctx context.Context, db sqlc.DBTX) (pgx.Tx, error) {
	beginner, ok := db.(txBeginner)
	if !ok {
		return nil, errors.New("store database handle does not support transactions")
	}
	return beginner.Begin(ctx)
}

func resolveChildAgentMentions(ctx context.Context, queries *sqlc.Queries, run sqlc.GetCompletableAgentRunForUpdateRow, mentions []string) ([]mentionedProjectAgent, []SkippedAgentMention, error) {
	mentionedAgents := make([]mentionedProjectAgent, 0, len(mentions))
	skippedMentions := make([]SkippedAgentMention, 0)
	seenMentions := make(map[string]struct{}, len(mentions))
	seenTargets := make(map[string]struct{}, len(mentions))

	for _, mention := range mentions {
		if _, ok := seenMentions[mention]; ok {
			continue
		}
		seenMentions[mention] = struct{}{}

		agent, err := queries.GetActiveMentionedProjectAgent(ctx, sqlc.GetActiveMentionedProjectAgentParams{
			ProjectID:   mustUUID(run.RProjectID),
			MentionName: strings.TrimPrefix(mention, "@"),
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				skippedMentions = append(skippedMentions, SkippedAgentMention{Mention: mention, Reason: "unknown_or_inactive_agent"})
				continue
			}
			return nil, nil, err
		}

		if agent.ProjectAgentID == run.RProjectAgentID {
			skippedMentions = append(skippedMentions, SkippedAgentMention{Mention: mention, ProjectAgentID: agent.ProjectAgentID, Reason: "self_trigger"})
			continue
		}
		if _, ok := seenTargets[agent.ProjectAgentID]; ok {
			skippedMentions = append(skippedMentions, SkippedAgentMention{Mention: mention, ProjectAgentID: agent.ProjectAgentID, Reason: "duplicate_target"})
			continue
		}

		seenTargets[agent.ProjectAgentID] = struct{}{}
		mentionedAgents = append(mentionedAgents, mentionedProjectAgent{
			projectAgentID: agent.ProjectAgentID,
			agentID:        agent.AgentID,
			name:           agent.Name,
			slug:           agent.Slug,
			connectorType:  agent.ConnectorType,
		})
	}

	return mentionedAgents, skippedMentions, nil
}

func completionSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return auditSourceRuntime
	}
	return source
}

func secretKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return "model_provider"
	}
	return kind
}

func completionAuditEvent(source string) string {
	if source == auditSourceHTTPAgent {
		return auditHTTPAgentCompleted
	}
	return auditAgentRunCompleted
}

func normalizeUsageLog(input UsageInput, workspaceID string, projectID string, runID string, now time.Time, source string) UsageLogRead {
	// Recorded verbatim; missing connector usage persists as '' / 0 rather than fabricated.
	provider := strings.TrimSpace(input.Provider)
	model := strings.TrimSpace(input.Model)
	inputTokens := input.InputTokens
	if inputTokens < 0 {
		inputTokens = 0
	}
	outputTokens := input.OutputTokens
	if outputTokens < 0 {
		outputTokens = 0
	}
	costUSD := input.CostUSD
	if costUSD < 0 || math.IsNaN(costUSD) || math.IsInf(costUSD, 0) {
		costUSD = 0
	}
	raw := input.Raw
	if raw == nil {
		raw = map[string]any{}
	}
	raw["source"] = source

	return UsageLogRead{
		ID:           newID(),
		WorkspaceID:  workspaceID,
		ProjectID:    projectID,
		AgentRunID:   runID,
		Provider:     provider,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CostUSD:      costUSD,
		Raw:          raw,
		CreatedAt:    now,
	}
}

func nullableUUID(value string) pgtype.UUID {
	if strings.TrimSpace(value) == "" {
		return pgtype.UUID{}
	}
	return mustUUID(value)
}

func uuid(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func mustUUID(value string) pgtype.UUID {
	id, err := uuid(value)
	if err != nil {
		panic(err)
	}
	return id
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pgTime(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func pgOptionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func numeric(value float64) pgtype.Numeric {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(value, 'f', 6, 64)); err != nil {
		panic(err)
	}
	return n
}

func numericFloat64(value pgtype.Numeric) float64 {
	if !value.Valid {
		return 0
	}
	f, err := value.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

func decodeJSONMap(value []byte) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil || decoded == nil {
		return map[string]any{}
	}
	return decoded
}

func decodeJSONStringSlice(value []byte) []string {
	if len(value) == 0 {
		return []string{}
	}
	var decoded []string
	if err := json.Unmarshal(value, &decoded); err != nil || decoded == nil {
		return []string{}
	}
	return normalizeStringSlice(decoded)
}

// DecodeMessageAttachments lifts the attachments slice out of a messages.metadata
// jsonb payload. Lossy on purpose: malformed entries are skipped silently so a single
// bad attachment cannot nuke a run with other content. Shape matches MessageAttachment.
func DecodeMessageAttachments(metadata map[string]any) []MessageAttachment {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["attachments"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make([]MessageAttachment, 0, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		att := MessageAttachment{
			Kind:       stringFromAny(m["kind"]),
			MIME:       stringFromAny(m["mime"]),
			DataBase64: stringFromAny(m["data_base64"]),
		}
		if sizeRaw, ok := m["size"]; ok {
			switch v := sizeRaw.(type) {
			case float64:
				att.Size = int(v)
			case int:
				att.Size = v
			case int64:
				att.Size = int(v)
			case json.Number:
				if n, err := v.Int64(); err == nil {
					att.Size = int(n)
				}
			}
		}
		if att.Kind == "" || att.DataBase64 == "" {
			continue
		}
		out = append(out, att)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EncodeMessageAttachments is the inverse of DecodeMessageAttachments. Returns nil
// when there's nothing to encode so callers can avoid emitting metadata.attachments=[].
func EncodeMessageAttachments(attachments []MessageAttachment) []map[string]any {
	if len(attachments) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(attachments))
	for _, att := range attachments {
		if strings.TrimSpace(att.Kind) == "" || strings.TrimSpace(att.DataBase64) == "" {
			continue
		}
		entry := map[string]any{
			"kind":        att.Kind,
			"data_base64": att.DataBase64,
		}
		if mime := strings.TrimSpace(att.MIME); mime != "" {
			entry["mime"] = mime
		}
		if att.Size > 0 {
			entry["size"] = att.Size
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func normalizeStringSlice(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func validConnectorType(value string) bool {
	switch value {
	case "http", "agent_daemon":
		return true
	default:
		return false
	}
}

// connectorNeedsStreamingDispatch reports whether the given connector_type uses the async
// streaming path where the server pushes the prompt via Connector.StreamPrompt.
func connectorNeedsStreamingDispatch(connectorType string) bool {
	return connectorType == "agent_daemon"
}

func stringFromMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func runtimePtr(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// agentConfigJSON builds the JSON to be written into agents.config. `bindings`
// carries `credential_bindings` / `model_credential_binding` extracted from the
// caller's project-agent config; without piping them through here the runtime's
// ParseCredentialBindings (which reads agent_config) would always see {}.
func agentConfigJSON(systemPrompt, defaultModelID string, capabilities []string, runtime, connectorType string, bindings map[string]any) ([]byte, error) {
	config := map[string]any{"capabilities": normalizeStringSlice(capabilities)}
	if trimmed := strings.TrimSpace(systemPrompt); trimmed != "" {
		config["system_prompt"] = trimmed
	}
	if trimmed := strings.TrimSpace(defaultModelID); trimmed != "" {
		config["default_model_id"] = trimmed
	}
	r := strings.TrimSpace(runtime)
	if connectorUsesServerRuntime(connectorType) {
		if r == "" {
			r = "sandbox"
		}
		if !validRuntimeMode(r) {
			return nil, fmt.Errorf("%w: runtime must be sandbox or local", ErrInvalidProjectInput)
		}
		config["runtime"] = r
	} else if r != "" {
		return nil, fmt.Errorf("%w: %s agents have no server-side runtime; got runtime=%q", ErrInvalidProjectInput, connectorType, r)
	}
	if v, ok := bindings["credential_bindings"]; ok && v != nil {
		config["credential_bindings"] = v
	}
	if v, ok := bindings["model_credential_binding"]; ok && v != nil {
		config["model_credential_binding"] = v
	}
	return json.Marshal(config)
}

func projectAgentConfigJSON(input map[string]any, connectorType string) ([]byte, error) {
	config := map[string]any{}
	if connectorType == "agent_daemon" {
		for _, key := range []string{"device_id", "daemon_mode", "agent_kind", "work_dir"} {
			if v, ok := input[key]; ok {
				if s, ok := v.(string); ok {
					trimmed := strings.TrimSpace(s)
					if trimmed != "" {
						config[key] = trimmed
					}
				}
			}
		}
	}
	return json.Marshal(config)
}

func connectorUsesServerRuntime(connectorType string) bool {
	return false
}

func validRuntimeMode(value string) bool {
	switch strings.TrimSpace(value) {
	case "local", "sandbox":
		return true
	default:
		return false
	}
}

func agentSummaryFromRow(id, workspaceID, name, slug, description, connectorType, status string, configJSON []byte, createdAt, updatedAt pgtype.Timestamptz) AgentSummary {
	config := decodeJSONMap(configJSON)
	capabilities, _ := config["capabilities"].([]any)
	caps := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if s, ok := capability.(string); ok {
			caps = append(caps, s)
		}
	}
	// Strip stale config["runtime"] from historical agent_daemon rows so list/detail
	// don't surface a misleading value. Cheaper than a destructive data migration.
	if connectorType == "agent_daemon" {
		delete(config, "runtime")
	}
	return AgentSummary{ID: id, WorkspaceID: workspaceID, Name: name, Slug: slug, Description: description, ConnectorType: connectorType, Status: status, Capabilities: normalizeStringSlice(caps), Config: config, CreatedAt: pgTime(createdAt), UpdatedAt: pgTime(updatedAt)}
}

func projectAgentSummaryFromRow(id, workspaceID, projectID, agentID, status string, configJSON []byte, createdAt, updatedAt pgtype.Timestamptz) ProjectAgentSummary {
	return ProjectAgentSummary{ID: id, WorkspaceID: workspaceID, ProjectID: projectID, AgentID: agentID, Status: status, Config: decodeJSONMap(configJSON), CreatedAt: pgTime(createdAt), UpdatedAt: pgTime(updatedAt)}
}

func changedAgentFields(current sqlc.GetAgentForUpdateRow, updated AgentSummary, input UpdateAgentInput) []string {
	changed := []string{}
	if current.Name != updated.Name {
		changed = append(changed, "name")
	}
	if current.Description != updated.Description {
		changed = append(changed, "description")
	}
	if current.ConnectorType != updated.ConnectorType {
		changed = append(changed, "connector_type")
	}
	if input.SystemPrompt != nil {
		changed = append(changed, "system_prompt")
	}
	if input.DefaultModelID != nil {
		changed = append(changed, "default_model_id")
	}
	if input.CapabilitiesSet {
		changed = append(changed, "capabilities")
	}
	return changed
}

func createAgentWithSlugRetry(ctx context.Context, queries *sqlc.Queries, params sqlc.CreateAgentCRUDParams, explicitSlug bool) (sqlc.CreateAgentCRUDRow, error) {
	for attempt := 0; attempt < autoSlugMaxAttempts; attempt++ {
		row, err := queries.CreateAgentCRUD(ctx, params)
		if err == nil {
			return row, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return sqlc.CreateAgentCRUDRow{}, err
		}
		if explicitSlug {
			return sqlc.CreateAgentCRUDRow{}, fmt.Errorf("%w: %s", ErrDuplicateAgentSlug, nextSlugSuggestion(ctx, queries, params.WorkspaceID, params.Slug))
		}

		params.ID = mustUUID(newID())
		params.Slug = generateAutoSlug("agent")
	}
	return sqlc.CreateAgentCRUDRow{}, fmt.Errorf("%w: could not generate unique slug after %d attempts", ErrDuplicateAgentSlug, autoSlugMaxAttempts)
}

func nextSlugSuggestion(ctx context.Context, queries *sqlc.Queries, workspaceID pgtype.UUID, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return generateAutoSlug("agent")
	}

	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		exists, err := queries.ActiveAgentSlugExists(ctx, sqlc.ActiveAgentSlugExistsParams{WorkspaceID: workspaceID, Slug: candidate})
		if err != nil {
			return base + "-2"
		}
		if !exists {
			return candidate
		}
	}

	return fmt.Sprintf("%s-%s", base, generateSlugSuffix(3))
}

func (s *Store) emitAgentAudit(now time.Time, actorID, eventType, targetType, targetID, workspaceID, projectID string, payload map[string]any) {
	s.emitAuditEvent(audit.Event{OccurredAt: now, Source: audit.SourceAdmin, EventType: eventType, ActorType: audit.ActorTypeUser, ActorID: actorID, TargetType: targetType, TargetID: targetID, WorkspaceID: workspaceID, ProjectID: projectID, Payload: payload})
}

// userFacingReasonFromMetadata extracts a human-readable failure reason from
// agent_runs.metadata. Falls back to deriving one from failure_reason or HTTP hints
// so older runs without user_facing_reason still render usefully.
func userFacingReasonFromMetadata(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta["user_facing_reason"].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	if v, ok := meta["failure_reason"].(string); ok {
		if mapped := mapUserFacingReason(v); mapped != "" {
			return mapped
		}
	}
	return ""
}

// mapUserFacingReason translates raw runner / connector errors into a short
// Chinese sentence that a non-engineer can act on. The match list is greedy
// and case-insensitive; unmatched reasons fall back to a generic message
// rather than leaking stack traces or HTTP bodies into the user surface.
func mapUserFacingReason(raw string) string {
	reason := strings.TrimSpace(raw)
	if reason == "" || strings.EqualFold(reason, "unknown") {
		return "Agent 执行失败，请稍后重试或联系管理员。"
	}
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "capability_credential_missing"):
		return "Agent 需要的能力凭据还没设置，请先到我的凭据补齐后重试。"
	case strings.Contains(lower, "capability_credential_decrypt_failed"):
		return "Agent 需要的能力凭据无法读取，请重新设置凭据或联系管理员。"
	case strings.Contains(lower, "capability_credential_kind_mismatch"):
		return "Agent 需要的能力凭据类型不匹配，请重新设置对应类型的凭据。"
	case strings.Contains(lower, "capability_version_unavailable"):
		// 老 oss_key 空的 binding 或者 latest 模式下 capability 还没传过 zip,
		// 都不会自爆出引擎错误,大多数情况下 agent 仍能继续跑(只是少装了
		// 一个 skill)。但当 capability 是 agent 跑通必需的(比如只挂了
		// 这一个 skill),失败原因会经此回流,告诉用户去管理页处理。
		return "Agent 绑定的能力还没有可用的上传版本，请到能力管理页重新上传或切换为 latest 模式。"
	case strings.Contains(lower, "secret") && (strings.Contains(lower, "disabled") || strings.Contains(lower, "unavailable") || strings.Contains(lower, "not found") || strings.Contains(lower, "missing")):
		return "依赖的 Secret 已停用或缺失，请到 Secrets 页确认。"
	case strings.Contains(lower, "model") && (strings.Contains(lower, "disabled") || strings.Contains(lower, "missing") || strings.Contains(lower, "not found")):
		return "Agent 绑定的模型已停用或不存在，请到 Agents 页重新选择。"
	case strings.Contains(lower, "provider") && (strings.Contains(lower, "disabled") || strings.Contains(lower, "missing")):
		return "Agent 依赖的模型服务商已停用，请到 Models 页恢复或重新选择。"
	case strings.Contains(lower, "context length") || strings.Contains(lower, "context_length") || strings.Contains(lower, "maximum context"):
		return "对话内容超过模型上下文长度上限，请新开会话或精简问题后重试。"
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "429"):
		return "模型服务被限流，请稍后重试。"
	// Must precede the generic 401 / timeout branches: daemon/sandbox dial-in errors
	// embed "401 Unauthorized" or "context deadline exceeded" and would be misclassified.
	case strings.Contains(lower, "ws upgrade rejected") ||
		strings.Contains(lower, "re-pair the daemon") ||
		strings.Contains(lower, "daemon dial-in") ||
		strings.Contains(lower, "acquiresandboxbinding") ||
		strings.Contains(lower, "sandbox acquire"):
		return "Agent 容器配对失效，请重试。"
	// "deleted by admin" included for backward-compat with historical rows.
	case strings.Contains(lower, "runtime retired") ||
		strings.Contains(lower, "runtime deleted"):
		return "Agent 容器已被回收，请重试。"
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "auth failed"):
		return "模型服务身份验证失败，请确认 Secret 配置。"
	case strings.Contains(lower, "403") || strings.Contains(lower, "forbidden"):
		return "模型服务拒绝了请求，请确认账号权限。"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "i/o timeout"):
		return "调用模型超时，请稍后重试。"
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host") || strings.Contains(lower, "dial tcp"):
		return "无法连接到模型服务，请确认网络或服务地址。"
	case strings.Contains(lower, "interrupted") || strings.Contains(lower, "cancel"):
		return "Agent 任务被取消或中断。"
	case strings.Contains(lower, "opencode") || strings.Contains(lower, "exit status") || strings.Contains(lower, "exec"):
		return "Agent 本地执行失败，请展开本轮错误详情查看原因。"
	default:
		return "Agent 执行失败，请展开本轮错误详情查看具体原因。"
	}
}

// MapUserFacingReason exposes mapUserFacingReason so dev/run_stream.go can stamp
// the user-visible message into the run.failed payload without duplicating the table.
func MapUserFacingReason(raw string) string {
	return mapUserFacingReason(raw)
}

func messageFromConversationRow(row sqlc.ListConversationMessagesRow) MessageRead {
	return MessageRead{
		ID:             row.MID,
		WorkspaceID:    row.MWorkspaceID,
		ProjectID:      row.MProjectID,
		ConversationID: row.MConversationID,
		SenderType:     row.SenderType,
		SenderID:       row.MSenderID,
		Kind:           row.Kind,
		ContentFormat:  row.ContentFormat,
		Content:        row.Content,
		Metadata:       decodeJSONMap(row.Metadata),
		CreatedAt:      pgTime(row.CreatedAt),
	}
}

func messageFromOutputRow(row sqlc.GetOutputMessageByRunIDRow) MessageRead {
	return MessageRead{
		ID:             row.MID,
		WorkspaceID:    row.MWorkspaceID,
		ProjectID:      row.MProjectID,
		ConversationID: row.MConversationID,
		SenderType:     row.SenderType,
		SenderID:       row.MSenderID,
		Kind:           row.Kind,
		ContentFormat:  row.ContentFormat,
		Content:        row.Content,
		Metadata:       decodeJSONMap(row.Metadata),
		CreatedAt:      pgTime(row.CreatedAt),
	}
}

func agentRunBriefFromConversationRow(row sqlc.ListConversationAgentRunsRow) AgentRunBriefRead {
	brief := AgentRunBriefRead{
		ID:               row.RID,
		WorkspaceID:      row.RWorkspaceID,
		ProjectID:        row.RProjectID,
		ConversationID:   row.RConversationID,
		TriggerMessageID: row.TriggerMessageID,
		OutputMessageID:  row.OutputMessageID,
		ProjectAgentID:   row.RProjectAgentID,
		AgentID:          row.PaAgentID,
		AgentName:        row.AgentName,
		AgentSlug:        row.AgentSlug,
		ConnectorType:    row.ConnectorType,
		Status:           row.Status,
		CreatedAt:        pgTime(row.CreatedAt),
		StartedAt:        pgOptionalTime(row.StartedAt),
		FinishedAt:       pgOptionalTime(row.FinishedAt),
	}
	if brief.Status == "failed" {
		brief.UserFacingReason = userFacingReasonFromMetadata(decodeJSONMap(row.Metadata))
	}
	return brief
}

// agentRunBriefFromProjectPageRow maps a single ListProjectAgentRunsPage row to the
// AgentRunBriefRead shape the admin API serves.
func agentRunBriefFromProjectPageRow(row sqlc.ListProjectAgentRunsPageRow) AgentRunBriefRead {
	brief := AgentRunBriefRead{
		ID:               row.RID,
		WorkspaceID:      row.RWorkspaceID,
		ProjectID:        row.RProjectID,
		ConversationID:   row.RConversationID,
		TriggerMessageID: row.TriggerMessageID,
		OutputMessageID:  row.OutputMessageID,
		ProjectAgentID:   row.RProjectAgentID,
		AgentID:          row.PaAgentID,
		AgentName:        row.AgentName,
		AgentSlug:        row.AgentSlug,
		ConnectorType:    row.ConnectorType,
		Status:           row.Status,
		CreatedAt:        pgTime(row.CreatedAt),
		StartedAt:        pgOptionalTime(row.StartedAt),
		FinishedAt:       pgOptionalTime(row.FinishedAt),
	}
	if brief.Status == "failed" {
		brief.UserFacingReason = userFacingReasonFromMetadata(decodeJSONMap(row.Metadata))
	}
	return brief
}

func usageLogFromProjectRow(row sqlc.ListProjectUsageLogsRow) UsageLogRead {
	return UsageLogRead{
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		ProjectID:    row.ProjectID,
		AgentRunID:   row.AgentRunID,
		Provider:     row.Provider,
		Model:        row.Model,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		CostUSD:      numericFloat64(row.CostUsd),
		Raw:          decodeJSONMap(row.Raw),
		CreatedAt:    pgTime(row.CreatedAt),
	}
}

func usageLogFromProjectRunRow(row sqlc.ListProjectUsageLogsByRunRow) UsageLogRead {
	return UsageLogRead{
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		ProjectID:    row.ProjectID,
		AgentRunID:   row.AgentRunID,
		Provider:     row.Provider,
		Model:        row.Model,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		CostUSD:      numericFloat64(row.CostUsd),
		Raw:          decodeJSONMap(row.Raw),
		CreatedAt:    pgTime(row.CreatedAt),
	}
}

func usageLogFromRunRow(row sqlc.ListUsageLogsByRunRow) UsageLogRead {
	return UsageLogRead{
		ID:           row.ID,
		WorkspaceID:  row.WorkspaceID,
		ProjectID:    row.ProjectID,
		AgentRunID:   row.AgentRunID,
		Provider:     row.Provider,
		Model:        row.Model,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		CostUSD:      numericFloat64(row.CostUsd),
		Raw:          decodeJSONMap(row.Raw),
		CreatedAt:    pgTime(row.CreatedAt),
	}
}

func secretReadFromCreateRow(row sqlc.CreateSecretRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromListRow(row sqlc.ListSecretsRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromDisableRow(row sqlc.DisableSecretRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromSecretRow(row sqlc.GetSecretPayloadRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

func secretRead(id, slug, name, kind, provider, authType, keyVersion, status string, metadataJSON []byte, createdAt, updatedAt pgtype.Timestamptz) SecretRead {
	metadata := decodeJSONMap(metadataJSON)
	masked, _ := metadata["masked"].(string)
	return SecretRead{
		ID:         id,
		Slug:       slug,
		Name:       name,
		Kind:       kind,
		Provider:   provider,
		AuthType:   authType,
		KeyVersion: keyVersion,
		Status:     status,
		Masked:     masked,
		Metadata:   metadata,
		CreatedAt:  pgTime(createdAt),
		UpdatedAt:  pgTime(updatedAt),
	}
}

func modelReadFromCreateRow(row sqlc.CreateModelRow) ModelRead {
	return ModelRead{ID: row.ID, Slug: row.Slug, Name: row.Name, ProviderType: row.ProviderType, Adapter: row.Adapter, BaseURL: row.BaseUrl, ModelKey: row.ModelKey, CredentialMode: row.CredentialMode, SecretID: row.SecretID, CredentialKindCode: row.CredentialKindCode, Status: row.Status, Config: decodeJSONMap(row.Config), CreatedBy: row.CreatedBy, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func modelReadFromDisableRow(row sqlc.DisableModelRow) ModelRead {
	return ModelRead{ID: row.ID, Slug: row.Slug, Name: row.Name, ProviderType: row.ProviderType, Adapter: row.Adapter, BaseURL: row.BaseUrl, ModelKey: row.ModelKey, CredentialMode: row.CredentialMode, SecretID: row.SecretID, CredentialKindCode: row.CredentialKindCode, Status: row.Status, Config: decodeJSONMap(row.Config), CreatedBy: row.CreatedBy, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func modelReadFromUpdateRow(row sqlc.UpdateModelRow) ModelRead {
	return ModelRead{ID: row.ID, Slug: row.Slug, Name: row.Name, ProviderType: row.ProviderType, Adapter: row.Adapter, BaseURL: row.BaseUrl, ModelKey: row.ModelKey, CredentialMode: row.CredentialMode, SecretID: row.SecretID, CredentialKindCode: row.CredentialKindCode, Status: row.Status, Config: decodeJSONMap(row.Config), CreatedBy: row.CreatedBy, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func modelReadFromListRow(row sqlc.ListModelsRow) ModelRead {
	return ModelRead{ID: row.ID, Slug: row.Slug, Name: row.Name, ProviderType: row.ProviderType, Adapter: row.Adapter, BaseURL: row.BaseUrl, ModelKey: row.ModelKey, CredentialMode: row.CredentialMode, SecretID: row.SecretID, CredentialKindCode: row.CredentialKindCode, Status: row.Status, Config: decodeJSONMap(row.Config), CreatedBy: row.CreatedBy, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func modelReadFromGetRow(row sqlc.GetModelRow) ModelRead {
	return ModelRead{ID: row.ID, Slug: row.Slug, Name: row.Name, ProviderType: row.ProviderType, Adapter: row.Adapter, BaseURL: row.BaseUrl, ModelKey: row.ModelKey, CredentialMode: row.CredentialMode, SecretID: row.SecretID, CredentialKindCode: row.CredentialKindCode, Status: row.Status, Config: decodeJSONMap(row.Config), CreatedBy: row.CreatedBy, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func modelRuntimeFromRow(row sqlc.ResolveModelRuntimeRow) ModelRuntime {
	modelConfig := decodeJSONMap(row.ModelConfig)
	return ModelRuntime{
		ModelID:            row.ModelID,
		ModelName:          row.ModelName,
		ModelKey:           row.ModelKey,
		Capabilities:       childMap(modelConfig, "capabilities"),
		Limits:             childMap(modelConfig, "limits"),
		ModelConfig:        modelConfig,
		ProviderType:       row.ProviderType,
		Adapter:            row.Adapter,
		BaseURL:            row.BaseUrl,
		CredentialMode:     row.CredentialMode,
		SecretID:           row.SecretID,
		CredentialKindCode: row.CredentialKindCode,
		EncryptedPayload:   row.SecretEncryptedPayload,
		// Legacy alias for modelConfig; kept so existing renderers that read ProviderConfig work.
		ProviderConfig: modelConfig,
	}
}

// childMap returns the nested map under key, or an empty map when the key is absent
// or not an object.
func childMap(m map[string]any, key string) map[string]any {
	if child, ok := m[key].(map[string]any); ok {
		return child
	}
	return map[string]any{}
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func mustJSONBytes(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

var _ txBeginner = txBeginnerFunc(nil)

// ProjectAgentRuntime is the (project_agent, agent) config pair the OpenCode Connector
// pulls back from the DB for admin "warm" actions (no Prompt context to derive them from).
type ProjectAgentRuntime struct {
	ProjectAgentID     string
	WorkspaceID        string
	ProjectID          string
	AgentID            string
	ConnectorType      string
	ProjectAgentConfig map[string]any
	AgentConfig        map[string]any
}

// GetProjectAgentRuntime returns the (project_agent.config, agent.config) blobs.
// Filters out disabled / soft-deleted rows so warm cannot revive a turned-off agent.
// Returns wrapped pgx.ErrNoRows when no live row matches (callers map to HTTP 404).
func (s *Store) GetProjectAgentRuntime(ctx context.Context, workspaceID, projectAgentID string) (ProjectAgentRuntime, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return ProjectAgentRuntime{}, fmt.Errorf("project agent runtime: workspace_id: %w", err)
	}
	paUUID, err := uuid(projectAgentID)
	if err != nil {
		return ProjectAgentRuntime{}, fmt.Errorf("project agent runtime: project_agent_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetProjectAgentRuntime(ctx, sqlc.GetProjectAgentRuntimeParams{
		ProjectAgentID: paUUID,
		WorkspaceID:    wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentRuntime{}, fmt.Errorf("project agent runtime: %w", err)
		}
		return ProjectAgentRuntime{}, err
	}
	return ProjectAgentRuntime{
		ProjectAgentID:     row.ProjectAgentID,
		WorkspaceID:        row.WorkspaceID,
		ProjectID:          row.ProjectID,
		AgentID:            row.AgentID,
		ConnectorType:      row.ConnectorType,
		ProjectAgentConfig: unmarshalJSONOrEmpty(row.ProjectAgentConfig),
		AgentConfig:        unmarshalJSONOrEmpty(row.AgentConfig),
	}, nil
}

// ProjectAgentRuntimeBinding is the read-side view of
// project_agents.runtime_id. Empty RuntimeID means the user has not yet
// picked a runtime for this agent.
type ProjectAgentRuntimeBinding struct {
	ProjectAgentID string `json:"project_agent_id"`
	WorkspaceID    string `json:"workspace_id"`
	RuntimeID      string `json:"runtime_id"`
}

// GetProjectAgentRuntimeBinding returns the explicit runtime binding
// for a project_agent. Used by the agent settings page to populate the
// runtime picker. Returns ErrUnknownProjectAgent when the row does not
// exist (or has been soft-deleted / belongs to a different workspace),
// so handlers can map it to a 404.
func (s *Store) GetProjectAgentRuntimeBinding(ctx context.Context, workspaceID, projectAgentID string) (ProjectAgentRuntimeBinding, error) {
	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return ProjectAgentRuntimeBinding{}, fmt.Errorf("project agent runtime binding: workspace_id: %w", err)
	}
	paUUID, err := uuid(projectAgentID)
	if err != nil {
		return ProjectAgentRuntimeBinding{}, fmt.Errorf("project agent runtime binding: project_agent_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetProjectAgentRuntimeBinding(ctx, sqlc.GetProjectAgentRuntimeBindingParams{
		ProjectAgentID: paUUID,
		WorkspaceID:    wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentRuntimeBinding{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, projectAgentID)
		}
		return ProjectAgentRuntimeBinding{}, err
	}
	return ProjectAgentRuntimeBinding{
		ProjectAgentID: row.ProjectAgentID,
		WorkspaceID:    row.WorkspaceID,
		RuntimeID:      row.RuntimeID,
	}, nil
}

// SetProjectAgentRuntimeInput carries the parameters for binding a
// project_agent to a runtime. Empty RuntimeID is a valid clear request
// (turns the agent back into an unbound state).
type SetProjectAgentRuntimeInput struct {
	WorkspaceID    string
	ProjectAgentID string
	RuntimeID      string // empty → clear
}

// SetProjectAgentRuntime binds (or clears) the runtime a project_agent dispatches on.
// Empty RuntimeID writes NULL. Returns ErrUnknownProjectAgent on no match.
// Caller must validate that the runtime belongs to the same workspace.
func (s *Store) SetProjectAgentRuntime(ctx context.Context, input SetProjectAgentRuntimeInput) (ProjectAgentRuntimeBinding, error) {
	wsUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return ProjectAgentRuntimeBinding{}, fmt.Errorf("set project agent runtime: workspace_id: %w", err)
	}
	paUUID, err := uuid(input.ProjectAgentID)
	if err != nil {
		return ProjectAgentRuntimeBinding{}, fmt.Errorf("set project agent runtime: project_agent_id: %w", err)
	}
	var runtimeUUID pgtype.UUID
	if v := strings.TrimSpace(input.RuntimeID); v != "" {
		parsed, err := uuid(v)
		if err != nil {
			return ProjectAgentRuntimeBinding{}, fmt.Errorf("set project agent runtime: runtime_id: %w", err)
		}
		runtimeUUID = parsed
	}
	row, err := sqlc.New(s.db).SetProjectAgentRuntime(ctx, sqlc.SetProjectAgentRuntimeParams{
		RuntimeID:      runtimeUUID,
		ProjectAgentID: paUUID,
		WorkspaceID:    wsUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectAgentRuntimeBinding{}, fmt.Errorf("%w: %s", ErrUnknownProjectAgent, input.ProjectAgentID)
		}
		return ProjectAgentRuntimeBinding{}, err
	}
	return ProjectAgentRuntimeBinding{
		ProjectAgentID: row.ProjectAgentID,
		WorkspaceID:    row.WorkspaceID,
		RuntimeID:      row.RuntimeID,
	}, nil
}

// ResolveAgentNameForConversation returns the display name of the Agent currently
// selected on a conversation. Per-card header fallback when the caller has no
// agent_run row in hand.
//
// Returns ("", nil) — never ErrNoRows — when the conversation is unknown, no agent
// has been /select-ed, the selected agent was soft-deleted, or conversationID is
// empty/unparseable. Callers fall back to gateway.FeishuCardTitle on empty.
func (s *Store) ResolveAgentNameForConversation(ctx context.Context, conversationID string) (string, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return "", nil
	}
	convUUID, err := uuid(conversationID)
	if err != nil {
		return "", nil
	}
	name, err := sqlc.New(s.db).ResolveAgentNameForConversation(ctx, convUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(name), nil
}
