package blob

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreRoundTrip(t *testing.T) {
	s := NewMemoryStore("https://api.test")
	ref, err := s.NewRef("plugin", "ws-1", "thing.zip")
	if err != nil {
		t.Fatalf("NewRef: %v", err)
	}
	if ref == "" {
		t.Fatal("NewRef returned empty ref")
	}
	payload := []byte("zip-bytes")
	if err := s.PutBytes(context.Background(), ref, "ws-1", payload); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	got, err := s.Download(context.Background(), ref)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("round trip mismatch: got %q want %q", got, payload)
	}
}

func TestMemoryStoreDownloadMissing(t *testing.T) {
	s := NewMemoryStore("https://api.test")
	if _, err := s.Download(context.Background(), "pg:nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMemoryStoreBelongsToWorkspace(t *testing.T) {
	s := NewMemoryStore("https://api.test")
	ref, _ := s.NewRef("plugin", "ws-1", "a.zip")
	_ = s.PutBytes(context.Background(), ref, "ws-1", []byte("x"))
	ok, err := s.BelongsToWorkspace(context.Background(), ref, "ws-1")
	if err != nil || !ok {
		t.Fatalf("want owned by ws-1, got ok=%v err=%v", ok, err)
	}
	ok, _ = s.BelongsToWorkspace(context.Background(), ref, "ws-2")
	if ok {
		t.Fatal("ref must not belong to ws-2")
	}
}

func TestMemoryStoreURLSpecs(t *testing.T) {
	s := NewMemoryStore("https://api.test")
	up, err := s.UploadURL(context.Background(), "pg:abc", "ws-1", time.Minute)
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	if up.Method != "PUT" || up.URL == "" {
		t.Fatalf("bad upload spec: %+v", up)
	}
	dn, err := s.DownloadURL(context.Background(), "pg:abc", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if dn.Method != "GET" || dn.URL == "" {
		t.Fatalf("bad download spec: %+v", dn)
	}
}
