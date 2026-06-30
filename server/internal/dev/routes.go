package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	authfeishu "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	gatewaypkg "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	e2bsandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

var mentionPattern = regexp.MustCompile(`@[\p{Han}A-Za-z0-9_-]+`)

type RuntimeStore interface {
	CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error)
	CompleteAgentRun(ctx context.Context, input store.CompleteAgentRunInput) (store.CompleteAgentRunResult, error)
	FailAgentRun(ctx context.Context, input store.FailAgentRunInput) error
	GetHTTPAgentRunInvocation(ctx context.Context, runID string) (store.AgentRunInvocation, error)
	ClaimNextQueuedHTTPAgentRun(ctx context.Context) (store.ClaimHTTPAgentRunResult, error)
	GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error)
	RequeueFailedAgentRun(ctx context.Context, input store.RequeueAgentRunInput) (store.RequeueAgentRunResult, error)
	ConfigureDevConversationExternalRef(ctx context.Context, input store.ConfigureDevConversationExternalRefInput) (store.ConfigureDevConversationExternalRefResult, error)
	ConfigureDevProjectAgentConnector(ctx context.Context, input store.ConfigureDevProjectAgentConnectorInput) (store.ConfigureDevProjectAgentConnectorResult, error)
	ConfigureProjectAgentProfile(ctx context.Context, input store.ConfigureProjectAgentProfileInput) (store.ConfigureDevProjectAgentConnectorResult, error)
	DisableProjectAgent(ctx context.Context, projectAgentID string) (store.ProjectAgentStatusRead, error)
	EnableProjectAgent(ctx context.Context, projectAgentID string) (store.ProjectAgentStatusRead, error)
	GetProjectAgentDetail(ctx context.Context, projectAgentID string) (store.ProjectAgentStatusRead, error)
	GetProjectAgentRuntimeBinding(ctx context.Context, workspaceID, projectAgentID string) (store.ProjectAgentRuntimeBinding, error)
	SetProjectAgentRuntime(ctx context.Context, input store.SetProjectAgentRuntimeInput) (store.ProjectAgentRuntimeBinding, error)
	GetWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error)
	GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (store.WorkspaceRuntimeSettingsRead, error)
	SetWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, secretID string, now time.Time) error
	ClearWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, name, kind string, now time.Time) error
	RegisterWorkspaceRuntimeCredential(ctx context.Context, input store.RegisterWorkspaceRuntimeCredentialInput) (store.SecretRead, error)
	PatchWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error)
	ListCapabilities(ctx context.Context, workspaceID string, filter store.ListCapabilityFilter) ([]store.CapabilityRead, error)
	ListMarketplaceCapabilities(ctx context.Context, targetWorkspaceID string) ([]store.MarketplaceCapabilityRead, error)
	ListWorkspaceMarketplaceInstalls(ctx context.Context, targetWorkspaceID string) ([]store.MarketplaceInstallRead, error)
	CountInstalls(ctx context.Context, sourceCapabilityID string) (int64, error)
	ListEnabledAgents(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) ([]store.EnabledMarketplaceAgentRead, error)
	CreateCapability(ctx context.Context, input store.CreateCapabilityInput) (store.CapabilityRead, error)
	GetCapability(ctx context.Context, capabilityID string) (store.CapabilityRead, error)
	UpdateCapability(ctx context.Context, input store.UpdateCapabilityInput) (store.CapabilityRead, error)
	SoftDeleteCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error)
	PublishCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error)
	UnpublishCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error)
	DeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error)
	UndeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (store.CapabilityRead, error)
	ListCapabilityVersions(ctx context.Context, capabilityID string) ([]store.CapabilityVersionRead, error)
	CreateCapabilityVersion(ctx context.Context, input store.CreateCapabilityVersionInput) (store.CapabilityVersionRead, error)
	GetCapabilityVersion(ctx context.Context, capabilityVersionID string) (store.CapabilityVersionRead, error)
	// --- capability import flow (plan M3) ---
	// ImportCapability runs the all-or-nothing import (capability +
	// capability_version + inline_secrets) in a single tx.
	ImportCapability(ctx context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error)
	// ImportCapabilityVersion is the version-only twin of ImportCapability:
	// adds a new version row to an already-existing capability, reusing the
	// canonical_spec + inline_secrets pipeline.
	ImportCapabilityVersion(ctx context.Context, input store.ImportCapabilityVersionInput) (store.ImportCapabilityResult, error)
	// credential_kinds CRUD.
	ListCredentialKinds(ctx context.Context) ([]store.CredentialKindRead, error)
	GetCredentialKindByCode(ctx context.Context, code string) (store.CredentialKindRead, error)
	CreateCredentialKind(ctx context.Context, input store.CreateCredentialKindInput) (store.CredentialKindRead, error)
	ListUserCredentials(ctx context.Context, userID string) ([]store.UserCredentialRead, error)
	CreateUserCredential(ctx context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error)
	GetUserCredential(ctx context.Context, credentialID string) (store.UserCredentialRead, error)
	UpdateUserCredential(ctx context.Context, input store.UpdateUserCredentialInput) (store.UserCredentialRead, error)
	SoftDeleteUserCredential(ctx context.Context, credentialID string) (store.UserCredentialRead, error)
	ListAgentCapabilities(ctx context.Context, projectAgentID string) ([]store.AgentCapabilityRead, error)
	GetEnabledMarketplaceCapabilitiesForAgent(ctx context.Context, projectAgentID string) ([]store.EnabledCapabilityRead, error)
	EnableAgentCapability(ctx context.Context, projectAgentID string, versionID string, configuration map[string]any, pinningMode string) (store.AgentCapabilityRead, error)
	UpgradeAgentCapability(ctx context.Context, projectAgentID string, capabilityID string, newVersionID string, pinningMode string) (store.AgentCapabilityRead, error)
	UninstallWorkspaceMarketplaceCapability(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) (int64, error)
	DeleteAgentCapability(ctx context.Context, projectAgentID string, capabilityVersionID string) error
	CreateAgent(ctx context.Context, input store.CreateAgentInput) (store.CreateAgentResult, error)
	GetAgent(ctx context.Context, agentID string) (store.AgentSummary, error)
	UpdateAgent(ctx context.Context, input store.UpdateAgentInput) (store.AgentSummary, []string, error)
	UpdateAgentVisibility(ctx context.Context, agentID, newVisibility, actorID string) (store.AgentVisibilityChange, error)
	GetFeishuConnectorDiagnostics(ctx context.Context, agentID string) (store.FeishuConnectorDiagnosticsRead, error)
	UpdateAgentFeishuConnector(ctx context.Context, input store.UpdateAgentFeishuConnectorInput, actorID string) (store.AgentFeishuConnectorChange, error)
	GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error)
	GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error)
	ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error)
	UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error
	GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error)
	// ClearGatewaySessionSelection wipes a stored selection — see the
	// matching method on router.Store. Forwarded here so the
	// HTTP webhook path satisfies router.Store when it dispatches
	// into HandleInbound for shared-bot routing mode.
	ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error
	FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error)
	IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error)
	// GetWorkspaceVisibility + ListActiveWorkspaceOwnerNames feed the
	// visibility=workspace rejection card so the Feishu sender sees
	// "管理员: A、B" plus a "申请加入 workspace" link. Both are read-
	// only and failures are swallowed by the gateway — the rejection
	// goes out regardless, just without the enrichment.
	GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error)
	ListActiveWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error)
	// FindConversationByExternalRef + CancelAllInflightForConversation
	// are forwarded so the HTTP webhook path satisfies router.Store
	// for the /cancel command — without these the shared-bot dispatch
	// site (RegisterRoutesWithStore → router.HandleInbound) fails
	// to compile.
	FindConversationByExternalRef(ctx context.Context, gateway, externalChatID, externalThreadID string) (string, error)
	CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]store.SupersededRun, error)
	// HasFeishuThreadInboundHistory reports whether the Feishu gateway
	// has previously stored an inbound message in (chat × thread). Used
	// to let users continue a 话题 (thread) conversation without re-
	// @mentioning the bot on every message.
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)
	DeleteProjectAgent(ctx context.Context, projectAgentID string, actorID string) (store.ProjectAgentSummary, error)
	DeleteAgent(ctx context.Context, agentID string, actorID string) (store.DeleteAgentResult, int64, error)
	ListProjectAgentsByAgentID(ctx context.Context, agentID string) ([]store.ProjectAgentSummary, error)
	CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error)
	ListSecrets(ctx context.Context, workspaceID string, limit int32) ([]store.SecretRead, error)
	DisableSecret(ctx context.Context, workspaceID string, secretID string) (store.SecretRead, error)
	CreateModel(ctx context.Context, input store.CreateModelInput) (store.ModelRead, error)
	DisableModel(ctx context.Context, workspaceID string, modelID string) (store.ModelRead, error)
	UpdateModel(ctx context.Context, input store.UpdateModelInput) (store.ModelRead, error)
	ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (store.ModelRuntime, error)
	ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (store.ModelRuntime, error)
	ListModels(ctx context.Context, workspaceID string, limit int32) ([]store.ModelRead, error)
	ListProjectEnabledAgents(ctx context.Context, projectID string) ([]store.ProjectAgentRead, error)
	ListProjectAgentsForAdmin(ctx context.Context, projectID string) ([]store.ProjectAgentRead, error)
	CreateProjectConversation(ctx context.Context, input store.CreateProjectConversationInput) (store.ConversationRead, error)
	ListProjectConversations(ctx context.Context, projectID string, agentID string, limit int32) ([]store.ConversationListItem, error)
	GetProjectConversation(ctx context.Context, conversationID string) (store.ConversationRead, error)
	UpdateConversationTitle(ctx context.Context, conversationID string, title string) error
	SoftDeleteConversation(ctx context.Context, conversationID string) error
	GetConversationTimeline(ctx context.Context, conversationID string, limit int32) (store.ConversationTimelineRead, error)
	SendUserMessageToConversation(ctx context.Context, input store.SendUserMessageToConversationInput) (store.SendUserMessageToConversationResult, error)
	GetAgentRun(ctx context.Context, runID string) (store.AgentRunDetailRead, error)
	CancelAgentRun(ctx context.Context, runID, reason string) (bool, error)
	ListAgentRunEvents(ctx context.Context, runID string, afterSequence int64) ([]store.AgentRunEventRead, error)
	ListActiveFeishuInflightConversations(ctx context.Context, cutoff time.Time, limit int32) ([]store.FeishuInflightConversation, error)
	MarkGatewayOutboundDelivered(ctx context.Context, input store.MarkGatewayOutboundDeliveredInput) (store.MarkGatewayOutboundDeliveredResult, error)
	ListProjectAgentRuns(ctx context.Context, projectID string, statuses []string, limit, offset int32) (store.ListProjectAgentRunsResult, error)
	GetProjectAgentMetrics(ctx context.Context, projectID, projectAgentID string, windowDays int32) (store.AgentMetricsRead, error)
	ListAuditRecords(ctx context.Context, filter store.ListAuditRecordsFilter, limit int32) ([]store.AuditRecordRead, error)
	ListProjectUsageLogs(ctx context.Context, projectID string, agentRunID string, limit int32) ([]store.UsageLogRead, error)
	ListWorkspaceMembers(ctx context.Context, workspaceID string, limit int32) ([]store.WorkspaceMemberRead, error)
	GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error)
	GetProjectWorkspace(ctx context.Context, projectID string) (string, error)
	GetUserByID(ctx context.Context, userID string) (store.UserRead, error)
	ListUserWorkspaces(ctx context.Context, userID string, limit int32) ([]store.UserWorkspaceRead, error)
	ListAllActiveWorkspaces(ctx context.Context, limit int32) ([]store.UserWorkspaceRead, error)
	ListWorkspaceProjects(ctx context.Context, workspaceID, userID string, limit int32) ([]store.WorkspaceProjectRead, error)
	ListWorkspaceProjectsForAdmin(ctx context.Context, workspaceID string, limit int32) ([]store.WorkspaceProjectRead, error)
	CreateWorkspace(ctx context.Context, input store.CreateWorkspaceInput) (store.CreateWorkspaceResult, error)
	UpdateWorkspace(ctx context.Context, input store.UpdateWorkspaceInput) (store.UserWorkspaceRead, error)
	ArchiveWorkspace(ctx context.Context, input store.ArchiveWorkspaceInput) (store.UserWorkspaceRead, error)
	CreateProject(ctx context.Context, input store.CreateProjectInput) (store.CreateProjectResult, error)
	UpdateProject(ctx context.Context, input store.UpdateProjectInput) (store.WorkspaceProjectRead, error)
	ArchiveProject(ctx context.Context, input store.ArchiveProjectInput) (store.WorkspaceProjectRead, error)
	AddWorkspaceMember(ctx context.Context, input store.AddWorkspaceMemberInput) (store.AddWorkspaceMemberResult, error)
	UpdateWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string, role string, now time.Time) (store.WorkspaceMemberRead, error)
	RemoveWorkspaceMember(ctx context.Context, workspaceID string, userID string, now time.Time) (store.RemoveWorkspaceMemberResult, error)
	SearchUsers(ctx context.Context, input store.SearchUsersInput) ([]store.SearchUsersResultItem, error)

	// 工作区主动申请加入(self-service join request)
	ListDiscoverableWorkspaces(ctx context.Context, input store.ListDiscoverableWorkspacesInput) (store.ListDiscoverableWorkspacesResult, error)
	ListPendingJoinRequests(ctx context.Context, workspaceID string) ([]store.PendingJoinRequestRead, error)
	CountPendingJoinRequests(ctx context.Context, workspaceID string) (int64, error)
	RequestJoinWorkspace(ctx context.Context, input store.RequestJoinWorkspaceInput) (store.RequestJoinWorkspaceResult, error)
	ApproveJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error)
	RejectJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error)
	WithdrawOwnJoinRequest(ctx context.Context, workspaceID, userID string, now time.Time) error

	// 定时任务(scheduled tasks)
	ListScheduledTasksByProjectAgent(ctx context.Context, projectAgentID string) ([]store.ScheduledTaskRead, error)
	ListScheduledTasksByProject(ctx context.Context, projectID string, limit, offset int32) (store.ListScheduledTasksByProjectResult, error)
	CreateScheduledTask(ctx context.Context, in store.CreateScheduledTaskInput) (store.ScheduledTaskRead, error)
	GetScheduledTask(ctx context.Context, taskID string) (store.ScheduledTaskRead, error)
	GetScheduledTaskScope(ctx context.Context, taskID string) (store.ScheduledTaskScope, error)
	UpdateScheduledTask(ctx context.Context, in store.UpdateScheduledTaskInput) (store.ScheduledTaskRead, error)
	SoftDeleteScheduledTask(ctx context.Context, taskID string) error
	RunScheduledTaskNow(ctx context.Context, taskID string) (string, error)
	ListAgentRunsByScheduledTask(ctx context.Context, taskID string, limit int32) ([]store.ScheduledTaskRunRead, error)
}

type FeishuAppRegistrationClient interface {
	Begin(ctx context.Context) (gatewaypkg.FeishuAppRegistrationBeginResult, error)
	Poll(ctx context.Context, deviceCode string, currentIntervalSec int, tenantBrand string) (gatewaypkg.FeishuAppRegistrationPollResult, error)
}

func RegisterRoutes(r chi.Router) {
	RegisterRoutesWithStore(r, nil)
}

// RouterOption tweaks the dev router wiring without breaking the
// existing two-arg signature. OpenCode connector wiring remains only
// for low-level dev smoke/admin surfaces; product run dispatch goes
// through connectorRegistry.
type RouterOption func(*routerConfig)

type routerConfig struct {
	openCodeConnector    connector.AgentConnector
	connectorRegistry    *connector.Registry
	authMiddleware       *auth.Middleware
	oauthDeps            *OAuthHandlerDeps
	githubConnectionDeps *GitHubConnectionDeps
	feishuWebhook        feishuWebhookConfig
	feishuRegistration   feishuRegistrationConfig
	// sandboxAdmin: store + daemonMgr. Either being nil makes the
	// corresponding handlers 503.
	sandboxAdmin sandboxAdminDeps
	// runtimeStatus: empty deps (zero-value Mode) makes the runtime
	// status handler 503 — a missing wiring means the operator should
	// be told the banner can't render, not silently see "local
	// always available".
	runtimeStatus RuntimeStatusDeps
	// auditIngester is used by handlers that emit audit events
	// directly. Nil falls through silently (audit is best-effort).
	auditIngester AuditIngester
	runBroker     *runstream.Broker
	// agentDaemonSandbox: lazy-create provider for sandbox-mode
	// project_agents. When wired, createAgent fires background Acquire
	// so the sandbox is ready before the first prompt. Nil → handler
	// degrades to lazy-create.
	agentDaemonSandbox AgentDaemonSandboxManager
	// dispatchCtx is the parent context handed to opencode run
	// dispatchers started by /start. cmd/server passes the server root
	// ctx so background goroutines exit cleanly on shutdown. Defaults
	// to context.Background() when unwired (fine for unit tests).
	dispatchCtx context.Context
	// ossClient backs the capability-plugin upload/download flow. Nil
	// means OSS is not configured — upload presign + plugin import
	// 503 with "OSS not configured" rather than failing boot.
	ossClient OSSClient

	// feishuJoinURLBuilder is invoked by the visibility=workspace
	// rejection card so the Feishu rejection surfaces a markdown
	// "申请加入 workspace" link. Nil keeps the card link-free and
	// falls back to "请联系上述管理员加入".
	feishuJoinURLBuilder func(workspaceID string) string
}

type feishuWebhookConfig struct {
	Enabled           bool
	MockEnabled       bool
	VerificationToken string
	EncryptKey        string
}

type feishuRegistrationConfig struct {
	Client         FeishuAppRegistrationClient
	OpenAPIBaseURL string
}

// WithConnectorRegistry injects the connector dispatch table used by
// streaming runs and cancellation. Production wiring lives in cmd/server.
func WithConnectorRegistry(r *connector.Registry) RouterOption {
	return func(cfg *routerConfig) {
		cfg.connectorRegistry = r
	}
}

