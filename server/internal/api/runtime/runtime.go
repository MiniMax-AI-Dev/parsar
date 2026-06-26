// Package runtime hosts the runtime lifecycle HTTP surface: workspace
// admin pairing/lifecycle endpoints plus bearer-authenticated runtime
// pairing and heartbeat endpoints.
//
// Two auth modes:
//
//   - Admin paths /api/v1/workspaces/{wid}/runtimes[...]
//     use the workspace session middleware (auth.UserIDFromContext) +
//     per-handler workspace-role check.
//
//   - Runtime paths /api/v1/runtimes/{id}/heartbeat use bearerAuth:
//     Authorization: Bearer <runner_credential>, hashed and compared
//     against runtimes.config.runner_credential_hash.
//     /api/v1/runtimes/pair is OPEN — the daemon has no credential yet.
package runtime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Deps bundles what the runtime package needs from cmd/server. Now is
// injected so tests can pin time-sensitive flows without sleeping.
// Logger is optional; nil silently swallows internal-error logs.
type Deps struct {
	Store *store.Store
	Now   func() time.Time
}

// RegisterAdminRoutes mounts the workspace-admin tree. Caller MUST wrap
// this with a session-resolving middleware so handlers can rely on
// auth.UserIDFromContext. Per-handler RBAC checks happen inside.
func RegisterAdminRoutes(r chi.Router, deps Deps) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	h := &handler{deps: deps}
	r.Route("/api/v1/workspaces/{workspaceID}/runtimes", func(r chi.Router) {
		r.Get("/", h.listRuntimes)
		r.Post("/", h.createPairing)
		r.Route("/{runtimeID}", func(r chi.Router) {
			r.Get("/", h.getRuntime)
			r.Patch("/", h.patchRuntime)
			r.Delete("/", h.deleteRuntime)
		})
	})
}

// RegisterRunnerRoutes mounts the runtime credential tree. Pair is OPEN
// (the daemon has no credential yet); heartbeat uses bearerAuth which
// compares the presented credential against runner_credential_hash.
// Do NOT wrap with session middleware — daemons have no session cookie.
func RegisterRunnerRoutes(r chi.Router, deps Deps) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	h := &handler{deps: deps}
	r.Post("/api/v1/runtimes/pair", h.pairRuntime)
	r.Route("/api/v1/runtimes/{runtimeID}", func(r chi.Router) {
		r.Use(h.bearerAuth)
		r.Post("/heartbeat", h.runnerHeartbeat)
	})
}

// RegisterRoutes is a convenience helper for tests and callers without
// real auth middleware in front. Production cmd/server uses the two-call
// form.
func RegisterRoutes(r chi.Router, deps Deps) {
	RegisterAdminRoutes(r, deps)
	RegisterRunnerRoutes(r, deps)
}

type handler struct {
	deps Deps
}

// ---------------------------------------------------------------------
// helpers: response shape + RBAC + auth
// ---------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

// requireWorkspaceAdmin returns the caller's user id when the session
// resolves to an owner/admin. Anything else writes 401/403 to w —
// caller MUST return on false.
func (h *handler) requireWorkspaceAdmin(w http.ResponseWriter, r *http.Request, workspaceID string) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
		return "", false
	}
	role, err := h.deps.Store.GetWorkspaceMemberRole(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not_member", "not a workspace member")
		return "", false
	}
	if role != "owner" && role != "admin" {
		writeError(w, http.StatusForbidden, "insufficient_role", "owner or admin required")
		return "", false
	}
	return userID, true
}

// requireWorkspaceMember is the read-side check (any role allowed).
func (h *handler) requireWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
		return "", false
	}
	if _, err := h.deps.Store.GetWorkspaceMemberRole(r.Context(), workspaceID, userID); err != nil {
		writeError(w, http.StatusForbidden, "not_member", "not a workspace member")
		return "", false
	}
	return userID, true
}

