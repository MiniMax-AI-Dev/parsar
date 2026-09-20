package dev

import (
	"encoding/json"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/coreaccess"
	"github.com/go-chi/chi/v5"
	"github.com/openai/openai-go/v3"
)

type coreResolver = coreaccess.Resolver

func WithCoreAccess(resolve coreaccess.Resolver) RouterOption {
	return func(cfg *routerConfig) { cfg.coreAccess = resolve }
}

func registerCoreEnvironmentRoutes(r chi.Router, st RuntimeStore, cfg *routerConfig) {
	const path = "/workspaces/{workspaceID}/core/environments/templates"
	r.Get(path, coreEnvironmentTemplates(st, cfg))
	r.Post(path, coreEnvironmentTemplates(st, cfg))
	r.Get(path+"/{templateID}", coreEnvironmentTemplates(st, cfg))
	r.Post(path+"/{templateID}", coreEnvironmentTemplates(st, cfg))
	r.Delete(path+"/{templateID}", coreEnvironmentTemplates(st, cfg))
}

// coreEnvironmentTemplates uses the workspace's independent Core project.
// @Summary Manage Core environment templates
// @Description Uses the pinned Agents API template schema and workspace-scoped Core credentials. Secrets and setup command bodies are never cached by the product. Mutations require workspace administration.
// @Tags core-environments
// @Accept json
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param templateID path string false "Core template ID"
// @Param after query string false "Core pagination cursor"
// @Param body body map[string]interface{} false "Official environment template create or update parameters"
// @Success 200 {object} map[string]interface{} "Core template, page, or deletion receipt"
// @Success 201 {object} map[string]interface{} "Created Core template"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 403 {object} map[string]string "Workspace access denied"
// @Failure 404 {object} map[string]string "Template not found in this workspace's Core project"
// @Failure 503 {object} map[string]string "Core operation unavailable"
// @Router /api/v1/workspaces/{workspaceID}/core/environments/templates [get]
// @Router /api/v1/workspaces/{workspaceID}/core/environments/templates [post]
// @Router /api/v1/workspaces/{workspaceID}/core/environments/templates/{templateID} [get]
// @Router /api/v1/workspaces/{workspaceID}/core/environments/templates/{templateID} [post]
// @Router /api/v1/workspaces/{workspaceID}/core/environments/templates/{templateID} [delete]
func coreEnvironmentTemplates(st RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := chi.URLParam(r, "workspaceID")
		if !isUUID(workspaceID) {
			writeJSON(w, 400, map[string]string{"error": "invalid workspace_id"})
			return
		}
		if st == nil {
			writeReadError(w, coreaccess.ErrNotConfigured, "")
			return
		}
		var err error
		if r.Method == http.MethodGet {
			err = requireWorkspaceMember(r, st, workspaceID)
		} else {
			err = requireWorkspaceOwnerOrAdmin(r, st, workspaceID)
		}
		if err != nil {
			writeRBACError(w, err)
			return
		}
		if cfg.coreAccess == nil {
			writeReadError(w, coreaccess.ErrNotConfigured, "")
			return
		}
		client, err := cfg.coreAccess(workspaceID)
		if err != nil {
			writeReadError(w, err, "Core is unavailable")
			return
		}
		svc := client.Environments.Templates
		id := chi.URLParam(r, "templateID")
		var result any
		status := http.StatusOK
		switch {
		case r.Method == http.MethodGet && id == "":
			params := openai.BetaAgentEnvironmentTemplateListParams{Limit: openai.Int(100)}
			if after := r.URL.Query().Get("after"); after != "" {
				params.After = openai.String(after)
			}
			result, err = svc.List(r.Context(), params)
		case r.Method == http.MethodGet:
			result, err = svc.Get(r.Context(), id)
		case r.Method == http.MethodDelete:
			result, err = svc.Delete(r.Context(), id)
		case r.Method == http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			var fields map[string]any
			if decodeErr := json.NewDecoder(r.Body).Decode(&fields); decodeErr != nil || fields == nil {
				writeJSON(w, 400, map[string]string{"error": "invalid template payload"})
				return
			}
			raw, marshalErr := json.Marshal(fields)
			if marshalErr != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid template payload"})
				return
			}
			if id == "" {
				var body openai.BetaAgentEnvironmentTemplateNewParams
				if decodeErr := json.Unmarshal(raw, &body); decodeErr != nil {
					writeJSON(w, 400, map[string]string{"error": "invalid template payload"})
					return
				}
				body.SetExtraFields(fields)
				result, err = svc.New(r.Context(), body)
				status = http.StatusCreated
			} else {
				var body openai.BetaAgentEnvironmentTemplateUpdateParams
				if decodeErr := json.Unmarshal(raw, &body); decodeErr != nil {
					writeJSON(w, 400, map[string]string{"error": "invalid template payload"})
					return
				}
				body.SetExtraFields(fields)
				result, err = svc.Update(r.Context(), id, body)
			}
		}
		if err != nil {
			writeReadError(w, err, "Core template operation failed")
			return
		}

		writeJSON(w, status, result)
	}
}
