// Package feishuoutbound assembles the running Feishu outbound worker
// from the standalone pieces in package gateway. It owns:
//
//   - the FeishuTenantClient cache keyed by (workspace_id, app_id);
//   - the CredentialResolver that uses env for the default shared Bot and
//     fetches + decrypts dedicated Bot app_secret refs from the vault;
//   - the Sender that pushes a message via Feishu im/v1/messages;
//   - the polling daemon goroutine that ties the loop together.
package feishuoutbound

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Logger is a tiny logging shim — interface (not *slog.Logger) so
// callers can wire their preferred logger or no-op in tests.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any) {}
func (noopLogger) Warn(string, ...any) {}

// Storer is the slice of *store.Store the worker needs, scoped as an
// interface so unit tests can fake it without Postgres.
type Storer interface {
	// MarkGatewayOutboundDelivered stamps gateway_delivered_at on the
	// originating messages row after the driver lands a terminal patch.
	// ClaimActiveFeishuInflightConversations filters on this stamp; if
	// it's missing the driver re-claims and re-sends the done card
	// every tick.
	MarkGatewayOutboundDelivered(ctx context.Context, input store.MarkGatewayOutboundDeliveredInput) (store.MarkGatewayOutboundDeliveredResult, error)
	GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error)
	GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error)

	ListActiveFeishuInflightConversations(ctx context.Context, cutoff time.Time, limit int32) ([]store.FeishuInflightConversation, error)
	ClaimActiveFeishuInflightConversations(ctx context.Context, input store.ClaimActiveFeishuInflightConversationsInput) ([]store.FeishuInflightConversation, error)
	ListAgentRunEventsAfterSeq(ctx context.Context, runID string, afterSeq int64, limit int32) ([]store.AgentRunEvent, error)
	UpsertConversationInflightWorkingCard(ctx context.Context, input store.UpsertConversationInflightWorkingCardInput) (store.WorkingInflightSlot, error)
	ClearConversationInflightSlot(ctx context.Context, conversationID string, slot store.InflightSlotKind, expectedAgentRunID string) error
	// MarkConversationInflightTerminalDelivered stamps a per-run
	// fingerprint so runs that failed without producing an
	// output_message_id (FailAgentRun path) don't loop forever in the
	// claim CTE — without it the messages-side gateway_delivered_at
	// marker is unreachable and the OR branch stays permanently true.
	MarkConversationInflightTerminalDelivered(ctx context.Context, conversationID, runID string) error

	UpsertConversationInflightPermissionCard(ctx context.Context, input store.UpsertConversationInflightPermissionCardInput) (store.PermissionInflightSlot, error)
	UpsertConversationInflightPromptForUserChoiceCard(ctx context.Context, input store.UpsertConversationInflightPromptForUserChoiceCardInput) (store.PromptForUserChoiceInflightSlot, error)
	GetConversationInflightCards(ctx context.Context, conversationID string) (store.ConversationInflightCards, error)
	FindConversationByPermissionRequestID(ctx context.Context, permissionRequestID string) (store.ConversationInflightCards, error)
	FindConversationByPromptForUserChoiceRequestID(ctx context.Context, requestID string) (store.ConversationInflightCards, error)
	ListStaleFeishuPermissionInflightCards(ctx context.Context, cutoff time.Time, limit int32) ([]store.ConversationInflightCards, error)
	ListStaleFeishuPromptForUserChoiceInflightCards(ctx context.Context, cutoff time.Time, limit int32) ([]store.ConversationInflightCards, error)

	FindLatestFeishuInboundReactionByConversation(ctx context.Context, conversationID string) (store.FeishuInboundReactionRow, error)
	// FindFeishuInboundReactionByAgentRun pins the undo target to the
	// specific inbound that triggered this run, so a fast-typing user
	// who sends a second message before the first run terminates does
	// not have the new typing reaction cleared by the old terminal.
	FindFeishuInboundReactionByAgentRun(ctx context.Context, agentRunID string) (store.FeishuInboundReactionRow, error)
	ClearFeishuInboundReaction(ctx context.Context, messageID string) error

	LoadDoneCardRunData(ctx context.Context, workspaceID, runID string) (store.DoneCardRunData, error)

	// SendSystemNoticeMessage is the dead-letter notice path used when
	// the retry budget is exhausted. Idempotent on
	// (conversation_id, metadata.kind).
	SendSystemNoticeMessage(ctx context.Context, input store.SendSystemNoticeMessageInput) (store.SendSystemNoticeMessageResult, error)

	ListCapabilityCredentialMissingForRun(ctx context.Context, conversationID, runID string) ([]store.CapabilityCredentialMissingNotice, error)
	GetInboundUserMessageForRun(ctx context.Context, conversationID, runID string) (store.InboundUserMessageForRun, error)
	// GetGuestReplyHintForRun surfaces the unregistered-user register
	// prompt onto the terminal Feishu error card; see store.Store impl
	// for why GetInboundUserMessageForRun can't carry it.
	GetGuestReplyHintForRun(ctx context.Context, conversationID, runID string) (string, error)
	WritePendingCredentialFormSlot(ctx context.Context, conversationID string, slot store.PendingCredentialFormSlot) (store.PendingCredentialFormSlot, error)
	UpdatePendingCredentialFormSlotMessageID(ctx context.Context, conversationID, qkey, externalMsgID string) error
	ClearPendingCredentialFormSlotByConversation(ctx context.Context, conversationID string) error

	// ClaimPendingQueuedFeishuRuns must use a claim CTE — without it,
	// every sibling pod's tick SELECT-ed the same queued runs and the
	// user got N duplicates of the same "排队中" card.
	ClaimPendingQueuedFeishuRuns(ctx context.Context, input store.ClaimPendingQueuedFeishuRunsInput) ([]store.PendingQueuedFeishuRun, error)
	QueuePositionForRun(ctx context.Context, runID string) (int, error)
	StampQueueCardSent(ctx context.Context, runID string, now time.Time) error

	// ResolveAgentNameForConversation is the per-card-title fallback
	// for paths with a conversation row but no agent_run to join
	// through (auto-expire permission card patch). Returns empty when
	// nothing is selected or the agent was soft-deleted; callers fall
	// back to gateway.FeishuCardTitle.
	ResolveAgentNameForConversation(ctx context.Context, conversationID string) (string, error)
}

