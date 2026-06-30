// Package feishuinbound owns the Feishu event-websocket consumer for
// QR-provisioned Agent Bots. It deliberately reuses package gateway's
// routing/gate logic so websocket and webhook inbound paths produce the
// same Parsar conversation/message/run records.
package feishuinbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	"github.com/larksuite/oapi-sdk-go/v3/ws"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/feishushared"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Logger is the small logging surface Manager uses. *slog.Logger satisfies it.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any) {}
func (noopLogger) Warn(string, ...any) {}

// Storer is the subset of *store.Store the websocket manager needs.
type Storer interface {
	ListFeishuWebSocketAgents(ctx context.Context) ([]store.FeishuAgentRoute, error)
	GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error)
	GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error)
	ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error)
	UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error
	GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error)
	ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error
	FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error)
	IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error)
	// GetWorkspaceVisibility + ListActiveWorkspaceOwnerNames feed the
	// visibility=workspace rejection card. Errors are swallowed by the
	// gateway router; the rejection still goes out when these reads fail.
	GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error)
	ListActiveWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error)
	GetSecretPayload(ctx context.Context, workspaceID string, secretID string) (store.SecretPayload, error)
	CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error)

	FindConversationByPermissionRequestID(ctx context.Context, permissionRequestID string) (store.ConversationInflightCards, error)
	FindConversationByPromptForUserChoiceRequestID(ctx context.Context, requestID string) (store.ConversationInflightCards, error)
	ClearConversationInflightSlot(ctx context.Context, conversationID string, slot store.InflightSlotKind, expectedAgentRunID string) error

	// RecordFeishuInboundReaction stamps the reaction_id returned by the
	// Typing-reaction API onto the inbound row; the outbound terminal
	// path reads it back via FindFeishuInboundReactionByExternalID
	// (store_reactions.go) to remove the reaction at terminal time.
	RecordFeishuInboundReaction(ctx context.Context, input store.RecordFeishuInboundReactionInput) error

	// Credential-form submit path:
	//   * ClearPendingCredentialFormSlotByConversation drops a stale
	//     pending slot when a fresh user query arrives without going
	//     through the form.
	//   * ClaimPendingCredentialFormSlot atomically returns the slot to
	//     exactly one caller so two pods racing on the same qkey cannot
	//     both write credentials.
	//   * ReplaceUserCredentials writes per-kind entries in one
	//     transaction so a partial failure rolls back instead of leaving
	//     half the kinds bound.
	ClearPendingCredentialFormSlotByConversation(ctx context.Context, conversationID string) error
	ClaimPendingCredentialFormSlot(ctx context.Context, qkey string) (store.ClaimedPendingCredentialForm, error)
	CreateUserCredential(ctx context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error)
	ReplaceUserCredentials(ctx context.Context, userID string, inputs []store.CreateUserCredentialInput) ([]store.ReplaceUserCredentialResult, error)
	GetConversation(ctx context.Context, conversationID string) (store.ConversationRead, error)
	// FindConversationByExternalRef + CancelAllInflightForConversation
	// back the /cancel and /cancel all Feishu commands.
	FindConversationByExternalRef(ctx context.Context, gateway, externalChatID, externalThreadID string) (string, error)
	CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]store.SupersededRun, error)
	// GetConversationInflightCards backs the ask-pending fast path: a
	// free-text reply that lands while a PromptForUserChoice slot is
	// still waiting should be delivered as the answer rather than
	// enqueued as a fresh prompt.
	GetConversationInflightCards(ctx context.Context, conversationID string) (store.ConversationInflightCards, error)
	// FindPendingAskByChat is the chat-wide ask-pending fallback used
	// when the new inbound lands on a different thread than the asking
	// conversation — common when the user replies as a fresh message
	// instead of a thread reply.
	FindPendingAskByChat(ctx context.Context, gateway, externalChatID string) (string, store.PromptForUserChoiceInflightSlot, error)
	// HasFeishuThreadInboundHistory backs the "话题续聊不必再 @" rule in
	// isGroupMessageWithoutBotMention.
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)

	// ResolveAgentNameForConversation returns the per-card header title
	// for inbound paths without an agent_run in hand. Returns empty on
	// miss; callers fall back to gateway.FeishuCardTitle.
	ResolveAgentNameForConversation(ctx context.Context, conversationID string) (string, error)
}

// PermissionRouter pushes a user's Allow / Deny verdict back into the
// runtime so the agent run resumes. A nil PermissionRouter is tolerated:
// handleCardAction returns a "permission router not configured" toast.
type PermissionRouter interface {
	SubmitPermission(ctx context.Context, decision PermissionDecision) error
}

// PermissionDecision mirrors connector.PermissionDecision; kept gateway-
// side so feishuinbound doesn't depend on the connector package.
type PermissionDecision struct {
	RequestID  string
	Approved   bool
	Note       string
	OperatorID string
}

// PromptForUserChoiceRouter pushes the human's pick for an outstanding
// AskUserQuestion back to the runtime. Nil-tolerant: missing router
// surfaces a "service not configured" toast.
type PromptForUserChoiceRouter interface {
	SubmitPromptForUserChoice(ctx context.Context, decision PromptForUserChoiceDecision) error
}

// PromptForUserChoiceQuestionAnswer mirrors
// connector.PromptForUserChoiceQuestionAnswer; gateway-side so this
// package stays free of the connector import.
type PromptForUserChoiceQuestionAnswer struct {
	Header string
	Answer string
}

// PromptForUserChoiceDecision mirrors connector.PromptForUserChoiceDecision;
// kept gateway-side so feishuinbound stays free of the connector
// package.
type PromptForUserChoiceDecision struct {
	RequestID       string
	QuestionAnswers []PromptForUserChoiceQuestionAnswer
	Answers         []string
	Cancelled       bool
	Reason          string
	OperatorID      string
}

// SecretDecrypter mirrors the small subset of *secrets.Service we use.
type SecretDecrypter interface {
	Decrypt(envelopeJSON []byte) (map[string]any, error)
	Encrypt(payload map[string]any) ([]byte, error)
}

// DefaultSharedBotConfig describes the instance-level default Feishu Bot.
// It is not stored on any Agent; Agents without their own dedicated Bot
// connector are selected through this shared entry point.
type DefaultSharedBotConfig struct {
	AppID     string
	AppSecret string
	BotOpenID string
}

func (c DefaultSharedBotConfig) normalized() DefaultSharedBotConfig {
	return DefaultSharedBotConfig{
		AppID:     strings.TrimSpace(c.AppID),
		AppSecret: strings.TrimSpace(c.AppSecret),
		BotOpenID: strings.TrimSpace(c.BotOpenID),
	}
}

func (c DefaultSharedBotConfig) configured() bool {
	c = c.normalized()
	return c.AppID != "" && c.AppSecret != ""
}

// Options configures Manager. Store and Secrets are required.
type Options struct {
	Store            Storer
	Secrets          SecretDecrypter
	Logger           Logger
	RefreshInterval  time.Duration // default 30s
	Domain           string        // default SDK domain; accepts https://open.feishu.cn or open.feishu.cn
	OpenAPIBaseURL   string        // optional REST OpenAPI base; empty uses FeishuTenantClient default
	DefaultSharedBot DefaultSharedBotConfig

	// AppSecretFields are tried in order inside decrypted secret payloads.
	// Defaults to app_secret, secret, value, api_key.
	AppSecretFields []string

	// PermissionRouter wires the permission-card callback. If nil,
	// handleCardAction responds with a "permission router not configured"
	// toast.
	PermissionRouter PermissionRouter

	// PromptForUserChoiceRouter wires the AskUserQuestion card
	// callback. Same nil-tolerance: missing router → toast.
	PromptForUserChoiceRouter PromptForUserChoiceRouter

	// JoinURLBuilder, when non-nil, mints the absolute URL the
	// visibility=workspace rejection card surfaces as a "申请加入" link.
	// Nil falls back to "请联系上述管理员加入".
	JoinURLBuilder func(workspaceID string) string
}

// Manager reconciles configured websocket Bots and keeps one SDK WS client per
// active app_id. Run blocks until ctx is cancelled.
type Manager struct {
	store          Storer
	secrets        SecretDecrypter
	logger         Logger
	refresh        time.Duration
	domain         string
	openAPIBaseURL string
	secretKeys     []string
	defaultBot     DefaultSharedBotConfig

	// permRouter is nil-tolerant: handleCardAction falls back to a
	// configuration-error toast when missing.
	permRouter PermissionRouter

	// pfucRouter is the ask-flow twin. Same nil-tolerance.
	pfucRouter PromptForUserChoiceRouter

	joinURLBuilder func(workspaceID string) string

	mu      sync.Mutex
	clients map[string]*clientHandle
}

type clientHandle struct {
	key    string
	route  store.FeishuAgentRoute
	cfg    gateway.FeishuConnectorConfig
	client *ws.Client
	ctx    context.Context
	cancel context.CancelFunc
	source string
}

