package dev

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
)

// WithBlobStore wires the capability-blob backend (OSS or PG). Pass nil
// when the selected backend is unavailable (OSS unconfigured, or no DB
// pool for PG) — upload/download routes then 503 with a clear message.
func WithBlobStore(s blob.Store) RouterOption {
	return func(cfg *routerConfig) {
		cfg.blobStore = s
	}
}

type presignUploadRequest struct {
	Filename       string `json:"filename"`
	Prefix         string `json:"prefix"`
	ExpiresSeconds int    `json:"expiresSeconds,omitempty"`
}

// presignUploadResponse carries the unified upload contract. method +
// headers are backend-specific (OSS: PUT + Content-Type; PG: PUT with the
// token baked into uploadUrl). ossKey is retained — now a generic ref —
// so existing clients keep working.
type presignUploadResponse struct {
	UploadURL string            `json:"uploadUrl"`
	OssKey    string            `json:"ossKey"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type presignDownloadRequest struct {
	OssKey         string `json:"ossKey"`
	ExpiresSeconds int    `json:"expiresSeconds,omitempty"`
}

type presignDownloadResponse struct {
	DownloadURL string            `json:"downloadUrl"`
	Method      string            `json:"method"`
	Headers     map[string]string `json:"headers,omitempty"`
	ExpiresAt   time.Time         `json:"expiresAt"`
}

// allowedKinds gates the upload endpoint at the API layer so a caller can't
// smuggle an arbitrary kind past the PG backend, whose NewRef ignores kind.
var allowedKinds = map[string]struct{}{
	"plugin": {},
	"skill":  {},
}

// presignUpload returns where/how the browser PUTs the zip bytes. The ref
// is minted by the backend (OSS key or pg:<uuid>) with workspaceID bound
// in so later downloads can verify ownership.
//
//	@Summary		Presign a plugin/skill upload
//	@Description	Returns a presigned URL the browser PUTs the plugin/skill zip to. The blob backend (OSS or PG) mints a workspace-scoped ref that later downloads verify against. Caller must be workspace capability admin.
//	@Tags			uploads
//	@ID				createDevWorkspaceUploadPresign
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path		string					true	"Workspace UUID"
//	@Param			body		body		presignUploadRequest	true	"Presign upload payload"
//	@Success		200			{object}	presignUploadResponse	"Presigned upload spec"
//	@Failure		400			{object}	map[string]string		"Body invalid, filename empty, or prefix not in {plugin,skill}"
//	@Failure		403			{object}	map[string]string		"Caller lacks capability admin permission"
//	@Failure		500			{object}	map[string]string		"Blob backend error"
//	@Failure		503			{object}	map[string]string		"Object storage not configured"
//	@Router			/api/v1/workspaces/{workspaceID}/uploads/presign-upload [post]
func presignUpload(runtimeStore RuntimeStore, store blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		if store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
			return
		}
		// 4 KiB bound on a malicious admin OOM attempt; generous for a
		// filename + prefix envelope.
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		var req presignUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be JSON: " + err.Error()})
			return
		}
		filename := strings.TrimSpace(req.Filename)
		if filename == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "filename is required"})
			return
		}
		kind := strings.TrimSpace(strings.ToLower(req.Prefix))
		if _, ok := allowedKinds[kind]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("prefix must be one of %s", knownKinds())})
			return
		}

		ref, err := store.NewRef(kind, workspaceID, filename)
		if err != nil {
			log.Bg().Error("blob new ref failed", "kind", kind, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not allocate storage reference"})
			return
		}

		ttl := time.Duration(req.ExpiresSeconds) * time.Second
		spec, err := store.UploadURL(r.Context(), ref, workspaceID, ttl)
		if err != nil {
			log.Bg().Error("presign upload failed", "ref", ref, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate upload URL"})
			return
		}
		writeJSON(w, http.StatusOK, presignUploadResponse{
			UploadURL: spec.URL,
			OssKey:    ref,
			Method:    spec.Method,
			Headers:   spec.Headers,
			ExpiresAt: spec.Expires,
		})
	}
}

// presignDownload returns where/how a client GETs the bytes. Validates the
// ref belongs to the calling workspace to close the cross-tenant read hole.
//
//	@Summary		Presign a plugin/skill download
//	@Description	Returns a presigned URL for downloading a previously-uploaded plugin/skill zip. Validates the ref belongs to the calling workspace (403 on mismatch) to close the cross-tenant read hole. Caller must be workspace capability admin.
//	@Tags			uploads
//	@ID				createDevWorkspaceDownloadPresign
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path		string						true	"Workspace UUID"
//	@Param			body		body		presignDownloadRequest		true	"Presign download payload"
//	@Success		200			{object}	presignDownloadResponse		"Presigned download spec"
//	@Failure		400			{object}	map[string]string			"Body invalid or ossKey empty"
//	@Failure		403			{object}	map[string]string			"Ref does not belong to this workspace"
//	@Failure		500			{object}	map[string]string			"Blob backend error"
//	@Failure		503			{object}	map[string]string			"Object storage not configured"
//	@Router			/api/v1/workspaces/{workspaceID}/uploads/presign-download [post]
func presignDownload(runtimeStore RuntimeStore, store blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		if store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		var req presignDownloadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be JSON: " + err.Error()})
			return
		}
		ref := strings.TrimSpace(req.OssKey)
		if ref == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ossKey is required"})
			return
		}
		owned, err := store.BelongsToWorkspace(r.Context(), ref, workspaceID)
		if err != nil {
			log.Bg().Error("blob ownership check failed", "ref", ref, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not verify storage reference"})
			return
		}
		if !owned {
			// 403 over 404: the ref structurally encodes the workspace, so
			// the mismatch is not existence-leaking.
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage reference does not belong to this workspace"})
			return
		}
		ttl := time.Duration(req.ExpiresSeconds) * time.Second
		spec, err := store.DownloadURL(r.Context(), ref, ttl)
		if err != nil {
			log.Bg().Error("presign download failed", "ref", ref, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate download URL"})
			return
		}
		writeJSON(w, http.StatusOK, presignDownloadResponse{
			DownloadURL: spec.URL,
			Method:      spec.Method,
			Headers:     spec.Headers,
			ExpiresAt:   spec.Expires,
		})
	}
}

func knownKinds() string {
	names := make([]string, 0, len(allowedKinds))
	for k := range allowedKinds {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}
