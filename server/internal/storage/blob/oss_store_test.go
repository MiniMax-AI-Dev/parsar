package blob

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/oss"
)

type fakeOSS struct {
	putURL, getURL string
	downloaded     []byte
	lastKey        string
}

func (f *fakeOSS) PresignPut(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	f.lastKey = key
	return f.putURL, time.Now().Add(ttl), nil
}
func (f *fakeOSS) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	f.lastKey = key
	return f.getURL, time.Now().Add(ttl), nil
}
func (f *fakeOSS) Download(ctx context.Context, key string) ([]byte, error) {
	f.lastKey = key
	return f.downloaded, nil
}

func TestOSSStoreNewRefUsesKeyBuilders(t *testing.T) {
	s := NewOSSStore(&fakeOSS{})
	ref, err := s.NewRef("plugin", "ws-1", "p.zip")
	if err != nil {
		t.Fatalf("NewRef: %v", err)
	}
	if !oss.KeyBelongsToWorkspace(ref, "ws-1") {
		t.Fatalf("plugin ref %q must belong to ws-1", ref)
	}
}

func TestOSSStoreUploadURLPresignsPUT(t *testing.T) {
	f := &fakeOSS{putURL: "https://oss/put"}
	s := NewOSSStore(f)
	spec, err := s.UploadURL(context.Background(), "capabilities/plugins/ws-1/x/p.zip", "ws-1", time.Minute)
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	if spec.URL != "https://oss/put" || spec.Method != "PUT" {
		t.Fatalf("bad spec: %+v", spec)
	}
	if spec.Headers["Content-Type"] != oss.PresignPutContentType {
		t.Fatalf("upload must require Content-Type %q, got %q", oss.PresignPutContentType, spec.Headers["Content-Type"])
	}
}

func TestOSSStoreDownloadURLPresignsGET(t *testing.T) {
	f := &fakeOSS{getURL: "https://oss/get"}
	s := NewOSSStore(f)
	spec, err := s.DownloadURL(context.Background(), "capabilities/plugins/ws-1/x/p.zip", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if spec.URL != "https://oss/get" || spec.Method != "GET" {
		t.Fatalf("bad spec: %+v", spec)
	}
}

func TestOSSStoreBelongsToWorkspaceUsesKeyShape(t *testing.T) {
	s := NewOSSStore(&fakeOSS{})
	ok, _ := s.BelongsToWorkspace(context.Background(), "capabilities/plugins/ws-1/x/p.zip", "ws-1")
	if !ok {
		t.Fatal("key under ws-1 must belong to ws-1")
	}
	ok, _ = s.BelongsToWorkspace(context.Background(), "capabilities/plugins/ws-1/x/p.zip", "ws-2")
	if ok {
		t.Fatal("key under ws-1 must not belong to ws-2")
	}
}
