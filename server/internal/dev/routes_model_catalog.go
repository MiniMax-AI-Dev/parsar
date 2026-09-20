package dev

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type catalogStore interface {
	ListCatalogProviders(context.Context, string) ([]store.CatalogProvider, error)
	SaveCatalogProvider(context.Context, string, string, store.CatalogProviderInput) (store.CatalogProvider, error)
	DeleteCatalogProvider(context.Context, string, string) error
	ListCatalogModels(context.Context, string) ([]store.CatalogModel, error)
	CreateCatalogModel(context.Context, string, string, store.CatalogModelInput) (store.CatalogModel, error)
	RenameCatalogModel(context.Context, string, string, string) (store.CatalogModel, error)
	DeleteCatalogModel(context.Context, string, string) error
	ResolveCatalogAgentModel(context.Context, string, map[string]any) error
	RecordCatalogAudit(string, string, string, string, string)
}

func registerModelCatalogRoutes(r chi.Router, st RuntimeStore) {
	r.Get("/workspaces/{workspaceID}/model-providers", listCatalogProviders(st))
	r.Post("/workspaces/{workspaceID}/model-providers", createCatalogProvider(st))
	r.Put("/workspaces/{workspaceID}/model-providers/{catalogID}", updateCatalogProvider(st))
	r.Delete("/workspaces/{workspaceID}/model-providers/{catalogID}", deleteCatalogProvider(st))
	r.Get("/workspaces/{workspaceID}/models", listCatalogModels(st))
	r.Post("/workspaces/{workspaceID}/models", createCatalogModel(st))
	r.Patch("/workspaces/{workspaceID}/models/{catalogID}", renameCatalogModel(st))
	r.Delete("/workspaces/{workspaceID}/models/{catalogID}", deleteCatalogModel(st))
}
func catalogAccess(w http.ResponseWriter, r *http.Request, st RuntimeStore) (catalogStore, string, bool) {
	workspace := chi.URLParam(r, "workspaceID")
	if !isUUID(workspace) || (chi.URLParam(r, "catalogID") != "" && !isUUID(chi.URLParam(r, "catalogID"))) {
		writeJSON(w, 400, map[string]string{"error": "invalid catalog or workspace UUID"})
		return nil, "", false
	}
	if st == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "model catalog unavailable"})
		return nil, "", false
	}
	var err error
	if r.Method == http.MethodGet {
		err = requireWorkspaceMember(r, st, workspace)
	} else {
		err = requireWorkspaceOwnerOrAdmin(r, st, workspace)
	}
	if err != nil {
		writeRBACError(w, err)
		return nil, "", false
	}
	catalog, ok := st.(catalogStore)
	if !ok {
		writeJSON(w, 503, map[string]string{"error": "model catalog unavailable"})
		return nil, "", false
	}
	return catalog, workspace, true
}
func decodeCatalogBody(w http.ResponseWriter, r *http.Request, body any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768))
	decoder.DisallowUnknownFields()
	if decoder.Decode(body) != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid catalog payload"})
		return false
	}
	return true
}
func resolveCatalogAgentModel(ctx context.Context, st RuntimeStore, workspace string, config map[string]any) error {
	if _, ok := config["model_id"]; !ok {
		return nil
	}
	catalog, ok := st.(catalogStore)
	if !ok {
		return store.ErrCatalogKeyUnavailable
	}
	return catalog.ResolveCatalogAgentModel(ctx, workspace, config)
}

type renameCatalogModelBody struct {
	Name string `json:"name"`
}

// listCatalogProviders serves the workspace model catalog.
// @Summary listCatalogProviders
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/model-providers [get]
func listCatalogProviders(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		result, err := catalog.ListCatalogProviders(r.Context(), workspace)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		writeJSON(w, 200, map[string]any{"providers": result})
	}
}