// WithOpenCodeConnector wires the OpenCode connector for the legacy
// /dev/connectors/opencode/stream smoke endpoint.
func WithOpenCodeConnector(c connector.AgentConnector) RouterOption {
	return func(cfg *routerConfig) {
		cfg.openCodeConnector = c
	}
}

// WithAuthMiddleware injects the session middleware. When supplied
// only OAuth / seed bootstrap endpoints stay Optional; stateful /dev
// handlers are wrapped with Require so real cookie auth is the
// default happy path. PARSAR_DEV_AUTH=true keeps the explicit dev
// header shim available through the middleware for local testing.
//
// When omitted the dev router behaves exactly as before — useful for
// tests and demo scripts that construct the router without Postgres.
func WithAuthMiddleware(m *auth.Middleware) RouterOption {
	return func(cfg *routerConfig) {
		cfg.authMiddleware = m
	}
}

// WithOAuthHandlers wires the Feishu OIDC start / callback / logout
// handlers. cmd/server constructs the deps and passes them in; tests
// that don't drive auth flow leave this empty and the routes simply
// 503 with a friendly "not configured" message.
func WithOAuthHandlers(deps OAuthHandlerDeps) RouterOption {
	return func(cfg *routerConfig) {
		cfg.oauthDeps = &deps
	}
}

// WithGitHubConnectionHandlers wires the personal GitHub OAuth connection
// flow. Missing deps keep the routes disabled without affecting login.
func WithGitHubConnectionHandlers(deps GitHubConnectionDeps) RouterOption {
	return func(cfg *routerConfig) {
		cfg.githubConnectionDeps = &deps
	}
}

// WithFeishuWebhookSecurity wires Feishu event callback verification.
// Mock mode skips verification for local e2e/back-compat; prod mode
// validates token and optionally decrypts encrypted events.
func WithFeishuWebhookSecurity(mockEnabled bool, verificationToken string, encryptKey string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.feishuWebhook = feishuWebhookConfig{Enabled: true, MockEnabled: mockEnabled, VerificationToken: verificationToken, EncryptKey: encryptKey}
	}
}

func WithFeishuAppRegistration(client FeishuAppRegistrationClient, openAPIBaseURL string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.feishuRegistration = feishuRegistrationConfig{Client: client, OpenAPIBaseURL: strings.TrimSpace(openAPIBaseURL)}
	}
}

// WithSandboxLifecycle wires the admin sandbox lifecycle endpoints
// (GET /sandbox status, POST /sandbox/kill, POST /sandbox/rebuild).
// Both deps are required for kill / rebuild; status alone needs only
// the store. Either nil makes the corresponding endpoint return 503.
func WithRunStreamBroker(b *runstream.Broker) RouterOption {
	return func(cfg *routerConfig) {
		cfg.runBroker = b
	}
}

// WithDispatchContext injects the parent context that conversation
// run dispatchers (/start) inherit. cmd/server passes the server root
// ctx so background goroutines exit cleanly on shutdown. Tests/demos
// default to context.Background().
func WithDispatchContext(ctx context.Context) RouterOption {
	return func(cfg *routerConfig) {
		cfg.dispatchCtx = ctx
	}
}

// WithSandboxLifecycle wires the admin sandbox lifecycle endpoints.
// bindingStore backs all DB reads/writes; daemonMgr provides Release
// and Acquire for kill/rebuild. Either being nil makes the handlers
// degrade (store nil = 503; daemonMgr nil = DB-only mark, no E2B kill).
func WithSandboxLifecycle(bindingStore SandboxBindingStore, daemonMgr AgentDaemonSandboxManager) RouterOption {
	return func(cfg *routerConfig) {
		cfg.sandboxAdmin = sandboxAdminDeps{store: bindingStore, daemonMgr: daemonMgr}
	}
}

// WithRuntimeStatus wires the GET /workspaces/{wid}/runtime/status
// handler. Tests / demos that don't need the banner leave this unset
// and the route 503s.
func WithRuntimeStatus(deps RuntimeStatusDeps) RouterOption {
	return func(cfg *routerConfig) {
		cfg.runtimeStatus = deps
	}
}

// WithAuditIngester wires the audit ingester for dev handlers that
// emit audit events directly (without going through *store.Store).
func WithAuditIngester(ingester AuditIngester) RouterOption {
	return func(cfg *routerConfig) {
		cfg.auditIngester = ingester
	}
}

// AgentDaemonSandboxAcquirer is the subset of
// connagentdaemon.SandboxProvider the dev router needs for eager
// sandbox acquisition on agent creation and the manual /acquire
// endpoint. Narrow interface avoids importing the full connector
// package.
type AgentDaemonSandboxAcquirer interface {
	Acquire(ctx context.Context, in connector.PromptInput) (deviceID string, err error)
}

// AgentDaemonSandboxManager extends AgentDaemonSandboxAcquirer with
// status and lifecycle methods. *E2BSandboxProvider and
// NoopSandboxProvider both satisfy this interface.
type AgentDaemonSandboxManager interface {
	AgentDaemonSandboxAcquirer
	SandboxStatus(ctx context.Context, projectAgentID string) (connector.SandboxInfo, bool, error)
	Release(ctx context.Context, projectAgentID string) error
	Renew(ctx context.Context, projectAgentID string) (expiresAt time.Time, found bool, err error)
	// SandboxRuntimeInfo queries e2b directly for a sandbox's live
	// expiry, bypassing the in-memory cache. The admin status handler
	// uses this so a GET /sandbox that lands on a pod which did NOT
	// cold-start the sandbox can still surface expires_at.
	SandboxRuntimeInfo(ctx context.Context, sandboxID string) (expiresAt time.Time, err error)
}

// WithAgentDaemonSandbox wires the agent_daemon sandbox provider so
// that createAgent can fire eager Acquire and the manual /acquire
// endpoint works. nil is safe — createAgent falls back to lazy-create
// and /acquire returns 503.
func WithAgentDaemonSandbox(provider AgentDaemonSandboxManager) RouterOption {
	return func(cfg *routerConfig) {
		cfg.agentDaemonSandbox = provider
	}
}

// WithFeishuJoinURLBuilder wires the function the visibility=workspace
// rejection card uses to mint absolute "申请加入" URLs. Nil keeps the
// card link-free and falls back to "请联系上述管理员加入".
func WithFeishuJoinURLBuilder(builder func(workspaceID string) string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.feishuJoinURLBuilder = builder
	}
}

// runnerDeps is kept so the dev HTTP-agent endpoints retain their call
// shape. Step 5 makes httprunner HTTP-only, so no connector deps are
// passed from the router.
func (cfg *routerConfig) runnerDeps() *httprunner.Deps {
	return nil
}