// runtimeCtxKey carries the authenticated runtime through the runner
// middleware -> handler. Unexported type avoids cross-package collisions.
type runtimeCtxKey struct{}

// bearerAuth resolves the URL's runtime, validates the Bearer credential
// against runner_credential_hash, and stuffs the runtime into ctx.
// Fails closed on every error path.
func (h *handler) bearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runtimeID := chi.URLParam(r, "runtimeID")
		if runtimeID == "" {
			writeError(w, http.StatusBadRequest, "missing_runtime_id", "")
			return
		}
		raw := r.Header.Get("Authorization")
		if raw == "" || !strings.HasPrefix(raw, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing_bearer", "")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		if token == "" {
			writeError(w, http.StatusUnauthorized, "empty_bearer", "")
			return
		}
		rt, ok, err := h.deps.Store.GetRuntime(r.Context(), runtimeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "runtime_not_found", "")
			return
		}
		storedHash, _ := rt.Config["runner_credential_hash"].(string)
		if storedHash == "" {
			writeError(w, http.StatusUnauthorized, "no_credential_on_runtime", "")
			return
		}
		// ConstantTimeCompare prevents the SHA-256 hex equality from
		// leaking timing info. SHA-256 hex is always 64 chars so the
		// equal-length precondition holds.
		presented := store.HashRuntimeCredential(token)
		if subtle.ConstantTimeCompare([]byte(presented), []byte(storedHash)) != 1 {
			writeError(w, http.StatusUnauthorized, "bad_credential", "")
			return
		}
		ctx := context.WithValue(r.Context(), runtimeCtxKey{}, rt)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func runtimeFromContext(ctx context.Context) (store.RuntimeRead, bool) {
	rt, ok := ctx.Value(runtimeCtxKey{}).(store.RuntimeRead)
	return rt, ok
}

// ---------------------------------------------------------------------
// admin handlers
// ---------------------------------------------------------------------

type createPairingRequest struct {
	Type            string `json:"type"`
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	TokenTTLSeconds int    `json:"token_ttl_seconds"`
}

type createPairingResponse struct {
	Runtime      runtimeDTO `json:"runtime"`
	PairingToken string     `json:"pairing_token"`
}

func (h *handler) createPairing(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceAdmin(w, r, wid)
	if !ok {
		return
	}
	var body createPairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.Type == "" {
		body.Type = store.RuntimeTypeAgentDaemon
	}
	if body.Provider == "" {
		body.Provider = store.RuntimeProviderAgentDaemon
	}
	if body.Type != store.RuntimeTypeAgentDaemon {
		writeError(w, http.StatusBadRequest, "unsupported_type", "type must be agent_daemon")
		return
	}
	if body.Provider != store.RuntimeProviderAgentDaemon {
		writeError(w, http.StatusBadRequest, "unsupported_provider", "provider must be agent_daemon")
		return
	}
	ttl := time.Duration(body.TokenTTLSeconds) * time.Second
	res, err := h.deps.Store.CreateRuntimePairing(r.Context(), store.CreateRuntimePairingInput{
		WorkspaceID: wid,
		Type:        body.Type,
		Name:        body.Name,
		Provider:    body.Provider,
		OwnerUserID: userID,
		ActorID:     userID,
		TokenTTL:    ttl,
	})
	if err != nil {
		if errors.Is(err, store.ErrRuntimeNameTaken) {
			// Surface a friendly 409 instead of leaking SQLSTATE 23505.
			writeError(w, http.StatusConflict, "name_taken",
				fmt.Sprintf("名称「%s」已被其他设备使用,请换一个", strings.TrimSpace(body.Name)))
			return
		}
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, createPairingResponse{
		Runtime:      newRuntimeDTO(res.Runtime),
		PairingToken: res.PairingToken,
	})
}