// NewManager validates options and returns an inert manager.
func NewManager(opts Options) (*Manager, error) {
	if opts.Store == nil {
		return nil, errors.New("feishuinbound: Store is required")
	}
	if opts.Secrets == nil {
		return nil, errors.New("feishuinbound: Secrets decrypter is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	refresh := opts.RefreshInterval
	if refresh <= 0 {
		refresh = 30 * time.Second
	}
	if refresh > 10*time.Minute {
		refresh = 10 * time.Minute
	}
	domain := normalizeDomain(opts.Domain)
	openAPIBaseURL := normalizeBaseURL(opts.OpenAPIBaseURL)
	if openAPIBaseURL == "" && domain != "" {
		openAPIBaseURL = "https://" + domain
	}
	secretKeys := opts.AppSecretFields
	if len(secretKeys) == 0 {
		secretKeys = []string{"app_secret", "secret", "value", "api_key"}
	}
	defaultBot := opts.DefaultSharedBot.normalized()
	if (defaultBot.AppID == "") != (defaultBot.AppSecret == "") {
		return nil, errors.New("feishuinbound: DefaultSharedBot requires both AppID and AppSecret")
	}
	return &Manager{
		store:          opts.Store,
		secrets:        opts.Secrets,
		logger:         logger,
		refresh:        refresh,
		domain:         domain,
		openAPIBaseURL: openAPIBaseURL,
		secretKeys:     append([]string(nil), secretKeys...),
		defaultBot:     defaultBot,
		permRouter:     opts.PermissionRouter,
		pfucRouter:     opts.PromptForUserChoiceRouter,
		joinURLBuilder: opts.JoinURLBuilder,
		clients:        make(map[string]*clientHandle),
	}, nil
}

// Run starts the reconcile loop. It returns ctx.Err() on normal shutdown.
func (m *Manager) Run(ctx context.Context) error {
	m.logger.Info("feishu websocket inbound manager starting", "refresh_interval", m.refresh.String())
	defer m.stopAll()

	if err := m.Reconcile(ctx); err != nil {
		m.logger.Warn("feishu websocket inbound initial reconcile failed", "err", err.Error())
	}
	ticker := time.NewTicker(m.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("feishu websocket inbound manager stopping", "reason", ctx.Err().Error())
			return ctx.Err()
		case <-ticker.C:
			if err := m.Reconcile(ctx); err != nil {
				m.logger.Warn("feishu websocket inbound reconcile failed", "err", err.Error())
			}
		}
	}
}

// Reconcile starts missing clients and stops clients for disabled/removed Bots.
func (m *Manager) Reconcile(ctx context.Context) error {
	routes, err := m.store.ListFeishuWebSocketAgents(ctx)
	if err != nil {
		return err
	}

	wanted := make(map[string]struct{}, len(routes)+1)
	if m.defaultBot.configured() {
		route, cfg := m.defaultSharedRouteAndConfig()
		key := defaultClientKey(cfg.AppID)
		wanted[key] = struct{}{}
		if !m.hasClient(key) {
			if err := m.startClientWithSecret(ctx, route, cfg, key, m.defaultBot.AppSecret, "default_shared"); err != nil {
				m.logger.Warn("feishu websocket inbound: start default shared client failed", "app_id", cfg.AppID, "err", err.Error())
			}
		}
	}

	for _, route := range routes {
		cfg, ok, err := gateway.DecodeFeishuConnectorConfig(route.Config)
		if err != nil {
			m.logger.Warn("feishu websocket inbound: decode connector failed", "agent_id", route.AgentID, "err", err.Error())
			continue
		}
		if !ok || !cfg.Enabled || !strings.EqualFold(strings.TrimSpace(cfg.EventMode), "websocket") {
			continue
		}
		appID := strings.TrimSpace(cfg.AppID)
		if appID == "" || strings.TrimSpace(cfg.AppSecretRef) == "" {
			m.logger.Warn("feishu websocket inbound: connector missing app_id or app_secret_ref", "agent_id", route.AgentID)
			continue
		}
		key := clientKey(route.WorkspaceID, appID)
		wanted[key] = struct{}{}
		if m.hasClient(key) {
			continue
		}
		if err := m.startClient(ctx, route, cfg, key); err != nil {
			m.logger.Warn("feishu websocket inbound: start client failed", "agent_id", route.AgentID, "app_id", appID, "err", err.Error())
		}
	}

	m.mu.Lock()
	stale := make([]*clientHandle, 0)
	for key, h := range m.clients {
		if _, ok := wanted[key]; !ok {
			stale = append(stale, h)
			delete(m.clients, key)
		}
	}
	m.mu.Unlock()
	for _, h := range stale {
		m.stopClient(h)
	}
	return nil
}

func (m *Manager) hasClient(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.clients[key]
	return ok
}

func (m *Manager) startClient(ctx context.Context, route store.FeishuAgentRoute, cfg gateway.FeishuConnectorConfig, key string) error {
	appSecret, err := m.loadAppSecret(ctx, route.WorkspaceID, cfg.AppSecretRef)
	if err != nil {
		return err
	}
	return m.startClientWithSecret(ctx, route, cfg, key, appSecret, "agent_connector")
}

func (m *Manager) startClientWithSecret(ctx context.Context, route store.FeishuAgentRoute, cfg gateway.FeishuConnectorConfig, key, appSecret, source string) error {
	appSecret = strings.TrimSpace(appSecret)
	if appSecret == "" {
		return errors.New("app_secret is required")
	}
	eventDispatcher := dispatcher.NewEventDispatcher("", "")
	eventDispatcher.OnP2MessageReceiveV1(func(eventCtx context.Context, event *larkim.P2MessageReceiveV1) error {
		return m.handleMessage(eventCtx, strings.TrimSpace(cfg.AppID), event)
	})
	eventDispatcher.OnP2CardActionTrigger(func(eventCtx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		return m.handleCardAction(eventCtx, strings.TrimSpace(cfg.AppID), event), nil
	})

	clientOpts := []ws.ClientOption{
		ws.WithEventHandler(eventDispatcher),
		ws.WithOnReady(func() {
			m.logger.Info("feishu websocket inbound client ready", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID)
		}),
		ws.WithOnError(func(err error) {
			if err != nil {
				m.logger.Warn("feishu websocket inbound client error", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID, "err", err.Error())
			}
		}),
		ws.WithOnReconnecting(func() {
			m.logger.Warn("feishu websocket inbound client reconnecting", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID)
		}),
		ws.WithOnReconnected(func() {
			m.logger.Info("feishu websocket inbound client reconnected", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID)
		}),
		ws.WithOnDisconnected(func() {
			m.logger.Warn("feishu websocket inbound client disconnected", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID)
		}),
	}
	if m.domain != "" {
		clientOpts = append(clientOpts, ws.WithDomain(m.domain))
	}
	client := ws.NewClient(strings.TrimSpace(cfg.AppID), appSecret, clientOpts...)
	clientCtx, cancel := context.WithCancel(ctx)
	handle := &clientHandle{key: key, route: route, cfg: cfg, client: client, ctx: clientCtx, cancel: cancel, source: source}

	m.mu.Lock()
	if _, exists := m.clients[key]; exists {
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.clients[key] = handle
	m.mu.Unlock()

	go func() {
		if err := client.Start(clientCtx); err != nil && !errors.Is(err, context.Canceled) {
			m.logger.Warn("feishu websocket inbound client exited", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID, "err", err.Error())
		}
	}()
	m.logger.Info("feishu websocket inbound client started", "source", source, "agent_id", route.AgentID, "app_id", cfg.AppID)
	return nil
}

func (m *Manager) stopClient(h *clientHandle) {
	if h == nil {
		return
	}
	h.cancel()
	if h.client != nil {
		h.client.Close()
	}
	m.logger.Info("feishu websocket inbound client stopped", "source", h.source, "agent_id", h.route.AgentID, "app_id", h.cfg.AppID)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	handles := make([]*clientHandle, 0, len(m.clients))
	for key, h := range m.clients {
		handles = append(handles, h)
		delete(m.clients, key)
	}
	m.mu.Unlock()
	for _, h := range handles {
		m.stopClient(h)
	}
}

func (m *Manager) loadAppSecret(ctx context.Context, workspaceID, secretID string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", errors.New("app_secret_ref is required")
	}
	payload, err := m.store.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil {
		return "", fmt.Errorf("read app_secret payload: %w", err)
	}
	decrypted, err := m.secrets.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return "", fmt.Errorf("decrypt app_secret: %w", err)
	}
	for _, key := range m.secretKeys {
		if raw, ok := decrypted[key].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw), nil
		}
	}
	return "", fmt.Errorf("app_secret payload missing expected string field")
}

func (m *Manager) handleMessage(ctx context.Context, appID string, event *larkim.P2MessageReceiveV1) error {
	inbound := inboundEventFromSDK(appID, event)
	if strings.TrimSpace(inbound.AppID) == "" || strings.TrimSpace(inbound.MessageID) == "" {
		m.logger.Warn("feishu websocket inbound: dropped malformed message event", "app_id", appID)
		return nil
	}
	// Debug: surface thread-related fields to diagnose why the
	// "thread follow-up without @" continuity rule in
	// isGroupMessageWithoutBotMention sometimes doesn't trigger for
	// replies inside a Feishu 话题 panel of a regular group chat.
	m.logger.Info("feishu websocket inbound: thread-field debug",
		"app_id", inbound.AppID,
		"message_id", inbound.MessageID,
		"chat_id", inbound.ChatID,
		"chat_type", inbound.ChatType,
		"thread_id", inbound.ThreadID,
		"root_id", inbound.RootID,
		"parent_id", inbound.ParentID,
		"mention_open_ids", strings.Join(inbound.MentionOpenIDs, ","),
	)

	r := router{store: m.store}
	if m.isDefaultSharedBotApp(inbound.AppID) {
		host, _ := m.defaultSharedRouteAndConfig()
		gatewayHost := routeFromStore(host)
		if isSelfMessage(gatewayHost.Config, inbound.SenderOpenID) {
			m.logger.Info("feishu websocket inbound: default shared self message skipped", "app_id", inbound.AppID, "message_id", inbound.MessageID)
			return nil
		}
		if isGroupMessageWithoutBotMention(ctx, m.store, gatewayHost.Config, inbound) {
			m.logger.Info("feishu websocket inbound: default shared group message without bot mention skipped", "app_id", inbound.AppID, "message_id", inbound.MessageID)
			return nil
		}
		m.enrichInboundAttachments(ctx, gatewayHost, &inbound)
		outcome, err := feishushared.HandleInbound(ctx, m.store, gatewayHost, inbound, m.sendImmediateText, m.quotedChainText, m.gateConfig())
		if err != nil {
			return fmt.Errorf("handle default shared feishu bot inbound: %w", err)
		}
		if outcome.Accepted && outcome.InboundMessageID != "" {
			m.asyncAddTypingReaction(outcome.InboundMessageID, inbound.MessageID, m.defaultBot.AppID, m.defaultBot.AppSecret)
		}
		m.logger.Info("feishu websocket default shared bot handled", "app_id", inbound.AppID, "message_id", inbound.MessageID, "accepted", outcome.Accepted, "replied", outcome.Replied, "reason", outcome.Reason, "agent_id", outcome.AgentID)
		return nil
	}

	host, err := r.GetAgentByFeishuAppID(ctx, inbound.AppID)
	if err != nil {
		if errors.Is(err, gateway.ErrFeishuRouterUnknownAgent) {
			m.logger.Warn("feishu websocket inbound: unknown app_id", "app_id", inbound.AppID)
			return nil
		}
		return fmt.Errorf("route feishu websocket inbound: %w", err)
	}
	if isSelfMessage(host.Config, inbound.SenderOpenID) {
		m.logger.Info("feishu websocket inbound: self message skipped", "app_id", inbound.AppID, "message_id", inbound.MessageID)
		return nil
	}
	if isGroupMessageWithoutBotMention(ctx, m.store, host.Config, inbound) {
		m.logger.Info("feishu websocket inbound: group message without bot mention skipped", "app_id", inbound.AppID, "message_id", inbound.MessageID)
		return nil
	}

	hostCfg, ok, err := gateway.DecodeFeishuConnectorConfig(host.Config)
	if err != nil {
		return fmt.Errorf("decode feishu connector config: %w", err)
	}
	if ok && feishushared.IsSharedRoutingMode(hostCfg.RoutingMode) {
		m.enrichInboundAttachments(ctx, host, &inbound)
		outcome, err := feishushared.HandleInbound(ctx, m.store, host, inbound, m.sendImmediateText, m.quotedChainText, m.gateConfig())
		if err != nil {
			return fmt.Errorf("handle shared feishu bot inbound: %w", err)
		}
		if outcome.Accepted && outcome.InboundMessageID != "" {
			if rAppID, rAppSecret, secErr := m.resolveImmediateReplyCredentials(ctx, host, inbound); secErr == nil {
				m.asyncAddTypingReaction(outcome.InboundMessageID, inbound.MessageID, rAppID, rAppSecret)
			} else {
				m.logger.Warn("feishu websocket inbound: skip typing reaction, credential resolve failed",
					"app_id", inbound.AppID, "external_message_id", inbound.MessageID, "err", secErr.Error())
			}
		}
		m.logger.Info("feishu websocket shared bot handled", "app_id", inbound.AppID, "message_id", inbound.MessageID, "accepted", outcome.Accepted, "replied", outcome.Replied, "reason", outcome.Reason, "agent_id", outcome.AgentID)
		return nil
	}

	decision, err := gateway.RouteFeishuInboundToAgent(ctx, r, inbound, host, m.gateConfig())
	if err != nil {
		return fmt.Errorf("route feishu websocket inbound: %w", err)
	}
	if !decision.Decision.Allowed {
		replied := false
		if strings.TrimSpace(decision.Decision.ReplyHint) != "" {
			if err := m.sendImmediateText(ctx, decision.Agent, inbound, decision.Decision.ReplyHint); err != nil {
				m.logger.Warn("feishu websocket inbound rejection reply failed", "app_id", inbound.AppID, "message_id", inbound.MessageID, "err", err.Error())
			} else {
				replied = true
			}
		}
		m.logger.Info("feishu websocket inbound: visibility gate rejected", "app_id", inbound.AppID, "message_id", inbound.MessageID, "reason", decision.Decision.Reason, "replied", replied)
		return nil
	}

	// /cancel and /cancel all: handled here, before CreateInboundIMMessage,
	// so the command itself is not stored as a user prompt or dispatched.
	// The cancel marks DB state; the daemon stops streaming on its next
	// tick. We do NOT call connector.Abort from here — that would couple
	// the inbound manager to the connector registry.
	if cancelCmd, ok := feishushared.ParseCancelCommand(decision.NormalizedText); ok {
		threadKey := strings.TrimSpace(inbound.ThreadID)
		conversationID, err := m.store.FindConversationByExternalRef(ctx, "feishu", inbound.ChatID, threadKey)
		if err != nil {
			if errors.Is(err, store.ErrUnknownConversation) {
				return m.sendImmediateText(ctx, decision.Agent, inbound,
					"当前会话还没有进行中的任务，无法取消。")
			}
			m.logger.Warn("feishu cancel command: conversation lookup failed",
				"chat_id", inbound.ChatID, "thread_id", threadKey, "err", err.Error())
			return m.sendImmediateText(ctx, decision.Agent, inbound,
				"取消失败：查询会话出错，请稍后再试。")
		}
		reason := "feishu_user_cancel"
		if cancelCmd.Scope == "all" {
			reason = "feishu_user_cancel_all"
		}
		cancelled, err := m.store.CancelAllInflightForConversation(ctx, conversationID, reason)
		if err != nil {
			m.logger.Warn("feishu cancel command: bulk cancel failed",
				"conversation_id", conversationID, "scope", cancelCmd.Scope, "err", err.Error())
			return m.sendImmediateText(ctx, decision.Agent, inbound,
				"取消失败，请稍后再试。")
		}
		if len(cancelled) == 0 {
			return m.sendImmediateText(ctx, decision.Agent, inbound,
				"当前没有进行中的任务。")
		}
		msg := fmt.Sprintf("已取消 %d 个任务。", len(cancelled))
		if cancelCmd.Scope != "all" && len(cancelled) > 1 {
			msg = fmt.Sprintf("已取消 %d 个进行中任务（如只想取消单个，请使用 web 端卡片上的取消按钮）。", len(cancelled))
		}
		return m.sendImmediateText(ctx, decision.Agent, inbound, msg)
	}

	externalUserID := strings.TrimSpace(inbound.SenderUnionID)
	if externalUserID == "" {
		externalUserID = strings.TrimSpace(inbound.SenderOpenID)
	}
	conversationForm := "group"
	if strings.EqualFold(strings.TrimSpace(inbound.ChatType), "p2p") {
		conversationForm = "dm"
	}
	m.enrichInboundAttachments(ctx, decision.Agent, &inbound)
	metadata := map[string]any{
		"chat_type":    inbound.ChatType,
		"tenant_key":   inbound.TenantKey,
		"sender_state": decision.SenderState,
		"message_type": inbound.MessageType,
		"raw_content":  inbound.RawContent,
		"root_id":      inbound.RootID,
		"parent_id":    inbound.ParentID,
		"thread_id":    inbound.ThreadID,
	}
	mergeMetadata(metadata, inbound.Metadata)
	if decision.Decision.GuestReplyHint != "" {
		metadata["guest_reply_hint"] = decision.Decision.GuestReplyHint
	}

	// Prefix lands in metadata, not Text, so messages.content stays the
	// user's verbatim input — store prepends it at dispatch time. The
	// chain walker may also append parent-hop images to
	// inbound.Metadata["attachments"] under the per-message cap; re-merge
	// that key into the prepared metadata snapshot after the call.
	if quoted := m.quotedChainText(ctx, decision.Agent, &inbound); quoted != "" {
		metadata[QuotedChainPrefixMetadataKey] = quoted
	}
	if att, ok := inbound.Metadata["attachments"]; ok {
		metadata["attachments"] = att
	}

	threadID := inbound.ReplyAnchorMessageID()
	created, err := m.store.CreateInboundIMMessage(ctx, store.CreateInboundIMMessageInput{
		ConversationTitle: feishushared.ConversationTitle(decision.NormalizedText),
		Text:              decision.NormalizedText,
		Mentions:          []string{"@" + decision.Agent.AgentName},
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalUserID:    externalUserID,
		// SenderOpenID is pinned so the credential-form submit callback
		// (which only carries open_id, not union_id) can verify the
		// click came from the inbound's original sender. Without this
		// any chat member could submit the form and have credentials
		// written under the initiator's account.
		SenderOpenID:      inbound.SenderOpenID,
		ExternalChatID:    inbound.ChatID,
		ExternalThreadID:  threadID,
		ExternalMessageID: inbound.MessageID,
		TargetAgentID:     decision.Agent.AgentID,
		SourceAppID:       inbound.AppID,
		ConversationForm:  conversationForm,
		Metadata:          metadata,
	})
	if err != nil {
		return fmt.Errorf("create feishu inbound message: %w", err)
	}
	// A fresh user query arriving without going through the credential-form
	// card means any stashed slot for this conversation is stale — drop it
	// so the next form-card path doesn't auto-resume the abandoned draft.
	if strings.TrimSpace(created.ConversationID) != "" {
		if err := m.store.ClearPendingCredentialFormSlotByConversation(ctx, created.ConversationID); err != nil {
			m.logger.Warn("feishu websocket inbound: clear stale credential form slot failed",
				"conversation_id", created.ConversationID,
				"err", err.Error(),
			)
		}
	}
	if created.MessageID != "" {
		// Log + skip the reaction on credential resolution failure
		// rather than failing the webhook — the message is stored.
		if rAppID, rAppSecret, secErr := m.resolveImmediateReplyCredentials(ctx, decision.Agent, inbound); secErr == nil {
			m.asyncAddTypingReaction(created.MessageID, inbound.MessageID, rAppID, rAppSecret)
		} else {
			m.logger.Warn("feishu websocket inbound: skip typing reaction, credential resolve failed",
				"app_id", inbound.AppID, "external_message_id", inbound.MessageID, "err", secErr.Error())
		}
	}
	m.logger.Info("feishu websocket inbound accepted", "app_id", inbound.AppID, "message_id", inbound.MessageID, "agent_id", decision.Agent.AgentID)
	return nil
}

