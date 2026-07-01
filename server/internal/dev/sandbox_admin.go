package dev

// Admin sandbox lifecycle endpoints. All admin queries go through the DB;
// the in-memory provider is touched only on Release (kill + evict cache)
// and Acquire (rebuild).

import (
	"context"
	"encoding/json"
	"errors"

	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// SandboxBindingStore is the data-layer dependency for admin sandbox
// lifecycle handlers. GetRuntime is needed by the connectivity check —
// agent_daemon sandboxes communicate over a reverse WS, so liveness is
// probed via the runtime row instead of an HTTP endpoint.
type SandboxBindingStore interface {
	GetActiveSandboxBindingForAgent(ctx context.Context, workspaceID, agentID string) (store.SandboxBindingRead, bool, error)
	MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error
	ListActiveSandboxBindings(ctx context.Context, workspaceID string, limit int32) ([]store.SandboxBindingRead, error)
	GetRuntime(ctx context.Context, runtimeID string) (store.RuntimeRead, bool, error)
	// GetAgentDetail is used by rebuildSandbox to load the
	// agent's config (sandbox_size etc.) so the re-Acquire goes
	// through the same template-resolution path a real prompt would.
	GetAgentDetail(ctx context.Context, agentID string) (store.AgentStatusRead, error)
}

// sandboxAdminDeps bundles the collaborators for admin handlers.
// daemonMgr is required for kill/rebuild (Release / Acquire).
type sandboxAdminDeps struct {
	store     SandboxBindingStore
	daemonMgr AgentDaemonSandboxManager
}

// sandboxStatusResponse is the GET response shape.
type sandboxStatusResponse struct {
	BindingID      string     `json:"binding_id"`
	WorkspaceID    string     `json:"workspace_id"`
	AgentID        *string    `json:"agent_id"`
	Name           *string    `json:"name,omitempty"`
	SandboxID      string     `json:"sandbox_id"`
	TemplateID     string     `json:"template_id"`
	Status         string     `json:"status"`
	StatusKind     string     `json:"status_kind"`
	CreatedAt      time.Time  `json:"created_at"`
	LastActiveAt   time.Time  `json:"last_active_at"`
	KilledAt       *time.Time `json:"killed_at,omitempty"`
	// ExpiresAt is fetched live from the e2b control plane. Nil when
	// the binding isn't in this pod's cache or the lookup failed.
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
	Metadata  map[string]any `json:"metadata"`
}

// listSandboxesResponse wraps the list of active bindings.
type listSandboxesResponse struct {
	Sandboxes []sandboxStatusResponse `json:"sandboxes"`
}

func listSandboxes(deps sandboxAdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox lifecycle store not wired"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a UUID"})
			return
		}
		var limit int32
		if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
			if n, err := parseInt32(raw); err == nil && n > 0 {
				limit = n
			}
		}
		bindings, err := deps.store.ListActiveSandboxBindings(r.Context(), workspaceID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed: " + err.Error()})
			return
		}
		out := make([]sandboxStatusResponse, 0, len(bindings))
		for _, b := range bindings {
			out = append(out, toSandboxStatusResponse(b))
		}
		writeJSON(w, http.StatusOK, listSandboxesResponse{Sandboxes: out})
	}
}

// parseInt32 is a tiny strconv.Atoi wrapper bounded to int32.
func parseInt32(s string) (int32, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n < 0 || n > (1<<31)-1 {
		return 0, errors.New("out of int32 range")
	}
	return int32(n), nil
}

