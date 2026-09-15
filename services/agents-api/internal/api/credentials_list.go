package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary List safe Vault Credential metadata
// @Description Lists only metadata from the authenticated project's requested Vault, without decryption or execution. Includes active and archived Credentials by default, independently of Vault status. Status accepts a scalar or SDK status[] array; mixed encodings and repeated scalars are rejected locally. Limits default to 20 and clamp to 1–100. Equal creation times use ID ordering. Hosted errors, concurrent-page behavior and archive/delete lifecycle remain unverified or unimplemented.
// @Tags Credentials
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param after query string false "Last Credential ID from the previous page"
// @Param limit query integer false "Requested page size, clamped to 1–100" default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Param status query string false "Scalar status filter" Enums(active,archived)
// @Param status[] query []string false "Array status filter; cannot be combined with status" collectionFormat(multi) Enums(active,archived)
// @Success 200 {object} v1.CredentialList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials [get]
func (h *Handler) listCredentials(w http.ResponseWriter, r *http.Request) {
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	options, statuses, ok := readVaultPage(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListCredentials(r.Context(), tenantID(r), vaultID, options.after, options.limit, options.ascending, statuses)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.CredentialList{Object: "list", Data: make([]v1.Credential, 0, len(page.Credentials)), HasMore: page.NextCursor != ""}
	for _, credential := range page.Credentials {
		response.Data = append(response.Data, credentialResponse(credential))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
