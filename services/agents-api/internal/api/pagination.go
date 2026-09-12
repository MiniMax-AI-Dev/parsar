package api

import (
	"net/http"
	"strconv"
	"strings"
)

type pageOptions struct {
	after     string
	limit     int
	ascending bool
}

func readPage(w http.ResponseWriter, r *http.Request) (pageOptions, bool) {
	return readPageSize(w, r, true)
}

func readPageSize(w http.ResponseWriter, r *http.Request, rejectLarger bool) (pageOptions, bool) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "after" && key != "limit" && key != "order") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "unsupported_parameter", "Supported list parameters are after, limit and order, each supplied once.")
			return pageOptions{}, false
		}
	}
	limit, order := 20, q.Get("order")
	if raw, ok := q["limit"]; ok {
		var err error
		var requested int64
		requested, err = strconv.ParseInt(raw[0], 10, 64)
		if err != nil || requested < 1 || (rejectLarger && requested > 100) {
			message := "limit must be a positive 64-bit integer."
			if rejectLarger {
				message = "limit must be between 1 and 100."
			}
			writeError(w, http.StatusBadRequest, "invalid_request", message)
			return pageOptions{}, false
		}
		limit = int(min(requested, 100))
	}
	if order != "" && order != "asc" && order != "desc" {
		writeError(w, http.StatusBadRequest, "invalid_request", "order must be asc or desc.")
		return pageOptions{}, false
	}
	return pageOptions{after: strings.TrimSpace(q.Get("after")), limit: limit, ascending: order == "asc"}, true
}
