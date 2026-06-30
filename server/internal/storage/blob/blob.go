// Package blob abstracts capability plugin/skill zip storage behind a
// pluggable backend. OSSStore keeps the existing Aliyun OSS behaviour;
// PGStore stores bytes in Postgres so the open-source edition needs no
// external object store. The backend is chosen by PARSAR_BLOB_BACKEND.
package blob

import (
	"context"
	"errors"
	"time"
)

// MaxBlobBytes caps a single stored object at 64 MiB, matching the OSS
// client's in-memory download cap. Enforced at write time (proxy PUT and
// server-side Put), not by a DB constraint.
const MaxBlobBytes int64 = 64 * 1024 * 1024

var (
	// ErrNotFound is returned by Download when the ref has no stored bytes.
	ErrNotFound = errors.New("blob: ref not found")
	// ErrTooLarge is returned when an object exceeds MaxBlobBytes.
	ErrTooLarge = errors.New("blob: object exceeds max size")
	// ErrInvalidRef is returned for an empty or malformed ref.
	ErrInvalidRef = errors.New("blob: invalid ref")
	// ErrUnsupported is returned by a backend that cannot serve an op
	// (e.g. server-side Download on a presign-only OSS deployment path).
	ErrUnsupported = errors.New("blob: operation not supported by backend")
)

// URLSpec is the unified upload/download contract returned to clients.
// OSS fills a presigned URL; PG fills a proxy-endpoint URL + token query.
// Headers carries any request headers the client MUST send (OSS sets
// Content-Type; PG leaves it empty — the token rides in the URL).
type URLSpec struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	Expires time.Time         `json:"expires"`
}

// Store is the pluggable capability-blob backend.
type Store interface {
	// NewRef mints a fresh backend-specific storage reference for an
	// upload. kind is "plugin" or "skill"; workspaceID + filename shape
	// OSS keys (PG ignores them and returns "pg:<uuid>").
	NewRef(kind, workspaceID, filename string) (string, error)

	// UploadURL returns where/how the browser PUTs the zip bytes for ref.
	// workspaceID is bound into the PG proxy token so the eventual write
	// persists ownership; OSS ignores it (the key already encodes it).
	UploadURL(ctx context.Context, ref, workspaceID string, ttl time.Duration) (URLSpec, error)

	// DownloadURL returns where/how a client (the daemon) GETs the bytes.
	DownloadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error)

	// Download fetches the object into memory (server-side import path).
	Download(ctx context.Context, ref string) ([]byte, error)

	// BelongsToWorkspace reports whether ref is owned by workspaceID,
	// reproducing the cross-tenant gate the OSS key shape provides.
	BelongsToWorkspace(ctx context.Context, ref, workspaceID string) (bool, error)
}
