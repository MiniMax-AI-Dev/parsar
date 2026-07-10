package dev

import (
	"encoding/json"
	"errors"

	"net/http"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type createSecretBody struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Provider string         `json:"provider"`
	AuthType string         `json:"auth_type"`
	Payload  map[string]any `json:"payload"`
}

// createSecret adds a secret entry to a workspace's vault.
//
//	@Summary		Create a workspace secret
//	@Description	Adds a secret entry to the workspace vault. Owner/admin only.
//	@Tags			secrets
//	@ID				createDevSecret
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string			true	"Workspace UUID"
//	@Param			body		body	createSecretBody	true	"Secret create payload"
//	@Success		201 {object} map[string]interface{} "Created secret"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/secrets [post]
func createSecret(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createSecretBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.AuthType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name, provider, and auth_type are required"})
			return
		}
		serverMasterKey := os.Getenv("PARSAR_MASTER_KEY")
		if serverMasterKey == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "server has no PARSAR_MASTER_KEY configured; refusing to create a secret"})
			return
		}
		secretService, err := secrets.New(serverMasterKey)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		encrypted, err := secretService.Encrypt(req.Payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encrypt secret"})
			return
		}
		secret, err := runtimeStore.CreateSecret(r.Context(), store.CreateSecretInput{
			WorkspaceID: workspaceID,
			Name:        req.Name,
			Kind:        req.Kind,
			Provider:    req.Provider,
			AuthType:    req.AuthType,
			Payload:     req.Payload,
			Masked:      secrets.MaskPayload(req.Payload),
		}, encrypted)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create secret"})
			return
		}
		writeJSON(w, http.StatusCreated, secret)
	}
}

// listSecrets lists secret rows for a workspace.
//
//	@Summary		List workspace secrets
//	@Description	Returns secret rows for the workspace. Values are redacted. Owner/admin only.
//	@Tags			secrets
//	@ID				listDevSecrets
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Secret list"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/secrets [get]
func listSecrets(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		limit := parseLimit(r, 100)
		secrets, err := runtimeStore.ListSecrets(r.Context(), workspaceID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list secrets"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
	}
}

// disableSecret marks a secret row as disabled.
//
//	@Summary		Disable a secret
//	@Description	Marks the secret as disabled so agents can no longer bind to it. Owner/admin only.
//	@Tags			secrets
//	@ID				disableDevSecret
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			secretID	path	string	true	"Secret UUID"
//	@Success		200 {object} map[string]interface{} "Disabled secret"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Secret not found"
//	@Router			/api/v1/workspaces/{workspaceID}/secrets/{secretID}/disable [post]
func disableSecret(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed secret vault is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		secretID := strings.TrimSpace(chi.URLParam(r, "secretID"))
		if !isUUID(secretID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "secret_id must be a valid uuid"})
			return
		}
		secret, err := runtimeStore.DisableSecret(r.Context(), workspaceID, secretID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownSecret) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to disable secret"})
			return
		}
		writeJSON(w, http.StatusOK, secret)
	}
}
