package dev

// Runtime status endpoint backs the web RuntimeStatusBanner. Runtime mode
// is per-Agent (`agents.config.runtime`); response carries
// `sandbox_agent_count` plus workspace-scoped `has_credential` /
// `credential_masked`. `available` means the sandbox provider is reachable
// for the active deployment profile.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// SandboxLivenessProber tests sandbox provider liveness. Implementations
// MUST honour ctx cancellation — the handler enforces a 1s budget so a
// misbehaving provider can't stall the StatusBanner poll loop.
type SandboxLivenessProber interface {
	Ping(ctx context.Context) error
}

type RuntimeStatusDeps struct {
	SettingsStore   RuntimeSettingsStore
	SandboxProber   SandboxLivenessProber
	Profile         string
	ConfiguredByOps bool
	SandboxImage    string
	PingTimeout     time.Duration
}

type RuntimeSettingsStore interface {
	GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (store.WorkspaceRuntimeSettingsRead, error)
}

const defaultRuntimePingTimeout = 1 * time.Second

type runtimeStatusResponse struct {
	// HasCredential is true when the workspace has a registered E2B
	// credential. Without this, sandbox-mode agents will be blocked
	// at first prompt.
	HasCredential bool `json:"has_credential"`

	// CredentialMasked is the redacted form (e.g. "e2b_•••••...xyz")
	// for surfacing in the UI without exposing the secret.
	CredentialMasked *string `json:"credential_masked"`

	// Available reports whether the sandbox provider is reachable.
	// Only meaningful after credential registration in oss/selfhost;
	// in managed profile no workspace credential is expected.
	Available bool `json:"available"`

	// SandboxAgentCount is the number of agents in this workspace
	// whose declared runtime is 'sandbox'.
	SandboxAgentCount int64 `json:"sandbox_agent_count"`

	// Profile is the runtime deployment profile. "managed" means the
	// deployment operator provides cloud sandbox credentials and
	// workspaces don't need to register an E2B key.
	Profile string `json:"profile"`

	// ConfiguredBy is "ops" when PARSAR_OPENCODE_RUNNER was set at
	// server boot. Informational only — admin UI badge.
	ConfiguredBy string `json:"configured_by,omitempty"`

	// SandboxImage is the operator-configured container image used for
	// docker-backed sandbox agents. Empty for non-docker providers.
	SandboxImage string `json:"sandbox_image,omitempty"`
}

// runtimeStatus returns the workspace runtime status. 503 when no
// SettingsStore is wired so the StatusBanner can render an explicit
// "not configured" rather than guess.
//
//	@Summary		Get workspace runtime status
//	@Description	Backs the RuntimeStatusBanner. Reports whether the workspace has a runtime credential registered, whether the sandbox provider is reachable, how many agents are configured for sandbox runtime, and the deployment profile (oss/selfhost/managed).
//	@Tags			runtimes
//	@ID				getDevWorkspaceRuntimeStatus
//	@Produce		json
//	@Param			workspaceID	path		string					true	"Workspace UUID"
//	@Success		200			{object}	runtimeStatusResponse	"Runtime status snapshot"
//	@Failure		403			{object}	map[string]string		"Caller is not a workspace member"
//	@Failure		404			{object}	map[string]string		"Workspace not found"
//	@Failure		500			{object}	map[string]string		"Runtime settings unavailable"
//	@Failure		503			{object}	map[string]string		"Runtime status not wired"
//	@Router			/api/v1/workspaces/{workspaceID}/runtime/status [get]
func runtimeStatus(deps RuntimeStatusDeps) http.HandlerFunc {
	timeout := deps.PingTimeout
	if timeout <= 0 {
		timeout = defaultRuntimePingTimeout
	}
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if deps.SettingsStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "runtime status not wired",
			})
			return
		}
		settings, err := deps.SettingsStore.GetWorkspaceRuntimeSettings(r.Context(), workspaceID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspace) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "runtime settings unavailable"})
			return
		}

		profile := normalizeRuntimeProfile(deps.Profile)
		hasCredential := strings.TrimSpace(settings.RuntimeCredentialSecretID) != ""
		// oss/selfhost needs a credential before probing; managed is
		// platform-configured so the prober alone determines readiness.
		available := false
		if hasCredential || profile == "managed" {
			available = computeSandboxReachable(r.Context(), deps.SandboxProber, timeout)
		}

		resp := runtimeStatusResponse{
			HasCredential:     hasCredential,
			Available:         available,
			SandboxAgentCount: settings.SandboxAgentCount,
			Profile:           profile,
			SandboxImage:      strings.TrimSpace(deps.SandboxImage),
		}
		if masked := strings.TrimSpace(settings.RuntimeCredentialMasked); masked != "" {
			resp.CredentialMasked = &masked
		}
		if deps.ConfiguredByOps {
			resp.ConfiguredBy = "ops"
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func normalizeRuntimeProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "managed":
		return "managed"
	case "selfhost":
		return "selfhost"
	default:
		return "oss"
	}
}

// computeSandboxReachable is the sandbox-prober adapter. nil prober
// returns false so the banner shows a red light, not a fake green.
// The timeout is enforced on a derived context so r.Context() stays clean.
func computeSandboxReachable(ctx context.Context, prober SandboxLivenessProber, timeout time.Duration) bool {
	if prober == nil {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := prober.Ping(pingCtx); err != nil {
		return false
	}
	return true
}