func (m *Manager) sendImmediateText(ctx context.Context, agent gateway.FeishuRouteAgent, inbound gateway.FeishuInboundEvent, text string) error {
	appID, appSecret, err := m.resolveImmediateReplyCredentials(ctx, agent, inbound)
	if err != nil {
		return err
	}
	// Control-plane echoes (/list, /help, /select, visibility-gate
	// refusals) render as a grey notice card so they aren't mistaken
	// for Agent output. Empty title falls back to FeishuCardTitle —
	// the common case before a /select has happened.
	title := m.resolveImmediateReplyTitle(ctx, inbound)
	content, err := gateway.BuildFeishuNoticeCardContent(title, text, gateway.NoticeColorInfo)
	if err != nil {
		return err
	}
	client, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: m.openAPIBaseURL,
	})
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if replyAnchor := inbound.ReplyAnchorMessageID(); replyAnchor != "" {
		_, err = client.ReplyMessage(sendCtx, appSecret, replyAnchor, gateway.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       content,
			ReplyInThread: true,
		})
		return err
	}
	chatID := strings.TrimSpace(inbound.ChatID)
	if chatID == "" {
		return errors.New("feishu inbound missing chat_id for immediate reply")
	}
	_, err = client.SendMessage(sendCtx, appSecret, gateway.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     chatID,
		MsgType:       "interactive",
		Content:       content,
	})
	return err
}

// resolveImmediateReplyTitle returns the per-Agent card-header title for
// a notice card sent before any CreateInboundIMMessage has landed.
// Returns empty on every degenerate case (no chat_id, no conversation
// yet, no /select-ed Agent, soft-deleted Agent, store error). Empty
// title → builder falls back to gateway.FeishuCardTitle. Errors are
// warn-logged but never propagated.
func (m *Manager) resolveImmediateReplyTitle(ctx context.Context, inbound gateway.FeishuInboundEvent) string {
	chatID := strings.TrimSpace(inbound.ChatID)
	if chatID == "" {
		return ""
	}
	conversationID, err := m.store.FindConversationByExternalRef(ctx, "feishu", chatID, inbound.ThreadKey())
	if err != nil {
		if !errors.Is(err, store.ErrUnknownConversation) {
			m.logger.Warn("feishu inbound: lookup conversation for immediate-reply title failed",
				"chat_id", chatID,
				"err", err.Error(),
			)
		}
		return ""
	}
	title, err := m.store.ResolveAgentNameForConversation(ctx, conversationID)
	if err != nil {
		m.logger.Warn("feishu inbound: resolve agent name for immediate-reply failed",
			"conversation_id", conversationID,
			"err", err.Error(),
		)
		return ""
	}
	return title
}

// inboundAttachmentDownloadBudget caps total time downloading attachments
// on a single inbound before giving up on the rest. Sized to leave plenty
// of headroom under the upstream retry window for healthy Feishu CDN
// round-trips.
const inboundAttachmentDownloadBudget = 10 * time.Second

// inboundQuoteChainMaxDepth caps how far up the parent chain we walk on
// a reply. Five hops covers realistic deep-thread cases without paying
// for an unbounded climb.
const inboundQuoteChainMaxDepth = 5

// inboundQuoteChainBudget bounds the chain walk wall-clock so a hanging
// upstream cannot stall dispatch.
const inboundQuoteChainBudget = 5 * time.Second

// inboundQuoteChainMaxBytes caps the rendered prefix length so a deep
// chain of long posts can't blow up LLM token spend or DB row size.
const inboundQuoteChainMaxBytes = 16 * 1024

// QuotedChainPrefixMetadataKey re-exports store's key so external
// callers (e.g. shared-bot HandleInbound) stamp the same field that the
// store reads at dispatch time.
const QuotedChainPrefixMetadataKey = store.TriggerMessageQuotedChainPrefixKey

// inboundAttachmentTotalCap caps the total number of image attachments
// a single inbound carries to the LLM, summed across the user's own
// message and every walked ancestor in the quote chain. Sized to cover
// the "show me each of these screenshots" case without blowing token
// budget. Excess images on either side are dropped + warn-logged.
const inboundAttachmentTotalCap = 5

// quoteHopImage carries the (message_id, image_key) tuple needed to
// download one parent-hop image. Feishu's resource endpoint validates
// the file_key belongs to the message_id, so each key MUST be downloaded
// against the hop it came from — not against the inbound's message_id.
type quoteHopImage struct {
	messageID string
	key       string
}

