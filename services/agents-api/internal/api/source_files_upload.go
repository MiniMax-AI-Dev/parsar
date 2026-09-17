package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const sourceTransferTimeout = 5 * time.Minute

// @Summary Upload a source file
// @Description Accepts one multipart file and purpose=user_data in either order, with a private 512 MiB content limit and 64 KiB envelope allowance. Commits only after the entire request validates. The source is project-owned, independent of Sessions and workspace copies. No Beta header is required. Other purposes, expires_after, listing, resumable Uploads, quotas/rate-limit and complete hosted error/status parity remain unsupported or unverified.
// @Tags Files
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Source bytes"
// @Param purpose formData string true "user_data" Enums(user_data)
// @Success 200 {object} v1.SourceFile
// @Failure 400,401,413,500,503 {object} v1.ErrorResponse
// @Router /files [post]
func (h *Handler) createSourceFile(w http.ResponseWriter, r *http.Request) {
	if !h.sourceFilesReady(w, r) {
		return
	}
	deadline := time.Now().Add(sourceTransferTimeout)
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(deadline) != nil || controller.SetWriteDeadline(deadline) != nil {
		writeError(w, http.StatusServiceUnavailable, "file_transfer_unavailable", "Bounded file transfer is unavailable.")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), deadline)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, store.MaxSourceFileBytes+(64<<10))
	file, err := h.sourceFiles.CreateSourceFile(ctx, tenantID(r), func(dst io.Writer) (store.SourceFileUpload, error) {
		return readSourceUpload(r, dst)
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sourceFileResponse(file))
}

func readSourceUpload(r *http.Request, dst io.Writer) (store.SourceFileUpload, error) {
	var input store.SourceFileUpload
	if r.Header.Get("Content-Encoding") != "" {
		return input, store.ErrInvalidInput
	}
	multi, err := r.MultipartReader()
	if err != nil {
		return input, store.ErrInvalidInput
	}
	seen := make(map[string]bool, 2)
	buffer := make([]byte, 256<<10)
	for {
		part, err := multi.NextRawPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return input, sourceUploadError(err)
		}
		kind, attrs, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		name := attrs["name"]
		if err != nil || kind != "form-data" || seen[name] || (name != "file" && name != "purpose") || part.Header.Get("Content-Transfer-Encoding") != "" {
			return input, store.ErrInvalidInput
		}
		seen[name] = true
		if name == "file" {
			input.Filename = attrs["filename"]
			if _, err := io.CopyBuffer(dst, part, buffer); err != nil {
				return input, sourceUploadError(err)
			}
		} else {
			if _, exists := attrs["filename"]; exists {
				return input, store.ErrInvalidInput
			}
			value, err := io.ReadAll(io.LimitReader(part, 65))
			if err != nil || len(value) > 64 {
				return input, store.ErrInvalidInput
			}
			input.Purpose = string(value)
		}
		if err := part.Close(); err != nil {
			return input, sourceUploadError(err)
		}
	}
	if _, err := io.CopyBuffer(io.Discard, r.Body, buffer); err != nil {
		return input, sourceUploadError(err)
	}
	if !seen["file"] || !seen["purpose"] || input.Purpose != "user_data" {
		return input, store.ErrInvalidInput
	}
	return input, nil
}

func sourceUploadError(err error) error {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) || errors.Is(err, store.ErrSourceFileTooLarge) {
		return store.ErrSourceFileTooLarge
	}
	return store.ErrInvalidInput
}
