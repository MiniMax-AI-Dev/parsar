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
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100.")
			return pageOptions{}, false
		}
	}
	if order != "" && order != "asc" && order != "desc" {
		writeError(w, http.StatusBadRequest, "invalid_request", "order must be asc or desc.")
		return pageOptions{}, false
	}
	return pageOptions{after: strings.TrimSpace(q.Get("after")), limit: limit, ascending: order == "asc"}, true
}
