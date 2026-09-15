package api

import (
	"errors"
	"net/http"
	"strconv"
)

func readVaultPage(w http.ResponseWriter, r *http.Request) (pageOptions, []string, bool) {
	q := r.URL.Query()
	statuses, scalar := q["status"]
	array, bracketed := q["status[]"]
	if (scalar && bracketed) || (scalar && len(statuses) != 1) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Supply status once or use status[] for an array.")
		return pageOptions{}, nil, false
	}
	if bracketed {
		statuses = array
	}
	for _, status := range statuses {
		if status != "active" && status != "archived" {
			writeError(w, http.StatusBadRequest, "invalid_request", "status must be active or archived.")
			return pageOptions{}, nil, false
		}
	}
	q.Del("status")
	q.Del("status[]")
	// Vault and Credential limits clamp at both ends; other lists retain their policy.
	if raw := q["limit"]; len(raw) == 1 {
		requested, err := strconv.ParseInt(raw[0], 10, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be an integer.")
			return pageOptions{}, nil, false
		}
		q.Set("limit", strconv.FormatInt(max(1, min(requested, 100)), 10))
	}
	options, ok := readPageQuery(w, q, false)
	return options, statuses, ok
}
