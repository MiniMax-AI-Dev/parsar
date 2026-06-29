package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestParsarDaemonDownloadRoute pins the local binary-serve endpoint so the
// one-line connect command can fetch the daemon from the minting server
// instead of a GitHub release. The os/arch allowlist is load-bearing: it
// stops a crafted `os=../../etc` from escaping BinaryDir.
func TestParsarDaemonDownloadRoute(t *testing.T) {
	t.Parallel()

	// Stage a binary dir with two of the four platform binaries present,
	// leaving linux/arm64 absent so the 404 path is exercised too.
	dir := t.TempDir()
	darwinArm64 := []byte("fake-darwin-arm64-binary\x00\x01\x02")
	linuxAmd64 := []byte("fake-linux-amd64-binary")
	if err := os.WriteFile(filepath.Join(dir, "parsar-daemon-darwin-arm64"), darwinArm64, 0o755); err != nil {
		t.Fatalf("write darwin binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "parsar-daemon-linux-amd64"), linuxAmd64, 0o755); err != nil {
		t.Fatalf("write linux binary: %v", err)
	}

	r := chi.NewRouter()
	RegisterParsarDaemonDownloadRoute(r, ParsarDaemonDownloadConfig{BinaryDir: dir})

	t.Run("serves darwin/arm64 bytes with attachment headers", func(t *testing.T) {
		t.Parallel()
		rec := daemonDownloadGet(r, "/api/v1/parsar-daemon/download?os=darwin&arch=arm64")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		if !bytes.Equal(body, darwinArm64) {
			t.Fatalf("body = %d bytes, want %d", len(body), len(darwinArm64))
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/octet-stream" {
			t.Fatalf("Content-Type = %q, want application/octet-stream", ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "parsar-daemon-darwin-arm64") {
			t.Fatalf("Content-Disposition = %q, want it to name the binary", cd)
		}
	})

	t.Run("serves linux/amd64 bytes", func(t *testing.T) {
		t.Parallel()
		rec := daemonDownloadGet(r, "/api/v1/parsar-daemon/download?os=linux&arch=amd64")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		if !bytes.Equal(body, linuxAmd64) {
			t.Fatalf("linux/amd64 body mismatch")
		}
	})

	t.Run("404 when that platform binary is not baked in", func(t *testing.T) {
		t.Parallel()
		rec := daemonDownloadGet(r, "/api/v1/parsar-daemon/download?os=linux&arch=arm64")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	badCases := []struct {
		name string
		path string
	}{
		{"missing os", "/api/v1/parsar-daemon/download?arch=arm64"},
		{"missing arch", "/api/v1/parsar-daemon/download?os=darwin"},
		{"unknown os", "/api/v1/parsar-daemon/download?os=windows&arch=amd64"},
		{"unknown arch", "/api/v1/parsar-daemon/download?os=linux&arch=riscv64"},
		{"path traversal via os", "/api/v1/parsar-daemon/download?os=..%2F..%2Fetc&arch=amd64"},
		{"path traversal via arch", "/api/v1/parsar-daemon/download?os=linux&arch=..%2F..%2Fpasswd"},
	}
	for _, tc := range badCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := daemonDownloadGet(r, tc.path)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func daemonDownloadGet(r http.Handler, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
