package blob

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

type fakePGQ struct {
	rows map[string]sqlc.InsertCapabilityBlobParams
}

func newFakePGQ() *fakePGQ { return &fakePGQ{rows: map[string]sqlc.InsertCapabilityBlobParams{}} }

func (f *fakePGQ) InsertCapabilityBlob(ctx context.Context, arg sqlc.InsertCapabilityBlobParams) error {
	f.rows[arg.StorageRef] = arg
	return nil
}
func (f *fakePGQ) GetCapabilityBlobBytes(ctx context.Context, storageRef string) ([]byte, error) {
	r, ok := f.rows[storageRef]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return r.Bytes, nil
}
func (f *fakePGQ) GetCapabilityBlobMeta(ctx context.Context, storageRef string) (sqlc.GetCapabilityBlobMetaRow, error) {
	r, ok := f.rows[storageRef]
	if !ok {
		return sqlc.GetCapabilityBlobMetaRow{}, pgx.ErrNoRows
	}
	return sqlc.GetCapabilityBlobMetaRow{Sha256: r.Sha256, SizeBytes: r.SizeBytes, WorkspaceID: r.WorkspaceID}, nil
}

func newTestPGStore() (*PGStore, *fakePGQ) {
	q := newFakePGQ()
	return NewPGStore(q, NewProxySigner("k"), "https://api.test"), q
}

func TestPGStorePutDownloadRoundTrip(t *testing.T) {
	s, _ := newTestPGStore()
	ref, _ := s.NewRef("plugin", "ws-1", "p.zip")
	if !strings.HasPrefix(ref, "pg:") {
		t.Fatalf("PG ref must be pg:<uuid>, got %q", ref)
	}
	if err := s.PutBytes(context.Background(), ref, "ws-1", []byte("zipdata")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	got, err := s.Download(context.Background(), ref)
	if err != nil || string(got) != "zipdata" {
		t.Fatalf("Download: got %q err %v", got, err)
	}
}

func TestPGStorePutRejectsTooLarge(t *testing.T) {
	s, _ := newTestPGStore()
	big := make([]byte, MaxBlobBytes+1)
	if err := s.PutBytes(context.Background(), "pg:x", "ws-1", big); err != ErrTooLarge {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestPGStoreDownloadMissingIsNotFound(t *testing.T) {
	s, _ := newTestPGStore()
	if _, err := s.Download(context.Background(), "pg:missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestPGStoreURLSpecsCarryVerifiableToken(t *testing.T) {
	signer := NewProxySigner("k")
	s := NewPGStore(newFakePGQ(), signer, "https://api.test")
	up, err := s.UploadURL(context.Background(), "pg:abc", "ws-1", time.Minute)
	if err != nil {
		t.Fatalf("UploadURL: %v", err)
	}
	if up.Method != "PUT" || !strings.Contains(up.URL, "/internal/blobs/pg:abc?token=") {
		t.Fatalf("bad upload url: %s", up.URL)
	}
	tok := up.URL[strings.Index(up.URL, "token=")+len("token="):]
	claims, err := signer.Verify(tok)
	if err != nil {
		t.Fatalf("upload token must verify: %v", err)
	}
	if claims.Ref != "pg:abc" || claims.WorkspaceID != "ws-1" || claims.Method != "PUT" {
		t.Fatalf("upload token claims mismatch: %+v", claims)
	}
	dn, err := s.DownloadURL(context.Background(), "pg:abc", time.Minute)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if dn.Method != "GET" {
		t.Fatalf("download method: %s", dn.Method)
	}
}

func TestPGStoreBelongsToWorkspace(t *testing.T) {
	s, _ := newTestPGStore()
	ref, _ := s.NewRef("plugin", "ws-1", "p.zip")
	_ = s.PutBytes(context.Background(), ref, "ws-1", []byte("x"))
	ok, _ := s.BelongsToWorkspace(context.Background(), ref, "ws-1")
	if !ok {
		t.Fatal("must belong to ws-1")
	}
	ok, _ = s.BelongsToWorkspace(context.Background(), ref, "ws-2")
	if ok {
		t.Fatal("must not belong to ws-2")
	}
}
