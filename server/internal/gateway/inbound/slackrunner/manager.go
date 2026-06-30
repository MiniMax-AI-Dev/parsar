// manager.go is the multi-tenant reconciler that turns workspace_im_connectors
// rows into live Socket Mode runners — the Slack twin of inbound.Manager's
// Feishu websocket loop.
//
// Where Runner (runner.go) drives ONE env-gated socket, Manager drives N: it
// ticks every refresh interval, lists every enabled Slack connector
// (ListWorkspaceConnectorsByPlatform), decrypts each one's bot/app token out of
// the vault, and keeps exactly one Runner per workspace|app_id key. Connectors
// that disappear (disabled, deleted, app_id changed) have their runner stopped;
// a token rotation is detected by a per-handle fingerprint and triggers a
// stop+restart so the new credential takes effect without a process restart.
//
// Only Socket Mode connectors (event_mode=socket, the default) get a runner —
// an events-API connector would need an inbound HTTP webhook, which this
// reconciler does not own. The adapter (with its action/credential routing) is
// built by an injected NewAdapter factory so this package stays free of the
// connector/inbound wiring that lives in main.
package slackrunner

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
	slackchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/slack"
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
	// Secrets decrypts the bot/app token payloads.
	Secrets SecretDecrypter
	// NewAdapter builds a Slack adapter for one workspace bot. main injects it
	// so the action-router / credential-resolver wiring (which needs the
	// connector registry and secret vault) stays out of this package.
	NewAdapter func(appID, botToken, appToken string) *slackchannel.Channel
	// GateConfig feeds the visibility rejection cards each runner renders.
	GateConfig gateway.GateConfig
	// Logger is optional (defaults to log.Bg()).
	Logger *slog.Logger
	// RefreshInterval defaults to 30s, capped at 10m.
	RefreshInterval time.Duration
}

// Manager reconciles configured Slack connectors and keeps one Socket Mode
// Runner per workspace|app_id. Run blocks until ctx is cancelled.
type Manager struct {
	store      ConnectorStore
	router     router.Store
	secrets    SecretDecrypter
	newAdapter func(appID, botToken, appToken string) *slackchannel.Channel
	gateCfg    gateway.GateConfig
	log        *slog.Logger
	refresh    time.Duration

	mu      sync.Mutex
	clients map[string]*slackClientHandle
}

// slackClientHandle is one live runner plus the state needed to stop it and to
// detect a token rotation (fingerprint) on the next reconcile.
type slackClientHandle struct {
	key         string
	appID       string
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager validates config and returns an inert manager (Run starts the
// loop). Mirrors inbound.NewManager.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	if cfg.Store == nil {
		return nil, errors.New("slack runner manager: Store is required")
	}
	if cfg.RouterStore == nil {
		return nil, errors.New("slack runner manager: RouterStore is required")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("slack runner manager: Secrets decrypter is required")
	}
	if cfg.NewAdapter == nil {
		return nil, errors.New("slack runner manager: NewAdapter factory is required")
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
		clients:    make(map[string]*slackClientHandle),
	}, nil
}

// Run starts the reconcile loop. It returns ctx.Err() on normal shutdown.
func (m *Manager) Run(ctx context.Context) error {
	m.log.Info("slack socket mode inbound manager starting", "refresh_interval", m.refresh.String())
	defer m.stopAll()

	if err := m.Reconcile(ctx); err != nil {
		m.log.Warn("slack socket mode inbound initial reconcile failed", "err", err.Error())
	}
	ticker := time.NewTicker(m.refresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("slack socket mode inbound manager stopping", "reason", ctx.Err().Error())
			return ctx.Err()
		case <-ticker.C:
			if err := m.Reconcile(ctx); err != nil {
				m.log.Warn("slack socket mode inbound reconcile failed", "err", err.Error())
			}
		}
	}
}

