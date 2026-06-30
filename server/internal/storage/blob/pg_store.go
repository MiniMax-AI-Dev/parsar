package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

// pgBlobQuerier is the slice of sqlc the PG backend needs. *sqlc.Queries
// satisfies it; tests use a fake.
type pgBlobQuerier interface {
	InsertCapabilityBlob(ctx context.Context, arg sqlc.InsertCapabilityBlobParams) error
	GetCapabilityBlobBytes(ctx context.Context, storageRef string) ([]byte, error)
	GetCapabilityBlobMeta(ctx context.Context, storageRef string) (sqlc.GetCapabilityBlobMetaRow, error)
}

// PGStore is the open-source backend: zip bytes live in capability_blob;
// clients exchange bytes through the authenticated /internal/blobs proxy.
type PGStore struct {
	q       pgBlobQuerier
	signer  *ProxySigner
	baseURL string
}

// NewPGStore builds a PG backend. baseURL is the daemon-reachable origin
// the proxy endpoint is served under (no trailing slash required).
func NewPGStore(q pgBlobQuerier, signer *ProxySigner, baseURL string) *PGStore {
	return &PGStore{q: q, signer: signer, baseURL: strings.TrimRight(baseURL, "/")}
}

func (s *PGStore) NewRef(kind, workspaceID, filename string) (string, error) {
	return "pg:" + uuid.NewString(), nil
}

// PutBytes persists data under ref for workspaceID. Server-side only
// (proxy PUT handler). Enforces the size cap and stores the sha256.
func (s *PGStore) PutBytes(ctx context.Context, ref, workspaceID string, data []byte) error {
	if strings.TrimSpace(ref) == "" {
		return ErrInvalidRef
	}
	if int64(len(data)) > MaxBlobBytes {
		return ErrTooLarge
	}
	sum := sha256.Sum256(data)
	return s.q.InsertCapabilityBlob(ctx, sqlc.InsertCapabilityBlobParams{
		StorageRef:  ref,
		WorkspaceID: workspaceID,
		Bytes:       data,
		Sha256:      hex.EncodeToString(sum[:]),
		SizeBytes:   int64(len(data)),
	})
}

func (s *PGStore) Download(ctx context.Context, ref string) ([]byte, error) {
	b, err := s.q.GetCapabilityBlobBytes(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("blob: pg download: %w", err)
	}
	return b, nil
}

func (s *PGStore) BelongsToWorkspace(ctx context.Context, ref, workspaceID string) (bool, error) {
	meta, err := s.q.GetCapabilityBlobMeta(ctx, ref)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("blob: pg meta: %w", err)
	}
	return meta.WorkspaceID == workspaceID, nil
}

func (s *PGStore) UploadURL(ctx context.Context, ref, workspaceID string, ttl time.Duration) (URLSpec, error) {
	return s.proxyURL(ref, workspaceID, "PUT", ttl)
}

func (s *PGStore) DownloadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error) {
	// The download token's workspace claim is informational for GET; the
	// daemon path is already server-authorized. We still bind it so the
	// proxy can log/scope. Look up the owner so the claim is accurate.
	ws := ""
	if meta, err := s.q.GetCapabilityBlobMeta(ctx, ref); err == nil {
		ws = meta.WorkspaceID
	}
	if ws == "" {
		ws = "_unknown"
	}
	return s.proxyURL(ref, ws, "GET", ttl)
}

func (s *PGStore) proxyURL(ref, workspaceID, method string, ttl time.Duration) (URLSpec, error) {
	// The browser presign flow omits expiresSeconds, so the handler hands
	// ttl=0 to the store. OSS absorbs that (oss.normalizeTTL); the PG signer
	// rejects ttl<=0, which would 500 every default upload. Normalise here so
	// both backends share one default-TTL contract and Expires matches the
	// lifetime actually signed into the token.
	ttl = normalizeProxyTTL(ttl)
	tok, err := s.signer.Sign(ProxyClaims{Ref: ref, WorkspaceID: workspaceID, Method: method}, ttl)
	if err != nil {
		return URLSpec{}, err
	}
	return URLSpec{
		URL:     fmt.Sprintf("%s/internal/blobs/%s?token=%s", s.baseURL, ref, tok),
		Method:  method,
		Expires: time.Now().Add(ttl),
	}, nil
}

// DefaultProxyTTL mirrors oss.DefaultPresignTTL so PG and OSS hand out
// equal-lifetime URLs when the client omits expiresSeconds.
const DefaultProxyTTL = time.Hour

// normalizeProxyTTL clamps a caller TTL with the same rules as
// oss.normalizeTTL (ttl<=0 → default, sub-minute → a minute, over-cap →
// MaxProxyTokenLifetime) so switching backends never changes URL lifetimes.
func normalizeProxyTTL(ttl time.Duration) time.Duration {
	switch {
	case ttl <= 0:
		return DefaultProxyTTL
	case ttl < time.Minute:
		return time.Minute
	case ttl > MaxProxyTokenLifetime:
		return MaxProxyTokenLifetime
	default:
		return ttl
	}
}
