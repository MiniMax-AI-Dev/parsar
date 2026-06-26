package dev

// Workspace runtime credential PUT/DELETE handlers — admin UI wires the
// RuntimeCredentialCard into secrets.New(PARSAR_MASTER_KEY).Encrypt
// (same vault as createSecret).
//
// PUT is an atomic upsert (store-level tx): SoftDelete prior credential
// → CreateSecret → flip workspaces.config.runtime_credential_secret_id.
// uk_secrets_workspace_name_active is freed by the soft-delete, so
// repeat PUTs don't collide on 23505.
//
// DELETE is also a store-level tx: soft-delete active row + clear
// pointer. Clearing the pointer alone would leak an active row that
// blocks the next PUT. Idempotent.
//
// Old secret rows keep deleted_at (audit) and are never GC'd in v0.1.
// kind='runtime', provider='e2b', name="Workspace Runtime Credential"
// (fixed — admin doesn't pick).

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	runtimeCredentialName     = "Workspace Runtime Credential"
	runtimeCredentialProvider = "e2b"
	runtimeCredentialKind     = "runtime"
	runtimeCredentialAuthType = "api_key"
)

type runtimeCredentialBody struct {
	APIKey string `json:"api_key"`
}

type runtimeCredentialResponse struct {
	HasCredential    bool    `json:"has_credential"`
	CredentialMasked *string `json:"credential_masked"`
	UpdatedAt        string  `json:"updated_at"`
}

// putRuntimeCredential registers or overwrites the workspace's sandbox
// runtime credential via an atomic store-level upsert.
//
// 400 invalid json / empty api_key; 503 master key missing;
// 403 not workspace owner/admin; 500 encrypt/store error.
func putRuntimeCredential(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed runtime credential is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		var req runtimeCredentialBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		apiKey := strings.TrimSpace(req.APIKey)
		if apiKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api_key is required"})
			return
		}

		serverMasterKey := os.Getenv("PARSAR_MASTER_KEY")
		if serverMasterKey == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "server has no PARSAR_MASTER_KEY configured; refusing to register a credential that could not be decrypted later",
			})
			return
		}
		secretService, err := secrets.New(serverMasterKey)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		payload := map[string]any{"api_key": apiKey}
		encrypted, err := secretService.Encrypt(payload)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encrypt runtime credential"})
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		now := time.Now().UTC()
		secret, err := runtimeStore.RegisterWorkspaceRuntimeCredential(r.Context(), store.RegisterWorkspaceRuntimeCredentialInput{
			WorkspaceID:      workspaceID,
			Name:             runtimeCredentialName,
			Kind:             runtimeCredentialKind,
			Provider:         runtimeCredentialProvider,
			AuthType:         runtimeCredentialAuthType,
			EncryptedPayload: encrypted,
			Masked:           secrets.MaskPayload(payload),
			CreatedBy:        actorID,
			Now:              now,
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspace) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to register runtime credential"})
			return
		}
		masked := secret.Masked
		resp := runtimeCredentialResponse{
			HasCredential: true,
			UpdatedAt:     now.Format(time.RFC3339),
		}
		if strings.TrimSpace(masked) != "" {
			resp.CredentialMasked = &masked
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// deleteRuntimeCredential clears the workspace's runtime credential
// pointer. Idempotent. The referenced secret row stays as orphan audit
// trail; v0.1 does not GC.
func deleteRuntimeCredential(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed runtime credential is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		now := time.Now().UTC()
		if err := runtimeStore.ClearWorkspaceRuntimeCredentialSecret(r.Context(), workspaceID, runtimeCredentialName, runtimeCredentialKind, now); err != nil {
			if errors.Is(err, store.ErrUnknownWorkspace) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to clear runtime credential pointer"})
			return
		}
		writeJSON(w, http.StatusOK, runtimeCredentialResponse{
			HasCredential: false,
			UpdatedAt:     now.Format(time.RFC3339),
		})
	}
}
