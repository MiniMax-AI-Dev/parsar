package dev

import (
	"context"

	"regexp"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	gatewaypkg "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
)

var mentionPattern = regexp.MustCompile(`@[\p{Han}A-Za-z0-9_-]+`)

type RuntimeStore interface {
	CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error)
	CompleteAgentRun(ctx context.Context, input store.CompleteAgentRunInput) (store.CompleteAgentRunResult, error)
	FailAgentRun(ctx context.Context, input store.FailAgentRunInput) error
	GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error)
	RequeueFailedAgentRun(ctx context.Context, input store.RequeueAgentRunInput) (store.RequeueAgentRunResult, error)
	ConfigureDevConversationExternalRef(ctx context.Context, input store.ConfigureDevConversationExternalRefInput) (store.ConfigureDevConversationExternalRefResult, error)
	DisableAgent(ctx context.Context, agentID, actorID string) (store.AgentStatusRead, error)
	EnableAgent(ctx context.Context, agentID, actorID string) (store.AgentStatusRead, error)
	GetAgentDetail(ctx context.Context, agentID string) (store.AgentStatusRead, error)
	GetAgentRuntimeBinding(ctx context.Context, workspaceID, agentID string) (store.AgentRuntimeBinding, error)
	SetAgentRuntime(ctx context.Context, input store.SetAgentRuntimeInput) (store.AgentRuntimeBinding, error)
	GetWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error)
	GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (store.WorkspaceRuntimeSettingsRead, error)
	SetWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, secretID string, now time.Time) error
	ClearWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, name, kind string, now time.Time) error
	RegisterWorkspaceRuntimeCredential(ctx context.Context, input store.RegisterWorkspaceRuntimeCredentialInput) (store.SecretRead, error)
	PatchWorkspaceSettings(ctx context.Context, workspaceID string) (store.WorkspaceSettingsRead, error)
	ListCapabilities(ctx context.Context, workspaceID string, filter store.ListCapabilityFilter) ([]store.CapabilityRead, error)
	ListSkillsDirectoryInstalls(ctx context.Context, workspaceID string) (map[string]string, error)
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
	agentCapabilityStore
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
	// "Admins: A, B" plus a "Join workspace" link. Both are read-
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
	// to let users continue a thread conversation without re-
	// @mentioning the bot on every message.
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)
	// HasThreadInboundHistory is the platform-scoped form used by the shared
	// router's neutral inbound gate.
	HasThreadInboundHistory(ctx context.Context, platform, externalChatID, threadKey string) (bool, error)
	DeleteAgent(ctx context.Context, agentID string, actorID string) (store.DeleteAgentResult, int64, error)
	CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error)
	ListSecrets(ctx context.Context, workspaceID string, limit int32) ([]store.SecretRead, error)
	DisableSecret(ctx context.Context, workspaceID string, secretID string) (store.SecretRead, error)
	CreateModel(ctx context.Context, input store.CreateModelInput) (store.ModelRead, error)
	DisableModel(ctx context.Context, workspaceID string, modelID string) (store.ModelRead, error)
	DeleteModel(ctx context.Context, modelID string, actorID string) error
	UpdateModel(ctx context.Context, input store.UpdateModelInput) (store.ModelRead, error)
	UpdateModelHealth(ctx context.Context, modelID string, health map[string]any) (store.ModelRead, error)
	ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (store.ModelRuntime, error)
	ResolveModelRuntimeForUser(ctx context.Context, modelID, userID string) (store.ModelRuntime, error)
	ListModels(ctx context.Context, workspaceID string, limit int32) ([]store.ModelRead, error)
	ListWorkspaceEnabledAgents(ctx context.Context, workspaceID string) ([]store.AgentRead, error)
	ListWorkspaceAgentsForAdmin(ctx context.Context, workspaceID string) ([]store.AgentRead, error)
	CreateWorkspaceConversation(ctx context.Context, input store.CreateWorkspaceConversationInput) (store.ConversationRead, error)
	ListWorkspaceConversations(ctx context.Context, workspaceID string, agentID string, limit int32) ([]store.ConversationListItem, error)
	GetConversation(ctx context.Context, conversationID string) (store.ConversationRead, error)
	UpdateConversationTitle(ctx context.Context, conversationID string, title string) error
	SoftDeleteConversation(ctx context.Context, conversationID string) error
	GetConversationTimeline(ctx context.Context, conversationID string, limit int32) (store.ConversationTimelineRead, error)
	SendUserMessageToConversation(ctx context.Context, input store.SendUserMessageToConversationInput) (store.SendUserMessageToConversationResult, error)
	GetAgentRun(ctx context.Context, runID string) (store.AgentRunDetailRead, error)
	CancelAgentRun(ctx context.Context, runID, reason string) (bool, error)
	ListAgentRunEvents(ctx context.Context, runID string, afterSequence int64) ([]store.AgentRunEventRead, error)
	ListActiveFeishuInflightConversations(ctx context.Context, cutoff time.Time, limit int32) ([]store.FeishuInflightConversation, error)
	MarkGatewayOutboundDelivered(ctx context.Context, input store.MarkGatewayOutboundDeliveredInput) (store.MarkGatewayOutboundDeliveredResult, error)
	ListWorkspaceAgentRuns(ctx context.Context, workspaceID string, statuses []string, limit, offset int32, search string) (store.ListWorkspaceAgentRunsResult, error)
	GetAgentMetrics(ctx context.Context, agentID string, windowDays int32) (store.AgentMetricsRead, error)
	ListAuditRecords(ctx context.Context, filter store.ListAuditRecordsFilter, limit int32) ([]store.AuditRecordRead, error)
	ListWorkspaceUsageLogs(ctx context.Context, workspaceID string, agentRunID string, limit int32) ([]store.UsageLogRead, error)
	ListWorkspaceMembers(ctx context.Context, workspaceID string, limit int32) ([]store.WorkspaceMemberRead, error)
	GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error)
	GetUserByID(ctx context.Context, userID string) (store.UserRead, error)
	ListUserWorkspaces(ctx context.Context, userID string, limit int32) ([]store.UserWorkspaceRead, error)
	ListAllActiveWorkspaces(ctx context.Context, limit int32) ([]store.UserWorkspaceRead, error)
	CreateWorkspace(ctx context.Context, input store.CreateWorkspaceInput) (store.CreateWorkspaceResult, error)
	UpdateWorkspace(ctx context.Context, input store.UpdateWorkspaceInput) (store.UserWorkspaceRead, error)
	ArchiveWorkspace(ctx context.Context, input store.ArchiveWorkspaceInput) (store.UserWorkspaceRead, error)
	AddWorkspaceMember(ctx context.Context, input store.AddWorkspaceMemberInput) (store.AddWorkspaceMemberResult, error)
	AcceptInvitation(ctx context.Context, input store.AcceptInvitationInput) (store.AddWorkspaceMemberResult, error)
	CreateInvitation(ctx context.Context, input store.CreateInvitationInput) error
	ListPendingInvitations(ctx context.Context, workspaceID string) ([]store.PendingInvitationRead, error)
	ListPendingInvitationsByInviter(ctx context.Context, workspaceID, invitedBy string) ([]store.PendingInvitationRead, error)
	UpdateInvitationRole(ctx context.Context, workspaceID, invitationID, role string) (int64, error)
	RevokeInvitation(ctx context.Context, workspaceID, invitationID string) (int64, error)
	RevokeOwnInvitation(ctx context.Context, workspaceID, invitationID, invitedBy string) (int64, error)
	GetInvitationByTokenHash(ctx context.Context, tokenHash []byte) (store.InvitationRead, error)
	UpdateWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string, role string, now time.Time) (store.WorkspaceMemberRead, error)
	RemoveWorkspaceMember(ctx context.Context, workspaceID string, userID string, now time.Time) (store.RemoveWorkspaceMemberResult, error)
	SearchUsers(ctx context.Context, input store.SearchUsersInput) ([]store.SearchUsersResultItem, error)

	// Workspace self-service join request
	ListDiscoverableWorkspaces(ctx context.Context, input store.ListDiscoverableWorkspacesInput) (store.ListDiscoverableWorkspacesResult, error)
	ListPendingJoinRequests(ctx context.Context, workspaceID string) ([]store.PendingJoinRequestRead, error)
	CountPendingJoinRequests(ctx context.Context, workspaceID string) (int64, error)
	RequestJoinWorkspace(ctx context.Context, input store.RequestJoinWorkspaceInput) (store.RequestJoinWorkspaceResult, error)
	ApproveJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error)
	RejectJoinRequest(ctx context.Context, input store.ReviewJoinRequestInput) (store.WorkspaceMemberRead, error)
	WithdrawOwnJoinRequest(ctx context.Context, workspaceID, userID string, now time.Time) error

	// Workspace-scoped IM connectors (feishu/slack/discord unified storage).
	// GET initializes the panel + upserts write for three platforms. Reads
	// go through the member gateway; writes go through the owner/admin gateway
	// (see route registration).
	GetWorkspaceIMConnectors(ctx context.Context, workspaceID string) ([]store.WorkspaceConnectorRead, error)
	UpsertWorkspaceSlackConnector(ctx context.Context, input store.UpsertWorkspaceSlackConnectorInput, actorID string) (store.WorkspaceConnectorChange, error)
	UpsertWorkspaceDiscordConnector(ctx context.Context, input store.UpsertWorkspaceDiscordConnectorInput, actorID string) (store.WorkspaceConnectorChange, error)
	UpsertWorkspaceTeamsConnector(ctx context.Context, input store.UpsertWorkspaceTeamsConnectorInput, actorID string) (store.WorkspaceConnectorChange, error)
	UpsertWorkspaceFeishuConnector(ctx context.Context, input store.UpsertWorkspaceFeishuConnectorInput, actorID string) (store.WorkspaceConnectorChange, error)

	// Scheduled tasks
	ListScheduledTasksByAgent(ctx context.Context, agentID string) ([]store.ScheduledTaskRead, error)
	ListScheduledTasksByWorkspace(ctx context.Context, workspaceID string, limit, offset int32) (store.ListScheduledTasksByWorkspaceResult, error)
	CreateScheduledTask(ctx context.Context, in store.CreateScheduledTaskInput) (store.ScheduledTaskRead, error)
	GetScheduledTask(ctx context.Context, taskID string) (store.ScheduledTaskRead, error)
	GetScheduledTaskScope(ctx context.Context, taskID string) (store.ScheduledTaskScope, error)
	UpdateScheduledTask(ctx context.Context, in store.UpdateScheduledTaskInput) (store.ScheduledTaskRead, error)
	SoftDeleteScheduledTask(ctx context.Context, taskID string) error
	RunScheduledTaskNow(ctx context.Context, taskID string) (string, error)
	ListAgentRunsByScheduledTask(ctx context.Context, taskID string, limit int32) ([]store.ScheduledTaskRunRead, error)

	// Per-agent switch for built-in capabilities (runtime-injected, e.g. fetch_chat_history).
	// No row = default enabled; write enabled=false to disable this Agent's built-in capability.
	IsBuiltinCapabilityEnabled(ctx context.Context, agentID, key string) (bool, error)
	SetBuiltinCapabilityEnabled(ctx context.Context, agentID, key string, enabled bool) error
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

