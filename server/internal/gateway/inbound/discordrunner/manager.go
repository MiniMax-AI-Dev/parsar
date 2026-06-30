// manager.go is the multi-tenant reconciler that turns workspace_im_connectors
// rows into live Gateway WebSocket runners — the Discord twin of slackrunner's
// Manager and inbound.Manager's Feishu websocket loop.
//
// Where Runner (runner.go) drives ONE env-gated gateway socket, Manager drives
// N: it ticks every refresh interval, lists every enabled Discord connector
// (ListWorkspaceConnectorsByPlatform), decrypts each one's bot token out of the
// vault, and keeps exactly one Runner per workspace|app_id key. Connectors that
// disappear (disabled, deleted, app_id changed) have their runner stopped; a
// token rotation is detected by a per-handle fingerprint and triggers a
// stop+restart so the new credential takes effect without a process restart.
//
// Unlike Slack there is no separate app-level socket token: the bot token is the
// only credential the Gateway IDENTIFY needs. The adapter (with its
// action/credential routing + pick store) is built by an injected NewAdapter
// factory so this package stays free of the connector/inbound wiring that lives
// in main.
package discordrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	discordchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/discord"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ConnectorStore is the read surface the reconciler needs: the per-platform
// connector list and the vault secret payloads its *_ref UUIDs point at.
// *store.Store satisfies it.
type ConnectorStore interface {
	ListWorkspaceConnectorsByPlatform(ctx context.Context, platform string) ([]store.WorkspaceConnectorRead, error)
	GetSecretPayload(ctx context.Context, workspaceID, secretID string) (store.SecretPayload, error)
}

// SecretDecrypter mirrors the small subset of *secrets.Service the reconciler
// uses to turn an encrypted vault payload into its plaintext token fields.
type SecretDecrypter interface {
	Decrypt(envelopeJSON []byte) (map[string]any, error)
}

// ManagerConfig configures Manager. Store, Secrets and NewAdapter are required.
type ManagerConfig struct {
	// Store reads the connector rows and their secret payloads.
	Store ConnectorStore
	// RouterStore is handed to every Runner as the shared inbound router store.
	RouterStore router.Store
	// Secrets decrypts the bot token payload.
	Secrets SecretDecrypter
	// NewAdapter builds a Discord adapter for one workspace bot. main injects it
	// so the action-router / credential-resolver / pick-store wiring stays out of
	// this package.
	NewAdapter func(appID, botToken string) *discordchannel.Channel
	// GateConfig feeds the visibility rejection cards each runner renders.
	GateConfig gateway.GateConfig
	// Logger is optional (defaults to log.Bg()).
	Logger *slog.Logger
	// RefreshInterval defaults to 30s, capped at 10m.
	RefreshInterval time.Duration
}

// Manager reconciles configured Discord connectors and keeps one Gateway
// WebSocket Runner per workspace|app_id. Run blocks until ctx is cancelled.
type Manager struct {
	store      ConnectorStore
	router     router.Store
	secrets    SecretDecrypter
	newAdapter func(appID, botToken string) *discordchannel.Channel
	gateCfg    gateway.GateConfig
	log        *slog.Logger
	refresh    time.Duration

	mu      sync.Mutex
	clients map[string]*discordClientHandle
}

// discordClientHandle is one live runner plus the state needed to stop it and
// to detect a token rotation (fingerprint) on the next reconcile.
type discordClientHandle struct {
	key         string
	appID       string
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager validates config and returns an inert manager (Run starts the
// loop). Mirrors slackrunner.NewManager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil {
		return nil, errors.New("discord runner manager: Store is required")
	}
	if cfg.RouterStore == nil {
		return nil, errors.New("discord runner manager: RouterStore is required")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("discord runner manager: Secrets decrypter is required")
	}
	if cfg.NewAdapter == nil {
		return nil, errors.New("discord runner manager: NewAdapter factory is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Bg()
	}
	refresh := cfg.RefreshInterval
	if refresh <= 0 {
		refresh = 30 * time.Second
	}
	if refresh > 10*time.Minute {
		refresh = 10 * time.Minute
	}
	return &Manager{
		store:      cfg.Store,
		router:     cfg.RouterStore,
		secrets:    cfg.Secrets,
		newAdapter: cfg.NewAdapter,
		gateCfg:    cfg.GateConfig,
		log:        logger,
		refresh:    refresh,
		clients:    make(map[string]*discordClientHandle),
	}, nil
}

// Run starts the reconcile loop. It returns ctx.Err() on normal shutdown.
func (m *Manager) Run(ctx context.Context) error {
	m.log.Info("discord gateway inbound manager starting", "refresh_interval", m.refresh.String())
	defer m.stopAll()

	if err := m.Reconcile(ctx); err != nil {
		m.log.Warn("discord gateway inbound initial reconcile failed", "err", err.Error())
	}
	ticker := time.NewTicker(m.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("discord gateway inbound manager stopping", "reason", ctx.Err().Error())
			return ctx.Err()
		case <-ticker.C:
			if err := m.Reconcile(ctx); err != nil {
				m.log.Warn("discord gateway inbound reconcile failed", "err", err.Error())
			}
		}
	}
}

