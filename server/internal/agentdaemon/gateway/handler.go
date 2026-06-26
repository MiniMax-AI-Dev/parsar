package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// HeartbeatTouch is the subset of store.Store the gateway uses to
// bump last_heartbeat_at / promote pending_pairing -> online when a
// daemon connects.
type HeartbeatTouch interface {
	TouchRuntimeHeartbeat(ctx context.Context, runtimeID string) (store.HeartbeatStatus, error)
	TouchAgentDaemonHeartbeat(ctx context.Context, input store.TouchAgentDaemonHeartbeatInput) (store.HeartbeatStatus, error)
	MarkRuntimeOffline(ctx context.Context, runtimeID string) error
}

// HandlerConfig wires the gateway's HTTP/WS handlers. nil values panic
// on use; the gateway only ever runs in a fully-configured production
// server, so loud failure beats silent fall-through.
type HandlerConfig struct {
	// Authenticator validates the bearer credential on /agent-daemon/ws
	// upgrade and on /agent-daemon/bootstrap.
	Authenticator *Authenticator

	Registry *Registry

	// Heartbeat flips pending_pairing -> online and keeps
	// last_heartbeat_at fresh. nil tracks liveness in-process only.
	Heartbeat HeartbeatTouch

	// PublicWSURL is the wss://... URL returned in the bootstrap
	// response so deployments behind a TLS terminator can advertise
	// the externally-reachable URL.
	PublicWSURL string

	// OwnerStore enables multi-pod WebSocket ownership. When set, every
	// successful daemon WS dial-in claims device_id -> owner_pod_id in
	// Postgres and receives a generation fencing token. nil preserves
	// the legacy single-pod in-memory Registry behavior.
	OwnerStore DeviceOwnerStore
	OwnerPodID string
	OwnerURL   string

	// OwnerLeaseTTL controls how long the owner row stays valid without
	// a renewing inbound daemon frame. Zero uses the package default.
	OwnerLeaseTTL time.Duration

	// HeartbeatInterval overrides DefaultHeartbeatInterval. Zero -> default.
	HeartbeatInterval time.Duration

	// Log is the gateway's leveled-ish log sink. nil silences logs.
	Log SessionLogger
}

// Handler exposes the agent_daemon HTTP endpoints.
type Handler struct {
	cfg      HandlerConfig
	upgrader websocket.Upgrader
}

