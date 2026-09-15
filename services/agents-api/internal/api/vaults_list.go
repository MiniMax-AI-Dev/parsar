package api

import (
	"errors"
	"net/http"
	"strconv"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary List Vaults
// @Description Lists project-owned Vaults independently of execution. Includes active and archived records by default. Status accepts a scalar or the SDK's status[] array; mixed encodings and repeated scalars are rejected locally. Limits default to 20 and clamp to 1–100. Equal creation times use ID ordering; exact hosted errors and concurrent-page behavior remain unverified. Archive/delete lifecycle is not implemented.
// @Tags Vaults
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Last Vault ID from the previous page"
// @Param limit query integer false "Requested page size, clamped to 1–100" default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Param status query string false "Scalar status filter" Enums(active,archived)
// @Param status[] query []string false "Array status filter; cannot be combined with status" collectionFormat(multi) Enums(active,archived)
// @Success 200 {object} v1.VaultList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults [get]
func (h *Handler) listVaults(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	statuses, scalar := q["status"]
	array, bracketed := q["status[]"]
	if (scalar && bracketed) || (scalar && len(statuses) != 1) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Supply status once or use status[] for an array.")
		return
	}
	if bracketed {
		statuses = array
	}
	for _, status := range statuses {
		if status != "active" && status != "archived" {
			writeError(w, http.StatusBadRequest, "invalid_request", "status must be active or archived.")
			return
		}
	}
	q.Del("status")
	q.Del("status[]")
	// Vault limits clamp at both ends; other list resources retain their policy.
	if raw := q["limit"]; len(raw) == 1 {
		requested, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer.")
			return
		}
		q.Set("limit", strconv.FormatInt(max(1, min(requested, 100)), 10))
	}
	options, ok := readPageQuery(w, q, false)
	if !ok {
		return
	}
	page, err := h.store.ListVaults(r.Context(), tenantID(r), options.after, options.limit, options.ascending, statuses)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.VaultList{Object: "list", Data: make([]v1.Vault, 0, len(page.Vaults)), HasMore: page.NextCursor != ""}
	for _, vault := range page.Vaults {
		response.Data = append(response.Data, vaultResponse(vault))
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