// getSandboxStatus returns the current binding for the (workspace,
// agent) tuple. Returns 200 + JSON `null` when no active binding
// exists — the frontend treats `null` as the empty state. Operation
// endpoints (kill/rebuild/test-connection) still 404 in this case.
func getSandboxStatus(deps sandboxAdminDeps, daemonMgr AgentDaemonSandboxManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox lifecycle store not wired"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and agent_id must be UUIDs"})
			return
		}
		binding, found, err := deps.store.GetActiveSandboxBindingForAgent(r.Context(), workspaceID, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed: " + err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		resp := toSandboxStatusResponse(binding)
		// Fold in live e2b TTL. Cache hit on the cold-start pod;
		// cache miss falls back to a direct e2b lookup by sandboxID
		// so any pod can answer. Failure leaves expires_at nil.
		if daemonMgr != nil && binding.SandboxID != "" {
			var expiresAt time.Time
			if binding.AgentID != nil {
				if info, ok, err := daemonMgr.SandboxStatus(r.Context(), *binding.AgentID); err == nil && ok {
					expiresAt = info.ExpiresAt
				}
			}
			if expiresAt.IsZero() {
				if liveExpiresAt, err := daemonMgr.SandboxRuntimeInfo(r.Context(), binding.SandboxID); err == nil {
					expiresAt = liveExpiresAt
				}
			}
			if !expiresAt.IsZero() {
				expiresAtCopy := expiresAt
				resp.ExpiresAt = &expiresAtCopy
			}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// killSandbox tears down the agent's sandbox: Release evicts cache + kills
// E2B + marks DB, then clears agents.runtime_id so dispatch stops
// routing to the dead device.
func killSandbox(deps sandboxAdminDeps, runtimeStore RuntimeStore, daemonMgr AgentDaemonSandboxManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		performLifecycleAction(w, r, deps, runtimeStore, daemonMgr, "killed")
	}
}

// rebuildSandbox kills the current sandbox and re-provisions a new one.
func rebuildSandbox(deps sandboxAdminDeps, runtimeStore RuntimeStore, daemonMgr AgentDaemonSandboxManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox lifecycle store not wired"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and agent_id must be UUIDs"})
			return
		}
		binding, found, err := deps.store.GetActiveSandboxBindingForAgent(r.Context(), workspaceID, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed: " + err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error":            "no active sandbox binding to rebuild",
				"workspace_id":     workspaceID,
				"agent_id": agentID,
			})
			return
		}

		if daemonMgr != nil {
			if releaseErr := daemonMgr.Release(r.Context(), agentID); releaseErr != nil {
				log.Bg().Warn("sandbox rebuild: release failed",
					"agent_id", agentID, "err", releaseErr)
			}
		}
		// Safety net for the case where Release didn't (or couldn't)
		// mark the DB row.
		if binding.KilledAt == nil {
			_ = deps.store.MarkSandboxBindingKilled(r.Context(), binding.ID, "killed")
		}

		// On successful re-Acquire, persist the new deviceID to
		// agents.runtime_id so dispatch picks up the new sandbox.
		if daemonMgr != nil {
			// Load the agent's current config snapshot BEFORE the goroutine
			// because the goroutine builds its own context — we want the
			// caller's request context for the lookup so RBAC / cancellation
			// still apply. The config is what feeds resolveTemplate inside
			// E2BSandboxProvider.Acquire (sandbox_size=xl etc.); without it
			// the new sandbox would silently fall back to the standard
			// template even when the agent is configured for XL.
			var agentConfig map[string]any
			if detail, detailErr := deps.store.GetAgentDetail(r.Context(), agentID); detailErr == nil {
				agentConfig = detail.Config
			} else {
				log.Bg().Warn("sandbox rebuild: load agent config failed; re-acquire will use default template",
					"agent_id", agentID, "err", detailErr)
			}

			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				deviceID, acquireErr := daemonMgr.Acquire(ctx, connector.PromptInput{
					AgentID:     agentID,
					WorkspaceID: workspaceID,
					AgentConfig: agentConfig,
				})
				if acquireErr != nil {
					log.Bg().Warn("sandbox rebuild re-acquire failed",
						"agent_id", agentID, "err", acquireErr)
					return
				}
				if runtimeStore != nil {
					if _, bindErr := runtimeStore.SetAgentRuntime(ctx, store.SetAgentRuntimeInput{
						WorkspaceID:    workspaceID,
						AgentID: agentID,
						RuntimeID:      deviceID,
					}); bindErr != nil {
						log.Bg().Error("sandbox rebuild: re-acquired but runtime_id persist failed",
							"agent_id", agentID,
							"device_id", deviceID,
							"err", bindErr)
						return
					}
				}
				log.Bg().Info("sandbox rebuild re-acquire succeeded",
					"agent_id", agentID, "device_id", deviceID)
			}()
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"binding_id": binding.ID,
			"sandbox_id": binding.SandboxID,
			"action":     "rebuilt",
			"message":    "sandbox killed and re-provisioning in background",
		})
	}
}

// renewSandbox bumps the e2b TTL on the live sandbox. 503 if daemonMgr
// isn't wired. 409 when this pod's cache doesn't own the binding (sibling
// pod cold-started it).
func renewSandbox(deps sandboxAdminDeps, daemonMgr AgentDaemonSandboxManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox lifecycle store not wired"})
			return
		}
		if daemonMgr == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent_daemon sandbox manager not wired"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and agent_id must be UUIDs"})
			return
		}
		binding, found, err := deps.store.GetActiveSandboxBindingForAgent(r.Context(), workspaceID, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed: " + err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error":            "no active sandbox binding to renew",
				"workspace_id":     workspaceID,
				"agent_id": agentID,
			})
			return
		}
		expiresAt, ok, renewErr := daemonMgr.Renew(r.Context(), agentID)
		if renewErr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error":      "renew failed: " + renewErr.Error(),
				"binding_id": binding.ID,
				"sandbox_id": binding.SandboxID,
			})
			return
		}
		if !ok {
			// This pod's cache doesn't own the binding; UI poll will
			// pick up the new owner within ~15s.
			writeJSON(w, http.StatusConflict, map[string]string{
				"error":            "sandbox not owned by this pod; refresh and retry",
				"workspace_id":     workspaceID,
				"agent_id": agentID,
			})
			return
		}
		resp := map[string]any{
			"binding_id": binding.ID,
			"sandbox_id": binding.SandboxID,
			"action":     "renewed",
			"message":    "sandbox TTL extended",
		}
		if !expiresAt.IsZero() {
			resp["expires_at"] = expiresAt
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// acquireSandbox provisions a sandbox for a agent that
// currently has no active binding. Returns 202 immediately;
// the front-end polls to pick up the result.
func acquireSandbox(deps sandboxAdminDeps, runtimeStore RuntimeStore, provider AgentDaemonSandboxAcquirer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "agent_daemon sandbox provider not wired",
			})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "workspace_id and agent_id must be UUIDs",
			})
			return
		}
		if deps.store != nil {
			if _, found, _ := deps.store.GetActiveSandboxBindingForAgent(
				r.Context(), workspaceID, agentID); found {
				writeJSON(w, http.StatusOK, map[string]string{
					"status":           "already_bound",
					"agent_id": agentID,
				})
				return
			}
		}
		// Fire-and-forget: return 202 immediately. On success, persist
		// the new deviceID to agents.runtime_id.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			deviceID, err := provider.Acquire(ctx, connector.PromptInput{
				AgentID:     agentID,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				log.Bg().Warn("sandbox acquire (manual) failed",
					"agent_id", agentID, "err", err)
				return
			}
			if runtimeStore != nil {
				if _, bindErr := runtimeStore.SetAgentRuntime(ctx, store.SetAgentRuntimeInput{
					WorkspaceID:    workspaceID,
					AgentID: agentID,
					RuntimeID:      deviceID,
				}); bindErr != nil {
					log.Bg().Error("sandbox acquire (manual) succeeded but runtime_id persist failed",
						"agent_id", agentID,
						"device_id", deviceID,
						"err", bindErr)
					return
				}
			}
			log.Bg().Info("sandbox acquire (manual) succeeded",
				"agent_id", agentID, "device_id", deviceID)
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{
			"status":           "provisioning",
			"agent_id": agentID,
		})
	}
}