// SecretDecrypter mirrors the small subset of *secrets.Service we use.
type SecretDecrypter interface {
	Decrypt(envelopeJSON []byte) (map[string]any, error)
}

type DefaultSharedBotConfig struct {
	AppID     string
	AppSecret string
}

func (c DefaultSharedBotConfig) normalized() DefaultSharedBotConfig {
	return DefaultSharedBotConfig{
		AppID:     strings.TrimSpace(c.AppID),
		AppSecret: strings.TrimSpace(c.AppSecret),
	}
}

func (c DefaultSharedBotConfig) configured() bool {
	c = c.normalized()
	return c.AppID != "" && c.AppSecret != ""
}

type Options struct {
	Store            Storer
	Secrets          SecretDecrypter
	Logger           Logger
	PollInterval     time.Duration                     // default 2s
	BaseURL          string                            // Feishu OpenAPI base; "" defaults
	HTTPClient       gateway.FeishuTenantClientOptions // shared HTTPClient if set
	DefaultSharedBot DefaultSharedBotConfig

	// PublicURL is the externally-reachable base URL of the Parsar web
	// UI; used to deep-link the failed-run ErrorCard back to the run
	// detail page. Empty disables the link.
	PublicURL string

	// AppSecretField is the key inside the decrypted secret payload
	// where the plain app_secret string lives. Defaults to "app_secret".
	AppSecretField string

	// PermissionRouter wires the auto-expire fallback that pushes a
	// Deny verdict back into the runtime. Nil-tolerant: auto-expire
	// becomes a no-op and the card stays pinned for inbound
	// handleCardAction to resolve.
	PermissionRouter PermissionRouter

	// DeviceResolver returns the agent_daemon device id owning a
	// conversation. Stamped onto the inflight slot when the card is
	// written so the card-callback path (Phase 2) can resolve the
	// owning pod without re-walking the binding tree. Nil-tolerant:
	// missing resolver → slot.DeviceID stays "" and the callback
	// degrades to same-pod lookup.
	DeviceResolver DeviceResolver

	// ClaimedBy is the pod identity stamped onto every claimed message
	// so operators can tell which pod owned the dispatch. Empty falls
	// back to HOSTNAME env, then to a random hex string.
	ClaimedBy string

	// Audit is the optional ingester for dead-letter audit rows. Nil
	// is fine — driver branches on w.audit != nil before emitting.
	Audit *audit.Ingester
}