// createCatalogProvider serves the workspace model catalog.
// @Summary createCatalogProvider
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Accept json
// @Param body body store.CatalogProviderInput true "Catalog input"
// @Success 201 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/model-providers [post]
func createCatalogProvider(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		var input store.CatalogProviderInput
		if !decodeCatalogBody(w, r, &input) {
			return
		}
		result, err := catalog.SaveCatalogProvider(r.Context(), workspace, "", input)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "provider", result.ID, "created")
		writeJSON(w, 201, map[string]any{"provider": result})
	}
}

// updateCatalogProvider serves the workspace model catalog.
// @Summary updateCatalogProvider
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param catalogID path string true "Catalog UUID"
// @Accept json
// @Param body body store.CatalogProviderInput true "Catalog input"
// @Success 200 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/model-providers/{catalogID} [put]
func updateCatalogProvider(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		var input store.CatalogProviderInput
		if !decodeCatalogBody(w, r, &input) {
			return
		}
		result, err := catalog.SaveCatalogProvider(r.Context(), workspace, chi.URLParam(r, "catalogID"), input)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "provider", result.ID, "updated")
		writeJSON(w, 200, map[string]any{"provider": result})
	}
}

// deleteCatalogProvider serves the workspace model catalog.
// @Summary deleteCatalogProvider
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param catalogID path string true "Catalog UUID"
// @Success 204 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/model-providers/{catalogID} [delete]
func deleteCatalogProvider(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		err := catalog.DeleteCatalogProvider(r.Context(), workspace, chi.URLParam(r, "catalogID"))
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "provider", chi.URLParam(r, "catalogID"), "deleted")
		w.WriteHeader(http.StatusNoContent)
	}
}

// listCatalogModels serves the workspace model catalog.
// @Summary listCatalogModels
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Success 200 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/models [get]
func listCatalogModels(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		result, err := catalog.ListCatalogModels(r.Context(), workspace)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		writeJSON(w, 200, map[string]any{"models": result})
	}
}

// createCatalogModel serves the workspace model catalog.
// @Summary createCatalogModel
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Accept json
// @Param body body store.CatalogModelInput true "Catalog input"
// @Success 201 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/models [post]
func createCatalogModel(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		var input store.CatalogModelInput
		if !decodeCatalogBody(w, r, &input) {
			return
		}
		result, err := catalog.CreateCatalogModel(r.Context(), workspace, actorIDFromRequest(r), input)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "model", result.ID, "created")
		writeJSON(w, 201, map[string]any{"model": result})
	}
}

// renameCatalogModel serves the workspace model catalog.
// @Summary renameCatalogModel
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param catalogID path string true "Catalog UUID"
// @Accept json
// @Param body body renameCatalogModelBody true "Catalog input"
// @Success 200 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/models/{catalogID} [patch]
func renameCatalogModel(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		var input renameCatalogModelBody
		if !decodeCatalogBody(w, r, &input) {
			return
		}
		result, err := catalog.RenameCatalogModel(r.Context(), workspace, chi.URLParam(r, "catalogID"), input.Name)
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "model", result.ID, "updated")
		writeJSON(w, 200, map[string]any{"model": result})
	}
}

// deleteCatalogModel serves the workspace model catalog.
// @Summary deleteCatalogModel
// @Description Workspace members can read; owner/admin can mutate. API keys are write-only. Existing Sessions retain their snapshot.
// @Tags models
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param catalogID path string true "Catalog UUID"
// @Success 204 {object} map[string]interface{}
// @Failure 400,403,404,503 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/models/{catalogID} [delete]
func deleteCatalogModel(st RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		catalog, workspace, ok := catalogAccess(w, r, st)
		if !ok {
			return
		}
		err := catalog.DeleteCatalogModel(r.Context(), workspace, chi.URLParam(r, "catalogID"))
		if err != nil {
			writeReadError(w, err, "model catalog operation failed")
			return
		}
		catalog.RecordCatalogAudit(workspace, actorIDFromRequest(r), "model", chi.URLParam(r, "catalogID"), "deleted")
		w.WriteHeader(http.StatusNoContent)
	}
}