// NewHandler panics on missing Authenticator / Registry — a
// misconfigured gateway is a bug we'd rather catch at boot than at the
// first dial-in.
func NewHandler(cfg HandlerConfig) *Handler {
	if cfg.Authenticator == nil {
		panic("agentdaemon gateway: HandlerConfig.Authenticator is required")
	}
	if cfg.Registry == nil {
		panic("agentdaemon gateway: HandlerConfig.Registry is required")
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	cfg.OwnerLeaseTTL = normalizeOwnerTTL(cfg.OwnerLeaseTTL)
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	return &Handler{
		cfg: cfg,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Daemon is a non-browser client and sends no Origin;
			// the bearer in the query param is the actual auth boundary.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// WS is the websocket upgrade entry point. Errors before the upgrade
// return JSON 4xx; errors during the WS read loop fall to Session.Close
// which fans synthetic error/done to every active subscriber.
func (h *Handler) WS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	deviceID := q.Get("device_id")
	token := q.Get("token")
	version := q.Get("version")
	if deviceID == "" || token == "" || version == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_params", "device_id, token, version are required")
		return
	}
	if !proto.VersionCompatible(version) {
		writeAuthError(w, http.StatusUpgradeRequired, "incompatible_version",
			"daemon protocol "+version+" incompatible with server "+proto.Version)
		return
	}
	auth, err := h.cfg.Authenticator.AuthenticateBearer(r.Context(), deviceID, token)
	if err != nil {
		status, code := mapAuthError(err)
		// Without this log line a WS-upgrade 401 leaves no server-side
		// trace; the credential itself stays out of the log.
		h.cfg.Log("agentdaemon gateway: ws auth rejected device_id=%s code=%s status=%d err=%v",
			deviceID, code, status, err)
		writeAuthError(w, status, code, err.Error())
		return
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrader has already written a response.
		h.cfg.Log("agentdaemon gateway: ws upgrade: %v", err)
		return
	}
	if h.cfg.Heartbeat != nil {
		// First inbound action — promote pending_pairing -> online.
		// Best-effort; a transient DB blip shouldn't refuse the
		// upgrade since we already accepted the credential.
		if _, hbErr := h.cfg.Heartbeat.TouchRuntimeHeartbeat(r.Context(), auth.DeviceID); hbErr != nil {
			h.cfg.Log("agentdaemon gateway: heartbeat on connect: %v", hbErr)
		}
	}
	var lease *ownerLease
	if h.cfg.OwnerStore != nil {
		now := time.Now().UTC()
		owner, ownerErr := h.cfg.OwnerStore.ClaimAgentDaemonDeviceOwner(r.Context(), store.ClaimAgentDaemonDeviceOwnerInput{
			DeviceID:       auth.DeviceID,
			WorkspaceID:    auth.WorkspaceID,
			OwnerPodID:     h.cfg.OwnerPodID,
			OwnerURL:       h.cfg.OwnerURL,
			Now:            now,
			LeaseExpiresAt: now.Add(h.cfg.OwnerLeaseTTL),
		})
		if ownerErr != nil {
			h.cfg.Log("agentdaemon gateway: owner claim failed: %v", ownerErr)
			_ = conn.Close()
			return
		}
		lease = &ownerLease{
			store:      h.cfg.OwnerStore,
			deviceID:   auth.DeviceID,
			ownerPodID: owner.OwnerPodID,
			ownerURL:   owner.OwnerURL,
			generation: owner.Generation,
			ttl:        h.cfg.OwnerLeaseTTL,
		}
	}
	sess := NewSessionWithOwner(conn, auth.DeviceID, auth.WorkspaceID, version, h.cfg.Registry, h.cfg.Log, lease)
	sess.heartbeat = h.cfg.Heartbeat
	h.cfg.Log("agentdaemon gateway: ws upgrade ok, registering device_id=%s owner_pod=%s waiters=%d",
		auth.DeviceID, h.cfg.OwnerPodID, len(h.cfg.Registry.PendingWaiters(auth.DeviceID)))
	if prev := h.cfg.Registry.Register(sess); prev != nil {
		// Latest dial-in wins; close the zombie out-of-band.
		prev.Close("preempted by newer connection from same device_id")
	}
	h.cfg.Log("agentdaemon gateway: device_id=%s registered in registry, starting session", auth.DeviceID)
	sess.Start()
}

// Bootstrap is the daemon's first HTTP call after pairing. Validating
// the bearer in a separate HTTP step (rather than folded into the WS
// upgrade) lets the daemon fail fast on credential problems with a
// real HTTP status rather than the opaque WS close code.
func (h *Handler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	bearer := bearerFromAuthHeader(r)
	if bearer == "" {
		writeAuthError(w, http.StatusUnauthorized, "missing_bearer", "Authorization: Bearer <runner_credential> required")
		return
	}
	var body struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAuthError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.DeviceID == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_device_id", "request body must contain device_id")
		return
	}
	auth, err := h.cfg.Authenticator.AuthenticateBearer(r.Context(), body.DeviceID, bearer)
	if err != nil {
		status, code := mapAuthError(err)
		h.cfg.Log("agentdaemon gateway: bootstrap auth rejected device_id=%s code=%s status=%d err=%v",
			body.DeviceID, code, status, err)
		writeAuthError(w, status, code, err.Error())
		return
	}
	resp := map[string]any{
		"device_id":         auth.DeviceID,
		"workspace_id":      auth.WorkspaceID,
		"ws_url":            h.cfg.PublicWSURL,
		"heartbeat_seconds": int(h.cfg.HeartbeatInterval.Seconds()),
		"protocol_version":  proto.Version,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// DeviceStatus is a lightweight liveness probe the daemon hits before
// the WS dial.
func (h *Handler) DeviceStatus(w http.ResponseWriter, r *http.Request) {
	bearer := bearerFromAuthHeader(r)
	if bearer == "" {
		writeAuthError(w, http.StatusUnauthorized, "missing_bearer", "")
		return
	}
	deviceID := r.URL.Query().Get("device_id")
	if deviceID == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_device_id", "device_id query param required")
		return
	}
	auth, err := h.cfg.Authenticator.AuthenticateBearer(r.Context(), deviceID, bearer)
	if err != nil {
		status, code := mapAuthError(err)
		h.cfg.Log("agentdaemon gateway: device-status auth rejected device_id=%s code=%s status=%d err=%v",
			deviceID, code, status, err)
		writeAuthError(w, status, code, err.Error())
		return
	}
	_, regErr := h.cfg.Registry.LookupDevice(auth.DeviceID)
	online := regErr == nil
	var owner map[string]any
	if h.cfg.OwnerStore != nil {
		if current, ok, ownerErr := h.cfg.OwnerStore.GetAgentDaemonDeviceOwner(r.Context(), auth.DeviceID); ownerErr != nil {
			h.cfg.Log("agentdaemon gateway: device-status owner lookup failed: %v", ownerErr)
		} else if ok {
			leaseOnline := current.Status == store.AgentDaemonOwnerStatusConnected && current.LeaseExpiresAt.After(time.Now().UTC())
			online = online || leaseOnline
			owner = map[string]any{
				"owner_pod_id":     current.OwnerPodID,
				"owner_url":        current.OwnerURL,
				"generation":       current.Generation,
				"status":           current.Status,
				"lease_expires_at": current.LeaseExpiresAt,
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"device_id": auth.DeviceID,
		"online":    online,
		"owner":     owner,
	})
}

// ----------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------

func bearerFromAuthHeader(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}

func writeAuthError(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  code,
		"detail": detail,
	})
}

// mapAuthError translates a typed auth error into (HTTP status,
// machine-readable code). Keeps the wire response stable across handlers.
func mapAuthError(err error) (int, string) {
	switch {
	case errors.Is(err, ErrAuthMissingParams):
		return http.StatusBadRequest, "missing_params"
	case errors.Is(err, ErrAuthUnknownDevice):
		return http.StatusUnauthorized, "unknown_device"
	case errors.Is(err, ErrAuthWrongRuntimeType):
		return http.StatusForbidden, "wrong_runtime_type"
	case errors.Is(err, ErrAuthBadCredential):
		return http.StatusUnauthorized, "bad_credential"
	case errors.Is(err, ErrAuthIncompatibleVersion):
		return http.StatusUpgradeRequired, "incompatible_version"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