// PermissionRouter is the auto-expire path's hook for pushing a Deny
// verdict back into the runtime. Kept separate from
// feishuinbound.PermissionRouter so the two packages don't have to
// depend on each other.
type PermissionRouter interface {
	SubmitPermission(ctx context.Context, decision PermissionDecision) error
}

// DeviceResolver maps a conversation_id to the agent_daemon device id
// that owns it. Outbound uses this to stamp device_id onto inflight
// slots when writing permission / prompt_for_user_choice cards.
type DeviceResolver interface {
	ResolveDeviceByConversation(ctx context.Context, conversationID string) (string, error)
}

// PermissionDecision mirrors connector.PermissionDecision in shape;
// re-declared here so feishuoutbound doesn't depend on the connector
// package.
type PermissionDecision struct {
	RequestID  string
	Approved   bool
	Note       string
	OperatorID string
}

// Worker runs the inflight-card driver poll loop.
type Worker struct {
	store          Storer
	secrets        SecretDecrypter
	logger         Logger
	pollEvery      time.Duration
	baseURL        string
	publicURL      string
	httpOpts       gateway.FeishuTenantClientOptions
	secretKey      string
	defaultBot     DefaultSharedBotConfig
	permRouter     PermissionRouter
	deviceResolver DeviceResolver
	claimedBy      string

	audit *audit.Ingester

	mu      sync.Mutex
	clients map[string]*gateway.FeishuTenantClient // key = workspace_id + "|" + app_id
}

// NewWorker validates options and constructs the worker. The returned
// Worker is inert until Run() is called.
func NewWorker(opts Options) (*Worker, error) {
	if opts.Store == nil {
		return nil, errors.New("feishuoutbound: Store is required")
	}
	if opts.Secrets == nil {
		return nil, errors.New("feishuoutbound: Secrets decrypter is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	pollEvery := opts.PollInterval
	if pollEvery <= 0 {
		pollEvery = 2 * time.Second
	}
	secretKey := strings.TrimSpace(opts.AppSecretField)
	if secretKey == "" {
		secretKey = "app_secret"
	}
	defaultBot := opts.DefaultSharedBot.normalized()
	if (defaultBot.AppID == "") != (defaultBot.AppSecret == "") {
		return nil, errors.New("feishuoutbound: DefaultSharedBot requires both AppID and AppSecret")
	}

	claimedBy := strings.TrimSpace(opts.ClaimedBy)
	if claimedBy == "" {
		claimedBy = strings.TrimSpace(os.Getenv("HOSTNAME"))
	}
	if claimedBy == "" {
		// HOSTNAME is empty outside k8s. Random hex keeps two pods on
		// the same host distinct in the metadata trace; uniqueness
		// only, not unpredictability.
		buf := make([]byte, 8)
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("feishuoutbound: derive claimed_by fallback: %w", err)
		}
		claimedBy = "worker-" + hex.EncodeToString(buf)
	}

	return &Worker{
		store:          opts.Store,
		secrets:        opts.Secrets,
		logger:         logger,
		pollEvery:      pollEvery,
		baseURL:        strings.TrimSpace(opts.BaseURL),
		publicURL:      strings.TrimSpace(opts.PublicURL),
		httpOpts:       opts.HTTPClient,
		secretKey:      secretKey,
		defaultBot:     defaultBot,
		permRouter:     opts.PermissionRouter,
		deviceResolver: opts.DeviceResolver,
		claimedBy:      claimedBy,
		audit:          opts.Audit,
		clients:        make(map[string]*gateway.FeishuTenantClient),
	}, nil
}

// resolveDeviceID looks up the agent_daemon device id that owns
// conversationID. Returns "" when no resolver is wired (test paths),
// when no agent_daemon binding exists (non-agent_daemon connectors),
// or on transient DB errors — slots tolerate an empty DeviceID by
// falling back to a same-pod lookup on the callback path.
func (w *Worker) resolveDeviceID(ctx context.Context, conversationID string) string {
	if w.deviceResolver == nil {
		return ""
	}
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ""
	}
	deviceID, err := w.deviceResolver.ResolveDeviceByConversation(ctx, conversationID)
	if err != nil {
		// ErrNotBound is expected for non-agent_daemon connectors; quiet
		// log so the working-card path doesn't spam.
		w.logger.Info("feishu inflight: device resolve skipped",
			"conversation_id", conversationID, "err", err.Error())
		return ""
	}
	return strings.TrimSpace(deviceID)
}

