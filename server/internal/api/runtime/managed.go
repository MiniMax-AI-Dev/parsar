package runtime

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

const managedRuntimeName = "Default Docker Runtime"

type managedEnrollResponse struct {
	ServerURL    string `json:"server_url"`
	PairingToken string `json:"pairing_token"`
}

func RegisterManagedRoutes(r chi.Router, deps Deps) {
	h := &handler{deps: deps}
	r.Post("/internal/managed-daemon/enroll", h.enrollManagedRuntime)
}

// enrollManagedRuntime creates a fresh pairing for the built-in Docker runtime.
//
//	@Summary		Enroll managed local daemon
//	@Description	Loopback-only endpoint used by the all-in-one container after first-owner bootstrap. Replaces the fixed Default Docker Runtime and returns a one-shot pairing token.
//	@Tags			internal
//	@ID			enrollManagedLocalDaemon
//	@Produce		json
//	@Success		201	{object}	managedEnrollResponse
//	@Failure		403	{object}	map[string]string	"Request is not loopback"
//	@Failure		409	{object}	map[string]string	"First workspace has not been created"
//	@Failure		500	{object}	map[string]string	"Managed runtime enrollment failed"
//	@Router			/internal/managed-daemon/enroll [post]
func (h *handler) enrollManagedRuntime(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "loopback_required", "managed daemon enrollment is loopback-only")
		return
	}
	owner, ok, err := h.deps.Store.GetFirstWorkspaceOwner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "workspace_lookup_failed", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "bootstrap_pending", "first workspace has not been created")
		return
	}
	runtimes, err := h.deps.Store.ListRuntimes(r.Context(), owner.WorkspaceID, store.RuntimeTypeAgentDaemon, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "runtime_list_failed", err.Error())
		return
	}
	for _, runtime := range runtimes {
		if runtime.Name != managedRuntimeName {
			continue
		}
		if err := h.deps.Store.SoftDeleteRuntime(r.Context(), runtime.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "runtime_reset_failed", err.Error())
			return
		}
	}
	created, err := h.deps.Store.CreateRuntimePairing(r.Context(), store.CreateRuntimePairingInput{
		WorkspaceID: owner.WorkspaceID,
		Type:        store.RuntimeTypeAgentDaemon,
		Name:        managedRuntimeName,
		Provider:    store.RuntimeProviderAgentDaemon,
		OwnerUserID: owner.OwnerUserID,
		ActorID:     owner.OwnerUserID,
		Config: map[string]any{
			"created_by": "managed_local",
			"placement":  "docker",
		},
	})
	if err != nil {
		if errors.Is(err, store.ErrRuntimeNameTaken) {
			writeError(w, http.StatusConflict, "runtime_name_taken", "managed runtime name is already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, "runtime_create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, managedEnrollResponse{
		ServerURL:    "http://127.0.0.1:8080",
		PairingToken: created.PairingToken,
	})
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
