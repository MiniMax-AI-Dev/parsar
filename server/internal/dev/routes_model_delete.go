package dev

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type bulkDeleteModelsBody struct {
	ModelIDs []string `json:"model_ids"`
}

type bulkDeleteModelFailure struct {
	ModelID    string                 `json:"model_id"`
	Error      string                 `json:"error"`
	References []store.ModelReference `json:"references,omitempty"`
}

type bulkDeleteModelsResponse struct {
	Deleted []string                 `json:"deleted"`
	Failed  []bulkDeleteModelFailure `json:"failed"`
}

// deleteModel hard-deletes a model row after checking active agent references.
//
//	@Summary		Delete a model
//	@Description	Hard-deletes a model. Owner/admin only. Models referenced by active agents are rejected.
//	@Tags			models
//	@ID				deleteDevModel
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			modelID		path	string	true	"Model UUID"
//	@Success		204 "Deleted model"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Model not found"
//	@Failure		409 {object} map[string]interface{} "Model is still referenced"
//	@Router			/api/v1/workspaces/{workspaceID}/models/{modelID} [delete]
func deleteModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		if _, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore); !ok {
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		if err := runtimeStore.DeleteModel(r.Context(), modelID, actorIDFromRequest(r)); err != nil {
			writeModelDeleteError(w, modelID, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// bulkDeleteModels hard-deletes multiple model rows. Each model reports its
// own failure so admins can clear unreferenced rows without losing the whole
// batch to one referenced model.
//
//	@Summary		Bulk delete models
//	@Description	Hard-deletes selected models. Owner/admin only. Referenced models are skipped and returned in failed.
//	@Tags			models
//	@ID				bulkDeleteDevModels
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string					true	"Workspace UUID"
//	@Param			body		body	bulkDeleteModelsBody	true	"Model ids"
//	@Success		200 {object} bulkDeleteModelsResponse "Bulk delete result"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/models/bulk-delete [post]
func bulkDeleteModels(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		if _, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore); !ok {
			return
		}
		var req bulkDeleteModelsBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		response := bulkDeleteModelsResponse{Deleted: []string{}, Failed: []bulkDeleteModelFailure{}}
		seen := map[string]bool{}
		for _, rawID := range req.ModelIDs {
			modelID := strings.TrimSpace(rawID)
			if modelID == "" || seen[modelID] {
				continue
			}
			seen[modelID] = true
			if !isUUID(modelID) {
				response.Failed = append(response.Failed, bulkDeleteModelFailure{ModelID: modelID, Error: "model_id must be a valid uuid"})
				continue
			}
			if err := runtimeStore.DeleteModel(r.Context(), modelID, actorIDFromRequest(r)); err != nil {
				response.Failed = append(response.Failed, modelDeleteFailure(modelID, err))
				continue
			}
			response.Deleted = append(response.Deleted, modelID)
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func modelDeleteFailure(modelID string, err error) bulkDeleteModelFailure {
	failure := bulkDeleteModelFailure{ModelID: modelID, Error: err.Error()}
	var inUse *store.ModelInUseError
	if errors.As(err, &inUse) {
		failure.Error = "model_in_use"
		failure.References = inUse.References
	}
	if errors.Is(err, store.ErrUnknownModel) {
		failure.Error = "model_not_found"
	}
	return failure
}

func writeModelDeleteError(w http.ResponseWriter, modelID string, err error) {
	var inUse *store.ModelInUseError
	if errors.As(err, &inUse) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "model_in_use",
			"model_id":   modelID,
			"references": inUse.References,
		})
		return
	}
	if errors.Is(err, store.ErrUnknownModel) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete model"})
}