// quotedChainText fetches the parent chain for a reply and:
//  1. Renders "[Quoted message]\n<text>\n[image:N]\n[/Quoted message]\n"
//     blocks, deepest ancestor first, with [image:N] indices keyed to the
//     downloaded attachment slice positions for round-trip clarity.
//  2. Downloads the per-hop images (best-effort) and merges them into
//     inbound.Metadata["attachments"] under the global inboundAttachmentTotalCap.
//
// Mutates inbound.Metadata so the downstream CreateInboundIMMessage call
// persists the chain's images alongside the user's own. Returns the
// rendered text prefix; "" on missing parent, missing credentials, or
// nothing to render — the caller proceeds with the user's text alone.
func (m *Manager) quotedChainText(ctx context.Context, agent gateway.FeishuRouteAgent, inbound *gateway.FeishuInboundEvent) string {
	if inbound == nil {
		return ""
	}
	startID := strings.TrimSpace(inbound.ParentID)
	existingCount := countExistingAttachments(inbound.Metadata)
	m.logger.Info("feishu inbound quote chain: enter",
		"app_id", inbound.AppID,
		"external_message_id", inbound.MessageID,
		"parent_id", inbound.ParentID,
		"root_id", inbound.RootID,
		"existing_attachments", existingCount,
		"will_walk", startID != "",
	)
	if startID == "" {
		// Feishu replies always carry ParentID; an empty value means
		// this isn't a reply at all, no chain to walk.
		return ""
	}
	appID, appSecret, err := m.resolveImmediateReplyCredentials(ctx, agent, *inbound)
	if err != nil {
		m.logger.Warn("feishu inbound quote chain: credential resolve failed",
			"app_id", inbound.AppID, "external_message_id", inbound.MessageID, "err", err.Error())
		return ""
	}
	client, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: m.openAPIBaseURL,
	})
	if err != nil {
		m.logger.Warn("feishu inbound quote chain: build client failed",
			"app_id", inbound.AppID, "external_message_id", inbound.MessageID, "err", err.Error())
		return ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, inboundQuoteChainBudget)
	defer cancel()

	// hopBlock represents one walked ancestor, kept leaf-first while we
	// crawl. We render in reverse (deepest-ancestor-first) at the end so
	// the text reads top-down toward the user's reply. images carries
	// per-image (message_id, key) tuples — most hops share a single
	// message_id (the hop's own), but merge_forward expansion can fold in
	// children that each have their own.
	type hopBlock struct {
		text   string
		images []quoteHopImage
	}

	hops := make([]hopBlock, 0, inboundQuoteChainMaxDepth)
	cursor := startID
	hopIndex := 0
	for ; hopIndex < inboundQuoteChainMaxDepth && cursor != ""; hopIndex++ {
		got, err := client.GetMessage(fetchCtx, appSecret, cursor)
		if err != nil {
			m.logger.Warn("feishu inbound quote chain: GetMessage failed",
				"external_message_id", inbound.MessageID,
				"probed_message_id", cursor,
				"hop", hopIndex,
				"err", err.Error(),
			)
			break
		}
		text, imageKeys := gateway.FeishuFetchedMessageText(got.MsgType, got.BodyContent)
		bodyPreview := got.BodyContent
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		m.logger.Info("feishu inbound quote chain: probed",
			"external_message_id", inbound.MessageID,
			"probed_message_id", cursor,
			"hop", hopIndex,
			"msg_type", got.MsgType,
			"has_parent_id", got.ParentID != "",
			"has_upper_message_id", got.UpperMessageID != "",
			"rendered_text", text != "",
			"image_keys", len(imageKeys),
			"body_preview", bodyPreview,
		)
		switch {
		case text != "" || len(imageKeys) > 0:
			images := make([]quoteHopImage, 0, len(imageKeys))
			for _, k := range imageKeys {
				images = append(images, quoteHopImage{messageID: got.MessageID, key: k})
			}
			hops = append(hops, hopBlock{text: text, images: images})
		case strings.EqualFold(got.MsgType, "merge_forward"):
			// merge_forward bodies are opaque placeholders ("Merged and
			// Forwarded Message"); the actual conversation is in
			// sub-messages. Try inline items first, fall back to listing
			// the chat container. expandMergeForward now also surfaces
			// the children's image_keys with each child's own message_id
			// so we can download them against the binding Feishu expects.
			expanded, expandedImages := m.expandMergeForward(fetchCtx, client, appSecret, got, inbound.MessageID, 0)
			if expanded != "" || len(expandedImages) > 0 {
				hops = append(hops, hopBlock{text: expanded, images: expandedImages})
			}
		}
		next := got.ParentID
		if next == "" {
			next = got.UpperMessageID
		}
		cursor = next
	}
	if len(hops) == 0 {
		m.logger.Info("feishu inbound quote chain: nothing rendered",
			"external_message_id", inbound.MessageID,
			"hops_walked", hopIndex,
		)
		return ""
	}

	// Pre-assign attachment indices in render order (deepest-ancestor-first)
	// so the [image:N] placeholders inside the rendered text match the
	// hopImages slice we'll feed the downloader. Cap is applied here so the
	// placeholders never reference an index the downloader wasn't allowed
	// to fill.
	hopImages := make([]quoteHopImage, 0)
	type renderedBlock struct {
		text      string
		startIdx  int // 1-based index of first downloaded image in this block
		imageRefs int // count of images actually slotted (post-cap)
		dropped   int // count of images dropped due to cap
	}
	rendered := make([]renderedBlock, len(hops))
	cursorIdx := existingCount + 1 // 1-based attachment index for the next slot
	remaining := inboundAttachmentTotalCap - existingCount
	if remaining < 0 {
		remaining = 0
	}
	for renderPos := 0; renderPos < len(hops); renderPos++ {
		hop := hops[len(hops)-1-renderPos]
		blk := renderedBlock{text: hop.text, startIdx: cursorIdx}
		for _, img := range hop.images {
			if remaining > 0 {
				hopImages = append(hopImages, img)
				blk.imageRefs++
				cursorIdx++
				remaining--
			} else {
				blk.dropped++
			}
		}
		rendered[renderPos] = blk
	}

	var b strings.Builder
	for i, blk := range rendered {
		b.WriteString("[Quoted message]\n")
		if blk.text != "" {
			b.WriteString(blk.text)
		}
		for k := 0; k < blk.imageRefs; k++ {
			if blk.text != "" || k > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[image:%d]", blk.startIdx+k)
		}
		if blk.dropped > 0 {
			if blk.text != "" || blk.imageRefs > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "[%d image(s) omitted: over per-message cap]", blk.dropped)
		}
		b.WriteString("\n[/Quoted message]\n")
		if b.Len() > inboundQuoteChainMaxBytes {
			// Tail-truncate the oldest content first (it's least relevant
			// to the user's current question) and stamp a marker so the
			// LLM sees the truncation explicitly. The downloader still
			// uses hopImages as the source of truth — a truncated text
			// referencing an [image:N] is harmless noise.
			tail := b.String()[:inboundQuoteChainMaxBytes] + "\n[…earlier quoted context truncated]\n"
			m.logger.Info("feishu inbound quote chain: rendered (truncated)",
				"external_message_id", inbound.MessageID,
				"hops_walked", hopIndex,
				"hops_rendered", i+1,
				"image_keys", len(hopImages),
				"final_bytes", len(tail),
			)
			m.downloadQuoteChainAttachments(fetchCtx, appSecret, inbound, hopImages, client)
			return tail
		}
	}
	out := b.String()
	m.logger.Info("feishu inbound quote chain: rendered",
		"external_message_id", inbound.MessageID,
		"hops_walked", hopIndex,
		"hops_rendered", len(hops),
		"image_keys", len(hopImages),
		"final_bytes", len(out),
	)
	m.downloadQuoteChainAttachments(fetchCtx, appSecret, inbound, hopImages, client)
	return out
}

// downloadQuoteChainAttachments downloads each (message_id, image_key)
// tuple via Feishu's resource endpoint and appends successful results
// to inbound.Metadata["attachments"]. Best-effort: per-image failures
// log and continue. Each download MUST go against the hop's own
// message_id — Feishu validates the file_key ↔ message_id binding.
func (m *Manager) downloadQuoteChainAttachments(ctx context.Context, appSecret string, inbound *gateway.FeishuInboundEvent, images []quoteHopImage, client *gateway.FeishuTenantClient) {
	if inbound == nil || len(images) == 0 || client == nil {
		return
	}
	if inbound.Metadata == nil {
		inbound.Metadata = map[string]any{}
	}
	existing := store.DecodeMessageAttachments(inbound.Metadata)
	downloaded := make([]store.MessageAttachment, 0, len(images))
	for _, img := range images {
		if strings.TrimSpace(img.messageID) == "" || strings.TrimSpace(img.key) == "" {
			continue
		}
		resource, err := client.DownloadMessageResource(ctx, appSecret, img.messageID, img.key, gateway.FeishuResourceTypeImage)
		if err != nil {
			m.logger.Warn("feishu inbound quote chain: image download failed, skipping",
				"app_id", inbound.AppID,
				"external_message_id", inbound.MessageID,
				"hop_message_id", img.messageID,
				"image_key", img.key,
				"err", err.Error())
			continue
		}
		downloaded = append(downloaded, store.MessageAttachment{
			Kind:       "image",
			MIME:       resource.MIME,
			Size:       len(resource.Data),
			DataBase64: base64.StdEncoding.EncodeToString(resource.Data),
		})
	}
	if len(downloaded) == 0 {
		m.logger.Info("feishu inbound quote chain: no attachments materialised",
			"app_id", inbound.AppID,
			"external_message_id", inbound.MessageID,
			"image_keys_requested", len(images),
		)
		return
	}
	merged := append(append([]store.MessageAttachment{}, existing...), downloaded...)
	materialiseAttachments(inbound, merged)
	m.logger.Info("feishu inbound quote chain: attachments materialised",
		"app_id", inbound.AppID,
		"external_message_id", inbound.MessageID,
		"image_keys_requested", len(images),
		"attachments_added", len(downloaded),
		"attachments_total", len(merged),
	)
}

// countExistingAttachments returns how many attachments inbound.Metadata
// currently carries. Used to size the per-message cap against the chain
// walker's remaining slots.
func countExistingAttachments(metadata map[string]any) int {
	if metadata == nil {
		return 0
	}
	return len(store.DecodeMessageAttachments(metadata))
}

// materialiseAttachments writes a final attachments slice to
// inbound.Metadata under the "attachments" key in the same shape the
// store layer expects (anySliceFromMaps over EncodeMessageAttachments).
// Removes the "attachments" key entirely on an empty input so a
// per-message metadata blob doesn't carry a stray empty list.
func materialiseAttachments(inbound *gateway.FeishuInboundEvent, attachments []store.MessageAttachment) {
	if inbound == nil {
		return
	}
	if inbound.Metadata == nil {
		inbound.Metadata = map[string]any{}
	}
	if len(attachments) == 0 {
		delete(inbound.Metadata, "attachments")
		return
	}
	encoded := store.EncodeMessageAttachments(attachments)
	if encoded == nil {
		delete(inbound.Metadata, "attachments")
		return
	}
	inbound.Metadata["attachments"] = anySliceFromMaps(encoded)
}

// inboundMergeForwardMaxDepth caps nested merge_forward expansion so a
// pathological "forward of forward of forward" can't fan out forever.
// Reuses the same magnitude as the top-level chain depth.
const inboundMergeForwardMaxDepth = 5

// inboundMergeForwardListMaxPages caps the chat-list fallback. With
// page_size=50 this lets us look through the most recent 250 messages
// for sub-message hits before giving up.
const inboundMergeForwardListMaxPages = 5