func (h *handler) listRuntimes(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "workspaceID")
	if _, ok := h.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	typeFilter := r.URL.Query().Get("type")
	// Empty = all types; anything else must match the openapi.yaml enum
	// or it's a client bug — return 400 rather than silently emptying.
	if typeFilter != "" &&
		typeFilter != store.RuntimeTypeSandbox &&
		typeFilter != store.RuntimeTypeExternal &&
		typeFilter != store.RuntimeTypeAgentDaemon {
		writeError(w, http.StatusBadRequest, "bad_type_filter",
			"type must be one of agent_daemon, sandbox, external")
		return
	}
	filters, ok := parseRuntimeListFilters(w, r)
	if !ok {
		return
	}
	limit := parseLimit(r, 100, 500)
	fetchLimit := limit
	if filters.Any() {
		// Filters apply at the API layer because placement / agent_kind
		// are derived from provider+config. Fetch the allowed maximum
		// then apply the requested limit after filtering, so a burst
		// of sandbox rows cannot hide older local devices.
		fetchLimit = 500
	}
	list, err := h.deps.Store.ListRuntimes(r.Context(), wid, typeFilter, int32(fetchLimit))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	out := make([]runtimeDTO, 0, len(list))
	for _, rt := range list {
		if !runtimeMatchesListFilters(rt, filters) {
			continue
		}
		out = append(out, newRuntimeDTO(rt))
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": out})
}

// runtimeListFilters are read-side convenience filters derived in this
// API layer so the store stays a neutral runtimes table accessor.
type runtimeListFilters struct {
	Placement string
	Liveness  string
	AgentKind string
}

func (f runtimeListFilters) Any() bool {
	return f.Placement != "" || f.Liveness != "" || f.AgentKind != ""
}

func parseRuntimeListFilters(w http.ResponseWriter, r *http.Request) (runtimeListFilters, bool) {
	q := r.URL.Query()
	filters := runtimeListFilters{
		Placement: strings.TrimSpace(q.Get("placement")),
		Liveness:  strings.TrimSpace(q.Get("liveness")),
		AgentKind: strings.TrimSpace(q.Get("agent_kind")),
	}
	if filters.Placement != "" &&
		filters.Placement != "local_device" &&
		filters.Placement != "cloud_sandbox" &&
		filters.Placement != "external_agent" {
		writeError(w, http.StatusBadRequest, "bad_placement_filter",
			"placement must be one of local_device, cloud_sandbox, external_agent")
		return runtimeListFilters{}, false
	}
	if filters.Liveness != "" &&
		filters.Liveness != store.RuntimeLivenessPendingPairing &&
		filters.Liveness != store.RuntimeLivenessOffline &&
		filters.Liveness != store.RuntimeLivenessOnline &&
		filters.Liveness != store.RuntimeLivenessError {
		writeError(w, http.StatusBadRequest, "bad_liveness_filter",
			"liveness must be one of pending_pairing, offline, online, error")
		return runtimeListFilters{}, false
	}
	return filters, true
}

func runtimeMatchesListFilters(rt store.RuntimeRead, filters runtimeListFilters) bool {
	if filters.Placement != "" && runtimePlacement(rt) != filters.Placement {
		return false
	}
	if filters.Liveness != "" && rt.Liveness != filters.Liveness {
		return false
	}
	if filters.AgentKind != "" && !runtimeSupportsAgentKind(rt, filters.AgentKind) {
		return false
	}
	return true
}

func runtimePlacement(rt store.RuntimeRead) string {
	switch rt.Type {
	case store.RuntimeTypeAgentDaemon:
		if isSandboxDaemonRuntime(rt) {
			return "cloud_sandbox"
		}
		return "local_device"
	case store.RuntimeTypeSandbox:
		return "cloud_sandbox"
	case store.RuntimeTypeExternal:
		return "external_agent"
	default:
		return ""
	}
}

