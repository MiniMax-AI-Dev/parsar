package blob

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/oss"
)

// ossBackend is the slice of *oss.Client that OSSStore needs. Defined
// here so the blob package depends on the narrow surface, not the SDK.
type ossBackend interface {
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error)
	Download(ctx context.Context, key string) ([]byte, error)
}

// OSSStore is the hosted-edition backend: refs are OSS object keys and
// clients talk to OSS directly via presigned URLs.
type OSSStore struct {
	oss ossBackend
}

// NewOSSStore wraps an OSS client (*oss.Client satisfies ossBackend).
func NewOSSStore(backend ossBackend) *OSSStore { return &OSSStore{oss: backend} }

func (s *OSSStore) NewRef(kind, workspaceID, filename string) (string, error) {
	switch kind {
	case "plugin":
		return oss.NewPluginObjectKey(workspaceID, filename), nil
	case "skill":
		return oss.NewSkillObjectKey(workspaceID, filename), nil
	default:
		return "", ErrInvalidRef
	}
}

func (s *OSSStore) UploadURL(ctx context.Context, ref, workspaceID string, ttl time.Duration) (URLSpec, error) {
	url, exp, err := s.oss.PresignPut(ctx, ref, ttl)
	if err != nil {
		return URLSpec{}, err
	}
	return URLSpec{
		URL:     url,
		Method:  "PUT",
		Headers: map[string]string{"Content-Type": oss.PresignPutContentType},
		Expires: exp,
	}, nil
}

func (s *OSSStore) DownloadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error) {
	url, exp, err := s.oss.PresignGet(ctx, ref, ttl)
	if err != nil {
		return URLSpec{}, err
	}
	return URLSpec{URL: url, Method: "GET", Expires: exp}, nil
}

func (s *OSSStore) Download(ctx context.Context, ref string) ([]byte, error) {
	return s.oss.Download(ctx, ref)
}

func (s *OSSStore) BelongsToWorkspace(ctx context.Context, ref, workspaceID string) (bool, error) {
	return oss.KeyBelongsToWorkspace(ref, workspaceID), nil
}
