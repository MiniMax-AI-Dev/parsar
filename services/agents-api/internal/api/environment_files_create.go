package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"path"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type EnvironmentFileWriter interface {
	WriteEnvironmentFile(context.Context, store.Environment, string, []byte) (int64, error)
}

func WithEnvironmentFileWriter(writer EnvironmentFileWriter) Option {
	return func(h *Handler) { h.fileWriter = writer }
}

// @Summary Create an Environment file from inline bytes
// @Description Uploads standard Base64 bytes to a file beneath /workspace in a preconfigured, qualified local Environment. This partial implementation accepts inline only; the pinned file_id union member remains unimplemented. Public hosted Session creation is not enabled. A private 50 MiB decoded-content limit applies. The parent directory must exist. Replacement installs a new mode-0600 inode; upstream overwrite metadata semantics remain unverified. Idle writes exclude execution. Missing receipts return unavailable and retain a durable mutation gate without automatic replay. Error/timing parity with upstream remains unverified.
// @Tags Environments
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Param request body v1.InlineEnvironmentFileCreateRequest true "Inline file bytes and absolute workspace path"
// @Success 200 {object} v1.EnvironmentFile
// @Failure 400,401,404,409,413,500,503 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id}/files [post]
func (h *Handler) createEnvironmentFile(w http.ResponseWriter, r *http.Request) {
	environment, err := h.store.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if r.URL.RawQuery != "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	const maxJSON = int64(((proto.WorkspaceWriteMaxBytes+2)/3)*4 + (16 << 10))
	raw, ok := readJSONBodyLimit(w, r, maxJSON, "Inline upload exceeds this service's bounded file limit.")
	if !ok {
		return
	}
	var request v1.InlineEnvironmentFileCreateRequest
	if err := decodeInputObject(raw, &request, "type", "data", "path"); err != nil || request.Type != "inline" || request.Data == nil || request.Path == nil {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "This implementation requires type=inline, data and path; file_id uploads are not implemented.")
		return
	}
	if !validEnvironmentFilePath(*request.Path) || path.Clean(*request.Path) != *request.Path || !strings.HasPrefix(*request.Path, "/workspace/") {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	data, err := base64.StdEncoding.Strict().DecodeString(*request.Data)
	if err != nil {
		writeStoreError(w, r, store.ErrInvalidInput)
		return
	}
	if len(data) > proto.WorkspaceWriteMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Inline upload exceeds this service's bounded file limit.")
		return
	}
	if h.fileWriter == nil || !execution.LocalWorkspaceConfiguration(environment.Configuration) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(215 * time.Second)); err != nil {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	size, err := h.fileWriter.WriteEnvironmentFile(r.Context(), environment, strings.TrimPrefix(*request.Path, "/workspace/"), data)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if size != int64(len(data)) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, v1.EnvironmentFile{EnvironmentID: environment.ID, Object: "agent.environment.file", Path: *request.Path, SizeBytes: size})
}