// Reconcile starts missing runners, restarts ones whose token rotated, and
// stops runners for connectors that were disabled/removed.
func (m *Manager) Reconcile(ctx context.Context) error {
	conns, err := m.store.ListWorkspaceConnectorsByPlatform(ctx, "discord")
	if err != nil {
		return err
	}

	wanted := make(map[string]struct{}, len(conns))
	for _, conn := range conns {
		appID := strings.TrimSpace(conn.AppID)
		if !conn.Enabled || appID == "" {
			continue
		}
		botRef := configString(conn.Config, "bot_token_ref")
		if botRef == "" {
			m.log.Warn("discord gateway inbound: connector missing bot_token_ref",
				"workspace_id", conn.WorkspaceID, "app_id", appID)
			continue
		}
		key := clientKey(conn.WorkspaceID, appID)
		wanted[key] = struct{}{}
		if m.upToDate(key, botRef) {
			continue
		}
		m.stopByKey(key)
		if err := m.startClient(ctx, conn, key, botRef); err != nil {
			m.log.Warn("discord gateway inbound: start client failed",
				"workspace_id", conn.WorkspaceID, "app_id", appID, "err", err.Error())
		}
	}

	m.mu.Lock()
	stale := make([]*discordClientHandle, 0)
	for key, h := range m.clients {
		if _, ok := wanted[key]; !ok {
			stale = append(stale, h)
			delete(m.clients, key)
		}
	}
	m.mu.Unlock()
	for _, h := range stale {
		m.stopHandle(h)
	}
	return nil
}

// upToDate reports whether a running handle for key already matches fingerprint
// (same token ref), so the reconcile can skip a needless restart.
func (m *Manager) upToDate(key, fingerprint string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.clients[key]
	return ok && h.fingerprint == fingerprint
}

func (m *Manager) startClient(ctx context.Context, conn store.WorkspaceConnectorRead, key, botRef string) error {
	appID := strings.TrimSpace(conn.AppID)
	botToken, err := m.loadToken(ctx, conn.WorkspaceID, botRef)
	if err != nil {
		return fmt.Errorf("load bot token: %w", err)
	}
	adapter := m.newAdapter(appID, botToken)
	runner, err := New(Config{
		BotToken:   botToken,
		Channel:    adapter,
		Store:      m.router,
		GateConfig: m.gateCfg,
		Logger:     m.log,
	})
	if err != nil {
		return err
	}

	clientCtx, cancel := context.WithCancel(ctx)
	handle := &discordClientHandle{key: key, appID: appID, cancel: cancel, fingerprint: botRef}

	m.mu.Lock()
	if _, exists := m.clients[key]; exists {
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.clients[key] = handle
	m.mu.Unlock()

	go func() {
		if err := runner.Run(clientCtx); err != nil && !errors.Is(err, context.Canceled) {
			m.log.Warn("discord gateway inbound client exited",
				"workspace_id", conn.WorkspaceID, "app_id", appID, "err", err.Error())
		}
	}()
	m.log.Info("discord gateway inbound client started", "workspace_id", conn.WorkspaceID, "app_id", appID)
	return nil
}

// stopByKey stops and removes a running handle for key, if present.
func (m *Manager) stopByKey(key string) {
	m.mu.Lock()
	h, ok := m.clients[key]
	if ok {
		delete(m.clients, key)
	}
	m.mu.Unlock()
	if ok {
		m.stopHandle(h)
	}
}

func (m *Manager) stopHandle(h *discordClientHandle) {
	if h == nil {
		return
	}
	h.cancel()
	m.log.Info("discord gateway inbound client stopped", "app_id", h.appID)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	handles := make([]*discordClientHandle, 0, len(m.clients))
	for key, h := range m.clients {
		handles = append(handles, h)
		delete(m.clients, key)
	}
	m.mu.Unlock()
	for _, h := range handles {
		m.stopHandle(h)
	}
}

// loadToken reads a secret payload by id and returns its plaintext token using
// the shared key precedence (api_key → token → access_token → value).
func (m *Manager) loadToken(ctx context.Context, workspaceID, secretID string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", errors.New("empty secret ref")
	}
	payload, err := m.store.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil {
		return "", fmt.Errorf("read secret payload: %w", err)
	}
	decrypted, err := m.secrets.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	token := tokenFromPayload(decrypted)
	if token == "" {
		return "", errors.New("secret payload missing token value")
	}
	return token, nil
}

// clientKey is the per-runner identity: workspace|app_id (the Feishu manager's
// key shape).
func clientKey(workspaceID, appID string) string {
	return strings.TrimSpace(workspaceID) + "|" + strings.TrimSpace(appID)
}

// configString reads a trimmed string field out of a decoded connector config.
func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	v, _ := config[key].(string)
	return strings.TrimSpace(v)
}

// tokenFromPayload extracts a token from a decrypted secret payload using the
// shared key precedence used elsewhere for bot credentials.
func tokenFromPayload(payload map[string]any) string {
	for _, key := range []string{"bot_token", "api_key", "token", "access_token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
