package dev

import (
	"context"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type skillUploadStore interface {
	RuntimeStore
	GetAgentRunInvocation(context.Context, string) (store.AgentRunInvocation, error)
}

func RegisterSkillUploadRoute(r chi.Router, runtimeStore skillUploadStore, signer *auth.SkillUploadSigner) {
	if runtimeStore != nil && signer != nil {
		r.Post("/api/v1/agent-authoring/skill-bundles", uploadSkillBundle(runtimeStore, signer))
	}
}

// @Summary Upload a Skill bundle from a running Agent
// @Description Accepts a run-scoped upload bearer credential. The running task's requesting user must currently be an owner/admin. Creates a workspace-only bundle of inline Skills; no server/client code or credential references.
// @Tags capabilities
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer run-scoped Skill upload credential"
// @Param body body installPluginBody true "Inline Skill bundle"
// @Success 201 {object} map[string]interface{} "Created capability and version"
// @Failure 400 {object} map[string]string "Invalid or unsupported bundle"
// @Failure 401 {object} map[string]string "Invalid or expired credential"
// @Failure 403 {object} map[string]string "Run inactive or requester is not owner/admin"
// @Failure 404 {object} map[string]string "Requester no longer belongs to the workspace"
// @Failure 500 {object} map[string]string "Permission or storage read failed"
// @Failure 409 {object} map[string]string "Capability name already exists"
// @Router /api/v1/agent-authoring/skill-bundles [post]
func uploadSkillBundle(runtimeStore skillUploadStore, signer *auth.SkillUploadSigner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := strings.Fields(r.Header.Get("Authorization"))
		if len(header) != 2 || !strings.EqualFold(header[0], "Bearer") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "skill upload credential required"})
			return
		}
		runID, err := signer.Verify(header[1])
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired skill upload credential"})
			return
		}
		run, err := runtimeStore.GetAgentRunInvocation(r.Context(), runID)
		if err != nil || run.Status != "running" || run.RequestedByType != "user" || run.RequestedByID == "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "skill upload requires an active user-requested run"})
			return
		}
		r = r.WithContext(auth.WithUserID(r.Context(), run.RequestedByID))
		if err := auth.RequireWorkspaceRole(r.Context(), runtimeStore, run.WorkspaceID, "owner", "admin"); err != nil {
			writeRBACError(w, err)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		installPluginForActor(w, r, runtimeStore, run.WorkspaceID, run.RequestedByID, true)
	}
}