// performLifecycleAction is the shared kill body. Release evicts cache +
// kills E2B + marks DB; we mark the DB row as a safety net, then clear
// agents.runtime_id so dispatch stops routing to the dead device.
func performLifecycleAction(w http.ResponseWriter, r *http.Request, deps sandboxAdminDeps, runtimeStore RuntimeStore, daemonMgr AgentDaemonSandboxManager, terminalStatus string) {
	if deps.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "sandbox lifecycle store not wired"})
		return
	}
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
	agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
	if !isUUID(workspaceID) || !isUUID(agentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and agent_id must be UUIDs"})
		return
	}
	binding, found, err := deps.store.GetActiveSandboxBindingForAgent(r.Context(), workspaceID, agentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed: " + err.Error()})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error":            "no active sandbox binding to act on",
			"workspace_id":     workspaceID,
			"agent_id": agentID,
		})
		return
	}

	if daemonMgr != nil {
		if releaseErr := daemonMgr.Release(r.Context(), agentID); releaseErr != nil {
			log.Bg().Warn("sandbox kill: release failed (continuing to mark DB)",
				"agent_id", agentID, "err", releaseErr)
		}
	}
	// Safety net for the case where Release didn't (or couldn't) mark
	// the DB row.
	if binding.KilledAt == nil {
		if dbErr := deps.store.MarkSandboxBindingKilled(r.Context(), binding.ID, terminalStatus); dbErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error":      "DB mark killed failed",
				"db_error":   dbErr.Error(),
				"binding_id": binding.ID,
			})
			return
		}
	}
	// Best-effort: clear agents.runtime_id so dispatch stops
	// handing out the dead device. A failure here only degrades the
	// next dispatch experience — never block the kill response on it.
	if runtimeStore != nil {
		if _, clearErr := runtimeStore.SetAgentRuntime(r.Context(), store.SetAgentRuntimeInput{
			WorkspaceID:    workspaceID,
			AgentID: agentID,
			RuntimeID:      "",
		}); clearErr != nil {
			log.Bg().Warn("sandbox kill: clear agent runtime_id failed (next dispatch may target dead device)",
				"agent_id", agentID, "err", clearErr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"binding_id": binding.ID,
		"sandbox_id": binding.SandboxID,
		"action":     terminalStatus,
		"message":    "sandbox killed; next prompt will re-spawn a fresh one",
	})
}

// toSandboxStatusResponse maps the store read into the admin JSON shape.
func toSandboxStatusResponse(b store.SandboxBindingRead) sandboxStatusResponse {
	kind := "live"
	switch b.Status {
	case store.SandboxBindingStatusSpawning, store.SandboxBindingStatusKilling:
		kind = "transient"
	case store.SandboxBindingStatusKilled,
		store.SandboxBindingStatusKilledOrphaned,
		store.SandboxBindingStatusKilledError:
		kind = "terminal"
	}
	return sandboxStatusResponse{
		BindingID:   b.ID,
		WorkspaceID: b.WorkspaceID,
		AgentID:     b.AgentID,
		Name:        b.Name,
		SandboxID:      b.SandboxID,
		TemplateID:     b.TemplateID,
		Status:         b.Status,
		StatusKind:     kind,
		CreatedAt:      b.CreatedAt,
		LastActiveAt:   b.LastActiveAt,
		KilledAt:       b.KilledAt,
		Metadata:       b.Metadata,
	}
}

// Compile-assert response shape stays JSON-encodable.
var _ = json.Marshal
var _ = errors.New
