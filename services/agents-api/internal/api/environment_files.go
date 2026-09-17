package api

import (
	"context"
	"net/http"
	"path"
	"slices"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type EnvironmentDirectoryReader interface {
	ReadEnvironmentDirectory(context.Context, store.Environment, string) (proto.WorkspaceDirectoryResult, error)
}

func WithEnvironmentDirectoryReader(reader EnvironmentDirectoryReader) Option {
	return func(h *Handler) { h.directoryReader = reader }
}

// @Summary List live Environment files
// @Description Lists direct regular files in one authorized self_hosted workspace directory. This partial implementation defaults to the workspace root and limit 20; recursive scope, directory/symlink treatment and these defaults are not verified upstream semantics. Sorts by case-sensitive path components, descending by default. Keep the same path, order and limit when using page. Each page rereads the complete bounded directory; changed file paths/sizes invalidate continuation locally with 400. There is no snapshot guarantee. Truncated or uncertain native results fail with 503 without returning a partial page. This read never starts a Turn or admits model input. Actual transport disconnect/reconnect events remain observable.
// @Tags Environments
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Param path query string false "Absolute directory inside the Environment workspace"
// @Param limit query int false "Maximum file count; local default 20" minimum(1) maximum(100)
// @Param order query string false "Case-sensitive path-component order" Enums(asc,desc) default(desc)
// @Param page query string false "Opaque continuation token; keep path, order and limit unchanged"
// @Success 200 {object} v1.EnvironmentFileList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id}/files [get]
func (h *Handler) listEnvironmentFiles(w http.ResponseWriter, r *http.Request) {
	environment, err := h.store.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	options, ok := readEnvironmentFileQuery(w, r, environment)
	if !ok {
		return
	}
	if h.directoryReader == nil {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	result, err := h.directoryReader.ReadEnvironmentDirectory(r.Context(), environment, options.relativeDirectory)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if result.Truncated || !proto.ValidWorkspaceDirectory(&result, proto.WorkspaceDirectoryMaxEntries) {
		writeStoreError(w, r, execution.ErrExecutionUnavailable)
		return
	}
	files := make([]v1.EnvironmentFile, 0, len(result.Entries))
	for _, entry := range result.Entries {
		if entry.Kind == "file" {
			files = append(files, v1.EnvironmentFile{
				EnvironmentID: environment.ID, Object: "agent.environment.file",
				Path: path.Join(options.directory, entry.Name), SizeBytes: *entry.SizeBytes,
			})
		}
	}
	// All entries share one parent, so comparing their final components is sufficient.
	slices.SortFunc(files, func(a, b v1.EnvironmentFile) int {
		comparison := strings.Compare(a.Path, b.Path)
		if !options.ascending {
			return -comparison
		}
		return comparison
	})
	response, err := environmentFilePage(files, options)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