func RegisterRoutesWithStore(r chi.Router, runtimeStore RuntimeStore, opts ...RouterOption) {
	cfg := &routerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.runBroker == nil {
		cfg.runBroker = runstream.NewBroker(runstream.DefaultBufferSize)
	}
	deps := cfg.runnerDeps()

	require := func(r chi.Router) {}
	if cfg.authMiddleware != nil {
		require = func(r chi.Router) { r.Use(cfg.authMiddleware.Require) }
	}

	// /dev — dev seed + fake/simulator systems ONLY. These have no
	// production role. In a production deployment they should be gated
	// off (PARSAR_DEV_AUTH / not wired). The real product API lives
	// under /api/v1 below.
	r.Route("/dev", func(r chi.Router) {
		// Public dev endpoints stay reachable before a session exists.
		// Optional is mounted inside this group so chi's middleware
		// ordering invariant holds on callers that registered other
		// routes on the outer mux.
		r.Group(func(r chi.Router) {
			if cfg.authMiddleware != nil {
				r.Use(cfg.authMiddleware.Optional)
			}
			r.Get("/seed", getSeed)
			r.Post("/auth/verify", verifyDevAuth)
		})
		r.Group(func(r chi.Router) {
			require(r)
			// Test/dev gateway message injection. Real inbound IM
			// arrives via /api/v1/feishu/events/message; this is the
			// generic injection path used by e2e + the devgateway tool.
			r.Post("/gateway/inbound", createGatewayInbound(runtimeStore))
			// Outbound delivery transport for the devgateway worker.
			r.Get("/gateway/outbound", listGatewayOutbound(runtimeStore))
			r.Post("/gateway/outbound/{messageID}/delivered", markGatewayOutboundDelivered(runtimeStore))
			r.Post("/http-agent/runs/{runID}/invoke", invokeHTTPAgentRun(runtimeStore, http.DefaultClient, deps))
			r.Post("/http-agent/runner/run-once", runHTTPAgentOnce(runtimeStore, http.DefaultClient, deps))
			// E2B-compatible sandbox smoke. Triggered from the admin
			// Runners page; takes a one-off API key, lifts a sandbox,
			// runs `command`, returns the result. Handler redacts the
			// supplied key from any error path before responding.
			r.Post("/sandboxes/e2b/smoke", smokeE2BSandbox)
			// SSE streaming surface so the Web UI can drive a prompt
			// and watch deltas land in real time. No agent_run
			// lifecycle — caller manages via existing dev endpoints.
			r.Post("/connectors/opencode/stream", streamConnectorPrompt(cfg))
		})
	})

	// /api/v1 — real product API. Mounted as a catch-all subrouter
	// alongside the flat /api/v1/health + /api/v1/bootstrap routes and
	// the deeper /api/v1/workspaces/{workspaceID}/runtimes +
	// /api/v1/runtimes/... mounts registered in cmd/server; chi resolves
	// the more-specific routes before falling through here.
	r.Route("/api/v1", func(r chi.Router) {
		// Public endpoints: reachable before a Parsar session exists.
		r.Group(func(r chi.Router) {
			if cfg.authMiddleware != nil {
				r.Use(cfg.authMiddleware.Optional)
			}
			// Feishu event callbacks are authenticated by Feishu's
			// verification token / optional encryption, not by a Parsar
			// browser session. Keep this public so Feishu can deliver URL
			// challenges and message events.
			r.Post("/feishu/events/message", createFeishuMessageEvent(runtimeStore, cfg.feishuWebhook, cfg.feishuJoinURLBuilder))
			if cfg.oauthDeps != nil {
				// Real Feishu OIDC login. start → redirect to Feishu;
				// callback → upsert user + bind auth_identity + issue
				// session cookie; logout → revoke + clear cookie.
				r.Get("/auth/feishu/start", feishuStartHandler(*cfg.oauthDeps))
				r.Get("/auth/feishu/callback", feishuCallbackHandler(*cfg.oauthDeps))
				r.Post("/auth/logout", authLogoutHandler(*cfg.oauthDeps))
			}
		})

		r.Group(func(r chi.Router) {
			require(r)
			if cfg.githubConnectionDeps != nil {
				r.Get("/connections/github/start", githubConnectionStartHandler(runtimeStore, *cfg.githubConnectionDeps))
				r.Get("/connections/github/callback", githubConnectionCallbackHandler(runtimeStore, *cfg.githubConnectionDeps))
			}
			r.Post("/agent-runs/{runID}/requeue", requeueAgentRun(runtimeStore))
			r.Post("/project-agents/{projectAgentID}/connector", configureProjectAgentConnector(runtimeStore))
			r.Post("/project-agents/{projectAgentID}/profile", configureProjectAgentProfile(runtimeStore))
			r.Post("/project-agents/{projectAgentID}/disable", disableProjectAgent(runtimeStore))
			r.Post("/project-agents/{projectAgentID}/enable", enableProjectAgent(runtimeStore))
			r.Delete("/project-agents/{projectAgentID}", deleteProjectAgent(runtimeStore))
			r.Get("/project-agents/{projectAgentID}/scheduled-tasks", listScheduledTasks(runtimeStore))
			r.Post("/project-agents/{projectAgentID}/scheduled-tasks", createScheduledTask(runtimeStore))
			r.Get("/projects/{projectID}/scheduled-tasks", listScheduledTasksByProject(runtimeStore))
			r.Get("/scheduled-tasks/{taskID}", getScheduledTask(runtimeStore))
			r.Patch("/scheduled-tasks/{taskID}", updateScheduledTask(runtimeStore))
			r.Delete("/scheduled-tasks/{taskID}", deleteScheduledTask(runtimeStore))
			r.Post("/scheduled-tasks/{taskID}/run-now", runScheduledTaskNow(runtimeStore))
			r.Get("/scheduled-tasks/{taskID}/runs", listScheduledTaskRuns(runtimeStore))
			// Runtime binding: user picks which Runtime this agent
			// runs on. Replaces the legacy auto-sandbox path.
			r.Get("/workspaces/{workspaceID}/project-agents/{projectAgentID}/runtime", getProjectAgentRuntimeBinding(runtimeStore))
			r.Put("/workspaces/{workspaceID}/project-agents/{projectAgentID}/runtime", setProjectAgentRuntimeBinding(runtimeStore))
			r.Patch("/agents/{agentID}", updateAgent(runtimeStore))
			r.Patch("/agents/{agentID}/visibility", updateAgentVisibility(runtimeStore))
			r.Get("/agents/{agentID}/connector/feishu/diagnostics", getAgentFeishuConnectorDiagnostics(runtimeStore))
			r.Patch("/agents/{agentID}/connector/feishu", updateAgentFeishuConnector(runtimeStore))
			r.Post("/agents/{agentID}/connector/feishu/provision/begin", beginAgentFeishuProvisioning(runtimeStore, cfg.feishuRegistration))
			r.Post("/agents/{agentID}/connector/feishu/provision/poll", pollAgentFeishuProvisioning(runtimeStore, cfg.feishuRegistration))
			r.Delete("/agents/{agentID}", deleteAgent(runtimeStore))
			// Sandbox lifecycle (admin UI). Scoped to (workspace,
			// project_agent). 503 when cfg.sandboxAdmin is not wired.
			// Reads require any active workspace member;
			// kill/rebuild (destructive — interrupts an in-flight run)
			// require owner/admin.
			r.Get("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox", gateWorkspaceMember(runtimeStore, getSandboxStatus(cfg.sandboxAdmin, cfg.agentDaemonSandbox)))
			r.Post("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox/kill", gateWorkspaceOwnerOrAdmin(runtimeStore, killSandbox(cfg.sandboxAdmin, runtimeStore, cfg.agentDaemonSandbox)))
			r.Post("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox/rebuild", gateWorkspaceOwnerOrAdmin(runtimeStore, rebuildSandbox(cfg.sandboxAdmin, runtimeStore, cfg.agentDaemonSandbox)))
			r.Post("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox/renew", gateWorkspaceOwnerOrAdmin(runtimeStore, renewSandbox(cfg.sandboxAdmin, cfg.agentDaemonSandbox)))
			r.Post("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox/acquire", gateWorkspaceOwnerOrAdmin(runtimeStore, acquireSandbox(cfg.sandboxAdmin, runtimeStore, cfg.agentDaemonSandbox)))
			r.Post("/workspaces/{workspaceID}/project-agents/{projectAgentID}/sandbox/test-connection", gateWorkspaceOwnerOrAdmin(runtimeStore, sandboxConnectivityTest(cfg.sandboxAdmin, cfg.auditIngester)))
			// Workspace-scoped overview: every active sandbox binding,
			// newest-active first. Backs the admin Sandboxes page.
			// 503 in local mode.
			r.Get("/workspaces/{workspaceID}/sandboxes", gateWorkspaceMember(runtimeStore, listSandboxes(cfg.sandboxAdmin)))
			// Runtime banner status. Tells the operator which runner
			// mode this server is wired for and (for sandbox mode)
			// whether the provider is reachable.
			r.Get("/workspaces/{workspaceID}/runtime/status", gateWorkspaceMember(runtimeStore, runtimeStatus(cfg.runtimeStatus)))
			r.Get("/capabilities/marketplace", listMarketplaceCapabilities(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities", listWorkspaceCapabilities(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities", createWorkspaceCapability(runtimeStore))
			// Capability import — preview is a pure parse; commit runs
			// the all-or-nothing materialization.
			r.Post("/workspaces/{workspaceID}/capabilities/import/preview", previewCapabilityImport(runtimeStore, cfg.ossClient))
			r.Post("/workspaces/{workspaceID}/capabilities/import/commit", commitCapabilityImport(runtimeStore, cfg.ossClient))
			// Plugin upload presign — browser PUTs the zip directly to
			// OSS, then calls import/commit with the returned ossKey.
			// presign-download checks ossKey belongs to the calling
			// workspace to close the cross-tenant read hole.
			r.Post("/workspaces/{workspaceID}/uploads/presign-upload", presignUpload(runtimeStore, cfg.ossClient))
			r.Post("/workspaces/{workspaceID}/uploads/presign-download", presignDownload(runtimeStore, cfg.ossClient))
			// Workspace-scoped credential_kinds CRUD (table is global;
			// scoped for RBAC consistency with import endpoints).
			r.Get("/workspaces/{workspaceID}/credential-kinds", listCredentialKinds(runtimeStore))
			r.Post("/workspaces/{workspaceID}/credential-kinds", createCredentialKind(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities/marketplace-installs", listWorkspaceMarketplaceInstalls(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/uninstall", uninstallWorkspaceMarketplaceCapability(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities/{capabilityID}", getWorkspaceCapability(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities/{capabilityID}/install-count", getCapabilityInstallCount(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities/{capabilityID}/enabled-agents", listMarketplaceEnabledAgents(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/capabilities/{capabilityID}", patchWorkspaceCapability(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/capabilities/{capabilityID}", deleteWorkspaceCapability(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/publish", publishWorkspaceCapability(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/unpublish", unpublishWorkspaceCapability(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/deprecate", deprecateWorkspaceCapability(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/undeprecate", undeprecateWorkspaceCapability(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities/{capabilityID}/versions", listWorkspaceCapabilityVersions(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/versions", createWorkspaceCapabilityVersion(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/versions/import/commit", commitCapabilityVersionImport(runtimeStore, cfg.ossClient))
			r.Post("/agent-runs/{runID}/cancel", cancelAgentRun(runtimeStore, cfg))
			r.Post("/conversations/{conversationID}/cancel-all", cancelConversationRuns(runtimeStore, cfg))
			r.Put("/workspaces/{workspaceID}/runtime/credential", gateWorkspaceOwnerOrAdmin(runtimeStore, putRuntimeCredential(runtimeStore)))
			r.Delete("/workspaces/{workspaceID}/runtime/credential", gateWorkspaceOwnerOrAdmin(runtimeStore, deleteRuntimeCredential(runtimeStore)))
			r.Post("/workspaces/{workspaceID}/secrets", createSecret(runtimeStore))
			r.Get("/workspaces/{workspaceID}/secrets", listSecrets(runtimeStore))
			r.Post("/workspaces/{workspaceID}/secrets/{secretID}/disable", disableSecret(runtimeStore))
			r.Post("/workspaces/{workspaceID}/models", createModel(runtimeStore))
			r.Get("/workspaces/{workspaceID}/models", listModels(runtimeStore))
			r.Post("/workspaces/{workspaceID}/models/{modelID}/disable", disableModel(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/models/{modelID}", updateModel(runtimeStore))
			r.Post("/workspaces/{workspaceID}/models/{modelID}/test", testModelConnectivity(runtimeStore))
			r.Post("/conversations/{conversationID}/external-ref", configureConversationExternalRef(runtimeStore))
			r.Get("/projects/{projectID}/agents", listProjectEnabledAgents(runtimeStore))
			r.Get("/projects/{projectID}/agent-runs", listProjectAgentRuns(runtimeStore))
			r.Get("/projects/{projectID}/agents/{projectAgentID}/metrics", getProjectAgentMetrics(runtimeStore))
			r.Get("/projects/{projectID}/agent-runs/{runID}/events", listAgentRunEvents(runtimeStore))
			r.Get("/projects/{projectID}/audit-records", listProjectAuditRecords(runtimeStore))
			r.Get("/projects/{projectID}/connectors", listProjectConnectors(runtimeStore))
			r.Get("/workspaces/{workspaceID}/gateways", listWorkspaceGateways())
			r.Get("/workspaces/{workspaceID}/members", listWorkspaceMembers(runtimeStore))
			r.Post("/workspaces/{workspaceID}/members", addWorkspaceMember(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/members/{userID}", updateWorkspaceMemberRole(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/members/{userID}", removeWorkspaceMember(runtimeStore))
			// 工作区主动申请加入(self-service join request):
			//   POST   /workspaces/{wid}/join-requests              用户提交申请(身份:已登录)
			//   DELETE /workspaces/{wid}/join-requests/mine          申请人自助撤回自己的 pending
			//   GET    /workspaces/{wid}/join-requests              owner/admin 看待审批清单
			//   POST   /workspaces/{wid}/join-requests/{rid}/approve owner/admin 同意
			//   POST   /workspaces/{wid}/join-requests/{rid}/reject  owner/admin 拒绝
			r.Post("/workspaces/{workspaceID}/join-requests", createJoinRequest(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/join-requests/mine", withdrawOwnJoinRequest(runtimeStore))
			r.Get("/workspaces/{workspaceID}/join-requests", listJoinRequests(runtimeStore))
			r.Post("/workspaces/{workspaceID}/join-requests/{requestID}/approve", approveJoinRequest(runtimeStore))
			r.Post("/workspaces/{workspaceID}/join-requests/{requestID}/reject", rejectJoinRequest(runtimeStore))
			r.Get("/workspaces/{workspaceID}/projects", listWorkspaceProjects(runtimeStore))
			r.Post("/workspaces/{workspaceID}/projects", createProject(runtimeStore))
			r.Get("/workspaces/{workspaceID}/settings", getWorkspaceSettings(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/settings", patchWorkspaceSettings(runtimeStore))
			r.Post("/workspaces/{workspaceID}/projects/{projectID}/agents", createAgent(runtimeStore, cfg.agentDaemonSandbox))
			r.Post("/workspaces", createWorkspace(runtimeStore))
			r.Patch("/workspaces/{workspaceID}", updateWorkspace(runtimeStore))
			r.Post("/workspaces/{workspaceID}/archive", archiveWorkspace(runtimeStore))
			r.Patch("/projects/{projectID}", updateProject(runtimeStore))
			r.Post("/projects/{projectID}/archive", archiveProject(runtimeStore))
			r.Get("/me", meHandler(runtimeStore))
			r.Get("/me/workspaces", listMyWorkspaces(runtimeStore))
			// 工作区发现:返回当前用户可申请加入的 public 工作区
			r.Get("/me/discoverable-workspaces", listDiscoverableWorkspaces(runtimeStore))
			// Platform-wide user picker. Backs the search box in
			// AddMember dialogs. Any authenticated user can search;
			// the actual add still requires workspace owner/admin.
			r.Get("/users/search", searchUsers(runtimeStore))
			r.Get("/me/credentials", listMyCredentials(runtimeStore))
			r.Post("/me/credentials", createMyCredential(runtimeStore))
			r.Patch("/me/credentials/{credentialID}", patchMyCredential(runtimeStore))
			r.Delete("/me/credentials/{credentialID}", deleteMyCredential(runtimeStore))
			r.Get("/projects/{projectID}/usage", listProjectUsageLogs(runtimeStore))
			r.Get("/projects/{projectID}/agents/{projectAgentID}/capabilities", listProjectAgentCapabilities(runtimeStore))
			r.Post("/projects/{projectID}/agents/{projectAgentID}/capabilities/{capabilityVersionID}/enable", enableProjectAgentCapability(runtimeStore))
			r.Post("/projects/{projectID}/agents/{projectAgentID}/capabilities/{capabilityID}/upgrade", upgradeProjectAgentCapability(runtimeStore))
			r.Delete("/projects/{projectID}/agents/{projectAgentID}/capabilities/{capabilityVersionID}", deleteProjectAgentCapability(runtimeStore))
			r.Get("/projects/{projectID}/conversations", listProjectConversations(runtimeStore))
			r.Post("/projects/{projectID}/conversations", createProjectConversation(runtimeStore))
			r.Get("/conversations/{conversationID}", getProjectConversation(runtimeStore))
			r.Patch("/conversations/{conversationID}", updateConversationTitle(runtimeStore))
			r.Delete("/conversations/{conversationID}", deleteConversation(runtimeStore))
			r.Get("/conversations/{conversationID}/timeline", getConversationTimeline(runtimeStore))
			r.Post("/conversations/{conversationID}/messages", createConversationUserMessage(runtimeStore))
			r.Post("/conversations/{conversationID}/runs/{runID}/start", startConversationAgentRun(runtimeStore, cfg))
			r.Get("/conversations/{conversationID}/runs/{runID}/stream", streamConversationAgentRun(runtimeStore, cfg))
			r.Get("/agent-runs/{runID}", getAgentRun(runtimeStore))
		})
	})
}

func getSeed(w http.ResponseWriter, r *http.Request) {
	// SeedData is the human-readable fixture (back-compat with
	// existing dev consumers). The `db` key carries the real DB UUIDs
	// `cmd/seeddev` writes so the admin frontend can auto-bind.
	seed := DefaultSeed()
	ids := store.DefaultDevFixtureIDs()
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace":     seed.Workspace,
		"users":         seed.Users,
		"agents":        seed.Agents,
		"conversations": seed.Conversations,
		// Deterministic DB UUIDs from store.DefaultDevFixtureIDs —
		// match exactly what `make seed-dev-db` inserts.
		"db": map[string]any{
			"workspace_id":    ids.WorkspaceID,
			"user_id":         ids.UserID,
			"project_id":      ids.ProjectID,
			"conversation_id": ids.ConversationID,
			"agents": map[string]string{
				"product_agent_id": ids.ProductAgentID,
				"backend_agent_id": ids.BackendAgentID,
				"test_agent_id":    ids.TestAgentID,
			},
		},
	})
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

func verifyDevAuth(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Code != DevVerificationCode {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid dev credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        "dev-token",
		"token_type":   "Bearer",
		"workspace_id": DefaultSeed().Workspace.ID,
		"user": map[string]string{
			"id":    "dev_admin",
			"email": req.Email,
			"name":  "Dev Admin",
		},
	})
}

type gatewayInboundRequest struct {
	Gateway           string              `json:"gateway"`
	Conversation      string              `json:"conversation"`
	Sender            string              `json:"sender"`
	Text              string              `json:"text"`
	ExternalChatID    string              `json:"external_chat_id"`
	ExternalUserID    string              `json:"external_user_id"`
	ExternalThreadID  string              `json:"external_thread_id"`
	ExternalMessageID string              `json:"external_message_id"`
	TargetAgentID     string              `json:"target_agent_id"`
	SourceAppID       string              `json:"source_app_id"`
	ConversationForm  string              `json:"conversation_form"`
	Message           gatewayMessage      `json:"message"`
	Actor             gatewayActor        `json:"actor"`
	ConversationRef   gatewayConversation `json:"conversation_ref"`
	Metadata          map[string]any      `json:"metadata"`
}

type gatewayMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type gatewayActor struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type gatewayConversation struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ThreadID string `json:"thread_id"`
}

type configureConversationExternalRefBody struct {
	Gateway          string `json:"gateway"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id"`
}

func createGatewayInbound(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req gatewayInboundRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		createGatewayInboundFromRequest(w, r, runtimeStore, req)
	}
}

func createFeishuMessageEvent(runtimeStore RuntimeStore, webhook feishuWebhookConfig, joinURLBuilder func(workspaceID string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if webhook.Enabled && !webhook.MockEnabled {
			decoded, isChallenge, challenge, err := verifyFeishuWebhookEvent(r.Context(), runtimeStore, body, webhook)
			if err != nil {
				switch {
				case errors.Is(err, authfeishu.ErrWebhookTokenMismatch):
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid feishu verification token"})
				case errors.Is(err, authfeishu.ErrWebhookDecryptFailed):
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to decrypt feishu event"})
				default:
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				}
				return
			}
			if isChallenge {
				writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
				return
			}
			body = decoded
		}
		event, err := gatewaypkg.FeishuInboundEventFromWebhook(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}

		// Tests and legacy dev shims may mount the Feishu endpoint without
		// a DB-backed store or may send the pre-v2 shape without header.app_id.
		// Keep the old normalization fallback there; real deployments go
		// through app_id -> Agent routing below.
		if runtimeStore == nil || strings.TrimSpace(event.AppID) == "" {
			var legacy gatewaypkg.FeishuMessageEvent
			if err := json.Unmarshal(body, &legacy); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
			inbound := gatewaypkg.NormalizeFeishuInbound(legacy)
			createGatewayInboundFromRequest(w, r, runtimeStore, gatewayInboundRequest{
				Gateway:         inbound.Gateway,
				Message:         gatewayMessage{ID: inbound.Message.ID, Text: inbound.Message.Text},
				Actor:           gatewayActor{ID: inbound.Actor.ID, Email: inbound.Actor.Email},
				ConversationRef: gatewayConversation{ID: inbound.ConversationRef.ID, Title: inbound.ConversationRef.Title, ThreadID: inbound.ConversationRef.ThreadID},
				Metadata:        inbound.Metadata,
			})
			return
		}

		route := feishuRuntimeRouter{store: runtimeStore}
		host, err := route.GetAgentByFeishuAppID(r.Context(), event.AppID)
		if err != nil {
			switch {
			case errors.Is(err, gatewaypkg.ErrFeishuRouterUnknownAgent):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown feishu app_id"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to route feishu inbound"})
			}
			return
		}
		if isFeishuSelfMessage(host.Config, event.SenderOpenID) {
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"accepted": false,
				"reason":   "bot_self_message",
			})
			return
		}
		if isFeishuGroupMessageWithoutBotMention(r.Context(), runtimeStore, host.Config, event) {
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"accepted": false,
				"reason":   "group_without_bot_mention",
			})
			return
		}
		hostCfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(host.Config)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode feishu connector"})
			return
		}
		if ok && router.IsSharedRoutingMode(hostCfg.RoutingMode) {
			reply := func(ctx context.Context, agent gatewaypkg.FeishuRouteAgent, _ gatewaypkg.InboundEvent, text string) error {
				return sendFeishuImmediateText(ctx, runtimeStore, agent, event, text)
			}
			outcome, err := router.HandleInbound(r.Context(), runtimeStore, host, gatewaypkg.NeutralFromFeishuEvent(event), reply, nil, gatewaypkg.GateConfig{JoinURLBuilder: joinURLBuilder})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to handle shared feishu bot inbound"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"shared":   true,
				"accepted": outcome.Accepted,
				"replied":  outcome.Replied,
				"reason":   outcome.Reason,
				"agent_id": outcome.AgentID,
			})
			return
		}

		decision, err := gatewaypkg.RouteInboundToAgent(r.Context(), route, gatewaypkg.NeutralFromFeishuEvent(event), host, gatewaypkg.GateConfig{JoinURLBuilder: joinURLBuilder})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to route feishu inbound"})
			return
		}
		if !decision.Decision.Allowed {
			replied := false
			if decision.Decision.ReplyHint != "" {
				if err := sendFeishuImmediateText(r.Context(), runtimeStore, decision.Agent, event, decision.Decision.ReplyHint); err != nil {
					log.Bg().Warn("feishu inbound rejection reply failed", "app_id", event.AppID, "chat_id", event.ChatID, "err", err)
				} else {
					replied = true
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":    "feishu",
				"accepted":   false,
				"replied":    replied,
				"reply_hint": decision.Decision.ReplyHint,
				"reason":     decision.Decision.Reason,
			})
			return
		}

		externalUserID := strings.TrimSpace(event.SenderUnionID)
		if externalUserID == "" {
			externalUserID = strings.TrimSpace(event.SenderOpenID)
		}
		conversationForm := "group"
		if strings.EqualFold(strings.TrimSpace(event.ChatType), "p2p") {
			conversationForm = "dm"
		}
		metadata := map[string]any{
			"chat_type":    event.ChatType,
			"tenant_key":   event.TenantKey,
			"sender_state": decision.SenderState,
			"message_type": event.MessageType,
			"raw_content":  event.RawContent,
			"root_id":      event.RootID,
			"parent_id":    event.ParentID,
			"thread_id":    event.ThreadID,
		}
		for key, value := range event.Metadata {
			if strings.TrimSpace(key) == "" || value == nil {
				continue
			}
			metadata[key] = value
		}
		if decision.Decision.GuestReplyHint != "" {
			metadata["guest_reply_hint"] = decision.Decision.GuestReplyHint
		}
		createGatewayInboundFromRequest(w, r, runtimeStore, gatewayInboundRequest{
			Gateway:          "feishu",
			Conversation:     router.ConversationTitle(decision.NormalizedText),
			ConversationForm: conversationForm,
			Text:             decision.NormalizedText,
			ExternalChatID:   event.ChatID,
			// ThreadKey (not ReplyAnchorMessageID): every inbound in
			// the same Feishu 话题 lands in the same Parsar
			// conversation. Mirrors gateway/router/router.go.
			ExternalThreadID:  event.ThreadKey(),
			ExternalMessageID: event.MessageID,
			ExternalUserID:    externalUserID,
			TargetAgentID:     decision.Agent.AgentID,
			SourceAppID:       event.AppID,
			Metadata:          metadata,
		})
	}
}

type feishuRuntimeRouter struct {
	store RuntimeStore
}

func (r feishuRuntimeRouter) GetAgentByFeishuAppID(ctx context.Context, appID string) (gatewaypkg.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gatewaypkg.FeishuRouteAgent{}, gatewaypkg.ErrFeishuRouterUnknownAgent
		}
		return gatewaypkg.FeishuRouteAgent{}, err
	}
	return gatewaypkg.FeishuRouteAgent{
		AgentID:       route.AgentID,
		WorkspaceID:   route.WorkspaceID,
		WorkspaceName: route.WorkspaceName,
		AgentName:     route.AgentName,
		AgentSlug:     route.AgentSlug,
		Visibility:    gatewaypkg.Visibility(route.Visibility),
		Config:        route.Config,
	}, nil
}

func (r feishuRuntimeRouter) GetAgentByID(ctx context.Context, agentID string) (gatewaypkg.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gatewaypkg.FeishuRouteAgent{}, gatewaypkg.ErrFeishuRouterUnknownAgent
		}
		return gatewaypkg.FeishuRouteAgent{}, err
	}
	return gatewaypkg.FeishuRouteAgent{
		AgentID:       route.AgentID,
		WorkspaceID:   route.WorkspaceID,
		WorkspaceName: route.WorkspaceName,
		AgentName:     route.AgentName,
		AgentSlug:     route.AgentSlug,
		Visibility:    gatewaypkg.Visibility(route.Visibility),
		Config:        route.Config,
	}, nil
}

func (r feishuRuntimeRouter) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	userID, err := r.store.FindUserIDByPlatformSubject(ctx, platform, subject)
	if err != nil {
		if errors.Is(err, store.ErrUnknownPlatformUser) {
			return "", gatewaypkg.ErrRouterUnknownUser
		}
		return "", err
	}
	return userID, nil
}

func (r feishuRuntimeRouter) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return r.store.IsActiveWorkspaceMember(ctx, workspaceID, userID)
}

func (r feishuRuntimeRouter) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	return r.store.GetWorkspaceVisibility(ctx, workspaceID)
}

func (r feishuRuntimeRouter) ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	return r.store.ListActiveWorkspaceOwnerNames(ctx, workspaceID, limit)
}

func verifyFeishuWebhookEvent(ctx context.Context, runtimeStore RuntimeStore, body []byte, webhook feishuWebhookConfig) ([]byte, bool, string, error) {
	decoded, isChallenge, challenge, err := authfeishu.VerifyAndDecodeEvent(body, webhook.VerificationToken, webhook.EncryptKey)
	if err == nil || !errors.Is(err, authfeishu.ErrWebhookTokenMismatch) {
		return decoded, isChallenge, challenge, err
	}
	if runtimeStore == nil || feishuEnvelopeEncrypted(body) {
		return nil, false, "", err
	}
	event, parseErr := gatewaypkg.FeishuInboundEventFromWebhook(body)
	if parseErr != nil || strings.TrimSpace(event.AppID) == "" {
		return nil, false, "", err
	}
	route, routeErr := runtimeStore.GetAgentByFeishuAppID(ctx, event.AppID)
	if routeErr != nil {
		return nil, false, "", err
	}
	cfg, ok, cfgErr := gatewaypkg.DecodeFeishuConnectorConfig(route.Config)
	if cfgErr != nil || !ok || !cfg.Enabled || strings.TrimSpace(cfg.VerificationTokenRef) == "" {
		return nil, false, "", err
	}
	verifyToken, tokenErr := loadFeishuSecretString(ctx, runtimeStore, route.WorkspaceID, cfg.VerificationTokenRef, "verification_token", "token", "value", "api_key")
	if tokenErr != nil {
		return nil, false, "", err
	}
	return authfeishu.VerifyAndDecodeEvent(body, verifyToken, "")
}

