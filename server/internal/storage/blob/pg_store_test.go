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

// TestPGStoreZeroTTLUsesDefault guards the default-upload path: the browser
// presign flow sends no expiresSeconds, so the handler delegates ttl=0 to
// the store. Before normalizeProxyTTL this hit the signer's ttl<=0 guard and
// 500'd every PG upload. Both URL kinds must now mint a verifiable token
// carrying the 1h default expiry.
func TestPGStoreZeroTTLUsesDefault(t *testing.T) {
	signer := NewProxySigner("k")
	s := NewPGStore(newFakePGQ(), signer, "https://api.test")

	for _, tc := range []struct {
		name   string
		spec   func() (URLSpec, error)
		method string
	}{
		{"upload", func() (URLSpec, error) { return s.UploadURL(context.Background(), "pg:abc", "ws-1", 0) }, "PUT"},
		{"download", func() (URLSpec, error) { return s.DownloadURL(context.Background(), "pg:abc", 0) }, "GET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := tc.spec()
			if err != nil {
				t.Fatalf("ttl=0 must not error, got %v", err)
			}
			tok := spec.URL[strings.Index(spec.URL, "token=")+len("token="):]
			claims, err := signer.Verify(tok)
			if err != nil {
				t.Fatalf("token from ttl=0 must verify: %v", err)
			}
			if claims.Method != tc.method {
				t.Fatalf("method = %q, want %q", claims.Method, tc.method)
			}
			lifetime := time.Unix(claims.ExpiresAt, 0).Sub(time.Unix(claims.IssuedAt, 0))
			if lifetime != DefaultProxyTTL {
				t.Fatalf("token lifetime = %s, want DefaultProxyTTL %s", lifetime, DefaultProxyTTL)
			}
		})
	}
}

func TestNormalizeProxyTTL(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, DefaultProxyTTL},
		{-time.Hour, DefaultProxyTTL},
		{30 * time.Second, time.Minute},
		{10 * time.Minute, 10 * time.Minute},
		{MaxProxyTokenLifetime + time.Hour, MaxProxyTokenLifetime},
	} {
		if got := normalizeProxyTTL(tc.in); got != tc.want {
			t.Errorf("normalizeProxyTTL(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