// Reconcile starts missing runners, restarts ones whose token rotated, and
// stops runners for connectors that were disabled/removed.
func (m *Manager) Reconcile(ctx context.Context) error {
	conns, err := m.store.ListWorkspaceConnectorsByPlatform(ctx, "slack")
	if err != nil {
		return err
	}

	wanted := make(map[string]struct{}, len(conns))
	for _, conn := range conns {
		appID := strings.TrimSpace(conn.AppID)
		if !conn.Enabled || appID == "" {
			continue
		}
		// Only Socket Mode connectors get a runner; events-API connectors need
		// an inbound HTTP webhook this reconciler does not own.
		if normalizeEventMode(conn.Config) != "socket" {
			continue
		}
		botRef := configString(conn.Config, "bot_token_ref")
		appRef := configString(conn.Config, "app_token_ref")
		if botRef == "" || appRef == "" {
			m.log.Warn("slack socket mode inbound: connector missing bot_token_ref or app_token_ref",
				"workspace_id", conn.WorkspaceID, "app_id", appID)
			continue
		}
		key := clientKey(conn.WorkspaceID, appID)
		fingerprint := botRef + "|" + appRef
		wanted[key] = struct{}{}
		if m.upToDate(key, fingerprint) {
			continue
		}
		// Either missing or rotated: stop a stale instance then (re)start.
		m.stopByKey(key)
		if err := m.startClient(ctx, conn, key, botRef, appRef, fingerprint); err != nil {
			m.log.Warn("slack socket mode inbound: start client failed",
				"workspace_id", conn.WorkspaceID, "app_id", appID, "err", err.Error())
		}
	}

	m.mu.Lock()
	stale := make([]*slackClientHandle, 0)
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
// (same token refs), so the reconcile can skip a needless restart.
func (m *Manager) upToDate(key, fingerprint string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.clients[key]
	return ok && h.fingerprint == fingerprint
}

func (m *Manager) startClient(ctx context.Context, conn store.WorkspaceConnectorRead, key, botRef, appRef, fingerprint string) error {
	appID := strings.TrimSpace(conn.AppID)
	botToken, err := m.loadToken(ctx, conn.WorkspaceID, botRef)
	if err != nil {
		return fmt.Errorf("load bot token: %w", err)
	}
	appToken, err := m.loadToken(ctx, conn.WorkspaceID, appRef)
	if err != nil {
		return fmt.Errorf("load app token: %w", err)
	}
	adapter := m.newAdapter(appID, botToken, appToken)
	runner, err := New(Config{
		BotToken:   botToken,
		AppToken:   appToken,
		Channel:    adapter,
		Store:      m.router,
		GateConfig: m.gateCfg,
		Logger:     m.log,
	})
	if err != nil {
		return err
	}

	clientCtx, cancel := context.WithCancel(ctx)
	handle := &slackClientHandle{key: key, appID: appID, cancel: cancel, fingerprint: fingerprint}

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
			m.log.Warn("slack socket mode inbound client exited",
				"workspace_id", conn.WorkspaceID, "app_id", appID, "err", err.Error())
		}
	}()
	m.log.Info("slack socket mode inbound client started", "workspace_id", conn.WorkspaceID, "app_id", appID)
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

func (m *Manager) stopHandle(h *slackClientHandle) {
	if h == nil {
		return
	}
	h.cancel()
	m.log.Info("slack socket mode inbound client stopped", "app_id", h.appID)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	handles := make([]*slackClientHandle, 0, len(m.clients))
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

// normalizeEventMode reads config.event_mode, defaulting to "socket" (the only
// mode the reconciler runs).
func normalizeEventMode(config map[string]any) string {
	switch strings.ToLower(configString(config, "event_mode")) {
	case "events", "events_api", "http", "webhook":
		return "events"
	default:
		return "socket"
	}
}

// tokenFromPayload extracts a token from a decrypted secret payload using the
// shared key precedence used elsewhere for bot credentials.
func tokenFromPayload(payload map[string]any) string {
	for _, key := range []string{"bot_token", "app_token", "api_key", "token", "access_token", "value"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
