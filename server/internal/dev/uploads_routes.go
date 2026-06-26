package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/oss"
)

// OSSClient is the narrow surface the dev router needs from the
// object-storage backend. The interface lives here (not in storage/oss)
// so dev never imports the concrete SDK.
type OSSClient interface {
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
	// Download fetches an object into memory; *oss.Client enforces a
	// 64 MiB in-memory cap.
	Download(ctx context.Context, key string) ([]byte, error)
}

// WithOSSClient wires the object-storage backend. When OSS isn't
// configured, pass nil — upload routes 503 with a clear message.
func WithOSSClient(c OSSClient) RouterOption {
	return func(cfg *routerConfig) {
		cfg.ossClient = c
	}
}

// presignUploadRequest is the JSON body of POST .../uploads/presign-upload.
// filename is preserved in the OSS key for human debugging only; prefix
// must be in the allowlist (today "plugin" or "skill").
type presignUploadRequest struct {
	Filename       string `json:"filename"`
	Prefix         string `json:"prefix"`
	ExpiresSeconds int    `json:"expiresSeconds,omitempty"`
}

type presignUploadResponse struct {
	UploadURL string    `json:"uploadUrl"`
	OssKey    string    `json:"ossKey"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// presignDownloadRequest is the JSON body of POST .../uploads/presign-download.
type presignDownloadRequest struct {
	OssKey         string `json:"ossKey"`
	ExpiresSeconds int    `json:"expiresSeconds,omitempty"`
}

type presignDownloadResponse struct {
	DownloadURL string    `json:"downloadUrl"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// allowedPrefixes is the closed set of OSS key prefixes the upload endpoint
// accepts. New types of uploads need an explicit entry here so a caller
// can't smuggle arbitrary keys ("../private/...") into the bucket.
var allowedPrefixes = map[string]string{
	"plugin": oss.PluginObjectPrefix,
	"skill":  oss.SkillObjectPrefix,
}

// presignUpload returns a V4-signed PUT URL the browser uses to upload
// directly to OSS. workspaceID is baked into the key path so later
// downloads can verify ownership without a side table.
//
// 503 if OSS isn't configured; 400 on missing/invalid prefix or filename;
// 500 on signer failure.
func presignUpload(runtimeStore RuntimeStore, client OSSClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		if client == nil {
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
		prefixKey := strings.TrimSpace(strings.ToLower(req.Prefix))
		base, ok := allowedPrefixes[prefixKey]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("prefix must be one of %s", knownPrefixes())})
			return
		}

		// Workspace-scoped key under the chosen prefix; known prefixes
		// route to their dedicated constructor so the in-bucket layout
		// matches what the import handler expects.
		var key string
		switch prefixKey {
		case "plugin":
			key = oss.NewPluginObjectKey(workspaceID, filename)
		case "skill":
			key = oss.NewSkillObjectKey(workspaceID, filename)
		default:
			// Defense in depth — the allowlist already gates this.
			key = oss.JoinKey(base, workspaceID, filename)
		}

		ttl := time.Duration(req.ExpiresSeconds) * time.Second
		uploadURL, expiresAt, err := client.PresignPut(r.Context(), key, ttl)
		if err != nil {
			log.Bg().Error("presign upload failed", "key", key, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate upload URL"})
			return
		}
		writeJSON(w, http.StatusOK, presignUploadResponse{
			UploadURL: uploadURL,
			OssKey:    key,
			ExpiresAt: expiresAt,
		})
	}
}

// presignDownload returns a V4-signed GET URL. Validates ossKey was
// minted under the calling workspace to close the cross-tenant read hole.
func presignDownload(runtimeStore RuntimeStore, client OSSClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		if client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
		var req presignDownloadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be JSON: " + err.Error()})
			return
		}
		key := strings.TrimSpace(req.OssKey)
		if key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ossKey is required"})
			return
		}
		if !oss.KeyBelongsToWorkspace(key, workspaceID) {
			// 403 over 404: the key path already encodes the workspace
			// so the mismatch is structural and not existence-leaking.
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "oss key does not belong to this workspace"})
			return
		}
		ttl := time.Duration(req.ExpiresSeconds) * time.Second
		downloadURL, expiresAt, err := client.PresignGet(r.Context(), key, ttl)
		if err != nil {
			// ErrInvalidKey leaks no secrets — the caller passed it.
			if errors.Is(err, oss.ErrInvalidKey) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			log.Bg().Error("presign download failed", "key", key, "error", err.Error())
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate download URL"})
			return
		}
		writeJSON(w, http.StatusOK, presignDownloadResponse{
			DownloadURL: downloadURL,
			ExpiresAt:   expiresAt,
		})
	}
}

func knownPrefixes() string {
	names := make([]string, 0, len(allowedPrefixes))
	for k := range allowedPrefixes {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}
