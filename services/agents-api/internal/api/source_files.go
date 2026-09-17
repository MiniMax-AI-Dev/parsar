package api

import (
	"context"
	"io"
	"net/http"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type SourceFileStore interface {
	CreateSourceFile(context.Context, string, func(io.Writer) (store.SourceFileUpload, error)) (store.SourceFile, error)
	GetSourceFile(context.Context, string, string) (store.SourceFile, error)
	ReadSourceFile(context.Context, string, string, func(store.SourceFile, io.Reader) error) error
	DeleteSourceFile(context.Context, string, string) error
}

func WithSourceFiles(s SourceFileStore) Option {
	return func(h *Handler) { h.sourceFiles = s }
}

func (h *Handler) sourceFilesReady(w http.ResponseWriter, r *http.Request) bool {
	if h.sourceFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "file_storage_unavailable", "Source file storage is unavailable.")
		return false
	}
	if r.URL.RawQuery != "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return false
	}
	return true
}

// @Summary Retrieve source file metadata
// @Description Returns immutable project-owned user_data file metadata. No Beta header is required. Other purposes, expiration and full hosted status/error semantics remain unimplemented or unverified.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFile
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id} [get]
func (h *Handler) getSourceFile(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesReady(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	file, err := h.sourceFiles.GetSourceFile(ctx, tenantID(r), chi.URLParam(r, "file_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceFileResponse(file))
}

// @Summary Delete a source file
// @Description Atomically deletes project-owned metadata and stored bytes. Already-admitted reads or copies may finish. Workspace copies remain independent. Historical WAL/backups are not erased. No Beta header is required; exact hosted concurrent deletion/error semantics remain unverified.
// @Tags Files
// @Produce json
// @Security BearerAuth
// @Param file_id path string true "Source file ID"
// @Success 200 {object} v1.SourceFileDeleted
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /files/{file_id} [delete]
func (h *Handler) deleteSourceFile(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesReady(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	id := chi.URLParam(r, "file_id")
	if err := h.sourceFiles.DeleteSourceFile(ctx, tenantID(r), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SourceFileDeleted{ID: id, Object: "file", Deleted: true})
}

func sourceFileResponse(file store.SourceFile) v1.SourceFile {
	return v1.SourceFile{ID: file.ID, Object: "file", Bytes: file.SizeBytes,
		CreatedAt: file.CreatedAt.Unix(), Filename: file.Filename,
		Purpose: file.Purpose, Status: "processed"}
}
