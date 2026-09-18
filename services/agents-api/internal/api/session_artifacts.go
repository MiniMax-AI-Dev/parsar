package api

import (
	"context"
	"io"
	"net/http"
	"path"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

type SessionArtifactStore interface {
	GetSessionArtifact(context.Context, string, string, string) (store.SessionArtifact, error)
	ListSessionArtifacts(context.Context, string, string, string, string, int, bool) (store.ArtifactPage, error)
	ReadSessionArtifact(context.Context, string, string, string, func(store.SessionArtifact, io.Reader) error) error
	DeleteSessionArtifact(context.Context, string, string, string) error
}

func WithSessionArtifacts(s SessionArtifactStore) Option {
	return func(h *Handler) { h.artifacts = s }
}

func (h *Handler) artifactsReady(w http.ResponseWriter, r *http.Request, list bool) bool {
	if h.artifacts == nil {
		writeError(w, http.StatusServiceUnavailable, "artifact_storage_unavailable", "Artifact storage is unavailable.")
		return false
	}
	if !list && r.URL.RawQuery != "" {
		writeStoreError(w, r, store.ErrInvalidInput)
		return false
	}
	return true
}

// @Summary List immutable Session artifacts
// @Description Lists published outputs independently of Environment availability. Sorting uses publication time and ID. The local default page size is 20; exact upstream defaults and error parity remain unverified.
// @Tags Artifacts
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param environment_id query string false "Producing Environment ID"
// @Param after query string false "Last immutable artifact ID"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Publication order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SessionArtifactList
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/artifacts [get]
func (h *Handler) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	if !h.artifactsReady(w, r, true) {
		return
	}
	options, ok := readPage(w, r, "environment_id")
	if !ok {
		return
	}
	page, err := h.artifacts.ListSessionArtifacts(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), r.URL.Query().Get("environment_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.SessionArtifactList{Data: make([]v1.SessionArtifact, 0, len(page.Artifacts)), HasMore: page.NextCursor != ""}
	for _, artifact := range page.Artifacts {
		response.Data = append(response.Data, artifactResponse(artifact))
	}
	writeJSON(w, http.StatusOK, response)
}

// @Summary Retrieve immutable artifact metadata
// @Tags Artifacts
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {object} v1.SessionArtifact
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/artifacts/{artifact_id} [get]
func (h *Handler) getSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.artifactsReady(w, r, false) {
		return
	}
	artifact, err := h.artifacts.GetSessionArtifact(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "artifact_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, artifactResponse(artifact))
}

// @Summary Delete a published artifact
// @Description Deletes the published copy without modifying its original workspace file. Already admitted content reads may finish; later reads reject.
// @Tags Artifacts
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {object} v1.SessionArtifactDeleted
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/artifacts/{artifact_id} [delete]
func (h *Handler) deleteSessionArtifact(w http.ResponseWriter, r *http.Request) {
	if !h.artifactsReady(w, r, false) {
		return
	}
	id := chi.URLParam(r, "artifact_id")
	if err := h.artifacts.DeleteSessionArtifact(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SessionArtifactDeleted{ID: id, Object: "agent.session.artifact.deleted", Deleted: true})
}

// @Summary Download immutable artifact bytes
// @Description Streams stored bytes after tenant and Session authorization, including after Environment expiration. Exact upstream headers and Range behavior remain unverified.
// @Tags Artifacts
// @Produce octet-stream
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param artifact_id path string true "Artifact ID"
// @Success 200 {file} binary
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/artifacts/{artifact_id}/content [get]
func (h *Handler) sessionArtifactContent(w http.ResponseWriter, r *http.Request) {
	if !h.artifactsReady(w, r, false) {
		return
	}
	serveStoredContent(w, r, func(ctx context.Context, consume func(string, int64, io.Reader) error) error {
		return h.artifacts.ReadSessionArtifact(ctx, tenantID(r), chi.URLParam(r, "session_id"), chi.URLParam(r, "artifact_id"), func(a store.SessionArtifact, body io.Reader) error {
			return consume(path.Base(a.Path), a.SizeBytes, body)
		})
	})
}

func artifactResponse(a store.SessionArtifact) v1.SessionArtifact {
	return v1.SessionArtifact{ID: a.ID, CreatedAt: a.CreatedAt.Unix(), EnvironmentID: a.EnvironmentID,
		Object: "agent.session.artifact", Path: a.Path, SessionID: a.SessionID, SizeBytes: a.SizeBytes, TurnID: a.TurnID}
}