func feishuEnvelopeEncrypted(body []byte) bool {
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return strings.TrimSpace(envelope.Encrypt) != ""
}

func isFeishuSelfMessage(rawConfig []byte, senderOpenID string) bool {
	senderOpenID = strings.TrimSpace(senderOpenID)
	if senderOpenID == "" {
		return false
	}
	cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(rawConfig)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(cfg.BotOpenID) != "" && strings.TrimSpace(cfg.BotOpenID) == senderOpenID
}

// feishuThreadHistoryLookup is the narrow store surface
// isFeishuGroupMessageWithoutBotMention needs to support thread follow-up.
// It is satisfied by RuntimeStore (production) and by the
// feishuSecretRouteStore test double.
type feishuThreadHistoryLookup interface {
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)
}

// isFeishuGroupMessageWithoutBotMention decides whether a group-chat
// inbound should be silently dropped before any routing / storage work.
//
// Decision order (true = drop, false = let it through):
//  1. p2p chat → false.
//  2. mentions present: include bot_open_id → false; else → true.
//  3. no mentions in a group: if (chat_id, thread_id) has prior bot
//     history → false (话题续聊不必再 @); else → true.
//
// bot_open_id missing → bot defaults to refusing all group messages;
// operator must configure via the connector panel or provisioning.
func isFeishuGroupMessageWithoutBotMention(ctx context.Context, store feishuThreadHistoryLookup, rawConfig []byte, event gatewaypkg.FeishuInboundEvent) bool {
	chatType := strings.ToLower(strings.TrimSpace(event.ChatType))
	if chatType == "p2p" || chatType == "" {
		return false
	}
	// Other Feishu apps/bots post interactive cards whose "@bot" text
	// lives in the card body, never in message.mentions. Treat any
	// non-user sender as already-targeted at us.
	if event.IsBotSender() {
		return false
	}
	botOpenID := ""
	if cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(rawConfig); err == nil && ok {
		botOpenID = strings.TrimSpace(cfg.BotOpenID)
	}
	// Step 2 — mentions present.
	if len(event.MentionOpenIDs) > 0 {
		if botOpenID == "" {
			return true
		}
		for _, mentionedOpenID := range event.MentionOpenIDs {
			if strings.TrimSpace(mentionedOpenID) == botOpenID {
				return false
			}
		}
		// Mentions exist but bot is not among them — message is aimed
		// at another participant, do not respond.
		return true
	}
	// Step 3 — no mentions in a group. Check thread participation via
	// ThreadKey (thread_id → root_id → message_id fallback). ThreadKey
	// == MessageID for non-thread inbounds; brand-new top-level
	// messages have no conversation yet, so that branch is a no-op.
	threadKey := strings.TrimSpace(event.ThreadKey())
	if threadKey != "" && store != nil {
		hasHistory, err := store.HasFeishuThreadInboundHistory(ctx, strings.TrimSpace(event.ChatID), threadKey)
		if err == nil && hasHistory {
			return false
		}
		// Fail closed on lookup error: drop. The next @mention recovers.
	}
	return true
}

func sendFeishuImmediateText(ctx context.Context, runtimeStore RuntimeStore, agent gatewaypkg.FeishuRouteAgent, event gatewaypkg.FeishuInboundEvent, text string) error {
	if runtimeStore == nil {
		return errors.New("runtime store is not configured")
	}
	cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(agent.Config)
	if err != nil {
		return err
	}
	if !ok || !cfg.Enabled || strings.TrimSpace(cfg.AppSecretRef) == "" {
		return errors.New("feishu connector missing app_secret_ref")
	}
	appSecret, err := loadFeishuSecretString(ctx, runtimeStore, agent.WorkspaceID, cfg.AppSecretRef, "app_secret", "secret", "value", "api_key")
	if err != nil {
		return err
	}
	content, err := gatewaypkg.BuildFeishuInteractiveContent(text)
	if err != nil {
		return err
	}
	client, err := gatewaypkg.NewFeishuTenantClient(gatewaypkg.FeishuTenantClientOptions{
		AppID:   cfg.AppID,
		BaseURL: strings.TrimSpace(os.Getenv("PARSAR_FEISHU_OPENAPI_BASE_URL")),
	})
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if replyAnchor := event.ReplyAnchorMessageID(); replyAnchor != "" {
		_, err = client.ReplyMessage(sendCtx, appSecret, replyAnchor, gatewaypkg.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       content,
			ReplyInThread: true,
		})
		return err
	}
	chatID := strings.TrimSpace(event.ChatID)
	if chatID == "" {
		return errors.New("feishu inbound missing chat_id for immediate reply")
	}
	_, err = client.SendMessage(sendCtx, appSecret, gatewaypkg.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     chatID,
		MsgType:       "interactive",
		Content:       content,
	})
	return err
}

func loadFeishuSecretString(ctx context.Context, runtimeStore RuntimeStore, workspaceID, secretID string, keys ...string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", errors.New("secret id is required")
	}
	payload, err := runtimeStore.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil {
		return "", err
	}
	masterKey := strings.TrimSpace(os.Getenv("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return "", errors.New("PARSAR_MASTER_KEY env not set")
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return "", err
	}
	decoded, err := secretService.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if raw, ok := decoded[key].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw), nil
		}
	}
	return "", fmt.Errorf("secret %s payload missing expected string field", secretID)
}

func createGatewayInboundFromRequest(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, req gatewayInboundRequest) {
	if strings.TrimSpace(req.Gateway) == "" {
		req.Gateway = "dev"
	}
	normalizeGatewayInbound(&req)
	if strings.TrimSpace(req.Conversation) == "" && strings.TrimSpace(req.ExternalChatID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation or external_chat_id is required"})
		return
	}
	if strings.TrimSpace(req.Sender) == "" && strings.TrimSpace(req.ExternalUserID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sender or external_user_id is required"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	mentions := mentionPattern.FindAllString(req.Text, -1)
	if runtimeStore == nil {
		writeJSON(w, http.StatusCreated, map[string]any{
			"gateway":    req.Gateway,
			"message_id": fmt.Sprintf("gateway_msg_%d", time.Now().UnixNano()),
			"run_ids":    []string{},
			"mentions":   mentions,
			"created_at": time.Now().UTC(),
		})
		return
	}

	result, err := runtimeStore.CreateInboundIMMessage(r.Context(), store.CreateInboundIMMessageInput{
		ConversationTitle: req.Conversation,
		SenderEmail:       req.Sender,
		Text:              req.Text,
		Mentions:          mentions,
		Source:            "gateway",
		Gateway:           req.Gateway,
		ExternalUserID:    req.ExternalUserID,
		ExternalChatID:    req.ExternalChatID,
		ExternalThreadID:  req.ExternalThreadID,
		ExternalMessageID: req.ExternalMessageID,
		TargetAgentID:     req.TargetAgentID,
		SourceAppID:       req.SourceAppID,
		ConversationForm:  req.ConversationForm,
		Metadata:          req.Metadata,
	})
	if err != nil {
		if errors.Is(err, store.ErrUnknownMention) || errors.Is(err, store.ErrUnknownConversation) || errors.Is(err, store.ErrUnknownSender) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create gateway inbound message"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"gateway":    req.Gateway,
		"message_id": result.MessageID,
		"run_ids":    result.RunIDs,
		"mentions":   result.Mentions,
		"created_at": result.CreatedAt,
	})
}

func normalizeGatewayInbound(req *gatewayInboundRequest) {
	if strings.TrimSpace(req.Text) == "" {
		req.Text = req.Message.Text
	}
	if strings.TrimSpace(req.ExternalMessageID) == "" {
		req.ExternalMessageID = req.Message.ID
	}
	if strings.TrimSpace(req.Sender) == "" {
		req.Sender = req.Actor.Email
	}
	if strings.TrimSpace(req.ExternalUserID) == "" {
		req.ExternalUserID = req.Actor.ID
	}
	if strings.TrimSpace(req.Conversation) == "" {
		req.Conversation = req.ConversationRef.Title
	}
	if strings.TrimSpace(req.ExternalChatID) == "" {
		req.ExternalChatID = req.ConversationRef.ID
	}
	if strings.TrimSpace(req.ExternalThreadID) == "" {
		req.ExternalThreadID = req.ConversationRef.ThreadID
	}
}

type httpAgentInvokeBody struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
}

type configureProjectAgentConnectorBody struct {
	ConnectorType string `json:"connector_type"`
	Endpoint      string `json:"endpoint"`
	SecretID      string `json:"secret_id"`
	Model         string `json:"model"`
	ModelID       string `json:"model_id"`
	Workdir       string `json:"workdir"`
	SystemPrompt  string `json:"system_prompt"`
}

type createSecretBody struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Provider string         `json:"provider"`
	AuthType string         `json:"auth_type"`
	Payload  map[string]any `json:"payload"`
}

// createModelBody is the request body for POST /models in the new
// shared catalog. Provider info is inlined (no more model_providers
// table). Credential binding is one-of:
//   - credential_mode="inline_secret" + secret_id  → shared credential
//   - credential_mode="credential_ref" + credential_kind_code → per-user
//
// Capabilities/limits are accepted as optional top-level convenience
// fields and folded into config server-side.
type createModelBody struct {
	Name               string         `json:"name"`
	ProviderType       string         `json:"provider_type"`
	Adapter            string         `json:"adapter"`
	BaseURL            string         `json:"base_url"`
	ModelKey           string         `json:"model_key"`
	CredentialMode     string         `json:"credential_mode"`
	SecretID           string         `json:"secret_id"`
	CredentialKindCode string         `json:"credential_kind_code"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	Config             map[string]any `json:"config"`
}

// updateModelBody is the request body for PATCH /models/{id}.
// CredentialMode / ProviderType / Adapter are NOT editable here — to
// change them, create a new model.
type updateModelBody struct {
	Name               string         `json:"name"`
	ModelKey           string         `json:"model_key"`
	BaseURL            string         `json:"base_url"`
	SecretID           string         `json:"secret_id"`
	CredentialKindCode string         `json:"credential_kind_code"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	Config             map[string]any `json:"config"`
}

// foldModelConfig merges capabilities / limits into the config bag. Empty
// inputs are skipped so a caller that already nested them under config (or
// omitted them) is not clobbered with empty objects.
func foldModelConfig(config, capabilities, limits map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range config {
		merged[k] = v
	}
	if len(capabilities) > 0 {
		merged["capabilities"] = capabilities
	}
	if len(limits) > 0 {
		merged["limits"] = limits
	}
	return merged
}

type configureProjectAgentProfileBody struct {
	ModelID      string         `json:"model_id"`
	Workdir      string         `json:"workdir"`
	SystemPrompt string         `json:"system_prompt"`
	Config       map[string]any `json:"config"`
}

type requeueAgentRunBody struct {
	Reason string `json:"reason"`
}

type markGatewayOutboundDeliveredBody struct {
	DeliveryID string `json:"delivery_id"`
}

func invokeHTTPAgentRun(runtimeStore RuntimeStore, client *http.Client, deps *httprunner.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed http agent connector is disabled"})
			return
		}

		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}

		var req httpAgentInvokeBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}

		runHTTPAgentInvocation(w, r, runtimeStore, client, runID, req, deps)
	}
}

func runHTTPAgentOnce(runtimeStore RuntimeStore, client *http.Client, deps *httprunner.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed http runner is disabled"})
			return
		}

		result, err := httprunner.RunOnce(r.Context(), runtimeStore, client, deps)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgentRun):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrInvalidHTTPConnector), errors.Is(err, store.ErrAgentRunNotCompletable), errors.Is(err, store.ErrInvalidProjectAgent):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, httprunner.ErrInvalidEndpoint):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, httprunner.ErrRequestFailed), errors.Is(err, httprunner.ErrNon2xx), errors.Is(err, httprunner.ErrInvalidJSON):
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to run http agent once"})
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func runHTTPAgentInvocation(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, client *http.Client, runID string, req httpAgentInvokeBody, deps *httprunner.Deps) {
	result, err := httprunner.Invoke(r.Context(), runtimeStore, client, httprunner.InvokeInput{RunID: runID, Endpoint: req.Endpoint, Headers: req.Headers}, deps)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUnknownAgentRun):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, store.ErrInvalidHTTPConnector), errors.Is(err, store.ErrAgentRunNotCompletable), errors.Is(err, store.ErrInvalidProjectAgent):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, httprunner.ErrInvalidEndpoint):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, httprunner.ErrRequestFailed), errors.Is(err, httprunner.ErrNon2xx), errors.Is(err, httprunner.ErrInvalidJSON):
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to run http agent"})
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func configureProjectAgentConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed connector config is disabled"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), runtimeStore, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		var req configureProjectAgentConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.ConnectorType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connector_type is required"})
			return
		}
		if req.ConnectorType == "http" && !isSafeHTTPAgentEndpoint(req.Endpoint) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "http connector endpoint must be an http(s) URL"})
			return
		}

		result, err := runtimeStore.ConfigureDevProjectAgentConnector(r.Context(), store.ConfigureDevProjectAgentConnectorInput{
			ProjectAgentID: projectAgentID,
			ConnectorType:  req.ConnectorType,
			Endpoint:       req.Endpoint,
			SecretID:       req.SecretID,
			Model:          req.Model,
			ModelID:        req.ModelID,
			Workdir:        req.Workdir,
			SystemPrompt:   req.SystemPrompt,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrInvalidConnectorType):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownProjectAgent):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure project agent connector"})
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func configureProjectAgentProfile(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed project agent profile config is disabled"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), runtimeStore, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req configureProjectAgentProfileBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := runtimeStore.ConfigureProjectAgentProfile(r.Context(), store.ConfigureProjectAgentProfileInput{ProjectAgentID: projectAgentID, ModelID: req.ModelID, Workdir: req.Workdir, SystemPrompt: req.SystemPrompt, Config: req.Config})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownProjectAgent):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownModel):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrModelDisabled):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure project agent profile"})
			}
			return
		}

		// Local-device binding: mirror createAgent so editing a
		// local-mode agent's bound device keeps pa.runtime_id in sync.
		// Without this the admin list keeps reading "未绑定 Runtime".
		if result.ProjectAgentConfig != nil {
			if mode, _ := result.ProjectAgentConfig["daemon_mode"].(string); mode == "local" {
				if deviceID, _ := result.ProjectAgentConfig["device_id"].(string); strings.TrimSpace(deviceID) != "" {
					detail, detailErr := runtimeStore.GetProjectAgentDetail(r.Context(), projectAgentID)
					if detailErr != nil {
						log.Bg().Warn("configureProjectAgentProfile: workspace lookup failed for runtime_id sync",
							"project_agent_id", projectAgentID, "err", detailErr)
					} else if _, bindErr := runtimeStore.SetProjectAgentRuntime(r.Context(), store.SetProjectAgentRuntimeInput{
						WorkspaceID:    detail.WorkspaceID,
						ProjectAgentID: projectAgentID,
						RuntimeID:      deviceID,
					}); bindErr != nil {
						log.Bg().Warn("configureProjectAgentProfile: persist local device runtime_id failed",
							"project_agent_id", projectAgentID,
							"device_id", deviceID,
							"err", bindErr)
					}
				}
			}
		}

		writeJSON(w, http.StatusOK, result)
	}
}

func disableProjectAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return projectAgentStatusHandler(runtimeStore, "disable")
}

func enableProjectAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return projectAgentStatusHandler(runtimeStore, "enable")
}

// getProjectAgentRuntimeBinding returns the runtime currently bound to
// this project_agent. Empty runtime_id means the user hasn't picked one
// yet — the dispatcher surfaces "请绑定 Runtime" when a run starts.
func getProjectAgentRuntimeBinding(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed project agent runtime binding is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		binding, err := runtimeStore.GetProjectAgentRuntimeBinding(r.Context(), workspaceID, projectAgentID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownProjectAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read runtime binding"})
			return
		}
		writeJSON(w, http.StatusOK, binding)
	}
}

