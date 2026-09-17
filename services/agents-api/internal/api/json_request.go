package api

import (
	"errors"
	"io"
	"net/http"
)

func readJSONBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	return readJSONBodyLimit(w, r, 1024*1024, "Request exceeds 1 MiB.")
}

func readJSONBodyLimit(w http.ResponseWriter, r *http.Request, limit int64, message string) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err == nil {
		return raw, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", message)
	} else {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain one JSON object.")
	}
	return nil, false
}