type interactionResolver interface {
	Resolve(ctx context.Context, req interaction.ResolveRequest) (interaction.ResolveResult, error)
}

type routerConfig struct {
	coreAccess           coreResolver
	connectorRegistry    *connector.Registry
	interactionService   interactionResolver
	authMiddleware       *auth.Middleware
	oauthDeps            *OAuthHandlerDeps
	authProviders        AuthProviderRegistry
	githubConnectionDeps *GitHubConnectionDeps
	feishuWebhook        feishuWebhookConfig
	feishuRegistration   feishuRegistrationConfig
	// auditIngester is used by handlers that emit audit events
	// directly. Nil falls through silently (audit is best-effort).
	auditIngester AuditIngester
	runBroker     *runstream.Broker
	// dispatchCtx is the parent context handed to Core run
	// dispatchers started by /start. cmd/server passes the server root
	// ctx so background goroutines exit cleanly on shutdown. Defaults
	// to context.Background() when unwired (fine for unit tests).
	dispatchCtx context.Context
	// blobStore backs the capability plugin/skill upload + import flow.
	// Nil means the selected backend is unavailable — upload presign +
	// import 503 rather than failing boot.
	blobStore blob.Store
	// pluginsDir is the on-disk directory where installed plugin bundles
	// live (<DataDir>/plugins/). Empty disables plugin client serving.
	pluginsDir string
	// pluginHookInvoker calls plugin hooks (e.g. before_permission_forward).
	// Nil means hooks are disabled — all permission requests proceed
	// to the normal approval flow.
	pluginHookInvoker PluginHookInvoker
	// skillInstallRunner downloads Skills.sh skills via the skills CLI.
	// Nil uses npx at request time; tests inject a fake runner.
	skillInstallRunner     skillInstallCommandRunner
	skillInstallHTTPClient skillInstallHTTPDoer

	// feishuJoinURLBuilder is invoked by the visibility=workspace
	// rejection card so the Feishu rejection surfaces a markdown
	// "Join workspace" link. Nil keeps the card link-free and
	// falls back to "Please contact the administrator above to join".
	feishuJoinURLBuilder func(workspaceID string) string

	// inviteSessions creates sessions for newly accepted invitees.
	inviteSessions auth.SessionStore
	// inviteCookieSecure mirrors the login handler's secure flag.
	inviteCookieSecure bool
	// publicURL is the base URL for invite links (e.g. https://app.example.com).
	publicURL string
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

func WithInteractionService(service interactionResolver) RouterOption {
	return func(cfg *routerConfig) {
		cfg.interactionService = service
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

// WithAuthProviders wires read-only auth provider discovery and diagnostics.
func WithAuthProviders(registry AuthProviderRegistry) RouterOption {
	return func(cfg *routerConfig) {
		cfg.authProviders = registry
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

// WithRunStreamBroker attaches product SSE subscriptions.
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

// WithAuditIngester wires the audit ingester for dev handlers that
// emit audit events directly (without going through *store.Store).
func WithAuditIngester(ingester AuditIngester) RouterOption {
	return func(cfg *routerConfig) {
		cfg.auditIngester = ingester
	}
}

// WithFeishuJoinURLBuilder wires the function the visibility=workspace
// rejection card uses to mint absolute "Join request" URLs. Nil keeps the
// card link-free and falls back to "Please contact the administrator above to join".
func WithFeishuJoinURLBuilder(builder func(workspaceID string) string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.feishuJoinURLBuilder = builder
	}
}

func WithInvite(sessions auth.SessionStore, cookieSecure bool, publicURL string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.inviteSessions = sessions
		cfg.inviteCookieSecure = cookieSecure
		cfg.publicURL = strings.TrimRight(publicURL, "/")
	}
}

// WithSkillInstallRunner lets tests drive /skills/install without executing
// npx. Production leaves this unset and uses the real skills CLI via npx.
func WithSkillInstallRunner(runner skillInstallCommandRunner) RouterOption {
	return func(cfg *routerConfig) {
		cfg.skillInstallRunner = runner
	}
}

func WithSkillInstallHTTPClient(client skillInstallHTTPDoer) RouterOption {
	return func(cfg *routerConfig) {
		cfg.skillInstallHTTPClient = client
	}
}

func RegisterRoutesWithStore(r chi.Router, runtimeStore RuntimeStore, opts ...RouterOption) {
	cfg := &routerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.runBroker == nil {
		cfg.runBroker = runstream.NewBroker(runstream.DefaultBufferSize)
	}

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
			// E2B-compatible sandbox smoke. Triggered from the admin
			// Runners page; takes a one-off API key, lifts a sandbox,
			// runs `command`, returns the result. Handler redacts the
			// supplied key from any error path before responding.
			// SSE streaming surface so the Web UI can drive a prompt
			// and watch deltas land in real time. No agent_run
			// lifecycle — caller manages via existing dev endpoints.
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
			if cfg.inviteSessions != nil {
				r.Post("/invite/info", getInviteInfo(runtimeStore))
				r.Group(func(r chi.Router) {
					r.Use(httprate.LimitBy(10, time.Minute, httprate.KeyByIP))
					r.Post("/invite/accept", acceptInvitation(runtimeStore, cfg))
				})
			}
			if cfg.oauthDeps != nil {
				// Real Feishu OIDC login. start → redirect to Feishu;
				// callback → upsert user + bind auth_identity + issue
				// session cookie; logout → revoke + clear cookie.
				r.Get("/auth/feishu/start", feishuStartHandler(*cfg.oauthDeps))
				r.Get("/auth/feishu/callback", feishuCallbackHandler(*cfg.oauthDeps))
				r.Post("/auth/logout", authLogoutHandler(*cfg.oauthDeps))
			}
			r.Get("/auth/providers", listAuthProviders(cfg.authProviders))
		})

		r.Group(func(r chi.Router) {
			require(r)
			if cfg.githubConnectionDeps != nil {
				r.Get("/connections/github/start", githubConnectionStartHandler(runtimeStore, *cfg.githubConnectionDeps))
				r.Get("/connections/github/callback", githubConnectionCallbackHandler(runtimeStore, *cfg.githubConnectionDeps))
			}
			r.Post("/agent-runs/{runID}/retry", retryAgentRun(runtimeStore))
			r.Post("/agents/{agentID}/disable", disableAgent(runtimeStore))
			r.Post("/agents/{agentID}/enable", enableAgent(runtimeStore))
			r.Get("/agents/{agentID}/scheduled-tasks", listScheduledTasks(runtimeStore))
			r.Post("/agents/{agentID}/scheduled-tasks", createScheduledTask(runtimeStore))
			r.Get("/workspaces/{workspaceID}/scheduled-tasks", listScheduledTasksByWorkspace(runtimeStore))
			r.Get("/scheduled-tasks/{taskID}", getScheduledTask(runtimeStore))
			r.Patch("/scheduled-tasks/{taskID}", updateScheduledTask(runtimeStore))
			r.Delete("/scheduled-tasks/{taskID}", deleteScheduledTask(runtimeStore))
			r.Post("/scheduled-tasks/{taskID}/run-now", runScheduledTaskNow(runtimeStore))
			r.Get("/scheduled-tasks/{taskID}/runs", listScheduledTaskRuns(runtimeStore))
			// Runtime binding: user picks which Runtime this agent
			// runs on. Replaces the legacy auto-sandbox path.
			r.Patch("/agents/{agentID}", updateAgent(runtimeStore))
			r.Patch("/agents/{agentID}/visibility", updateAgentVisibility(runtimeStore))
			r.Get("/agents/{agentID}/connector/feishu/diagnostics", getAgentFeishuConnectorDiagnostics(runtimeStore))
			r.Patch("/agents/{agentID}/connector/feishu", updateAgentFeishuConnector(runtimeStore))
			// Workspace-dimension IM connectors (feishu/slack/discord).
			// Read = any member; write = owner/admin (a misconfigured bot
			// can leak the workspace to the internet).
			r.Get("/workspaces/{workspaceID}/connectors", gateWorkspaceMember(runtimeStore, listWorkspaceConnectors(runtimeStore)))
			r.Patch("/workspaces/{workspaceID}/connector/slack", gateWorkspaceOwnerOrAdmin(runtimeStore, updateWorkspaceSlackConnector(runtimeStore)))
			r.Patch("/workspaces/{workspaceID}/connector/discord", gateWorkspaceOwnerOrAdmin(runtimeStore, updateWorkspaceDiscordConnector(runtimeStore)))
			r.Patch("/workspaces/{workspaceID}/connector/teams", gateWorkspaceOwnerOrAdmin(runtimeStore, updateWorkspaceTeamsConnector(runtimeStore)))
			r.Patch("/workspaces/{workspaceID}/connector/feishu", gateWorkspaceOwnerOrAdmin(runtimeStore, updateWorkspaceFeishuConnector(runtimeStore)))
			r.Post("/workspaces/{workspaceID}/connector/feishu/provision/begin", gateWorkspaceOwnerOrAdmin(runtimeStore, beginWorkspaceFeishuProvisioning(cfg.feishuRegistration)))
			r.Post("/workspaces/{workspaceID}/connector/feishu/provision/poll", gateWorkspaceOwnerOrAdmin(runtimeStore, pollWorkspaceFeishuProvisioning(runtimeStore, cfg.feishuRegistration)))
			r.Post("/agents/{agentID}/connector/feishu/provision/begin", beginAgentFeishuProvisioning(runtimeStore, cfg.feishuRegistration))
			r.Post("/agents/{agentID}/connector/feishu/provision/poll", pollAgentFeishuProvisioning(runtimeStore, cfg.feishuRegistration))
			r.Delete("/agents/{agentID}", deleteAgent(runtimeStore))
			// Sandbox lifecycle (admin UI). Scoped to (workspace,
			// agent). 503 when cfg.sandboxAdmin is not wired.
			// Reads require any active workspace member;
			// kill/rebuild (destructive — interrupts an in-flight run)
			// require owner/admin.
			// Workspace-scoped overview: every active sandbox binding,
			// newest-active first. Backs the admin Sandboxes page.
			// 503 in local mode.
			// Runtime banner status. Tells the operator which runner
			// mode this server is wired for and (for sandbox mode)
			// whether the provider is reachable.
			r.Get("/capabilities/marketplace", listMarketplaceCapabilities(runtimeStore))
			r.Get("/capabilities/marketplace/{capabilityID}", getMarketplaceCapabilityDetail(runtimeStore))
			r.Get("/workspaces/{workspaceID}/capabilities", listWorkspaceCapabilities(runtimeStore))
			r.Post("/workspaces/{workspaceID}/capabilities", createWorkspaceCapability(runtimeStore))
			// Capability import — preview is a pure parse; commit runs
			// the all-or-nothing materialization.
			r.Post("/workspaces/{workspaceID}/capabilities/import/preview", previewCapabilityImport(runtimeStore, cfg.blobStore))
			r.Post("/workspaces/{workspaceID}/capabilities/import/commit", commitCapabilityImport(runtimeStore, cfg.blobStore))
			registerSkillsDirectoryRoutes(r, runtimeStore, cfg)
			r.Post("/workspaces/{workspaceID}/capabilities/plugins/install", installPlugin(runtimeStore))
			// Plugin client bundle serving — browser fetches the built
			// client.js for each enabled plugin.
			r.Get("/plugins/{pluginName}/client.js", servePluginClient(cfg.pluginsDir))
			// Plugin upload presign — browser PUTs the zip directly to
			// the blob backend, then calls import/commit with the returned
			// ossKey. presign-download checks ossKey belongs to the calling
			// workspace to close the cross-tenant read hole.
			r.Post("/workspaces/{workspaceID}/uploads/presign-upload", presignUpload(runtimeStore, cfg.blobStore))
			r.Post("/workspaces/{workspaceID}/uploads/presign-download", presignDownload(runtimeStore, cfg.blobStore))
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
			r.Post("/workspaces/{workspaceID}/capabilities/{capabilityID}/versions/import/commit", commitCapabilityVersionImport(runtimeStore, cfg.blobStore))
			registerCoreEnvironmentRoutes(r, runtimeStore, cfg)
			r.Post("/agent-runs/{runID}/cancel", cancelAgentRun(runtimeStore, cfg))
			r.Post("/conversations/{conversationID}/cancel-all", cancelConversationRuns(runtimeStore, cfg))
			r.Post("/workspaces/{workspaceID}/secrets", createSecret(runtimeStore))
			r.Get("/workspaces/{workspaceID}/secrets", listSecrets(runtimeStore))
			r.Post("/workspaces/{workspaceID}/secrets/{secretID}/disable", disableSecret(runtimeStore))
			r.Post("/conversations/{conversationID}/external-ref", configureConversationExternalRef(runtimeStore))
			r.Get("/workspaces/{workspaceID}/agents", listWorkspaceEnabledAgents(runtimeStore))
			r.Get("/workspaces/{workspaceID}/agents/{agentID}", getWorkspaceAgent(runtimeStore))
			r.Get("/workspaces/{workspaceID}/agent-runs", listWorkspaceAgentRuns(runtimeStore))
			r.Get("/workspaces/{workspaceID}/interactions", gateWorkspaceMember(runtimeStore, listAgentInteractions(runtimeStore)))
			r.Post("/workspaces/{workspaceID}/interactions/{interactionID}/resolve", resolveAgentInteraction(runtimeStore, cfg))
			r.Get("/workspaces/{workspaceID}/agents/{agentID}/metrics", getAgentMetrics(runtimeStore))
			r.Get("/workspaces/{workspaceID}/agent-runs/{runID}/events", listAgentRunEvents(runtimeStore))
			r.Get("/workspaces/{workspaceID}/audit-records", listWorkspaceAuditRecords(runtimeStore))
			r.Get("/workspaces/{workspaceID}/connector-usage", listWorkspaceConnectorUsage(runtimeStore))
			r.Get("/workspaces/{workspaceID}/gateways", listWorkspaceGateways())
			r.Get("/workspaces/{workspaceID}/members", listWorkspaceMembers(runtimeStore))
			r.Post("/workspaces/{workspaceID}/members", addWorkspaceMember(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/members/{userID}", updateWorkspaceMemberRole(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/members/{userID}", removeWorkspaceMember(runtimeStore))
			if cfg.inviteSessions != nil {
				r.Post("/workspaces/{workspaceID}/invitations", createInvitation(runtimeStore, cfg))
				r.Get("/workspaces/{workspaceID}/invitations", listInvitations(runtimeStore, cfg))
				r.Patch("/workspaces/{workspaceID}/invitations/{invitationID}", updateInvitationRole(runtimeStore))
				r.Delete("/workspaces/{workspaceID}/invitations/{invitationID}", revokeInvitation(runtimeStore))
			}
			// Workspace self-service join request:
			//   POST   /workspaces/{wid}/join-requests              User submits request (identity: logged in)
			//   DELETE /workspaces/{wid}/join-requests/mine          Requester self-withdraws own pending
			//   GET    /workspaces/{wid}/join-requests              owner/admin views pending approval list
			//   POST   /workspaces/{wid}/join-requests/{rid}/approve owner/admin approves
			//   POST   /workspaces/{wid}/join-requests/{rid}/reject  owner/admin rejects
			r.Post("/workspaces/{workspaceID}/join-requests", createJoinRequest(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/join-requests/mine", withdrawOwnJoinRequest(runtimeStore))
			r.Get("/workspaces/{workspaceID}/join-requests", listJoinRequests(runtimeStore))
			r.Post("/workspaces/{workspaceID}/join-requests/{requestID}/approve", approveJoinRequest(runtimeStore))
			r.Post("/workspaces/{workspaceID}/join-requests/{requestID}/reject", rejectJoinRequest(runtimeStore))
			r.Get("/workspaces/{workspaceID}/settings", getWorkspaceSettings(runtimeStore))
			r.Patch("/workspaces/{workspaceID}/settings", patchWorkspaceSettings(runtimeStore))
			r.Get("/workspaces/{workspaceID}/auth/providers", listWorkspaceAuthProviders(runtimeStore, cfg.authProviders))
			r.Post("/workspaces/{workspaceID}/agents", createAgent(runtimeStore))
			r.Post("/workspaces", createWorkspace(runtimeStore))
			r.Patch("/workspaces/{workspaceID}", updateWorkspace(runtimeStore))
			r.Post("/workspaces/{workspaceID}/archive", archiveWorkspace(runtimeStore))
			r.Get("/me", meHandler(runtimeStore))
			r.Get("/me/workspaces", listMyWorkspaces(runtimeStore))
			// Workspace discovery: returns public workspaces the current user can request to join
			r.Get("/me/discoverable-workspaces", listDiscoverableWorkspaces(runtimeStore))
			// Platform-wide user picker. Backs the search box in
			// AddMember dialogs. Any authenticated user can search;
			// the actual add still requires workspace owner/admin.
			r.Get("/users/search", searchUsers(runtimeStore))
			r.Get("/me/credentials", listMyCredentials(runtimeStore))
			r.Post("/me/credentials", createMyCredential(runtimeStore))
			r.Patch("/me/credentials/{credentialID}", patchMyCredential(runtimeStore))
			r.Delete("/me/credentials/{credentialID}", deleteMyCredential(runtimeStore))
			r.Get("/workspaces/{workspaceID}/usage", listWorkspaceUsageLogs(runtimeStore))
			r.Get("/workspaces/{workspaceID}/agents/{agentID}/capabilities", listAgentCapabilities(runtimeStore))
			r.Delete("/workspaces/{workspaceID}/agents/{agentID}/capabilities/{capabilityVersionID}", deleteAgentCapability(runtimeStore))
			r.Get("/workspaces/{workspaceID}/conversations", listWorkspaceConversations(runtimeStore))
			r.Post("/workspaces/{workspaceID}/conversations", createWorkspaceConversation(runtimeStore))
			r.Get("/conversations/{conversationID}", getConversation(runtimeStore))
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