// expandMergeForward renders a merge_forward parent into a "[会话记录]"
// block by walking its sub-messages. Two-step lookup: prefer the
// inline data.items[1..] returned by GetMessage, fall back to listing
// the chat container and filtering by upper_message_id.
//
// Returns "[会话记录]" placeholder (not "") when children can't be
// located so the LLM at least knows a forwarded conversation was
// attached but unrenderable. Also surfaces any image_keys carried by
// child messages (image type + post-with-embedded-img) with each
// child's own message_id so the quote-chain caller can download them
// against Feishu's file_key ↔ message_id binding. The text uses
// "[image]" placeholders inline; the caller renumbers those into
// "[image:N]" once the global slot positions are known.
func (m *Manager) expandMergeForward(ctx context.Context, client *gateway.FeishuTenantClient, appSecret string, parent gateway.FeishuFetchedMessage, triggerMessageID string, depth int) (string, []quoteHopImage) {
	if depth >= inboundMergeForwardMaxDepth {
		m.logger.Info("feishu inbound merge_forward: depth cap reached",
			"trigger_message_id", triggerMessageID,
			"parent_message_id", parent.MessageID,
			"depth", depth,
		)
		return "", nil
	}

	subs := parent.SubItems
	source := "inline_items"
	if len(subs) == 0 && parent.ChatID != "" {
		source = "chat_list_fallback"
		subs = m.listMergeForwardChildren(ctx, client, appSecret, parent, triggerMessageID)
	}
	m.logger.Info("feishu inbound merge_forward: resolved children",
		"trigger_message_id", triggerMessageID,
		"parent_message_id", parent.MessageID,
		"depth", depth,
		"source", source,
		"child_count", len(subs),
	)
	if len(subs) == 0 {
		return "[会话记录]", nil
	}

	lines := make([]string, 0, len(subs))
	images := make([]quoteHopImage, 0)
	for _, sub := range subs {
		text, subImageKeys := gateway.FeishuFetchedMessageText(sub.MsgType, sub.BodyContent)
		var nestedImages []quoteHopImage
		if text == "" && len(subImageKeys) == 0 && strings.EqualFold(sub.MsgType, "merge_forward") {
			text, nestedImages = m.expandMergeForward(ctx, client, appSecret, sub, triggerMessageID, depth+1)
		}
		for _, k := range subImageKeys {
			images = append(images, quoteHopImage{messageID: sub.MessageID, key: k})
		}
		images = append(images, nestedImages...)
		// Each child line carries its own bare "[image]" placeholders so
		// the LLM can read a child that's image-only as something more
		// than blank; the caller will not renumber inside merge_forward
		// children (the global "[image:N]" lives at the hop boundary).
		if text == "" && len(subImageKeys) == 0 {
			continue
		}
		line := text
		for range subImageKeys {
			if line != "" {
				line += "\n"
			}
			line += "[image]"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 && len(images) == 0 {
		return "[会话记录]", nil
	}
	if len(lines) == 0 {
		// All children were image-only; show a placeholder so the LLM
		// reads the block as "forwarded conversation, here are the
		// images" rather than dropping it.
		return "[会话记录]\n[/会话记录]", images
	}
	return "[会话记录]\n" + strings.Join(lines, "\n---\n") + "\n[/会话记录]", images
}

// listMergeForwardChildren paginates the parent's chat container,
// newest-first, collecting messages whose upper_message_id points back
// at the parent. We stop on the first page that yielded matches but
// produces no new hits — sub-messages cluster in time near the parent.
func (m *Manager) listMergeForwardChildren(ctx context.Context, client *gateway.FeishuTenantClient, appSecret string, parent gateway.FeishuFetchedMessage, triggerMessageID string) []gateway.FeishuFetchedMessage {
	var collected []gateway.FeishuFetchedMessage
	var pageToken string
	for page := 0; page < inboundMergeForwardListMaxPages; page++ {
		items, next, err := client.ListMessagesByChatPage(ctx, appSecret, parent.ChatID, pageToken)
		if err != nil {
			m.logger.Warn("feishu inbound merge_forward: list page failed",
				"trigger_message_id", triggerMessageID,
				"parent_message_id", parent.MessageID,
				"chat_id", parent.ChatID,
				"page", page,
				"err", err.Error(),
			)
			break
		}
		foundOnThisPage := 0
		for _, item := range items {
			if item.UpperMessageID == parent.MessageID {
				collected = append(collected, item)
				foundOnThisPage++
			}
		}
		// First page hit something but this one didn't — children
		// don't span backwards through unrelated traffic, stop.
		if len(collected) > 0 && foundOnThisPage == 0 {
			break
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	// Chat list is newest-first; flip to chronological so the rendered
	// transcript reads in send order.
	for i, j := 0, len(collected)-1; i < j; i, j = i+1, j-1 {
		collected[i], collected[j] = collected[j], collected[i]
	}
	return collected
}

// enrichInboundAttachments downloads binary payloads stashed onto
// inbound.Metadata["image_keys"] and rewrites inbound.Metadata so the
// downstream CreateInboundIMMessage call persists them under
// metadata.attachments. Best-effort: any per-file failure logs and
// proceeds with the remaining attachments. Mutates inbound.Metadata
// in place. Applies the per-message inboundAttachmentTotalCap; excess
// keys are dropped + warn-logged so the chain walker can still claim
// slots without us already having spent them all.
func (m *Manager) enrichInboundAttachments(ctx context.Context, agent gateway.FeishuRouteAgent, inbound *gateway.FeishuInboundEvent) {
	if inbound == nil || inbound.Metadata == nil {
		return
	}
	rawKeys, ok := inbound.Metadata["image_keys"]
	if !ok {
		return
	}
	keys := normalizeStringSliceAny(rawKeys)
	if len(keys) == 0 {
		delete(inbound.Metadata, "image_keys")
		return
	}

	appID, appSecret, err := m.resolveImmediateReplyCredentials(ctx, agent, *inbound)
	if err != nil {
		m.logger.Warn("feishu inbound: skip attachment download, credential resolve failed",
			"app_id", inbound.AppID, "external_message_id", inbound.MessageID,
			"image_key_count", len(keys), "err", err.Error())
		return
	}
	client, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: m.openAPIBaseURL,
	})
	if err != nil {
		m.logger.Warn("feishu inbound: build attachment client failed",
			"app_id", inbound.AppID, "err", err.Error())
		return
	}

	downloadCtx, cancel := context.WithTimeout(ctx, inboundAttachmentDownloadBudget)
	defer cancel()

	// Cap the inbound's own keys to the per-message ceiling. Any beyond
	// the cap are dropped here so we don't pay download cost on something
	// the chain walker will be forced to drop again.
	capped := keys
	droppedOverCap := 0
	if len(capped) > inboundAttachmentTotalCap {
		droppedOverCap = len(capped) - inboundAttachmentTotalCap
		capped = capped[:inboundAttachmentTotalCap]
	}

	attachments := make([]store.MessageAttachment, 0, len(capped))
	for _, key := range capped {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		resource, err := client.DownloadMessageResource(downloadCtx, appSecret, inbound.MessageID, key, gateway.FeishuResourceTypeImage)
		if err != nil {
			m.logger.Warn("feishu inbound: image download failed, skipping",
				"app_id", inbound.AppID, "external_message_id", inbound.MessageID,
				"image_key", key, "err", err.Error())
			continue
		}
		attachments = append(attachments, store.MessageAttachment{
			Kind:       "image",
			MIME:       resource.MIME,
			Size:       len(resource.Data),
			DataBase64: base64.StdEncoding.EncodeToString(resource.Data),
		})
	}

	// Drop the staging key regardless so the chain walker / downstream
	// consumers don't reprocess it.
	delete(inbound.Metadata, "image_keys")
	if len(attachments) == 0 {
		return
	}
	materialiseAttachments(inbound, attachments)
	m.logger.Info("feishu inbound: attachments materialised",
		"app_id", inbound.AppID, "external_message_id", inbound.MessageID,
		"image_keys_requested", len(keys),
		"attachments_attached", len(attachments),
		"dropped_over_cap", droppedOverCap)
}

// normalizeStringSliceAny accepts the loose shapes Feishu's metadata
// might produce — []string, []any with strings inside, or a single
// bare string — and returns a trimmed []string.
func normalizeStringSliceAny(v any) []string {
	switch typed := v.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, s := range typed {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			if s, ok := raw.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
		return out
	case string:
		if s := strings.TrimSpace(typed); s != "" {
			return []string{s}
		}
	}
	return nil
}

// anySliceFromMaps converts EncodeMessageAttachments output to []any so
// json marshalling produces the consistent "metadata is opaque jsonb"
// shape when nested under another map[string]any.
func anySliceFromMaps(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

func (m *Manager) resolveImmediateReplyCredentials(ctx context.Context, agent gateway.FeishuRouteAgent, inbound gateway.FeishuInboundEvent) (string, string, error) {
	cfg, ok, err := gateway.DecodeFeishuConnectorConfig(agent.Config)
	if err != nil {
		return "", "", err
	}
	appID := strings.TrimSpace(inbound.AppID)
	if ok && strings.TrimSpace(cfg.AppID) != "" {
		appID = strings.TrimSpace(cfg.AppID)
	}
	if appID == "" {
		return "", "", errors.New("feishu connector missing app_id")
	}
	if m.isDefaultSharedBotApp(appID) {
		return m.defaultBot.AppID, m.defaultBot.AppSecret, nil
	}
	if !ok || !cfg.Enabled || strings.TrimSpace(cfg.AppSecretRef) == "" {
		return "", "", errors.New("feishu connector missing app_secret_ref")
	}
	appSecret, err := m.loadAppSecret(ctx, agent.WorkspaceID, cfg.AppSecretRef)
	if err != nil {
		return "", "", err
	}
	return appID, appSecret, nil
}

// asyncAddTypingReaction fires off the "Typing" emoji reaction in a
// goroutine. Fire-and-forget because the webhook ack must not block on
// best-effort UX and because the Feishu reaction API is per-Bot
// rate-limited — a burst of inbounds should not amplify into the same
// number of blocking outbound calls.
//
// Uses context.Background() with a 5s timeout: the inbound ctx is likely
// cancelled by the time the goroutine runs, but the hard cap prevents
// a hung Feishu API from leaking goroutines.
func (m *Manager) asyncAddTypingReaction(localMessageID, externalMessageID, appID, appSecret string) {
	if strings.TrimSpace(localMessageID) == "" || strings.TrimSpace(externalMessageID) == "" {
		return
	}
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
		// Empty credentials → programming error; warn loudly.
		m.logger.Warn("feishu websocket inbound: skip typing reaction, missing credentials",
			"app_id", appID, "external_message_id", externalMessageID)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
			AppID:   appID,
			BaseURL: m.openAPIBaseURL,
		})
		if err != nil {
			m.logger.Warn("feishu websocket inbound: build reaction client failed",
				"app_id", appID, "external_message_id", externalMessageID, "err", err.Error())
			return
		}
		reactionID, err := client.AddReaction(ctx, appSecret, externalMessageID, gateway.DefaultTypingReactionEmoji)
		if err != nil {
			// Most common cause: Bot lacks im:message.reaction:write.
			// We don't retry — the indicator is decorative.
			m.logger.Warn("feishu websocket inbound: add typing reaction failed",
				"app_id", appID, "external_message_id", externalMessageID, "err", err.Error())
			return
		}
		if strings.TrimSpace(reactionID) == "" {
			// Success without id; nothing to persist or undo.
			m.logger.Info("feishu websocket inbound: typing reaction returned empty id",
				"app_id", appID, "external_message_id", externalMessageID)
			return
		}
		if err := m.store.RecordFeishuInboundReaction(ctx, store.RecordFeishuInboundReactionInput{
			MessageID:  localMessageID,
			ReactionID: reactionID,
			AppID:      appID,
			EmojiType:  gateway.DefaultTypingReactionEmoji,
		}); err != nil {
			// Reaction is on Feishu's side already — the outbound
			// terminal path won't know to delete it, so it lingers.
			m.logger.Warn("feishu websocket inbound: persist reaction_id failed",
				"local_message_id", localMessageID, "reaction_id", reactionID, "err", err.Error())
			return
		}
	}()
}

func (m *Manager) handleCardAction(ctx context.Context, appID string, event *callback.CardActionTriggerEvent) *callback.CardActionTriggerResponse {
	meta := cardActionMetadata(appID, event)
	m.logger.Info("feishu websocket card action received",
		"app_id", meta.AppID,
		"open_message_id", meta.OpenMessageID,
		"open_chat_id", meta.OpenChatID,
		"operator_open_id", meta.OperatorOpenID,
		"action", meta.Action,
	)

	// Only permission_allow / permission_deny / credential_form_*
	// actions are recognised; other actions fall through to a generic
	// ack so new card types don't accidentally hang the user's click.
	switch meta.Action {
	case "permission_allow", "permission_deny":
		return m.handlePermissionDecisionAction(ctx, meta, event)
	case "credential_form_submit":
		return m.handleCredentialFormSubmitAction(ctx, meta, event)
	case "credential_form_acknowledged":
		// Placeholder button required by Feishu's "form container needs
		// a name-bearing interactive component" rule; clicks toast and
		// go nowhere (the card is already terminal).
		return ackToast("info", "本卡片已结束")
	case "ask_user_choice_submit":
		return m.handlePromptForUserChoiceSubmitAction(ctx, meta, event)
	case "ask_user_choice_pick":
		// Legacy per-option button from the pre-form AskUserQuestion card.
		// Cards sent before this deploy still carry this action; clicks
		// land here. The slot is unchanged on the server side — the daemon
		// 10-min watchdog will still fire — but a silent "操作已收到"
		// toast misleads the user into thinking it worked. Tell them
		// directly to re-send so they don't sit waiting.
		m.logger.Info("feishu inbound: legacy ask_user_choice_pick click (card pre-dates form upgrade)",
			"open_message_id", meta.OpenMessageID,
			"operator_open_id", meta.OperatorOpenID,
		)
		return ackToast("info", "卡片已升级,请重新 @机器人 发起本轮问题")
	default:
		return ackToast("info", "操作已收到")
	}
}