// Run drives the poll loop until ctx is cancelled. Returns ctx.Err()
// on clean shutdown. One tick per PollInterval; a tick that finds no
// work returns instantly.
func (w *Worker) Run(ctx context.Context) error {
	w.logger.Info("feishu inflight driver starting", "poll_interval", w.pollEvery.String())
	timer := time.NewTimer(w.pollEvery)
	defer timer.Stop()

	// First tick fires fast so smoke tests don't wait 10s.
	if !timer.Stop() {
		<-timer.C
	}
	timer.Reset(100 * time.Millisecond)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("feishu inflight driver stopping", "reason", ctx.Err().Error())
			return ctx.Err()
		case <-timer.C:
			if _, err := w.TickOnce(ctx); err != nil {
				// per-conversation errors are logged inside
				// InflightTickOnce; a top-level error here means the
				// LIST failed and we should keep looping rather than
				// exit Run.
				_ = err
			}
			timer.Reset(w.pollEvery)
		}
	}
}

// TickOnce runs a single driver pass. Exposed so tests can drive the
// worker deterministically without Run + sleeps.
//
// Delegates to:
//   - QueueCardTickOnce: one-shot "排队中" placeholders for queued runs
//   - InflightTickOnce: per-conversation working/permission card
//     patching for the run currently holding the inflight slot
//
// The two share no state on the conversation row — the queue card is
// a fire-and-forget reply, not an upsert into gateway_inflight — so
// order doesn't matter for correctness. Queue-card runs first so the
// user sees the placeholder ASAP after a quick second message even if
// the inflight tick is slow.
func (w *Worker) TickOnce(ctx context.Context) (processed int, err error) {
	queued, qErr := w.QueueCardTickOnce(ctx)
	if qErr != nil {
		w.logger.Warn("feishu queue card tick failed", "err", qErr.Error())
	}
	inflight, iErr := w.InflightTickOnce(ctx)
	if iErr != nil {
		return queued + inflight, iErr
	}
	return queued + inflight, nil
}

// resolveCredentials implements gateway.CredentialResolver. Returns
// ErrUnresolvableOutbound when the source_app_id no longer matches a
// live Agent (dead-letter signal).
func (w *Worker) resolveCredentials(ctx context.Context, msg gateway.PendingOutboundMessage) (gateway.OutboundCredentials, error) {
	if w.isDefaultSharedBotApp(msg.SourceAppID) {
		return gateway.OutboundCredentials{AppID: w.defaultBot.AppID, AppSecret: w.defaultBot.AppSecret}, nil
	}

	route, err := w.store.GetAgentByFeishuAppID(ctx, msg.SourceAppID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gateway.OutboundCredentials{}, fmt.Errorf("%w: app_id=%s no live agent", gateway.ErrUnresolvableOutbound, msg.SourceAppID)
		}
		return gateway.OutboundCredentials{}, fmt.Errorf("get agent by feishu app_id: %w", err)
	}
	cfg, ok, err := gateway.DecodeFeishuConnectorConfig(route.Config)
	if err != nil {
		return gateway.OutboundCredentials{}, fmt.Errorf("decode connector config: %w", err)
	}
	if !ok || !cfg.Enabled {
		return gateway.OutboundCredentials{}, fmt.Errorf("%w: connector not enabled on agent=%s", gateway.ErrUnresolvableOutbound, route.AgentID)
	}
	if strings.TrimSpace(cfg.AppSecretRef) == "" {
		return gateway.OutboundCredentials{}, fmt.Errorf("%w: agent=%s missing app_secret_ref", gateway.ErrUnresolvableOutbound, route.AgentID)
	}
	payload, err := w.store.GetSecretPayload(ctx, route.WorkspaceID, cfg.AppSecretRef)
	if err != nil {
		return gateway.OutboundCredentials{}, fmt.Errorf("read app_secret payload: %w", err)
	}
	decrypted, err := w.secrets.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return gateway.OutboundCredentials{}, fmt.Errorf("decrypt app_secret: %w", err)
	}
	rawSecret, _ := decrypted[w.secretKey].(string)
	rawSecret = strings.TrimSpace(rawSecret)
	if rawSecret == "" {
		return gateway.OutboundCredentials{}, fmt.Errorf("%w: agent=%s app_secret payload missing %q field", gateway.ErrUnresolvableOutbound, route.AgentID, w.secretKey)
	}
	return gateway.OutboundCredentials{AppID: cfg.AppID, AppSecret: rawSecret}, nil
}

