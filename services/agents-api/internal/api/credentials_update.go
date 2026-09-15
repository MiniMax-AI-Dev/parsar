package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// @Summary Replace a static-bearer Vault Credential token
// @Description Requires auth with type=static_bearer and a string token; empty and opaque token bytes are preserved. Only the write-only secret and updated_at change, atomically within the authenticated project and owning Vault. Identity, name, auth type, exact destination, created_at and Session bindings remain unchanged. Responses contain only safe metadata; no old-token decryption or network call occurs. Missing encryption configuration returns local 503 without modifying the credential. Subsequent dispatch reads use the committed replacement; already-resolved requests may retain the old token. OAuth, storage-key rotation, hot reload/revocation and exact hosted concurrent-update/retry/timestamp semantics remain gaps.
// @Tags Credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Param body body v1.UpdateCredentialRequest true "Write-only static bearer replacement"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,413,500,503 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials/{credential_id} [post]
func (h *Handler) updateCredential(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential update does not accept query parameters.")
		return
	}
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var request v1.UpdateCredentialRequest
	if decodeInputObject(raw, &request, "auth") != nil || request.Auth == nil || request.Auth.Type != "static_bearer" || request.Auth.Token == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "auth with type=static_bearer and a string token is required.")
		return
	}
	credential, err := h.store.UpdateStaticCredential(r.Context(), tenantID(r), vaultID, id, store.UpdateStaticCredentialInput{Token: *request.Auth.Token})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}