// handlePermissionDecisionAction parses the permission_request_id off
// the card button, forwards the verdict to the runtime, patches the
// card into its green/red result shape, and clears the inflight slot.
//
// Failure modes:
//   - missing request id → generic ack toast (defensive)
//   - slot already cleared → "该请求已处理或已过期" toast
//   - SubmitPermission error → retryable toast; slot stays
//   - PatchMessage failure → log warn but still clear the slot since
//     the verdict landed on the runtime
func (m *Manager) handlePermissionDecisionAction(
	ctx context.Context,
	meta cardActionLogMeta,
	event *callback.CardActionTriggerEvent,
) *callback.CardActionTriggerResponse {
	requestID := strings.TrimSpace(cardActionStringValue(event, "permission_request_id"))
	if requestID == "" {
		m.logger.Warn("feishu permission callback missing permission_request_id",
			"open_message_id", meta.OpenMessageID,
			"operator_open_id", meta.OperatorOpenID,
		)
		return ackToast("info", "操作已收到")
	}
	approved := meta.Action == "permission_allow"

	conv, err := m.store.FindConversationByPermissionRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			m.logger.Info("feishu permission callback: slot already cleared",
				"permission_request_id", requestID,
				"operator_open_id", meta.OperatorOpenID,
			)
			return ackToast("info", "该请求已处理或已过期")
		}
		m.logger.Warn("feishu permission callback: lookup failed",
			"permission_request_id", requestID,
			"err", err.Error(),
		)
		return ackToast("error", "查询失败,请稍后再试")
	}
	if !conv.HasPermission {
		m.logger.Info("feishu permission callback: no permission slot",
			"permission_request_id", requestID,
			"conversation_id", conv.ConversationID,
		)
		return ackToast("info", "该请求已处理或已过期")
	}

	if m.permRouter == nil {
		m.logger.Warn("feishu permission callback: permission router not configured",
			"permission_request_id", requestID,
		)
		return ackToast("error", "服务未配置,请联系管理员")
	}
	if err := m.permRouter.SubmitPermission(ctx, PermissionDecision{
		RequestID:  requestID,
		Approved:   approved,
		OperatorID: meta.OperatorOpenID,
	}); err != nil {
		m.logger.Warn("feishu permission callback: SubmitPermission failed",
			"permission_request_id", requestID,
			"approved", approved,
			"err", err.Error(),
		)
		return ackToast("error", "更新失败,请稍后再试")
	}

	// Patch the card into its green / red result shape under the bot
	// that owns the conversation.
	if err := m.patchPermissionResultCard(ctx, conv, approved); err != nil {
		// Verdict landed on the runtime; patch failure just leaves
		// the card in its waiting shape. Log loud and clear the slot.
		m.logger.Warn("feishu permission callback: patch result card failed",
			"permission_request_id", requestID,
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
	if err := m.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPermission, conv.Permission.AgentRunID); err != nil {
		m.logger.Warn("feishu permission callback: clear slot failed",
			"permission_request_id", requestID,
			"conversation_id", conv.ConversationID,
			"err", err.Error(),
		)
	}
	if approved {
		return ackToast("success", "已允许")
	}
	return ackToast("info", "已拒绝")
}

// handleCredentialFormSubmitAction routes the credential-form card's
// submit click through the auto-retry loop.
//
// Authorization gate (runs BEFORE any write):
//  1. operator.open_id must equal stash.initiator_open_id, else ANY
//     group member could submit a form rendered for another user and
//     have credentials persisted under that user's account. The stash
//     uses open_id because Feishu callbacks only carry open_id.
//  2. callback open_chat_id must equal stash.external_chat_id when
//     both are present.
//
// Concurrency: ClaimPendingCredentialFormSlot is a CTE that captures
// the pre-image under FOR UPDATE row lock and clears the slot. Two
// racing pods produce exactly one winner; the loser sees
// ErrPendingCredentialFormNotFound and short-circuits without writing.
//
// Multi-kind atomicity: ReplaceUserCredentials writes every submitted
// kind in one transaction; any per-kind failure rolls the batch back.
//
// Invariant: NEVER log credential plaintext. The submitted set of
// KINDS is logged (extracted from field names) for observability.
func (m *Manager) handleCredentialFormSubmitAction(
	ctx context.Context,
	meta cardActionLogMeta,
	event *callback.CardActionTriggerEvent,
) *callback.CardActionTriggerResponse {
	qkey := strings.TrimSpace(cardActionStringValue(event, "qkey"))
	if qkey == "" {
		m.logger.Warn("feishu credential form submit: missing qkey",
			"open_message_id", meta.OpenMessageID,
			"operator_open_id", meta.OperatorOpenID,
		)
		return ackToast("info", "操作已收到")
	}
	formValues := cardActionFormValues(event)
	if len(formValues) == 0 {
		return ackToast("error", "请填写凭据后再提交")
	}
	// Extract (kind, plaintext) pairs from form_value. Fields not
	// prefixed "credential_" are ignored.
	type kindBinding struct {
		kind      string
		plaintext string
	}
	const fieldPrefix = "credential_"
	bindings := make([]kindBinding, 0, len(formValues))
	kindsForLog := make([]string, 0, len(formValues))
	for name, raw := range formValues {
		if !strings.HasPrefix(name, fieldPrefix) {
			continue
		}
		kind := strings.TrimSpace(strings.TrimPrefix(name, fieldPrefix))
		if kind == "" {
			continue
		}
		plaintext, _ := raw.(string)
		plaintext = strings.TrimSpace(plaintext)
		if plaintext == "" {
			// Empty value means a client-side bypass of the
			// required-at-render-time form fields; reject loudly.
			m.logger.Warn("feishu credential form submit: empty credential value",
				"qkey", qkey,
				"kind", kind,
			)
			return ackToast("error", "请填写每个凭据后再提交")
		}
		bindings = append(bindings, kindBinding{kind: kind, plaintext: plaintext})
		kindsForLog = append(kindsForLog, kind)
	}
	if len(bindings) == 0 {
		return ackToast("error", "请填写凭据后再提交")
	}

	// Atomic claim — winning pod gets the slot, losers see NotFound.
	// The slot's host conversation IDs come back in the same call.
	claimed, err := m.store.ClaimPendingCredentialFormSlot(ctx, qkey)
	if err != nil {
		if errors.Is(err, store.ErrPendingCredentialFormNotFound) {
			m.logger.Info("feishu credential form submit: slot not found (expired or already processed)",
				"qkey", qkey,
				"operator_open_id", meta.OperatorOpenID,
			)
			return ackToast("info", "该请求已过期，请重新发送消息")
		}
		m.logger.Warn("feishu credential form submit: claim failed",
			"qkey", qkey,
			"err", err.Error(),
		)
		return ackToast("error", "查询失败，请稍后再试")
	}
	slot := claimed.Slot

	// Enforce auth AFTER the claim so the qkey is consumed exactly
	// once even when the auth check fails — otherwise an attacker
	// could repeatedly poke the callback to keep the stash alive.
	stashedOpenID := strings.TrimSpace(slot.InitiatorOpenID)
	if stashedOpenID == "" || stashedOpenID != strings.TrimSpace(meta.OperatorOpenID) {
		m.logger.Warn("feishu credential form submit: operator mismatch",
			"qkey", qkey,
			"operator_open_id", meta.OperatorOpenID,
			"stash_initiator_open_id_present", stashedOpenID != "",
			"conversation_id", claimed.ConversationID,
		)
		// Flip the form to a red terminal card so the legitimate
		// initiator sees the qkey is dead and re-sends their message —
		// otherwise an unchanged form keeps inviting submits that fail
		// with "expired", DoS'ing the sender after a single hostile
		// click. Returned inline so Feishu renders without a separate
		// PATCH round-trip.
		rejectCard := gateway.BuildCredentialFormRejectedCard(
			m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form reject"),
			gateway.CredentialFormRejectOperatorMismatch,
		)
		return ackToastWithCard("error", "凭据只能由发起人本人填写", rejectCard)
	}
	stashedChat := strings.TrimSpace(claimed.ExternalChatID)
	clickedChat := strings.TrimSpace(meta.OpenChatID)
	// Enforce only when both sides have a chat id — clickedChat may be
	// empty on Feishu DMs for some SDK versions; the open_id check
	// above is sufficient there.
	if stashedChat != "" && clickedChat != "" && stashedChat != clickedChat {
		m.logger.Warn("feishu credential form submit: chat mismatch",
			"qkey", qkey,
			"stash_chat_id", stashedChat,
			"clicked_chat_id", clickedChat,
			"operator_open_id", meta.OperatorOpenID,
		)
		rejectCard := gateway.BuildCredentialFormRejectedCard(
			m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form reject"),
			gateway.CredentialFormRejectChatMismatch,
		)
		return ackToastWithCard("error", "请在原会话中提交凭据", rejectCard)
	}

	m.logger.Info("feishu credential form submit accepted",
		"qkey", qkey,
		"submitted_kinds", strings.Join(kindsForLog, ","),
		"conversation_id", claimed.ConversationID,
		"initiator_user_id", slot.InitiatorUserID,
		"open_message_id", meta.OpenMessageID,
	)

	// Encrypt up front so an Encrypt failure aborts before we hit the
	// DB tx. Each payload writes both "api_key" and "value" so
	// capability_runtime.credentialPayloadValue (which tries
	// api_key → token → access_token → value) keeps working.
	now := time.Now().UTC()
	credentialInputs := make([]store.CreateUserCredentialInput, 0, len(bindings))
	for _, b := range bindings {
		envelope, err := m.secrets.Encrypt(map[string]any{
			"api_key": b.plaintext,
			"value":   b.plaintext,
		})
		if err != nil {
			m.logger.Warn("feishu credential form submit: encrypt failed",
				"qkey", qkey,
				"kind", b.kind,
				"err", err.Error(),
			)
			return ackToast("error", "保存凭据失败，请稍后再试")
		}
		credentialInputs = append(credentialInputs, store.CreateUserCredentialInput{
			UserID:         slot.InitiatorUserID,
			Kind:           b.kind,
			DisplayName:    b.kind,
			EncryptedValue: envelope,
			KeyVersion:     "v1",
			Now:            now,
		})
	}

	// Tx-wrapped multi-kind write; any per-kind failure rolls back.
	//
	// Note: the three operations in this handler (Claim, Replace,
	// CreateInboundIMMessage) each open their own tx. A crash between
	// them is bounded — the worst case is "user re-sends": either no
	// credentials saved, or credentials saved but same turn doesn't
	// auto-resume. No data corruption either way. Closing this fully
	// requires an outer tx or an outbox-driven reconciler.
	results, err := m.store.ReplaceUserCredentials(ctx, slot.InitiatorUserID, credentialInputs)
	if err != nil {
		m.logger.Warn("feishu credential form submit: persist user credentials failed",
			"qkey", qkey,
			"submitted_kinds", strings.Join(kindsForLog, ","),
			"err", err.Error(),
		)
		return ackToast("error", "保存凭据失败，请稍后再试")
	}

	replacedCount := 0
	for _, r := range results {
		if r.Replaced {
			replacedCount++
		}
	}

	// Routing target comes from the slot itself. Reading
	// gateway_sessions.selected_agent_id misses for direct @-mentions
	// of a dedicated bot (that table is only written by /select), so
	// the slot stashes agent_id at form-emission time. An empty value
	// here means the slot pre-dates this field.
	targetAgentID := strings.TrimSpace(slot.AgentID)
	if targetAgentID == "" {
		m.logger.Warn("feishu credential form submit: slot missing agent_id",
			"qkey", qkey,
			"conversation_id", claimed.ConversationID,
			"external_chat_id", claimed.ExternalChatID,
			"external_thread_id", claimed.ExternalThreadID,
		)
		return ackToast("error", "凭据已保存，但会话路由丢失，请重新 @ Agent")
	}

	// Re-enqueue the original raw_query. We bust gateway dedup by
	// using `qkey:<qkey>` as the external_message_id: qkey is mint-once
	// unique, so the dedup row misses and CreateInboundIMMessage
	// creates a fresh trigger_message + agent_run instead of returning
	// the original (terminated) run.
	rerunExternalMessageID := "qkey:" + qkey
	rerunMetadata := map[string]any{
		"source":           "gateway",
		"gateway":          "feishu",
		"reenqueued_qkey":  qkey,
		"reenqueued_from":  "credential_form_submit",
		"external_chat_id": claimed.ExternalChatID,
	}
	if strings.TrimSpace(claimed.ExternalThreadID) != "" {
		rerunMetadata["external_thread_id"] = claimed.ExternalThreadID
	}
	if _, err := m.store.CreateInboundIMMessage(ctx, store.CreateInboundIMMessageInput{
		ConversationTitle: feishushared.ConversationTitle(slot.RawQuery),
		Text:              slot.RawQuery,
		Source:            "gateway",
		Gateway:           "feishu",
		// Re-stamp sender open_id so the next form-card path (if the
		// turn needs another credential round) can still authenticate
		// the submit — without this the inflight driver's safety
		// guard trips.
		SenderOpenID: slot.InitiatorOpenID,
		// Pass the pre-resolved user_id directly so the store can
		// populate agent_runs.requested_by_id without round-tripping
		// open_id → union_id via the Feishu contact API. Without it,
		// the re-fired run lands with an empty initiator and any MCP
		// needing per-user credentials trips the
		// "conversation_initiator_id is empty" runtime error.
		InitiatorUserID:   slot.InitiatorUserID,
		ExternalChatID:    claimed.ExternalChatID,
		ExternalThreadID:  claimed.ExternalThreadID,
		ExternalMessageID: rerunExternalMessageID,
		TargetAgentID:     targetAgentID,
		SourceAppID:       claimed.SourceAppID,
		Metadata:          rerunMetadata,
	}); err != nil {
		m.logger.Warn("feishu credential form submit: re-enqueue inbound failed",
			"qkey", qkey,
			"err", err.Error(),
		)
		return ackToast("error", "凭据已保存，但会话重启失败，请重发消息")
	}

	// Return the finalized card on the callback response itself —
	// Feishu uses `response.card` as the canonical post-callback
	// render. PATCH alone is not enough: the client snaps back to
	// the original card whenever the callback response omits `card`,
	// even after a successful PATCH landed (observed in prod).
	finalizedCard := gateway.BuildCredentialFormSubmittedCard(
		m.resolveCredentialFormCardTitle(ctx, claimed.ConversationID, "credential form submit"),
	)
	if replacedCount > 0 {
		return ackToastWithCard("success", fmt.Sprintf("已替换 %d 项现有凭据，正在继续会话", replacedCount), finalizedCard)
	}
	return ackToastWithCard("success", "已收到，正在继续会话", finalizedCard)
}