func isSandboxDaemonRuntime(rt store.RuntimeRead) bool {
	if rt.Type != store.RuntimeTypeAgentDaemon {
		return false
	}
	cfg := rt.Config
	return rt.Provider == store.RuntimeProviderAgentDaemonSandbox ||
		configString(cfg, "created_by") == "sandbox_provider" ||
		configString(cfg, "daemon_mode") == "sandbox" ||
		configString(cfg, "sandbox_kind") != "" ||
		configString(cfg, "parsar.sandbox_kind") != "" ||
		configString(cfg, "sandbox_id") != "" ||
		configString(cfg, "parsar.sandbox_id") != ""
}

func runtimeSupportsAgentKind(rt store.RuntimeRead, agentKind string) bool {
	kind := strings.TrimSpace(agentKind)
	if kind == "" {
		return true
	}
	if rt.Type != store.RuntimeTypeAgentDaemon {
		return false
	}
	if !capabilitySnapshotPresent(rt.Config) {
		return kind == "claude_code"
	}
	for _, supported := range supportedAgentKindNames(rt.Config) {
		if supported == kind {
			return true
		}
	}
	return false
}

func capabilitySnapshotPresent(cfg map[string]any) bool {
	return configArrayPresent(cfg, "supported_agent_kinds") || configArrayPresent(cfg, "supported_agent_kind_names")
}

func supportedAgentKindNames(cfg map[string]any) []string {
	if names := configStringList(cfg["supported_agent_kind_names"]); len(names) > 0 {
		return names
	}
	return supportedAgentKindNamesFromKinds(cfg["supported_agent_kinds"])
}

func supportedAgentKindNamesFromKinds(raw any) []string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := m["kind"].(string)
		kind = strings.TrimSpace(kind)
		if kind == "" || m["available"] != true {
			continue
		}
		names = append(names, kind)
	}
	return names
}

func configArrayPresent(cfg map[string]any, key string) bool {
	if cfg == nil {
		return false
	}
	switch cfg[key].(type) {
	case []any, []string:
		return true
	default:
		return false
	}
}

func configString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	if v, ok := cfg[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func configStringList(raw any) []string {
	switch items := raw.(type) {
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(item) != "" {
				out = append(out, strings.TrimSpace(item))
			}
		}
		return out
	default:
		return nil
	}
}

func (h *handler) getRuntime(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "workspaceID")
	if _, ok := h.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	id := chi.URLParam(r, "runtimeID")
	rt, ok, err := h.deps.Store.GetRuntime(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !ok || rt.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, newRuntimeDTO(rt))
}

type patchRequest struct {
	Name string `json:"name"`
}

