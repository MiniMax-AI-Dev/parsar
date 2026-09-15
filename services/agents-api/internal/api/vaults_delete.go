package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary Delete a Vault and all its Credentials
// @Description Atomically removes the authenticated project's Vault and all its stored Credentials without an encryption key, decryption or external requests. Existing Session snapshots, history and recorded retries retain their frozen identities; subsequent credential lookups fail without reselection or anonymous fallback. Already-resolved tokens and running Sessions are not revoked or cancelled. Missing/repeated deletion locally returns 404. Exact hosted archive, post-delete visibility and concurrent/error semantics remain unverified; physical erasure from native history, WAL or backups is not established.
// @Tags Vaults
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Success 200 {object} v1.VaultDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id} [delete]
func (h *Handler) deleteVault(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Vault deletion does not accept query parameters.")
		return
	}
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Vault deletion does not accept a request body.")
		return
	}
	id, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	deleted, err := h.store.DeleteVault(r.Context(), tenantID(r), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.VaultDeleted{ID: deleted, Deleted: true, Object: "vault.deleted"})
}
