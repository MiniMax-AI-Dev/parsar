package api

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Download source file bytes
// @Description Streams an authorized immutable source snapshot. Already-admitted reads may finish after deletion; later reads reject. No Beta header is required. Range requests and exact hosted headers/error behavior are not implemented or verified.
// @Tags Files
// @Produce octet-stream
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {file} binary
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id}/content [get]
func (h *Handler) sourceFileContent(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesReady(w, r) {
		return
	}
	serveStoredContent(w, r, func(ctx context.Context, consume func(string, int64, io.Reader) error) error {
		return h.sourceFiles.ReadSourceFile(ctx, tenantID(r), chi.URLParam(r, "file_id"), func(file store.SourceFile, body io.Reader) error {
			return consume(file.Filename, file.SizeBytes, body)
		})
	})
}

func serveStoredContent(w http.ResponseWriter, r *http.Request, read func(context.Context, func(string, int64, io.Reader) error) error) {
	deadline := time.Now().Add(sourceTransferTimeout)
	if http.NewResponseController(w).SetWriteDeadline(deadline) != nil {
		writeError(w, http.StatusServiceUnavailable, "file_transfer_unavailable", "Bounded file transfer is unavailable.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	started := false
	err := read(ctx, func(filename string, size int64, body io.Reader) error {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		started = true
		w.WriteHeader(http.StatusOK)
		n, err := io.CopyBuffer(w, body, make([]byte, 256<<10))
		if err == nil && n != size {
			err = io.ErrUnexpectedEOF
		}
		return err
	})
	if err != nil {
		if started {
			panic(http.ErrAbortHandler)
		}
		writeStoreError(w, r, err)
	}
}