// setProjectAgentRuntimeBinding writes (or clears) the runtime a
// project_agent runs on. RuntimeID="" is a valid clear request that
// turns the agent back into an unbound state. Tenant guard: only
// project owners / workspace admins can change the binding.
func setProjectAgentRuntimeBinding(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed project agent runtime binding is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), runtimeStore, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var body struct {
			RuntimeID string `json:"runtime_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if v := strings.TrimSpace(body.RuntimeID); v != "" && !isUUID(v) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime_id must be a valid uuid or empty"})
			return
		}
		binding, err := runtimeStore.SetProjectAgentRuntime(r.Context(), store.SetProjectAgentRuntimeInput{
			WorkspaceID:    workspaceID,
			ProjectAgentID: projectAgentID,
			RuntimeID:      strings.TrimSpace(body.RuntimeID),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownProjectAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set runtime binding"})
			return
		}
		writeJSON(w, http.StatusOK, binding)
	}
}

type createAgentCapabilityBody struct {
	CapabilityVersionID string         `json:"capability_version_id"`
	Configuration       map[string]any `json:"configuration"`
	// PinningMode is "latest" or "pinned". Empty falls back to store's
	// default (pinned); create-agent dialog sends "latest" for new
	// bindings unless the user picks a specific version.
	PinningMode string `json:"pinning_mode,omitempty"`
}

// createAgentInlineSecretBody describes one new shared secret the user
// asked to materialise during agent creation. The handler creates the
// secret via store.CreateSecret, then patches its id into
// req.Config.credential_bindings[Kind] (or model_credential_binding when
// IsModel=true) before delegating to runtimeStore.CreateAgent.
type createAgentInlineSecretBody struct {
	Kind        string `json:"kind"`
	IsModel     bool   `json:"is_model"`
	DisplayName string `json:"display_name"`
	Plaintext   string `json:"plaintext"`
}

type createAgentBody struct {
	Name                string                        `json:"name"`
	Description         string                        `json:"description"`
	ConnectorType       string                        `json:"connector_type"`
	SystemPrompt        string                        `json:"system_prompt"`
	DefaultModelID      string                        `json:"default_model_id"`
	Capabilities        []string                      `json:"capabilities"`
	InitialCapabilities []createAgentCapabilityBody   `json:"initial_capabilities"`
	Visibility          string                        `json:"visibility"`
	Runtime             string                        `json:"runtime"`
	Config              map[string]any                `json:"config"`
	InlineNewSecrets    []createAgentInlineSecretBody `json:"inline_new_secrets"`
	Slug                string                        `json:"slug"`
}

type updateAgentBody struct {
	Name             *string                       `json:"name"`
	Description      *string                       `json:"description"`
	ConnectorType    *string                       `json:"connector_type"`
	SystemPrompt     *string                       `json:"system_prompt"`
	DefaultModelID   *string                       `json:"default_model_id"`
	Capabilities     []string                      `json:"capabilities"`
	Config           map[string]any                `json:"config"`
	InlineNewSecrets []createAgentInlineSecretBody `json:"inline_new_secrets"`
	Slug             *string                       `json:"slug"`
	WorkspaceID      *string                       `json:"workspace_id"`
}

func getWorkspaceSettings(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if runtimeStore == nil || !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.GetWorkspaceSettings(r.Context(), workspaceID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func patchWorkspaceSettings(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if runtimeStore == nil || !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		if _, err := decodeJSONWithFields(r, &struct{}{}); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := runtimeStore.PatchWorkspaceSettings(r.Context(), workspaceID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func createAgent(runtimeStore RuntimeStore, agentDaemonSandbox AgentDaemonSandboxAcquirer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, projectID := strings.TrimSpace(chi.URLParam(r, "workspaceID")), strings.TrimSpace(chi.URLParam(r, "projectID"))
		if runtimeStore == nil || !isUUID(workspaceID) || !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and project_id must be valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createAgentBody
		hasCaps, err := decodeJSONWithField(r, &req, "capabilities")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ConnectorType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and connector_type are required"})
			return
		}
		if strings.TrimSpace(req.Runtime) != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "runtime is no longer accepted; use config.daemon_mode, config.device_id, and config.agent_kind for agent_daemon agents"})
			return
		}

		// Materialise any inline_new_secrets the user pasted in step 3.
		// Each one becomes a capability_inline secret in the org-global
		// catalog; its id is then patched into the corresponding
		// credential_bindings entry (or model_credential_binding when
		// IsModel=true) inside req.Config so CreateAgent persists a
		// fully-resolved binding map. Failure here is fatal — the agent
		// is not created, the secrets that did succeed are left as
		// orphans (we explicitly chose not to clean them up).
		if cfg, ok := materialiseInlineSecrets(r.Context(), runtimeStore, req.Config, req.InlineNewSecrets, actorIDFromRequest(r)); ok {
			req.Config = cfg
		} else if len(req.InlineNewSecrets) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to materialise inline_new_secrets"})
			return
		}

		// Enforce visibility ⇄ binding consistency. Public agents may
		// not depend on any personal credential (no platform user_id
		// for lark guests); tenant agents are allowed but warned in UI.
		// 422 (not 400) so the FE can distinguish "semantically wrong"
		// from "malformed body" — same convention as updateAgent.
		if err := validateAgentVisibilityBindings(req.Visibility, req.Config); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		initialCapabilities := make([]store.InitialAgentCapabilityInput, 0, len(req.InitialCapabilities))
		for _, capability := range req.InitialCapabilities {
			initialCapabilities = append(initialCapabilities, store.InitialAgentCapabilityInput{CapabilityVersionID: capability.CapabilityVersionID, Configuration: capability.Configuration, PinningMode: capability.PinningMode})
		}
		result, err := runtimeStore.CreateAgent(r.Context(), store.CreateAgentInput{WorkspaceID: workspaceID, ProjectID: projectID, Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, DefaultModelID: req.DefaultModelID, Capabilities: req.Capabilities, CapabilitiesSet: hasCaps, InitialCapabilities: initialCapabilities, Runtime: "", ProjectAgentConfig: req.Config, Visibility: req.Visibility, Slug: req.Slug, CreatedBy: actorIDFromRequest(r)})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)

		// Sync capability checkboxes → agent_capabilities table so the
		// runtime's GetEnabledCapabilitiesForAgent sees them.
		if hasCaps && len(req.Capabilities) > 0 {
			if err := syncAgentCapabilities(r.Context(), runtimeStore, result.Agent.WorkspaceID, result.ProjectAgent.ID, req.Capabilities); err != nil {
				log.Bg().Warn("createAgent: capability sync failed", "project_agent_id", result.ProjectAgent.ID, "err", err)
			}
		}

		// Local-device binding: when the user picked a paired daemon
		// in the create form, the device_id sits in pa.config but
		// pa.runtime_id stays NULL. Mirror device_id → runtime_id so
		// the FK join lights up. device_id IS a runtime.id.
		if result.ProjectAgent.Config != nil {
			if mode, _ := result.ProjectAgent.Config["daemon_mode"].(string); mode == "local" {
				if deviceID, _ := result.ProjectAgent.Config["device_id"].(string); strings.TrimSpace(deviceID) != "" {
					if _, bindErr := runtimeStore.SetProjectAgentRuntime(r.Context(), store.SetProjectAgentRuntimeInput{
						WorkspaceID:    workspaceID,
						ProjectAgentID: result.ProjectAgent.ID,
						RuntimeID:      deviceID,
					}); bindErr != nil {
						// Non-fatal: row is created, the user can
						// re-save from the edit dialog to retry.
						log.Bg().Warn("createAgent: persist local device runtime_id failed",
							"project_agent_id", result.ProjectAgent.ID,
							"device_id", deviceID,
							"err", bindErr)
					}
				}
			}
		}

		// Eager sandbox provisioning: kick off Acquire so the sandbox
		// is ready before the user sends their first message. On
		// success, persist deviceID to project_agents.runtime_id —
		// without this write the connector's "user must bind a
		// runtime first" guard would reject the very first prompt.
		// Failure is non-fatal: the row is saved and SandboxPanel
		// (or a follow-up Rebuild) gives the admin a recovery surface.
		if agentDaemonSandbox != nil && result.ProjectAgent.Config != nil {
			if mode, _ := result.ProjectAgent.Config["daemon_mode"].(string); mode == "sandbox" {
				paID := result.ProjectAgent.ID
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					deviceID, err := agentDaemonSandbox.Acquire(ctx, connector.PromptInput{
						ProjectAgentID: paID,
						WorkspaceID:    workspaceID,
						ProjectID:      projectID,
					})
					if err != nil {
						log.Bg().Warn("eager sandbox acquire failed",
							"project_agent_id", paID, "err", err)
						return
					}
					if _, bindErr := runtimeStore.SetProjectAgentRuntime(ctx, store.SetProjectAgentRuntimeInput{
						WorkspaceID:    workspaceID,
						ProjectAgentID: paID,
						RuntimeID:      deviceID,
					}); bindErr != nil {
						// Sandbox is alive but runtime_id write failed.
						// Dispatch shows "未绑定 Runtime" until a retry
						// succeeds or admin Rebuild rewrites.
						log.Bg().Error("eager sandbox acquired but runtime_id persist failed",
							"project_agent_id", paID,
							"device_id", deviceID,
							"err", bindErr)
						return
					}
					log.Bg().Info("eager sandbox acquired and runtime bound",
						"project_agent_id", paID, "device_id", deviceID)
				}()
			}
		}
	}
}

func updateAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentBody
		fields, err := decodeJSONWithFields(r, &req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		hasCaps := fields["capabilities"]
		if fields["runtime"] {
			// runtime is immutable post-create — recreate the agent to
			// change runtime (it determines whether the agent runs in
			// cloud sandbox or on local subprocess and is tied to
			// every previous conversation's execution environment).
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "runtime is immutable post-create; recreate the agent to change runtime"})
			return
		}
		if req.Slug != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "slug is immutable"})
			return
		}
		if req.WorkspaceID != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "workspace_id is immutable"})
			return
		}
		// Materialise inline secrets + validate visibility ⇄ binding consistency
		// when the FE sends new credential bindings. Without this, edits that
		// switch the shared secret (or introduce a new one) never reach
		// agents.agent_config and the runtime keeps resolving the old binding.
		//
		// Same failure trade-off as createAgent: materialiseInlineSecrets is
		// "commit-each-as-you-go", and a downstream failure (mid-list secret
		// create, or visibility validation below) leaves the earlier secrets
		// dangling in the workspace. Edit makes this slightly worse because
		// users typically retry after fixing the offending field, accruing one
		// orphan per retry. We accept it for symmetry with create; a future
		// pass could wrap the chain in a tx + rollback.
		configChanged := fields["config"] || len(req.InlineNewSecrets) > 0
		if configChanged {
			if cfg, ok := materialiseInlineSecrets(r.Context(), runtimeStore, req.Config, req.InlineNewSecrets, actorIDFromRequest(r)); ok {
				req.Config = cfg
			} else if len(req.InlineNewSecrets) > 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to materialise inline_new_secrets"})
				return
			}
			if err := validateAgentVisibilityBindings(agent.Visibility, req.Config); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
		}
		updated, _, err := runtimeStore.UpdateAgent(r.Context(), store.UpdateAgentInput{AgentID: agentID, ActorID: actorIDFromRequest(r), Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, DefaultModelID: req.DefaultModelID, Capabilities: req.Capabilities, CapabilitiesSet: hasCaps, Config: req.Config, ConfigSet: configChanged})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent": updated})

		// Sync capability checkboxes → agent_capabilities table.
		if hasCaps {
			projectAgents, err := runtimeStore.ListProjectAgentsByAgentID(r.Context(), agentID)
			if err != nil {
				log.Bg().Warn("updateAgent: list project_agents for capability sync failed", "agent_id", agentID, "err", err)
			} else {
				for _, pa := range projectAgents {
					if err := syncAgentCapabilities(r.Context(), runtimeStore, updated.WorkspaceID, pa.ID, req.Capabilities); err != nil {
						log.Bg().Warn("updateAgent: capability sync failed", "project_agent_id", pa.ID, "err", err)
					}
				}
			}
		}
	}
}

// syncAgentCapabilities reconciles the agent_capabilities table with
// the capability name list from the agent edit form. Errors on
// individual capabilities are logged and skipped.
func syncAgentCapabilities(
	ctx context.Context,
	rs RuntimeStore,
	workspaceID string,
	projectAgentID string,
	capabilityNames []string,
) error {
	// 1. Current state on this project_agent.
	existing, err := rs.ListAgentCapabilities(ctx, projectAgentID)
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list existing: %w", err)
	}
	existingByCapID := make(map[string]store.AgentCapabilityRead, len(existing))
	for _, ac := range existing {
		existingByCapID[ac.CapabilityID] = ac
	}

	// 2. Resolve desired names. A name can come from this workspace's own
	// capabilities, OR from the marketplace (a public capability published
	// by another workspace and surfaced in the agent picker's marketplace
	// section). Local capabilities win on name collision: a user shadowing
	// a marketplace name with a private one should keep using their own.
	allCaps, err := rs.ListCapabilities(ctx, workspaceID, store.ListCapabilityFilter{})
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list capabilities: %w", err)
	}
	marketplaceCaps, err := rs.ListMarketplaceCapabilities(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list marketplace capabilities: %w", err)
	}
	type resolved struct {
		capabilityID    string
		latestVersionID string
		fromMarketplace bool
	}
	capByName := make(map[string]resolved, len(allCaps)+len(marketplaceCaps))
	for _, c := range allCaps {
		capByName[c.Name] = resolved{capabilityID: c.ID}
	}
	for _, m := range marketplaceCaps {
		if m.SelfPublished {
			continue
		}
		if _, exists := capByName[m.Name]; exists {
			continue
		}
		capByName[m.Name] = resolved{capabilityID: m.CapabilityID, latestVersionID: m.LatestVersionID, fromMarketplace: true}
	}

	desiredCapIDs := make(map[string]bool, len(capabilityNames))
	for _, name := range capabilityNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cap, ok := capByName[name]
		if !ok {
			log.Bg().Warn("syncAgentCapabilities: capability not found, skipping",
				"name", name, "workspace_id", workspaceID, "project_agent_id", projectAgentID)
			continue
		}
		desiredCapIDs[cap.capabilityID] = true

		// Already enabled — don't auto-upgrade version.
		if _, exists := existingByCapID[cap.capabilityID]; exists {
			continue
		}

		latestVersionID := cap.latestVersionID
		if latestVersionID == "" {
			// Local capability: marketplace row already carries the version,
			// but ListCapabilities does not, so we fetch lazily here.
			versions, err := rs.ListCapabilityVersions(ctx, cap.capabilityID)
			if err != nil {
				log.Bg().Warn("syncAgentCapabilities: list versions failed, skipping",
					"capability_id", cap.capabilityID, "name", name, "err", err)
				continue
			}
			if len(versions) == 0 {
				log.Bg().Warn("syncAgentCapabilities: no versions found, skipping",
					"capability_id", cap.capabilityID, "name", name)
				continue
			}
			latestVersionID = versions[0].ID // sorted created_at desc
		}

		// 默认 pinning_mode 取决于来源:
		//   * 本地 capability:用户的预期是"勾上就跟最新"。reupload 后
		//     无需再编辑 agent,本地工作坊里的 skill 迭代也不会有
		//     breaking change 风险(同一团队拥有)。
		//   * marketplace:发布者的新版本可能携带 breaking change,
		//     保留 pinned 让 UpgradeCapabilityDialog 主动确认路径继续
		//     有效,用户得显式从 picker 选 latest 才会自动跟随。
		mode := store.PinningModeLatest
		if cap.fromMarketplace {
			mode = store.PinningModePinned
		}
		if _, err := rs.EnableAgentCapability(ctx, projectAgentID, latestVersionID, nil, mode); err != nil {
			log.Bg().Warn("syncAgentCapabilities: enable failed, skipping",
				"capability_id", cap.capabilityID, "name", name, "version_id", latestVersionID, "err", err)
			continue
		}
		log.Bg().Info("syncAgentCapabilities: enabled capability",
			"project_agent_id", projectAgentID, "capability_id", cap.capabilityID, "name", name, "version_id", latestVersionID, "from_marketplace", cap.fromMarketplace, "pinning_mode", mode)
	}

	// 3. Remove capabilities no longer in the desired list.
	for capID, ac := range existingByCapID {
		if desiredCapIDs[capID] {
			continue
		}
		if err := rs.DeleteAgentCapability(ctx, projectAgentID, ac.CapabilityVersionID); err != nil {
			log.Bg().Warn("syncAgentCapabilities: delete failed, skipping",
				"capability_id", capID, "capability_version_id", ac.CapabilityVersionID, "err", err)
			continue
		}
		log.Bg().Info("syncAgentCapabilities: removed capability",
			"project_agent_id", projectAgentID, "capability_id", capID, "capability_version_id", ac.CapabilityVersionID)
	}
	return nil
}

// updateAgentVisibilityBody is the request body for
// PATCH /api/v1/agents/{agentID}/visibility.
type updateAgentVisibilityBody struct {
	Visibility string `json:"visibility"`
}

// updateAgentVisibility flips an Agent's visibility between
// workspace / tenant / public. Owner/admin only. Identical visibility
// is treated as a 200 noop so idempotent replays don't pollute audit.
func updateAgentVisibility(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentVisibilityBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpdateAgentVisibility(r.Context(), agentID, req.Visibility, actorIDFromRequest(r))
		if err != nil {
			if errors.Is(err, store.ErrInvalidAgentVisibility) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"visibility": change})
	}
}

// updateAgentFeishuConnectorBody is the request body for
// PATCH /api/v1/agents/{agentID}/connector/feishu.
type updateAgentFeishuConnectorBody struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
	RoutingMode          string `json:"routing_mode"`
}

type pollAgentFeishuProvisioningBody struct {
	DeviceCode  string `json:"device_code"`
	IntervalSec int    `json:"interval_sec"`
	TenantBrand string `json:"tenant_brand"`
}

type feishuProvisioningResponse struct {
	Status          string                                       `json:"status"`
	Begin           *gatewaypkg.FeishuAppRegistrationBeginResult `json:"begin,omitempty"`
	NextIntervalSec int                                          `json:"next_interval_sec,omitempty"`
	Error           string                                       `json:"error,omitempty"`
	Description     string                                       `json:"description,omitempty"`
	AppID           string                                       `json:"app_id,omitempty"`
	AppSecretRef    string                                       `json:"app_secret_ref,omitempty"`
	BotOpenID       string                                       `json:"bot_open_id,omitempty"`
	BotName         string                                       `json:"bot_name,omitempty"`
	FeishuConnector *store.AgentFeishuConnectorChange            `json:"feishu_connector,omitempty"`
}

// getAgentFeishuConnectorDiagnostics returns a read-only Feishu Bot
// observation snapshot for admins and workspace members. Unlike the
// write path, this is a read/debug surface and never exposes secret refs.
func getAgentFeishuConnectorDiagnostics(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		diagnostics, err := runtimeStore.GetFeishuConnectorDiagnostics(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
	}
}

// updateAgentFeishuConnector binds (or rebinds) an Agent to a Feishu
// Bot self-built app — writes agents.config.connectors.feishu so the
// inbound router and outbound worker can resolve this Agent.
// RBAC: workspace owner / admin (a misconfigured Bot can leak the
// workspace to the internet).
func updateAgentFeishuConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentFeishuConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpdateAgentFeishuConnector(r.Context(), store.UpdateAgentFeishuConnectorInput{
			AgentID:              agentID,
			Enabled:              req.Enabled,
			AppID:                req.AppID,
			AppSecretRef:         req.AppSecretRef,
			VerificationTokenRef: req.VerificationTokenRef,
			EncryptKeyRef:        req.EncryptKeyRef,
			BotOpenID:            req.BotOpenID,
			EventMode:            req.EventMode,
			RoutingMode:          req.RoutingMode,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				// api-client.ts copies the JSON `error` field into
				// both envelope.code and .message on the frontend, so
				// the discriminator lives in `error`. `detail` carries
				// human-readable text for logs/devtools.
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
					"error":  "feishu_connector_incomplete",
					"detail": err.Error(),
				})
				return
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{
					"error":  "feishu_app_id_in_use",
					"detail": err.Error(),
				})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"feishu_connector": change})
	}
}

func beginAgentFeishuProvisioning(runtimeStore RuntimeStore, cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		if _, ok := agentForFeishuConnectorWrite(w, r, runtimeStore); !ok {
			return
		}
		begin, err := cfg.Client.Begin(r.Context())
		if err != nil {
			log.Bg().Warn("feishu app registration begin failed", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_begin_failed"})
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", Begin: &begin, NextIntervalSec: begin.Interval})
	}
}

func pollAgentFeishuProvisioning(runtimeStore RuntimeStore, cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		agent, ok := agentForFeishuConnectorWrite(w, r, runtimeStore)
		if !ok {
			return
		}
		var req pollAgentFeishuProvisioningBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.DeviceCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_code is required"})
			return
		}
		status, err := cfg.Client.Poll(r.Context(), req.DeviceCode, req.IntervalSec, req.TenantBrand)
		if err != nil {
			log.Bg().Warn("feishu app registration poll failed", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_poll_failed"})
			return
		}
		switch status.Kind {
		case gatewaypkg.FeishuAppRegistrationPollPending:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", NextIntervalSec: status.NextIntervalSec})
			return
		case gatewaypkg.FeishuAppRegistrationPollError:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "error", Error: status.Error, Description: status.Description})
			return
		case gatewaypkg.FeishuAppRegistrationPollSuccess:
			// proceed below
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_unknown_status"})
			return
		}

		appID := strings.TrimSpace(status.ClientID)
		appSecret := strings.TrimSpace(status.ClientSecret)
		if appID == "" || appSecret == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_missing_credentials"})
			return
		}
		if ok := assertFeishuAppIDAvailableForAgent(w, r.Context(), runtimeStore, appID, agent.ID); !ok {
			return
		}

		botInfo, err := validateProvisionedFeishuBot(r.Context(), appID, appSecret, feishuOpenAPIBaseURL(cfg.OpenAPIBaseURL))
		if err != nil {
			log.Bg().Warn("feishu provisioned bot validation failed", "app_id", appID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_bot_validation_failed"})
			return
		}
		secret, err := createFeishuAppSecretFromProvisioning(r.Context(), runtimeStore, agent.WorkspaceID, agent.Name, appID, appSecret, actorIDFromRequest(r))
		if err != nil {
			log.Bg().Warn("feishu provisioned app secret write failed", "app_id", appID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed_to_store_feishu_app_secret"})
			return
		}
		change, err := runtimeStore.UpdateAgentFeishuConnector(r.Context(), store.UpdateAgentFeishuConnectorInput{
			AgentID:      agent.ID,
			Enabled:      true,
			AppID:        appID,
			AppSecretRef: secret.ID,
			BotOpenID:    botInfo.OpenID,
			EventMode:    "websocket",
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use", "detail": err.Error()})
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "feishu_connector_incomplete", "detail": err.Error()})
			default:
				writeStoreAgentError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{
			Status:          "success",
			AppID:           appID,
			AppSecretRef:    secret.ID,
			BotOpenID:       botInfo.OpenID,
			BotName:         botInfo.AppName,
			FeishuConnector: &change,
		})
	}
}

func agentForFeishuConnectorWrite(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (store.AgentSummary, bool) {
	agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
	if runtimeStore == nil || !isUUID(agentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
		return store.AgentSummary{}, false
	}
	agent, err := runtimeStore.GetAgent(r.Context(), agentID)
	if err != nil {
		writeStoreAgentError(w, err)
		return store.AgentSummary{}, false
	}
	if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
		writeRBACError(w, err)
		return store.AgentSummary{}, false
	}
	return agent, true
}

func assertFeishuAppIDAvailableForAgent(w http.ResponseWriter, ctx context.Context, runtimeStore RuntimeStore, appID, agentID string) bool {
	existing, err := runtimeStore.GetAgentByFeishuAppID(ctx, appID)
	switch {
	case err == nil:
		if existing.AgentID != agentID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use"})
			return false
		}
	case errors.Is(err, store.ErrUnknownFeishuAgent):
		return true
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed_to_check_feishu_app_id"})
		return false
	}
	return true
}

func validateProvisionedFeishuBot(ctx context.Context, appID, appSecret, openAPIBaseURL string) (gatewaypkg.FeishuBotInfo, error) {
	client, err := gatewaypkg.NewFeishuTenantClient(gatewaypkg.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: openAPIBaseURL,
	})
	if err != nil {
		return gatewaypkg.FeishuBotInfo{}, err
	}
	validateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return client.BotInfo(validateCtx, appSecret)
}

func createFeishuAppSecretFromProvisioning(ctx context.Context, runtimeStore RuntimeStore, workspaceID, agentName, appID, appSecret, actorID string) (store.SecretRead, error) {
	masterKey := strings.TrimSpace(os.Getenv("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return store.SecretRead{}, errors.New("PARSAR_MASTER_KEY env not set")
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return store.SecretRead{}, err
	}
	payload := map[string]any{
		"app_id":     appID,
		"app_secret": appSecret,
		"source":     "feishu_qr_provisioning",
	}
	encrypted, err := secretService.Encrypt(payload)
	if err != nil {
		return store.SecretRead{}, err
	}
	name := "Feishu Bot App Secret"
	if strings.TrimSpace(agentName) != "" {
		name = fmt.Sprintf("%s Feishu Bot App Secret", strings.TrimSpace(agentName))
	}
	return runtimeStore.CreateSecret(ctx, store.CreateSecretInput{
		WorkspaceID: workspaceID,
		Name:        name,
		Kind:        "feishu_app_secret",
		Provider:    "feishu",
		AuthType:    "app_secret",
		Payload:     payload,
		Masked:      secrets.MaskPayload(payload),
		CreatedBy:   actorID,
	}, encrypted)
}

func feishuOpenAPIBaseURL(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	if v := strings.TrimSpace(os.Getenv("PARSAR_FEISHU_OPENAPI_BASE_URL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(authfeishu.EnvAPIBase))
}

func deleteProjectAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if runtimeStore == nil || !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		detail, ok := projectIDForProjectAgent(w, r.Context(), runtimeStore, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, detail); err != nil {
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.DeleteProjectAgent(r.Context(), projectAgentID, actorIDFromRequest(r))
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project_agent": result})
	}
}

func deleteAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, runCount, err := runtimeStore.DeleteAgent(r.Context(), agentID, actorIDFromRequest(r))
		if errors.Is(err, store.ErrInFlightAgentRuns) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "in_flight_runs", "run_count": runCount})
			return
		}
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func projectAgentStatusHandler(runtimeStore RuntimeStore, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed project agent lifecycle is disabled"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		projectID, ok := projectIDForProjectAgent(w, r.Context(), runtimeStore, projectAgentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var (
			result store.ProjectAgentStatusRead
			err    error
		)
		switch action {
		case "disable":
			result, err = runtimeStore.DisableProjectAgent(r.Context(), projectAgentID)
		case "enable":
			result, err = runtimeStore.EnableProjectAgent(r.Context(), projectAgentID)
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unsupported project agent action"})
			return
		}
		if err != nil {
			if errors.Is(err, store.ErrUnknownProjectAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to %s project agent", action)})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func createSecret(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createSecretBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.AuthType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, provider, and auth_type are required"})
			return
		}
		serverMasterKey := os.Getenv("PARSAR_MASTER_KEY")
		if serverMasterKey == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server has no PARSAR_MASTER_KEY configured; refusing to create a secret"})
			return
		}
		secretService, err := secrets.New(serverMasterKey)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		encrypted, err := secretService.Encrypt(req.Payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encrypt secret"})
			return
		}
		secret, err := runtimeStore.CreateSecret(r.Context(), store.CreateSecretInput{
			WorkspaceID: workspaceID,
			Name:        req.Name,
			Kind:        req.Kind,
			Provider:    req.Provider,
			AuthType:    req.AuthType,
			Payload:     req.Payload,
			Masked:      secrets.MaskPayload(req.Payload),
		}, encrypted)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create secret"})
			return
		}
		writeJSON(w, http.StatusCreated, secret)
	}
}

func listSecrets(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		limit := parseLimit(r, 100)
		secrets, err := runtimeStore.ListSecrets(r.Context(), workspaceID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list secrets"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
	}
}

func disableSecret(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		secretID := strings.TrimSpace(chi.URLParam(r, "secretID"))
		if !isUUID(secretID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "secret_id must be a valid uuid"})
			return
		}
		secret, err := runtimeStore.DisableSecret(r.Context(), workspaceID, secretID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownSecret) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to disable secret"})
			return
		}
		writeJSON(w, http.StatusOK, secret)
	}
}

// ============================================================
func createModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		// Model catalog is org-global; URL workspaceID is only used
		// for RBAC. The created model is NOT scoped to it.
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createModelBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		mode := strings.TrimSpace(req.CredentialMode)
		if mode == "" {
			mode = "inline_secret"
		}
		model, err := runtimeStore.CreateModel(r.Context(), store.CreateModelInput{
			Name:               req.Name,
			ProviderType:       req.ProviderType,
			Adapter:            req.Adapter,
			BaseURL:            req.BaseURL,
			ModelKey:           req.ModelKey,
			CredentialMode:     mode,
			SecretID:           req.SecretID,
			CredentialKindCode: req.CredentialKindCode,
			Config:             foldModelConfig(req.Config, req.Capabilities, req.Limits),
			CreatedBy:          actorIDFromRequest(r),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create model"})
			return
		}
		writeJSON(w, http.StatusCreated, model)
	}
}

func disableModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		model, err := runtimeStore.DisableModel(r.Context(), workspaceID, modelID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to disable model"})
			return
		}
		writeJSON(w, http.StatusOK, model)
	}
}

func updateModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		var req updateModelBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if strings.TrimSpace(req.ModelKey) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_key is required"})
			return
		}
		model, err := runtimeStore.UpdateModel(r.Context(), store.UpdateModelInput{
			ModelID:            modelID,
			Name:               req.Name,
			ModelKey:           req.ModelKey,
			BaseURL:            req.BaseURL,
			SecretID:           req.SecretID,
			CredentialKindCode: req.CredentialKindCode,
			Config:             foldModelConfig(req.Config, req.Capabilities, req.Limits),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update model"})
			return
		}
		writeJSON(w, http.StatusOK, model)
	}
}

// testModelHTTPClient is overridable by tests so the unit tests can
// point the connectivity check at a httptest.Server instead of
// reaching the real upstream.
var testModelHTTPClient = &http.Client{Timeout: 15 * time.Second}

func isOpenAIChatCompletionsAdapter(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case "openai", "openai_compatible", "openai-compatible", "@ai-sdk/openai", "@ai-sdk/openai-compatible":
		return true
	default:
		return false
	}
}

func isAnthropicMessagesAdapter(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case "anthropic", "anthropic_compatible", "anthropic-compatible", "@ai-sdk/anthropic":
		return true
	default:
		return false
	}
}

func anthropicMessagesURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(base, "/v1/messages"), strings.HasSuffix(base, "/messages"):
		return base
	case strings.HasSuffix(base, "/v1"):
		return base + "/messages"
	default:
		return base + "/v1/messages"
	}
}

type connectivityTestResponse struct {
	Supported bool   `json:"supported"`
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latency_ms"`
	Status    int    `json:"http_status,omitempty"`
	Error     string `json:"error,omitempty"`
	Sample    string `json:"sample,omitempty"`
}