// patchPermissionResultCard PATCHes the permission card into its green /
// red result shape. Uses a short timeout so a stuck Feishu API doesn't
// pin the inbound websocket handler.
func (m *Manager) patchPermissionResultCard(ctx context.Context, conv store.ConversationInflightCards, approved bool) error {
	appID := strings.TrimSpace(conv.Permission.AppID)
	if appID == "" {
		appID = strings.TrimSpace(conv.SourceAppID)
	}
	if appID == "" {
		return errors.New("permission slot missing app_id")
	}
	appSecret, err := m.resolvePermissionPatchSecret(ctx, conv.WorkspaceID, appID)
	if err != nil {
		return err
	}
	// Lookup failure degrades to FeishuCardTitle via the empty-string
	// fallback in the builder.
	title, lookupErr := m.store.ResolveAgentNameForConversation(ctx, conv.ConversationID)
	if lookupErr != nil {
		m.logger.Warn("feishu inbound: resolve agent name for permission result failed",
			"conversation_id", conv.ConversationID,
			"err", lookupErr.Error(),
		)
		title = ""
	}
	content, err := gateway.BuildFeishuPermissionResultCardContent(title, approved)
	if err != nil {
		return err
	}
	client, err := gateway.NewFeishuTenantClient(gateway.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: m.openAPIBaseURL,
	})
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return client.PatchMessage(sendCtx, appSecret, strings.TrimSpace(conv.Permission.ExternalMsgID), content)
}

// handlePromptForUserChoiceSubmitAction handles the form-submit path.
// Every question on the card has a stable `q<i>` field name; we look
// the slot up by request_id, walk its question list in order, and
// pair each one with whatever the user picked. Missing answers become
// an empty string — the daemon treats "all blanks" as Cancelled so
// the agent gets a stop-signal instead of a half-answered tool_result.
func (m *Manager) handlePromptForUserChoiceSubmitAction(
	ctx context.Context,
	meta cardActionLogMeta,
	event *callback.CardActionTriggerEvent,
) *callback.CardActionTriggerResponse {
	requestID := strings.TrimSpace(cardActionStringValue(event, "request_id"))
	if requestID == "" {
		m.logger.Warn("feishu prompt_for_user_choice submit missing request_id",
			"open_message_id", meta.OpenMessageID,
			"operator_open_id", meta.OperatorOpenID,
		)
		return ackToast("info", "请稍后重试")
	}
	return m.routePromptForUserChoiceDecision(ctx, meta, requestID, cardActionFormValues(event))
}

// routePromptForUserChoiceDecision is the shared back-half of the
// form-submit handler. It looks up the slot by request_id, pairs each
// of its questions with the matching form value, forwards the decision
// to the runtime, and renders the done card.
func (m *Manager) routePromptForUserChoiceDecision(
	ctx context.Context,
	meta cardActionLogMeta,
	requestID string,
	formValues map[string]any,
) *callback.CardActionTriggerResponse {
	conv, err := m.store.FindConversationByPromptForUserChoiceRequestID(ctx, requestID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			m.logger.Info("feishu prompt_for_user_choice callback: slot already cleared",
				"request_id", requestID,
				"operator_open_id", meta.OperatorOpenID,
			)
			return ackToast("info", "该请求已处理或已过期")
		}
		m.logger.Warn("feishu prompt_for_user_choice callback: lookup failed",
			"request_id", requestID, "err", err.Error())
		return ackToast("error", "查询失败,请稍后再试")
	}
	if !conv.HasPromptForUserChoice {
		m.logger.Info("feishu prompt_for_user_choice callback: no slot",
			"request_id", requestID, "conversation_id", conv.ConversationID)
		return ackToast("info", "该请求已处理或已过期")
	}

	if m.pfucRouter == nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: router not configured",
			"request_id", requestID)
		return ackToast("error", "服务未配置,请联系管理员")
	}

	questions := conv.PromptForUserChoice.EffectiveQuestions()
	answers := make([]PromptForUserChoiceQuestionAnswer, 0, len(questions))
	anyAnswer := false
	for idx, q := range questions {
		answer := extractPromptForUserChoiceFormAnswer(formValues, idx)
		if answer != "" {
			anyAnswer = true
		}
		answers = append(answers, PromptForUserChoiceQuestionAnswer{
			Header: q.Header,
			Answer: answer,
		})
	}

	decision := PromptForUserChoiceDecision{
		RequestID:       requestID,
		QuestionAnswers: answers,
		OperatorID:      meta.OperatorOpenID,
	}
	if !anyAnswer {
		decision.Cancelled = true
		decision.Reason = "cancelled"
	}
	if err := m.pfucRouter.SubmitPromptForUserChoice(ctx, decision); err != nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: SubmitPromptForUserChoice failed",
			"request_id", requestID, "err", err.Error())
		return ackToast("error", "更新失败,请稍后再试")
	}

	// Build the done card inline AND return it on the callback response.
	// Feishu treats response.card as the canonical post-callback render —
	// a PATCH-only flow snaps the client back to the original "待回答"
	// card a beat later even when PatchMessage already landed (the
	// credential-form path documents the same behaviour). Building the
	// card map here means we don't need a separate patchPromptForUserChoiceDoneCard
	// network round-trip either.
	doneCard := m.buildPromptForUserChoiceDoneCardMap(ctx, conv, answers)

	if err := m.store.ClearConversationInflightSlot(ctx, conv.ConversationID, store.InflightSlotPromptForUserChoice, conv.PromptForUserChoice.AgentRunID); err != nil {
		m.logger.Warn("feishu prompt_for_user_choice callback: clear slot failed",
			"request_id", requestID, "conversation_id", conv.ConversationID, "err", err.Error())
	}
	if !anyAnswer {
		return ackToastWithCard("info", "已取消", doneCard)
	}
	return ackToastWithCard("success", "已记录: "+summarizePromptForUserChoiceAnswers(answers), doneCard)
}

// summarizePromptForUserChoiceAnswers builds the toast preview shown
// when the user submits. Single-question renders bare; multi shows
// each answer joined by " / " with the header prefix where set.
func summarizePromptForUserChoiceAnswers(answers []PromptForUserChoiceQuestionAnswer) string {
	if len(answers) == 1 {
		return strings.TrimSpace(answers[0].Answer)
	}
	parts := make([]string, 0, len(answers))
	for _, a := range answers {
		answer := strings.TrimSpace(a.Answer)
		if answer == "" {
			continue
		}
		header := strings.TrimSpace(a.Header)
		if header == "" {
			parts = append(parts, answer)
		} else {
			parts = append(parts, header+":"+answer)
		}
	}
	return strings.Join(parts, " / ")
}

// extractPromptForUserChoiceFormAnswer reads field "q<idx>" out of the
// form payload. select_static delivers a single string; multi_select_static
// delivers []any; input delivers a string. Missing field → "".
func extractPromptForUserChoiceFormAnswer(form map[string]any, idx int) string {
	if form == nil {
		return ""
	}
	raw, ok := form[fmt.Sprintf("q%d", idx)]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, entry := range v {
			s, ok := entry.(string)
			if !ok {
				s = fmt.Sprint(entry)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "、")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// buildPromptForUserChoiceDoneCardMap returns the JSON card used by
// ackToastWithCard. Mirrors patchPromptForUserChoiceDoneCard's content
// shape but skips the PatchMessage network call — Feishu renders the
// card directly from the callback response.
func (m *Manager) buildPromptForUserChoiceDoneCardMap(ctx context.Context, conv store.ConversationInflightCards, answers []PromptForUserChoiceQuestionAnswer) map[string]any {
	title, lookupErr := m.store.ResolveAgentNameForConversation(ctx, conv.ConversationID)
	if lookupErr != nil {
		m.logger.Warn("feishu inbound: resolve agent name for prompt_for_user_choice result failed",
			"conversation_id", conv.ConversationID, "err", lookupErr.Error())
		title = ""
	}
	cardAnswers := make([]gateway.PromptForUserChoiceCardAnswer, 0, len(answers))
	for _, a := range answers {
		cardAnswers = append(cardAnswers, gateway.PromptForUserChoiceCardAnswer{
			Header: a.Header,
			Answer: a.Answer,
		})
	}
	return gateway.BuildPromptForUserChoiceDoneCard(title, cardAnswers)
}

// patchPromptForUserChoiceDoneCard removed — the click handler now
// returns the done card body inline via ackToastWithCard, which the
// Feishu client renders directly without a follow-up PATCH. A
// PATCH-only flow would snap the client back to "待回答" a beat later
// (see ackToastWithCard's comment); ackToastWithCard pins the new
// content as canonical. If a future path needs an out-of-band patch
// (e.g. stale sweep), restore the function and call
// client.PatchMessage with BuildFeishuPromptForUserChoiceDoneCardContent.

// extractPromptForUserChoiceFormAnswers placeholder removed — the
// single-pick card we ship doesn't use a form +
// checker layout, so there's no form_value to walk. If a future
// iteration adds a real multi-select form, restore the helper here
// and re-wire handlePromptForUserChoiceSubmitAction in handleCardAction.

// resolvePermissionPatchSecret pulls the app_secret for the bot
// identified by appID. Keyed off the conversation row (what we have on
// a card callback) instead of a fresh inbound event.
func (m *Manager) resolvePermissionPatchSecret(ctx context.Context, workspaceID, appID string) (string, error) {
	if m.isDefaultSharedBotApp(appID) {
		return m.defaultBot.AppSecret, nil
	}
	route, err := m.store.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		return "", fmt.Errorf("lookup feishu agent: %w", err)
	}
	cfg, ok, err := gateway.DecodeFeishuConnectorConfig(route.Config)
	if err != nil {
		return "", err
	}
	if !ok || !cfg.Enabled || strings.TrimSpace(cfg.AppSecretRef) == "" {
		return "", errors.New("feishu connector missing app_secret_ref")
	}
	resolvedWorkspaceID := strings.TrimSpace(workspaceID)
	if resolvedWorkspaceID == "" {
		resolvedWorkspaceID = route.WorkspaceID
	}
	return m.loadAppSecret(ctx, resolvedWorkspaceID, cfg.AppSecretRef)
}

// cardActionStringValue safely pulls a string field out of an event's
// Action.Value map; missing / non-string values collapse to "".
func cardActionStringValue(event *callback.CardActionTriggerEvent, key string) string {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return ""
	}
	raw, ok := event.Event.Action.Value[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// cardActionFormValues returns the Action.FormValue map (never nil).
// Caller must NEVER log values verbatim — credential forms include
// sensitive cleartext keyed by "credential_<kind>".
func cardActionFormValues(event *callback.CardActionTriggerEvent) map[string]any {
	if event == nil || event.Event == nil || event.Event.Action == nil {
		return map[string]any{}
	}
	if event.Event.Action.FormValue == nil {
		return map[string]any{}
	}
	return event.Event.Action.FormValue
}

// resolveCredentialFormCardTitle returns the per-Agent display name for
// the post-submit / post-reject card; WARN-and-fallback on lookup
// failure so the callback path stays resilient.
func (m *Manager) resolveCredentialFormCardTitle(ctx context.Context, conversationID, logTag string) string {
	title, err := m.store.ResolveAgentNameForConversation(ctx, conversationID)
	if err != nil {
		m.logger.Warn("feishu inbound: resolve agent name for "+logTag+" failed",
			"conversation_id", conversationID,
			"err", err.Error(),
		)
		return ""
	}
	return title
}

func ackToast(kind, content string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    kind,
			Content: content,
		},
	}
}

