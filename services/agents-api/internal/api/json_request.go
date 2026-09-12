package api

import (
	"errors"
	"io"
	"net/http"
)

func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err == nil {
		return raw, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 1 MiB.")
	} else {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain one JSON object.")
	}
	return nil, false
}