func (w *Worker) isDefaultSharedBotApp(appID string) bool {
	return w.defaultBot.configured() && strings.TrimSpace(appID) == w.defaultBot.AppID
}

// clientFor returns the cached FeishuTenantClient for (workspace, app_id)
// or constructs a fresh one. The cached client does NOT hold the
// app_secret (only the tenant_access_token survives across dispatches),
// so vault rotation of the secret takes effect on the next token cache
// miss without needing to evict the client.
func (w *Worker) clientFor(workspaceID, appID string) (*gateway.FeishuTenantClient, error) {
	key := workspaceID + "|" + appID
	w.mu.Lock()
	defer w.mu.Unlock()
	if c, ok := w.clients[key]; ok {
		return c, nil
	}
	opts := w.httpOpts
	opts.AppID = appID
	if opts.BaseURL == "" {
		opts.BaseURL = w.baseURL
	}
	client, err := gateway.NewFeishuTenantClient(opts)
	if err != nil {
		return nil, fmt.Errorf("init tenant client for workspace=%s app=%s: %w", workspaceID, appID, err)
	}
	w.clients[key] = client
	return client, nil
}

// ResetClientCache discards all cached tenant clients (and their
// cached tenant_access_tokens). For a single Bot's secret rotation
// prefer InvalidateTokenCacheForApp.
func (w *Worker) ResetClientCache() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clients = make(map[string]*gateway.FeishuTenantClient)
}

// InvalidateTokenCacheForApp drops the cached tenant_access_token for
// (workspace, app_id) without evicting the client itself; wired into
// the admin "rotate Bot secret" endpoint so the next dispatch
// refetches a token using the new secret.
func (w *Worker) InvalidateTokenCacheForApp(workspaceID, appID string) {
	key := workspaceID + "|" + appID
	w.mu.Lock()
	c, ok := w.clients[key]
	w.mu.Unlock()
	if !ok {
		return
	}
	c.InvalidateTokenCache()
}

// MaskedConfigJSON dumps the connector config with secret refs (NOT
// decrypted secrets) for support / debug commands.
func MaskedConfigJSON(cfg gateway.FeishuConnectorConfig) ([]byte, error) {
	view := map[string]any{
		"enabled":                cfg.Enabled,
		"app_id":                 cfg.AppID,
		"app_secret_ref":         cfg.AppSecretRef,
		"verification_token_ref": cfg.VerificationTokenRef,
		"encrypt_key_ref":        cfg.EncryptKeyRef,
		"bot_open_id":            cfg.BotOpenID,
		"event_mode":             cfg.EventMode,
		"routing_mode":           cfg.RoutingMode,
	}
	return json.Marshal(view)
}

// MustSecretsDecrypter adapts *secrets.Service into SecretDecrypter.
func MustSecretsDecrypter(svc *secrets.Service) SecretDecrypter {
	if svc == nil {
		panic("feishuoutbound: nil secrets.Service")
	}
	return svc
}
