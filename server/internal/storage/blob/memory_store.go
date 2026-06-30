package blob

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is an in-process Store for tests and for exercising the
// proxy handler without a database. Not used in production.
type MemoryStore struct {
	baseURL string
	mu      sync.Mutex
	bytes   map[string][]byte
	owner   map[string]string
}

// NewMemoryStore returns an empty MemoryStore. baseURL shapes the
// synthetic upload/download URLs.
func NewMemoryStore(baseURL string) *MemoryStore {
	return &MemoryStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		bytes:   map[string][]byte{},
		owner:   map[string]string{},
	}
}

func (m *MemoryStore) NewRef(kind, workspaceID, filename string) (string, error) {
	return "pg:" + uuid.NewString(), nil
}

func (m *MemoryStore) PutBytes(ctx context.Context, ref, workspaceID string, data []byte) error {
	if strings.TrimSpace(ref) == "" {
		return ErrInvalidRef
	}
	if int64(len(data)) > MaxBlobBytes {
		return ErrTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.bytes[ref] = cp
	m.owner[ref] = workspaceID
	return nil
}

func (m *MemoryStore) Download(ctx context.Context, ref string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bytes[ref]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

func (m *MemoryStore) BelongsToWorkspace(ctx context.Context, ref, workspaceID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.owner[ref]
	return ok && w == workspaceID, nil
}

func (m *MemoryStore) UploadURL(ctx context.Context, ref, workspaceID string, ttl time.Duration) (URLSpec, error) {
	return URLSpec{
		URL:     fmt.Sprintf("%s/internal/blobs/%s?token=mem", m.baseURL, ref),
		Method:  "PUT",
		Expires: time.Now().Add(ttl),
	}, nil
}

func (m *MemoryStore) DownloadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error) {
	return URLSpec{
		URL:     fmt.Sprintf("%s/internal/blobs/%s?token=mem", m.baseURL, ref),
		Method:  "GET",
		Expires: time.Now().Add(ttl),
	}, nil
}
