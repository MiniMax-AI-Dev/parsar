package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary Delete a Vault Credential
// @Description Removes one Credential and its encrypted token within the authenticated project and owning Vault, without an encryption key or secret decryption. Subsequent metadata reads, updates and dispatch lookups cannot use it. Existing Session snapshots and history retain their frozen identities; already-resolved tokens and running Sessions are not revoked or cancelled. This local policy removes the row rather than defining archived lifecycle; missing/repeated deletion returns 404. Exact hosted archive, post-delete visibility and retry/error semantics remain unverified. Provider revocation and physical erasure from native history, WAL or backups are separate concerns.
// @Tags Credentials
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Success 200 {object} v1.CredentialDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials/{credential_id} [delete]
func (h *Handler) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential deletion does not accept query parameters.")
		return
	}
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential deletion does not accept a request body.")
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
	deleted, err := h.store.DeleteCredential(r.Context(), tenantID(r), vaultID, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.CredentialDeleted{ID: deleted, Deleted: true, Object: "vault.credential.deleted"})
}