// testModelConnectivity sends a minimal request to the upstream
// provider so the admin can verify base_url + api_key + custom headers
// + model_key without driving a full Agent Run. OpenAI-shaped adapters
// use chat-completions; Anthropic-shaped use Messages. Other protocols
// return supported=false.
func testModelConnectivity(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}

		// ResolveModelRuntimeForUser handles both modes in one shot.
		// For credential_ref mode passing "" returns ErrModelDisabled,
		// which we surface as supported=false below.
		callerUserID := actorIDFromRequest(r)
		if callerUserID == "" {
			callerUserID = ""
		}
		mr, err := runtimeStore.ResolveModelRuntimeForUser(r.Context(), modelID, callerUserID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if errors.Is(err, store.ErrModelDisabled) {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     err.Error(),
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve model"})
			return
		}

		isOpenAICompatible := isOpenAIChatCompletionsAdapter(mr.Adapter)
		isAnthropicCompatible := isAnthropicMessagesAdapter(mr.Adapter)

		if !isOpenAICompatible && !isAnthropicCompatible {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: false,
				Error:     "connectivity test only supports OpenAI chat-completions and Anthropic messages compatible providers",
			})
			return
		}

		// Pick the encrypted payload:
		//   inline_secret  → fetched via secret_id below.
		//   credential_ref → filled from the caller's user_credentials.
		var encryptedPayload []byte
		if mr.CredentialMode == "credential_ref" {
			encryptedPayload = mr.EncryptedPayload
		} else {
			if mr.SecretID == "" {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     "no API key bound to this model",
				})
				return
			}
			sp, err := runtimeStore.GetSecretPayload(r.Context(), workspaceID, mr.SecretID)
			if err != nil {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     fmt.Sprintf("failed to fetch secret: %v", err),
				})
				return
			}
			encryptedPayload = sp.EncryptedPayload
		}

		secretService, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "secrets service unavailable: " + err.Error()})
			return
		}
		payload, err := secretService.Decrypt(encryptedPayload)
		if err != nil {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "failed to decrypt credential: " + err.Error(),
			})
			return
		}
		// Two payload shapes coexist:
		//   `secrets` rows (inline_secret) carry {api_key: "..."}
		//   `user_credentials` rows (credential_ref) carry {value: "..."}
		// Both encode an upstream-provider API key; accept either.
		apiKey, _ := payload["api_key"].(string)
		if strings.TrimSpace(apiKey) == "" {
			if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
				apiKey = v
			}
		}
		if strings.TrimSpace(apiKey) == "" {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "credential payload missing api_key / value field",
			})
			return
		}

		// Build a minimal protocol-shaped request.
		url := ""
		body := map[string]any{}
		if isAnthropicCompatible {
			url = anthropicMessagesURL(mr.BaseURL)
			body = map[string]any{
				"model":      mr.ModelKey,
				"messages":   []map[string]any{{"role": "user", "content": "ping"}},
				"max_tokens": 16,
			}
		} else {
			url = strings.TrimRight(mr.BaseURL, "/") + "/chat/completions"
			body = map[string]any{
				"model":      mr.ModelKey,
				"messages":   []map[string]any{{"role": "user", "content": "ping"}},
				"max_tokens": 16,
			}
		}
		bodyBytes, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, strings.NewReader(string(bodyBytes)))
		if err != nil {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "failed to build request: " + err.Error(),
			})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if isAnthropicCompatible {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		// Provider-level custom headers (e.g. X-Sub-Module for an internal gateway).
		if hdrs, ok := mr.ProviderConfig["headers"].(map[string]any); ok {
			for k, v := range hdrs {
				if s, ok := v.(string); ok {
					req.Header.Set(k, s)
				}
			}
		}

		start := time.Now()
		resp, err := testModelHTTPClient.Do(req)
		latency := time.Since(start).Milliseconds()
		if err != nil {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				LatencyMS: latency,
				Error:     "request failed: " + err.Error(),
			})
			return
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)

		var parsed map[string]any
		_ = json.Unmarshal(respBytes, &parsed)

		// HTTP 200 != business success — many gateways respond 200 with
		// an `error` object inside the body. Treat non-2xx OR
		// `error` field in body as failure.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed["error"] != nil {
			msg := strings.TrimSpace(string(respBytes))
			if eo, ok := parsed["error"].(map[string]any); ok {
				if m, ok := eo["message"].(string); ok {
					msg = m
				}
			}
			if len(msg) > 500 {
				msg = msg[:500] + "…"
			}
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				LatencyMS: latency,
				Status:    resp.StatusCode,
				Error:     msg,
			})
			return
		}

		// Pull a sample first message content for the UI to display.
		sample := ""
		if isAnthropicCompatible {
			if content, ok := parsed["content"].([]any); ok {
				for _, part := range content {
					if p, ok := part.(map[string]any); ok {
						if text, ok := p["text"].(string); ok && strings.TrimSpace(text) != "" {
							sample = strings.TrimSpace(text)
							break
						}
					}
				}
			} else if content, ok := parsed["content"].(string); ok {
				sample = strings.TrimSpace(content)
			}
		} else if choices, ok := parsed["choices"].([]any); ok && len(choices) > 0 {
			if first, ok := choices[0].(map[string]any); ok {
				if msg, ok := first["message"].(map[string]any); ok {
					if c, ok := msg["content"].(string); ok {
						sample = strings.TrimSpace(c)
					}
				}
			}
		}
		if len(sample) > 200 {
			sample = sample[:200] + "…"
		}
		writeJSON(w, http.StatusOK, connectivityTestResponse{
			Supported: true,
			Success:   true,
			LatencyMS: latency,
			Status:    resp.StatusCode,
			Sample:    sample,
		})
	}
}

func listModels(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		models, err := runtimeStore.ListModels(r.Context(), workspaceID, parseLimit(r, 100))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list models"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
	}
}