// ackToastWithCard returns a callback response that replaces the source
// card with the supplied JSON in addition to showing the toast. Feishu
// treats `response.card` as the canonical post-callback render; the
// client snaps back to the original card whenever a PATCH-only flow
// omits this field, even if PATCH already landed (observed in prod).
func ackToastWithCard(kind, content string, card map[string]any) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    kind,
			Content: content,
		},
		Card: &callback.Card{
			Type: "raw",
			Data: card,
		},
	}
}

type cardActionLogMeta struct {
	AppID          string
	OpenMessageID  string
	OpenChatID     string
	OperatorOpenID string
	Action         string
}

func cardActionMetadata(appID string, event *callback.CardActionTriggerEvent) cardActionLogMeta {
	meta := cardActionLogMeta{AppID: strings.TrimSpace(appID)}
	if event == nil || event.Event == nil {
		return meta
	}
	if event.Event.Context != nil {
		meta.OpenMessageID = strings.TrimSpace(stringValue(event.Event.Context.OpenMessageID))
		meta.OpenChatID = strings.TrimSpace(stringValue(event.Event.Context.OpenChatID))
	}
	if event.Event.Operator != nil {
		meta.OperatorOpenID = strings.TrimSpace(stringValue(event.Event.Operator.OpenID))
	}
	if event.Event.Action != nil && len(event.Event.Action.Value) > 0 {
		if raw, ok := event.Event.Action.Value["action"]; ok {
			meta.Action = strings.TrimSpace(fmt.Sprint(raw))
		}
	}
	return meta
}

type router struct {
	store Storer
}

func (r router) GetAgentByFeishuAppID(ctx context.Context, appID string) (gateway.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gateway.FeishuRouteAgent{}, gateway.ErrFeishuRouterUnknownAgent
		}
		return gateway.FeishuRouteAgent{}, err
	}
	return routeFromStore(route), nil
}

func (r router) GetAgentByID(ctx context.Context, agentID string) (gateway.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gateway.FeishuRouteAgent{}, gateway.ErrFeishuRouterUnknownAgent
		}
		return gateway.FeishuRouteAgent{}, err
	}
	return routeFromStore(route), nil
}

func routeFromStore(route store.FeishuAgentRoute) gateway.FeishuRouteAgent {
	return gateway.FeishuRouteAgent{
		AgentID:       route.AgentID,
		WorkspaceID:   route.WorkspaceID,
		WorkspaceName: route.WorkspaceName,
		AgentName:     route.AgentName,
		AgentSlug:     route.AgentSlug,
		Visibility:    gateway.Visibility(route.Visibility),
		Config:        route.Config,
	}
}

func (r router) FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error) {
	userID, err := r.store.FindUserIDByFeishuUnionID(ctx, unionID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuUser) {
			return "", gateway.ErrFeishuRouterUnknownUser
		}
		return "", err
	}
	return userID, nil
}

func (r router) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return r.store.IsActiveWorkspaceMember(ctx, workspaceID, userID)
}

func (r router) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	return r.store.GetWorkspaceVisibility(ctx, workspaceID)
}

func (r router) ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	return r.store.ListActiveWorkspaceOwnerNames(ctx, workspaceID, limit)
}

// gateConfig is shared by feishushared callers and the direct path so
// the workspace-rejection card stays identical across routing modes.
func (m *Manager) gateConfig() gateway.GateConfig {
	return gateway.GateConfig{JoinURLBuilder: m.joinURLBuilder}
}

func inboundEventFromSDK(appID string, received *larkim.P2MessageReceiveV1) gateway.FeishuInboundEvent {
	if received == nil || received.Event == nil || received.Event.Message == nil {
		return gateway.FeishuInboundEvent{AppID: strings.TrimSpace(appID)}
	}
	message := received.Event.Message
	mentionOpenIDs := make([]string, 0, len(message.Mentions))
	mentionKeys := make([]string, 0, len(message.Mentions))
	for _, mention := range message.Mentions {
		if mention == nil {
			continue
		}
		if mention.Id != nil && strings.TrimSpace(stringValue(mention.Id.OpenId)) != "" {
			mentionOpenIDs = append(mentionOpenIDs, strings.TrimSpace(stringValue(mention.Id.OpenId)))
		}
		if strings.TrimSpace(stringValue(mention.Key)) != "" {
			mentionKeys = append(mentionKeys, strings.TrimSpace(stringValue(mention.Key)))
		}
	}
	messageType := strings.TrimSpace(stringValue(message.MessageType))
	rawContent := stringValue(message.Content)
	parsed := gateway.ParseFeishuMessageContent(messageType, rawContent, mentionKeys)

	sender := received.Event.Sender
	var senderOpenID, senderUserID, senderUnionID, senderType, tenantKey string
	if sender != nil {
		tenantKey = stringValue(sender.TenantKey)
		senderType = stringValue(sender.SenderType)
		if sender.SenderId != nil {
			senderOpenID = stringValue(sender.SenderId.OpenId)
			senderUserID = stringValue(sender.SenderId.UserId)
			senderUnionID = stringValue(sender.SenderId.UnionId)
		}
	}
	metadata := map[string]any{
		"mention_keys": mentionKeys,
	}
	mergeMetadata(metadata, parsed.Metadata)
	return gateway.FeishuInboundEvent{
		AppID:          strings.TrimSpace(appID),
		MessageID:      stringValue(message.MessageId),
		RootID:         stringValue(message.RootId),
		ParentID:       stringValue(message.ParentId),
		ChatID:         stringValue(message.ChatId),
		ChatType:       stringValue(message.ChatType),
		ThreadID:       stringValue(message.ThreadId),
		MessageType:    messageType,
		RawContent:     rawContent,
		Text:           parsed.Text,
		SenderOpenID:   senderOpenID,
		SenderUserID:   senderUserID,
		SenderUnionID:  senderUnionID,
		SenderType:     senderType,
		TenantKey:      tenantKey,
		MentionOpenIDs: mentionOpenIDs,
		MentionKeys:    mentionKeys,
		Metadata:       metadata,
	}
}

func isSelfMessage(rawConfig []byte, senderOpenID string) bool {
	senderOpenID = strings.TrimSpace(senderOpenID)
	if senderOpenID == "" {
		return false
	}
	cfg, ok, err := gateway.DecodeFeishuConnectorConfig(rawConfig)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(cfg.BotOpenID) != "" && strings.TrimSpace(cfg.BotOpenID) == senderOpenID
}

// feishuThreadHistoryLookup is the narrow surface
// isGroupMessageWithoutBotMention needs. Satisfied by Storer and the
// inboundFakeStore test double.
type feishuThreadHistoryLookup interface {
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)
}

// isGroupMessageWithoutBotMention decides whether a group-chat inbound
// should be silently dropped before any routing / storage work. Mirrors
// dev/routes.go isFeishuGroupMessageWithoutBotMention — the two paths
// must agree because the same Agent / connector flows through whichever
// event-mode the operator picked.
func isGroupMessageWithoutBotMention(ctx context.Context, store feishuThreadHistoryLookup, rawConfig []byte, event gateway.FeishuInboundEvent) bool {
	chatType := strings.ToLower(strings.TrimSpace(event.ChatType))
	if chatType == "p2p" || chatType == "" {
		return false
	}
	// Other Feishu apps/bots post interactive cards whose "@bot" text
	// lives in the card body, never in message.mentions. Treat any
	// non-user sender as already-targeted.
	if event.IsBotSender() {
		return false
	}
	botOpenID := ""
	if cfg, ok, err := gateway.DecodeFeishuConnectorConfig(rawConfig); err == nil && ok {
		botOpenID = strings.TrimSpace(cfg.BotOpenID)
	}
	if len(event.MentionOpenIDs) > 0 {
		if botOpenID == "" {
			return true
		}
		for _, mentionedOpenID := range event.MentionOpenIDs {
			if strings.TrimSpace(mentionedOpenID) == botOpenID {
				return false
			}
		}
		return true
	}
	// Thread continuation: when this inbound is inside a thread (or
	// reply chain — ThreadKey falls back to RootID when Feishu omits
	// thread_id) and we already have history for (chat_id, thread_key),
	// let it through without an @mention. For non-thread inbounds
	// ThreadKey() returns MessageID, which never has prior history on a
	// brand-new top-level message, so this branch is a no-op there.
	threadKey := strings.TrimSpace(event.ThreadKey())
	if threadKey != "" && store != nil {
		hasHistory, err := store.HasFeishuThreadInboundHistory(ctx, strings.TrimSpace(event.ChatID), threadKey)
		if err == nil && hasHistory {
			return false
		}
	}
	return true
}

func mergeMetadata(dst map[string]any, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	for k, v := range src {
		if strings.TrimSpace(k) == "" || v == nil {
			continue
		}
		dst[k] = v
	}
}

func normalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.TrimRight(raw, "/")
}

func normalizeDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	return "https://" + raw
}

func (m *Manager) defaultSharedRouteAndConfig() (store.FeishuAgentRoute, gateway.FeishuConnectorConfig) {
	cfg := gateway.FeishuConnectorConfig{
		Enabled:     true,
		AppID:       m.defaultBot.AppID,
		BotOpenID:   m.defaultBot.BotOpenID,
		EventMode:   "websocket",
		RoutingMode: "shared",
	}
	raw, _ := json.Marshal(map[string]any{
		"connectors": map[string]any{
			"feishu": cfg,
		},
	})
	return store.FeishuAgentRoute{
		AgentName:  "Default Feishu Bot",
		AgentSlug:  "default-feishu-bot",
		Visibility: string(gateway.VisibilityPublic),
		Config:     raw,
	}, cfg
}

func (m *Manager) isDefaultSharedBotApp(appID string) bool {
	return m.defaultBot.configured() && strings.TrimSpace(appID) == m.defaultBot.AppID
}

func clientKey(workspaceID, appID string) string {
	return strings.TrimSpace(workspaceID) + "|" + strings.TrimSpace(appID)
}

func defaultClientKey(appID string) string {
	return "default|" + strings.TrimSpace(appID)
}

func stringValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case *string:
		if x == nil {
			return ""
		}
		return *x
	default:
		return fmt.Sprint(x)
	}
}