func (h *handler) patchRuntime(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceAdmin(w, r, wid)
	if !ok {
		return
	}
	id := chi.URLParam(r, "runtimeID")
	existing, ok, _ := h.deps.Store.GetRuntime(r.Context(), id)
	if !ok || existing.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	var body patchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	rt, err := h.deps.Store.PatchRuntime(r.Context(), store.PatchRuntimeInput{
		ID:      id,
		NewName: body.Name,
		ActorID: userID,
	})
	if err != nil {
		if errors.Is(err, store.ErrRuntimeNameTaken) {
			writeError(w, http.StatusConflict, "name_taken",
				fmt.Sprintf("名称「%s」已被其他设备使用,请换一个", strings.TrimSpace(body.Name)))
			return
		}
		writeError(w, http.StatusBadRequest, "patch_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newRuntimeDTO(rt))
}

func (h *handler) deleteRuntime(w http.ResponseWriter, r *http.Request) {
	wid := chi.URLParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceAdmin(w, r, wid)
	if !ok {
		return
	}
	id := chi.URLParam(r, "runtimeID")
	existing, ok, _ := h.deps.Store.GetRuntime(r.Context(), id)
	if !ok || existing.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := h.deps.Store.SoftDeleteRuntimeWithActor(r.Context(), id, userID); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// runner handlers
// ---------------------------------------------------------------------

type pairRequest struct {
	PairingToken    string `json:"pairing_token"`
	Hostname        string `json:"hostname"`
	Version         string `json:"version"`
	RunnerPublicKey string `json:"runner_public_key"`
}

type pairResponse struct {
	Runtime          runtimeDTO `json:"runtime"`
	RunnerCredential string     `json:"runner_credential"`
}

func (h *handler) pairRuntime(w http.ResponseWriter, r *http.Request) {
	var body pairRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	// Consume the token first; only mint the long-lived credential once
	// the runtime row is promoted. ConsumePairingToken also installs
	// runner_public_key into config.
	rt, err := h.deps.Store.ConsumePairingToken(r.Context(), store.ConsumePairingTokenInput{
		Token:           body.PairingToken,
		Hostname:        body.Hostname,
		Version:         body.Version,
		RunnerPublicKey: body.RunnerPublicKey,
	})
	if err != nil {
		if errors.Is(err, store.ErrPairingTokenInvalid) {
			writeError(w, http.StatusUnauthorized, "pair_invalid", "")
			return
		}
		writeError(w, http.StatusBadRequest, "pair_failed", err.Error())
		return
	}
	// Mint runner_credential, persist hash next to runner_public_key.
	credential, hash, err := store.MintRuntimeCredential()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "mint_failed", err.Error())
		return
	}
	// Don't clobber runner_public_key already in config.
	cfg := rt.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["runner_credential_hash"] = hash
	if err := h.deps.Store.SetRuntimeRunnerCredentialHash(r.Context(), rt.ID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, "credential_persist_failed", err.Error())
		return
	}
	rt.Config["runner_credential_hash"] = hash
	writeJSON(w, http.StatusOK, pairResponse{
		Runtime:          newRuntimeDTO(rt),
		RunnerCredential: credential,
	})
}

type heartbeatResponse struct {
	Liveness string `json:"liveness"`
}

func (h *handler) runnerHeartbeat(w http.ResponseWriter, r *http.Request) {
	rt, _ := runtimeFromContext(r.Context())
	hb, err := h.deps.Store.TouchRuntimeHeartbeat(r.Context(), rt.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, heartbeatResponse{Liveness: hb.Liveness})
}

// ---------------------------------------------------------------------
// DTOs — wire shape mirrors components.schemas.Runtime in openapi.yaml.
// ---------------------------------------------------------------------

type runtimeDTO struct {
	ID                    string         `json:"id"`
	WorkspaceID           string         `json:"workspace_id"`
	Type                  string         `json:"type"`
	Name                  string         `json:"name"`
	Liveness              string         `json:"liveness"`
	Provider              string         `json:"provider"`
	OwnerUserID           *string        `json:"owner_user_id,omitempty"`
	Hostname              string         `json:"hostname"`
	Version               string         `json:"version"`
	LastHeartbeatAt       *time.Time     `json:"last_heartbeat_at,omitempty"`
	PairingTokenExpiresAt *time.Time     `json:"pairing_token_expires_at,omitempty"`
	Config                map[string]any `json:"config"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

func newRuntimeDTO(r store.RuntimeRead) runtimeDTO {
	// Strip the credential hash from the wire view; runner_public_key
	// stays so the admin UI can show + verify it.
	cfg := map[string]any{}
	for k, v := range r.Config {
		if k == "runner_credential_hash" {
			continue
		}
		cfg[k] = v
	}
	return runtimeDTO{
		ID:                    r.ID,
		WorkspaceID:           r.WorkspaceID,
		Type:                  r.Type,
		Name:                  r.Name,
		Liveness:              r.Liveness,
		Provider:              r.Provider,
		OwnerUserID:           r.OwnerUserID,
		Hostname:              r.Hostname,
		Version:               r.Version,
		LastHeartbeatAt:       r.LastHeartbeatAt,
		PairingTokenExpiresAt: r.PairingTokenExpiresAt,
		Config:                cfg,
		CreatedAt:             r.CreatedAt,
		UpdatedAt:             r.UpdatedAt,
	}
}
func parseLimit(r *http.Request, def, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