func requeueAgentRun(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed run retry is disabled"})
			return
		}
		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}

		var req requeueAgentRunBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, run.ProjectID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "manual_retry"
		}
		result, err := runtimeStore.RequeueFailedAgentRun(r.Context(), store.RequeueAgentRunInput{RunID: runID, Source: "dev_retry", Reason: reason})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgentRun):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrAgentRunNotCompletable):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to requeue agent run"})
			}
			return
		}
		if recorder, ok := runtimeStore.(runLifecycleEventRecorder); ok {
			recordRunLifecycleEvent(recorder, runID, "run.requeued", map[string]any{"source": "dev_retry", "reason": reason, "previous_status": run.Status}, time.Now().UTC())
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func configureConversationExternalRef(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation mapping is disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conversation, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to load conversation")
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, conversation.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req configureConversationExternalRefBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Gateway) == "" {
			req.Gateway = "dev"
		}
		if strings.TrimSpace(req.ExternalChatID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "external_chat_id is required"})
			return
		}
		result, err := runtimeStore.ConfigureDevConversationExternalRef(r.Context(), store.ConfigureDevConversationExternalRefInput{
			ConversationID:   conversationID,
			Gateway:          req.Gateway,
			ExternalChatID:   req.ExternalChatID,
			ExternalThreadID: req.ExternalThreadID,
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownConversation) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure conversation external ref"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func isSafeHTTPAgentEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func listProjectEnabledAgents(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		includeDisabled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_disabled")), "true")
		var (
			agents []store.ProjectAgentRead
			err    error
		)
		if includeDisabled {
			agents, err = runtimeStore.ListProjectAgentsForAdmin(r.Context(), projectID)
		} else {
			agents, err = runtimeStore.ListProjectEnabledAgents(r.Context(), projectID)
		}
		if err != nil {
			writeReadError(w, err, "failed to list project agents")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project_id": projectID, "agents": agents})
	}
}

func getConversationTimeline(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		// Timeline doesn't carry project_id, so reverse-lookup the
		// parent conversation to find the project to authorise against.
		conv, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to load conversation for rbac check")
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, conv.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}

		timeline, err := runtimeStore.GetConversationTimeline(r.Context(), conversationID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to get conversation timeline")
			return
		}
		writeJSON(w, http.StatusOK, timeline)
	}
}

func getAgentRun(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}

		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		// Load first to discover the parent project, then gate.
		if err := requireWorkspaceMemberByProject(r, runtimeStore, run.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
	}
}

// listGatewayOutbound dumps a slice of the inflight-card driver state
// for ops / smoke tests. Each row carries conversation_id / agent_run_id
// so ops can correlate against agent_runs + conversations.
func listGatewayOutbound(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed gateway outbound is disabled"})
			return
		}
		gateway := strings.TrimSpace(r.URL.Query().Get("gateway"))
		// inflightCutoffWindow is ~5m; debug surface uses a longer
		// look-back, with the limit capping response size.
		cutoff := time.Now().UTC().Add(-1 * time.Hour)
		convs, err := runtimeStore.ListActiveFeishuInflightConversations(r.Context(), cutoff, parseLimit(r, 100))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list gateway inflight conversations"})
			return
		}
		if gateway == "" {
			gateway = "feishu"
		}
		writeJSON(w, http.StatusOK, map[string]any{"gateway": gateway, "inflight": inflightDeliveries(convs)})
	}
}

func inflightDeliveries(convs []store.FeishuInflightConversation) []map[string]any {
	deliveries := make([]map[string]any, 0, len(convs))
	for _, c := range convs {
		row := map[string]any{
			"conversation_id":    c.ConversationID,
			"workspace_id":       c.WorkspaceID,
			"project_id":         c.ProjectID,
			"agent_run_id":       c.AgentRunID,
			"external_chat_id":   c.ExternalChatID,
			"external_thread_id": c.ExternalThreadID,
			"source_app_id":      c.SourceAppID,
			"run_status":         c.RunStatus,
			"max_seq":            c.MaxEventSequence,
		}
		// Pull the working slot (msg id / retry triad / seq_emitted)
		// out of conversation gateway_inflight metadata.
		if inflight, ok := c.ConversationMetadata["gateway_inflight"].(map[string]any); ok {
			if working, ok := inflight["working"].(map[string]any); ok {
				row["working_msg_id"] = working["external_msg_id"]
				row["seq_emitted"] = working["seq_emitted"]
				if v, ok := working["attempts"]; ok {
					row["working_attempts"] = v
				}
				if v, ok := working["last_error"]; ok {
					row["working_last_error"] = v
				}
				if v, ok := working["next_retry_at"]; ok {
					row["working_next_retry_at"] = v
				}
			}
		}
		deliveries = append(deliveries, row)
	}
	return deliveries
}

func markGatewayOutboundDelivered(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed gateway outbound is disabled"})
			return
		}
		messageID := strings.TrimSpace(chi.URLParam(r, "messageID"))
		if !isUUID(messageID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_id must be a valid uuid"})
			return
		}
		var req markGatewayOutboundDeliveredBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		result, err := runtimeStore.MarkGatewayOutboundDelivered(r.Context(), store.MarkGatewayOutboundDeliveredInput{MessageID: messageID, DeliveryID: req.DeliveryID})
		if err != nil {
			if errors.Is(err, store.ErrUnknownMessage) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mark gateway outbound delivered"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listProjectAgentRuns(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		// `?status=` accepts a comma-separated list so the admin
		// "进行中" tab can union {running,queued} in one round-trip.
		// Empty values are stripped. The SQL `cardinality(...) = 0`
		// branch handles the no-filter case.
		statuses := parseStatusList(r.URL.Query().Get("status"))
		limit := parseLimit(r, 100)
		offset := parseOffset(r)

		result, err := runtimeStore.ListProjectAgentRuns(r.Context(), projectID, statuses, limit, offset)
		if err != nil {
			writeReadError(w, err, "failed to list project agent runs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"project_id": projectID,
			"statuses":   statuses,
			"agent_runs": result.Runs,
			"total":      result.Total,
			"limit":      limit,
			"offset":     offset,
		})
	}
}

// getProjectAgentMetrics returns aggregated run-history counters for
// a single project_agent over a sliding window. Powers the agent-detail
// "近 N 天表现" panel: completion count, success rate, average duration.
// `?days=` is optional and clamps to [1, 365]; default 30.
func getProjectAgentMetrics(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
		if !isUUID(projectAgentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		days := int32(30)
		if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				if v < 1 {
					v = 1
				} else if v > 365 {
					v = 365
				}
				days = int32(v)
			}
		}

		metrics, err := runtimeStore.GetProjectAgentMetrics(r.Context(), projectID, projectAgentID, days)
		if err != nil {
			writeReadError(w, err, "failed to load agent metrics")
			return
		}
		writeJSON(w, http.StatusOK, metrics)
	}
}

// parseStatusList splits `?status=a,b,c` into a trimmed, non-empty
// list. Returns nil for "no filter" (empty query string or all blanks)
// so handler code can pass it straight through to the store layer.
func parseStatusList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// listProjectAuditRecords serves /projects/{projectID}/audit-records.
// It reads the unified audit_records table (5-category source taxonomy,
// jsonb payload). Optional query filters: source, event_type,
// target_type, target_id, actor_id.
func listProjectAuditRecords(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		q := r.URL.Query()
		filter := store.ListAuditRecordsFilter{
			ProjectID:  projectID,
			Source:     strings.TrimSpace(q.Get("source")),
			EventType:  strings.TrimSpace(q.Get("event_type")),
			ActorID:    strings.TrimSpace(q.Get("actor_id")),
			TargetType: strings.TrimSpace(q.Get("target_type")),
			TargetID:   strings.TrimSpace(q.Get("target_id")),
		}
		records, err := runtimeStore.ListAuditRecords(r.Context(), filter, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list project audit records")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"project_id":    projectID,
			"source":        filter.Source,
			"event_type":    filter.EventType,
			"target_type":   filter.TargetType,
			"audit_records": records,
		})
	}
}

// listProjectConnectors aggregates connector types in use by
// project_agents in a project. There is no `connectors` table — the
// connector identity lives on each agent's `connector_type` field.
func listProjectConnectors(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}

		agents, err := runtimeStore.ListProjectAgentsForAdmin(r.Context(), projectID)
		if err != nil {
			writeReadError(w, err, "failed to list project connectors")
			return
		}

		type connectorRow struct {
			ConnectorType string   `json:"connector_type"`
			Label         string   `json:"label"`
			Status        string   `json:"status"`
			AgentCount    int      `json:"agent_count"`
			AgentSlugs    []string `json:"agent_slugs"`
		}
		bucket := map[string]*connectorRow{}
		for _, a := range agents {
			ct := strings.TrimSpace(a.ConnectorType)
			if ct == "" {
				ct = "unknown"
			}
			row, ok := bucket[ct]
			if !ok {
				row = &connectorRow{
					ConnectorType: ct,
					Label:         connectorLabel(ct),
					Status:        connectorStatus(ct),
					AgentSlugs:    []string{},
				}
				bucket[ct] = row
			}
			row.AgentCount++
			row.AgentSlugs = append(row.AgentSlugs, a.Slug)
		}

		out := make([]connectorRow, 0, len(bucket))
		for _, row := range bucket {
			out = append(out, *row)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].AgentCount != out[j].AgentCount {
				return out[i].AgentCount > out[j].AgentCount
			}
			return out[i].ConnectorType < out[j].ConnectorType
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"project_id": projectID,
			"connectors": out,
		})
	}
}

// connectorLabel maps the raw connector_type to a UI-friendly label.
func connectorLabel(t string) string {
	switch t {
	case "agent_daemon":
		return "Agent Daemon"
	case "http-agent", "http":
		return "HTTP Agent"
	default:
		return t
	}
}

// connectorStatus is a coarse health hint. Per-run failures show up
// in Run Detail. Future connectors needing external setup can return
// "needs_config" / "offline".
func connectorStatus(t string) string {
	switch t {
	case "agent_daemon", "http-agent", "http":
		return "ready"
	default:
		return "unknown"
	}
}

// listWorkspaceGateways returns a static registry of known gateway types.
// No DB schema — connectors don't have one either.
func listWorkspaceGateways() http.HandlerFunc {
	type gatewayRow struct {
		Type        string `json:"type"`
		Label       string `json:"label"`
		Status      string `json:"status"`
		Phase       string `json:"phase"`
		Description string `json:"description"`
	}
	registry := []gatewayRow{
		{
			Type:        "dev",
			Label:       "Dev Gateway",
			Status:      "active",
			Phase:       "phase_1",
			Description: "Built-in dev gateway/inbound entry point used by the devgateway tool and E2E tests.",
		},
		{
			Type:        "feishu",
			Label:       "Feishu",
			Status:      "not_configured",
			Phase:       "phase_3",
			Description: "Feishu group / thread gateway with real webhook signature + OAuth.",
		},
		{
			Type:        "slack",
			Label:       "Slack",
			Status:      "not_configured",
			Phase:       "phase_3",
			Description: "Slack channel + thread gateway.",
		},
		{
			Type:        "web",
			Label:       "Web",
			Status:      "active",
			Phase:       "phase_1",
			Description: "Built-in web entrypoint for conversations created from the admin UI.",
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"gateways":     registry,
		})
	}
}

func listWorkspaceMembers(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		members, err := runtimeStore.ListWorkspaceMembers(r.Context(), workspaceID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list workspace members")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"members":      members,
		})
	}
}

// listWorkspaceProjects returns active projects inside a workspace.
// Drives the project picker. 200 with empty list when none, 404 when
// workspace unknown.
func listWorkspaceProjects(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		// Pass userID so the SQL join on workspace_members gates the
		// list to active workspace members (any role). Platform admins
		// bypass the gate and see every active project in the workspace.
		userID := actorIDFromRequest(r)
		limit := parseLimit(r, 100)
		var (
			projects []store.WorkspaceProjectRead
			err      error
		)
		if auth.IsPlatformAdmin(userID) {
			projects, err = runtimeStore.ListWorkspaceProjectsForAdmin(r.Context(), workspaceID, limit)
		} else {
			projects, err = runtimeStore.ListWorkspaceProjects(r.Context(), workspaceID, userID, limit)
		}
		if err != nil {
			writeReadError(w, err, "failed to list workspace projects")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"projects":     projects,
		})
	}
}

// searchUsers backs the platform-wide user picker. Returns at most 20
// users matching q substring, optionally hiding users already in a
// workspace. RBAC: any authenticated user (add-member action still
// goes through workspace owner/admin gate).
func searchUsers(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
			return
		}
		excludeWS := strings.TrimSpace(r.URL.Query().Get("exclude_workspace"))
		// Silently ignore garbage UUIDs — the store treats unparseable
		// input as "no filter" and the picker is read-only.
		if excludeWS != "" && !isUUID(excludeWS) {
			excludeWS = ""
		}

		items, err := runtimeStore.SearchUsers(r.Context(), store.SearchUsersInput{
			Query:              q,
			ExcludeWorkspaceID: excludeWS,
			Limit:              20,
		})
		if err != nil {
			writeReadError(w, err, "failed to search users")
			return
		}

		// Exclude the caller from results so the picker never offers
		// "add yourself".
		selfID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			if selfID != "" && it.ID == selfID {
				continue
			}
			out = append(out, map[string]any{
				"id":         it.ID,
				"email":      it.Email,
				"name":       it.Name,
				"avatar_url": it.AvatarURL,
				"status":     it.Status,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// listMyWorkspaces returns the workspaces the authenticated caller belongs to.
// Platform admins (auth.IsPlatformAdmin) get the full list of active
// workspaces with role=owner so they can drop into any tenant.
func listMyWorkspaces(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		if userID == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		limit := parseLimit(r, 50)
		var (
			workspaces []store.UserWorkspaceRead
			err        error
		)
		if auth.IsPlatformAdmin(userID) {
			workspaces, err = runtimeStore.ListAllActiveWorkspaces(r.Context(), limit)
		} else {
			workspaces, err = runtimeStore.ListUserWorkspaces(r.Context(), userID, limit)
		}
		if err != nil {
			writeReadError(w, err, "failed to list user workspaces")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id":    userID,
			"workspaces": workspaces,
		})
	}
}

// devActorID returns the authenticated caller for dev writes. Require
// middleware should have populated the context before handlers run.
func devActorID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
	if userID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
		return "", false
	}
	if !isUUID(userID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
		return "", false
	}
	return userID, true
}

type createWorkspaceRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility,omitempty"` // "public" / "private";空 → 服务端默认 "private"
}

func createWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createWorkspaceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		result, err := runtimeStore.CreateWorkspace(r.Context(), store.CreateWorkspaceInput{
			Name:       body.Name,
			Visibility: strings.TrimSpace(body.Visibility),
			CreatedBy:  actorID,
			Now:        time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to create workspace")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"workspace": result.Workspace,
			"member":    result.Member,
		})
	}
}

type updateWorkspaceRequest struct {
	Name       *string `json:"name,omitempty"`
	Visibility *string `json:"visibility,omitempty"` // "public" / "private";nil → 不变
}

func updateWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body updateWorkspaceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		row, err := runtimeStore.UpdateWorkspace(r.Context(), store.UpdateWorkspaceInput{
			WorkspaceID: workspaceID,
			Name:        body.Name,
			Visibility:  body.Visibility,
			ActorID:     actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to update workspace")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func archiveWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		row, err := runtimeStore.ArchiveWorkspace(r.Context(), store.ArchiveWorkspaceInput{
			WorkspaceID: workspaceID,
			ActorID:     actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to archive workspace")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

type createProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func createProject(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		result, err := runtimeStore.CreateProject(r.Context(), store.CreateProjectInput{
			WorkspaceID: workspaceID,
			Name:        body.Name,
			Description: body.Description,
			CreatedBy:   actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to create project")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"project": result.Project,
		})
	}
}

type updateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func updateProject(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body updateProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		row, err := runtimeStore.UpdateProject(r.Context(), store.UpdateProjectInput{
			ProjectID:   projectID,
			Name:        body.Name,
			Description: body.Description,
			ActorID:     actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to update project")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func archiveProject(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		row, err := runtimeStore.ArchiveProject(r.Context(), store.ArchiveProjectInput{
			ProjectID: projectID,
			ActorID:   actorID,
			Now:       time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to archive project")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

type addWorkspaceMemberRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

func addWorkspaceMember(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req addWorkspaceMemberRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.Name = strings.TrimSpace(req.Name)
		req.Role = strings.TrimSpace(req.Role)
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if !store.IsValidMemberRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be one of owner|admin|member|viewer"})
			return
		}
		result, err := runtimeStore.AddWorkspaceMember(r.Context(), store.AddWorkspaceMemberInput{
			WorkspaceID: workspaceID,
			Email:       req.Email,
			Name:        req.Name,
			Role:        req.Role,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add workspace member"})
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

type updateWorkspaceMemberRoleRequest struct {
	Role string `json:"role"`
}

func updateWorkspaceMemberRole(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		userID := strings.TrimSpace(chi.URLParam(r, "userID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateWorkspaceMemberRoleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Role = strings.TrimSpace(req.Role)
		if !store.IsValidMemberRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be one of owner|admin|member|viewer"})
			return
		}
		member, err := runtimeStore.UpdateWorkspaceMemberRole(r.Context(), workspaceID, userID, req.Role, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspaceMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update workspace member role"})
			return
		}
		writeJSON(w, http.StatusOK, member)
	}
}

func removeWorkspaceMember(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		userID := strings.TrimSpace(chi.URLParam(r, "userID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.RemoveWorkspaceMember(r.Context(), workspaceID, userID, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspaceMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove workspace member"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// ============================================================
// 工作区主动申请加入(self-service join request)handlers
//
//   POST /api/v1/workspaces/{wid}/join-requests              提交申请
//   GET  /api/v1/workspaces/{wid}/join-requests              owner/admin 列表
//   POST /api/v1/workspaces/{wid}/join-requests/{rid}/approve owner/admin 同意
//   POST /api/v1/workspaces/{wid}/join-requests/{rid}/reject  owner/admin 拒绝
//
// WHERE status='pending' 守卫在 SQL 层原子化,双 admin 竞态会拿到
// ErrJoinRequestAlreadyHandled,转换成 409。
// ============================================================

type createJoinRequestRequest struct {
	Reason string `json:"reason,omitempty"`
}

func createJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		userID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createJoinRequestRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		// 可选 reason;选填,长度软限 — 防 abuse 但不强制 schema
		if len(body.Reason) > 1000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason must be 1000 characters or less"})
			return
		}
		result, err := runtimeStore.RequestJoinWorkspace(r.Context(), store.RequestJoinWorkspaceInput{
			WorkspaceID: workspaceID,
			UserID:      userID,
			Reason:      body.Reason,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspace) {
				// 含两种情况:不存在 / 私有不公开 —— 一律 404 防枚举
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found or not open to join requests"})
				return
			}
			writeReadError(w, err, "failed to submit join request")
			return
		}
		if result.Already {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "already a member or pending request exists",
				"request": result.Request,
			})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"request": result.Request,
		})
	}
}

// withdrawOwnJoinRequest — 申请人自助撤回 pending 申请。路径不带
// requestID:申请人只有一行 pending,(workspace_id, current_user_id)
// 唯一定位,客户端也不用持有 request id。
func withdrawOwnJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		userID, ok := devActorID(w, r)
		if !ok {
			return
		}
		if err := runtimeStore.WithdrawOwnJoinRequest(r.Context(), workspaceID, userID, time.Now().UTC()); err != nil {
			if errors.Is(err, store.ErrJoinRequestAlreadyHandled) {
				// 没找到 pending 行:可能已被 owner 批准 / 拒绝,或者本来就没申请过
				writeJSON(w, http.StatusConflict, map[string]string{"error": "no pending request to withdraw"})
				return
			}
			writeReadError(w, err, "failed to withdraw join request")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listJoinRequests(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		rows, err := runtimeStore.ListPendingJoinRequests(r.Context(), workspaceID)
		if err != nil {
			writeReadError(w, err, "failed to list join requests")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"requests":     rows,
		})
	}
}

func approveJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return reviewJoinRequestHandler(runtimeStore, true)
}

func rejectJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return reviewJoinRequestHandler(runtimeStore, false)
}

func reviewJoinRequestHandler(runtimeStore RuntimeStore, approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		requestID := strings.TrimSpace(chi.URLParam(r, "requestID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(requestID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		reviewerID, ok := devActorID(w, r)
		if !ok {
			return
		}
		input := store.ReviewJoinRequestInput{
			WorkspaceID: workspaceID,
			RequestID:   requestID,
			ReviewerID:  reviewerID,
			Now:         time.Now().UTC(),
		}
		var (
			member store.WorkspaceMemberRead
			err    error
		)
		if approve {
			member, err = runtimeStore.ApproveJoinRequest(r.Context(), input)
		} else {
			member, err = runtimeStore.RejectJoinRequest(r.Context(), input)
		}
		if err != nil {
			if errors.Is(err, store.ErrJoinRequestAlreadyHandled) {
				// 已被其他 admin 处理 / row 不是 pending 状态
				writeJSON(w, http.StatusConflict, map[string]string{"error": "join request already handled"})
				return
			}
			writeReadError(w, err, "failed to review join request")
			return
		}
		writeJSON(w, http.StatusOK, member)
	}
}

// listDiscoverableWorkspaces — `GET /api/v1/me/discoverable-workspaces`
// 当前用户可以申请加入的 public 工作区。
//
// Query params:
//   - q     : 模糊搜索 workspace.name (case-insensitive),为空时返回全部
//   - limit : 默认 50,clamp 到 [1, 100]
//   - offset: 默认 0
//
// Response 带 total 字段(过滤后总数),前端做"查看全部 (N)" 和 pager。
func listDiscoverableWorkspaces(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		if userID == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		// 限制搜索词长度,防止恶意超长字符串拖慢 ILIKE 索引扫描
		if len(q) > 100 {
			q = q[:100]
		}
		offset := parseOffset(r)
		result, err := runtimeStore.ListDiscoverableWorkspaces(r.Context(), store.ListDiscoverableWorkspacesInput{
			UserID: userID,
			Search: q,
			Limit:  parseLimit(r, 50),
			Offset: offset,
		})
		if err != nil {
			writeReadError(w, err, "failed to list discoverable workspaces")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id":    userID,
			"workspaces": result.Workspaces,
			"total":      result.Total,
			"limit":      parseLimit(r, 50),
			"offset":     offset,
		})
	}
}

func listProjectUsageLogs(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		agentRunID := strings.TrimSpace(r.URL.Query().Get("agent_run_id"))
		if agentRunID != "" && !isUUID(agentRunID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_run_id must be a valid uuid"})
			return
		}

		usage, err := runtimeStore.ListProjectUsageLogs(r.Context(), projectID, agentRunID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list project usage")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"project_id": projectID, "agent_run_id": agentRunID, "usage_logs": usage})
	}
}

func parseLimit(r *http.Request, fallback int32) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return int32(limit)
}

// parseOffset reads ?offset=N for paginated endpoints. Defaults to 0;
// clamps negative inputs to 0 (pagination underflow is meaningless).
func parseOffset(r *http.Request) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0
	}
	return int32(offset)
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func writeReadError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrUnknownProject), errors.Is(err, store.ErrUnknownConversationForRead), errors.Is(err, store.ErrUnknownAgentRun), errors.Is(err, store.ErrUnknownConversation), errors.Is(err, store.ErrUnknownWorkspace):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrDuplicateWorkspaceSlug), errors.Is(err, store.ErrDuplicateProjectSlug):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceDependents):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "has_marketplace_dependents", "message": err.Error()})
	case errors.Is(err, store.ErrInvalidWorkspaceInput), errors.Is(err, store.ErrInvalidProjectInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fallback})
	}
}

func writeStoreAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrDuplicateAgentSlug):
		suggested := ""
		parts := strings.Split(err.Error(), ": ")
		if len(parts) > 1 {
			suggested = parts[len(parts)-1]
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_conflict", "suggested": suggested})
	case errors.Is(err, store.ErrUnknownCapability):
		invalid := []string{}
		parts := strings.Split(err.Error(), ": ")
		if len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) != "" {
			invalid = strings.Split(parts[len(parts)-1], ",")
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "unknown_capability", "invalid": invalid})
	case errors.Is(err, store.ErrUnknownCapabilityVersion):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceCapabilityUnavailable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrUnknownAgent), errors.Is(err, store.ErrUnknownProjectAgent), errors.Is(err, store.ErrUnknownWorkspace), errors.Is(err, store.ErrUnknownProject):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrInvalidConnectorType), errors.Is(err, store.ErrInvalidProjectInput), errors.Is(err, store.ErrInvalidAgentVisibility):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent operation failed"})
	}
}

func decodeJSONWithField(r *http.Request, target any, field string) (bool, error) {
	fields, err := decodeJSONWithFields(r, target)
	if err != nil {
		return false, err
	}
	return fields[field], nil
}

func decodeJSONWithFields(r *http.Request, target any) (map[string]bool, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return nil, err
	}
	fields := make(map[string]bool, len(raw))
	for field := range raw {
		fields[field] = true
	}
	return fields, nil
}

func actorIDFromRequest(r *http.Request) string {
	if userID := auth.UserIDFromContext(r.Context()); userID != "" {
		return userID
	}
	return store.DefaultDevFixtureIDs().UserID
}

func requestContextForRBAC(r *http.Request) context.Context {
	if auth.UserIDFromContext(r.Context()) != "" {
		return r.Context()
	}
	return auth.WithUserID(r.Context(), store.DefaultDevFixtureIDs().UserID)
}

func requireWorkspaceOwnerOrAdmin(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin")
}

// requireWorkspaceMember gates read endpoints scoped to a workspace.
// Returns ErrNotMember when the caller is not an active member of the
// workspace.
func requireWorkspaceMember(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin", "member", "viewer")
}

// requireWorkspaceMemberNotViewer gates write endpoints that any
// non-viewer member of the workspace can perform — creating a
// conversation, triggering a run, editing one's own conversation
// title, etc. viewer is read-only.
func requireWorkspaceMemberNotViewer(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin", "member")
}

// requireWorkspaceMemberByProject resolves projectID → workspaceID
// then runs the read gate. Used by project-scoped read routes
// (/projects/{pid}/...) after the project membership tier was removed.
func requireWorkspaceMemberByProject(r *http.Request, runtimeStore RuntimeStore, projectID string) error {
	workspaceID, err := runtimeStore.GetProjectWorkspace(r.Context(), projectID)
	if err != nil {
		return err
	}
	return requireWorkspaceMember(r, runtimeStore, workspaceID)
}

// requireWorkspaceOwnerOrAdminByProject is the management twin —
// project-scoped management routes (delete/configure agent, archive
// project, edit project, ...) map onto workspace owner/admin.
func requireWorkspaceOwnerOrAdminByProject(r *http.Request, runtimeStore RuntimeStore, projectID string) error {
	workspaceID, err := runtimeStore.GetProjectWorkspace(r.Context(), projectID)
	if err != nil {
		return err
	}
	return requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID)
}

// requireWorkspaceMemberNotViewerByProject is for "everyday write"
// project-scoped routes (create conversation, send message, trigger
// run) — viewer is locked out, member+ allowed.
func requireWorkspaceMemberNotViewerByProject(r *http.Request, runtimeStore RuntimeStore, projectID string) error {
	workspaceID, err := runtimeStore.GetProjectWorkspace(r.Context(), projectID)
	if err != nil {
		return err
	}
	return requireWorkspaceMemberNotViewer(r, runtimeStore, workspaceID)
}

// gateWorkspaceMember wraps a handler whose URL is
// /workspaces/{workspaceID}/... and rejects callers that aren't an
// active member. Used by sandbox admin endpoints whose handlers don't
// carry runtimeStore — wrapping at register-time avoids polluting
// sandboxAdminDeps.
//
// Returns 503 when runtimeStore is nil (local-mode server without
// DB) so the response surface matches the other DB-backed endpoints
// instead of silently bypassing RBAC.
func gateWorkspaceMember(runtimeStore RuntimeStore, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		next(w, r)
	}
}

// gateWorkspaceOwnerOrAdmin gates sandbox kill / rebuild on owner+admin
// only. Mid-run kill interrupts an Agent task in flight.
func gateWorkspaceOwnerOrAdmin(runtimeStore RuntimeStore, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		next(w, r)
	}
}

func projectIDForProjectAgent(w http.ResponseWriter, ctx context.Context, runtimeStore RuntimeStore, projectAgentID string) (string, bool) {
	agent, err := runtimeStore.GetProjectAgentDetail(ctx, projectAgentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownProjectAgent) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return "", false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load project agent"})
		return "", false
	}
	return agent.ProjectID, true
}

type createConversationUserMessageBody struct {
	Content           string   `json:"content"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
}

func createConversationUserMessage(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation messages are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		var req createConversationUserMessageBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		content := strings.TrimSpace(req.Content)
		if content == "" || len(content) > 32000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "content must be 1-32000 characters"})
			return
		}
		conversation, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, conversation.ProjectID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.SendUserMessageToConversation(r.Context(), store.SendUserMessageToConversationInput{
			ConversationID:    conversationID,
			UserID:            actorIDFromRequest(r),
			Content:           content,
			MentionedAgentIDs: req.MentionedAgentIDs,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownConversation):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownMention), errors.Is(err, store.ErrInvalidProjectInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				log.Bg().Error("send conversation message failed",
					"error", err,
					"conversation_id", conversationID,
					"user_id", actorIDFromRequest(r))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send conversation message"})
			}
			return
		}
		agentRunID := any(nil)
		if len(result.RunIDs) > 0 {
			agentRunID = result.RunIDs[0]
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"message":                result.Message,
			"agent_run_id":           agentRunID,
			"dispatched_agent_count": len(result.RunIDs),
		})
	}
}

type createProjectConversationBody struct {
	Title    string         `json:"title"`
	Surface  string         `json:"surface"`
	Form     string         `json:"form"`
	AgentID  string         `json:"agent_id"`
	Metadata map[string]any `json:"metadata"`
}

func listProjectConversations(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		conversations, err := runtimeStore.ListProjectConversations(r.Context(), projectID, agentFilter, parseLimit(r, 100))
		if err != nil {
			if errors.Is(err, store.ErrInvalidProjectInput) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			writeReadError(w, err, "failed to list project conversations")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
	}
}

func createProjectConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation creation is disabled"})
			return
		}
		projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
		if !isUUID(projectID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, projectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createProjectConversationBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		conversation, err := runtimeStore.CreateProjectConversation(r.Context(), store.CreateProjectConversationInput{
			ProjectID:      projectID,
			Title:          req.Title,
			Surface:        req.Surface,
			Form:           req.Form,
			PrimaryAgentID: req.AgentID,
			Metadata:       req.Metadata,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownProject):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownMention), errors.Is(err, store.ErrInvalidProjectInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return
		}
		writeJSON(w, http.StatusCreated, conversation)
	}
}

func getProjectConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conversation, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		// URL is keyed by conversation_id, so load before knowing
		// which project to authorise against.
		if err := requireWorkspaceMemberByProject(r, runtimeStore, conversation.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, conversation)
	}
}

// updateConversationTitleBody — PATCH /api/v1/conversations/{cid}; only title is editable.
type updateConversationTitleBody struct {
	Title string `json:"title"`
}

func updateConversationTitle(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed write APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		// Load row first so RBAC can gate on the resolved project.
		conv, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceMemberNotViewerByProject(r, runtimeStore, conv.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateConversationTitleBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		if err := runtimeStore.UpdateConversationTitle(r.Context(), conversationID, req.Title); err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownConversation):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrInvalidProjectInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update conversation"})
			}
			return
		}
		// Re-read so response shape matches GET.
		updated, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to read updated conversation")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func deleteConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed write APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conv, err := runtimeStore.GetProjectConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceOwnerOrAdminByProject(r, runtimeStore, conv.ProjectID); err != nil {
			writeRBACError(w, err)
			return
		}
		if err := runtimeStore.SoftDeleteConversation(r.Context(), conversationID); err != nil {
			if errors.Is(err, store.ErrUnknownConversation) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete conversation"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// E2B sandbox smoke handler. One-off API key from request body or
// `E2B_API_KEY` env; sandbox is created, command runs, and (unless
// keep_alive=true) the sandbox is killed. Errors bubble up via
// `sanitizeE2BSmokeError` which redacts the API key.

// defaultDevOpenCodeSandboxTemplate is the E2B template id the dev
// smoke endpoints default to. `parsar-opencode-base` (infra/e2b-
// templates/opencode/) preinstalls the opencode CLI, Node 22, and the
// four ai-sdk provider adapters, avoiding a ~30s npm install round.
// Callers can override via `template`; non-dev callers set TemplateID
// on BuildSandboxRunnerOptions directly.
const defaultDevOpenCodeSandboxTemplate = "parsar-opencode-base"

type e2bSmokeRequest struct {
	APIKey                string            `json:"api_key"`
	APIBaseURL            string            `json:"api_base_url"`
	SandboxHost           string            `json:"sandbox_host"`
	SandboxBaseURL        string            `json:"sandbox_base_url"`
	Template              string            `json:"template"`
	Command               string            `json:"command"`
	TimeoutSeconds        int               `json:"timeout_seconds"`
	CommandTimeoutSeconds int               `json:"command_timeout_seconds"`
	KeepAlive             bool              `json:"keep_alive"`
	Env                   map[string]string `json:"env"`
}

type e2bSmokeResponse struct {
	SandboxID  string                   `json:"sandbox_id"`
	TemplateID string                   `json:"template_id"`
	Killed     bool                     `json:"killed"`
	Command    e2bsandbox.CommandResult `json:"command"`
}

func smokeE2BSandbox(w http.ResponseWriter, r *http.Request) {
	var req e2bSmokeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("E2B_API_KEY"))
	}
	if apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api_key or E2B_API_KEY is required"})
		return
	}
	template := strings.TrimSpace(req.Template)
	if template == "" {
		template = defaultDevOpenCodeSandboxTemplate
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = `printf "hello from parsar e2b smoke\n"`
	}
	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	commandTimeout := time.Duration(req.CommandTimeoutSeconds) * time.Second
	if commandTimeout <= 0 {
		commandTimeout = 60 * time.Second
	}
	secure := true
	client := &e2bsandbox.Client{
		HTTPClient:     http.DefaultClient,
		APIBaseURL:     req.APIBaseURL,
		SandboxHost:    req.SandboxHost,
		SandboxBaseURL: req.SandboxBaseURL,
		APIKey:         apiKey,
	}
	sandbox, err := client.Create(r.Context(), e2bsandbox.CreateInput{
		TemplateID:     template,
		TimeoutSeconds: timeoutSeconds,
		Secure:         &secure,
		Env:            req.Env,
		Metadata: map[string]string{
			"source": "parsar_dev_smoke",
		},
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sanitizeE2BSmokeError(err, apiKey)})
		return
	}
	killed := false
	cleanupNeeded := !req.KeepAlive
	defer func() {
		if !cleanupNeeded {
			return
		}
		killCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.Kill(killCtx, sandbox.SandboxID); err == nil {
			killed = true
		}
	}()
	result, err := client.RunCommand(r.Context(), e2bsandbox.RunCommandInput{
		Sandbox: sandbox,
		Command: command,
		Timeout: commandTimeout,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":       sanitizeE2BSmokeError(err, apiKey),
			"sandbox_id":  sandbox.SandboxID,
			"template_id": sandbox.TemplateID,
			"keep_alive":  req.KeepAlive,
		})
		return
	}
	if !req.KeepAlive {
		killCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		killErr := client.Kill(killCtx, sandbox.SandboxID)
		cancel()
		killed = killErr == nil
		cleanupNeeded = false
	}
	writeJSON(w, http.StatusOK, e2bSmokeResponse{
		SandboxID:  sandbox.SandboxID,
		TemplateID: sandbox.TemplateID,
		Killed:     killed,
		Command:    result,
	})
}

func sanitizeE2BSmokeError(err error, apiKey string) string {
	if err == nil {
		return ""
	}
	return e2bsandbox.RedactSecret(err.Error(), apiKey)
}
