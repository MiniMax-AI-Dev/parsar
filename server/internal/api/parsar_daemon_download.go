package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// defaultDaemonBinaryDir is where the image bakes the cross-compiled daemon
// binaries (see Dockerfile runtime stage). Overridable so self-host images
// or tests can point elsewhere.
const defaultDaemonBinaryDir = "/usr/local/share/parsar/daemon"

// daemonBinaryOS / daemonBinaryArch are the only values the install script
// ever sends (it normalizes uname output to these). Treating them as a fixed
// allowlist is what keeps a crafted os/arch from escaping BinaryDir.
var (
	daemonBinaryOS   = map[string]bool{"darwin": true, "linux": true}
	daemonBinaryArch = map[string]bool{"amd64": true, "arm64": true}
)

type ParsarDaemonDownloadConfig struct {
	// BinaryDir holds the per-platform parsar-daemon binaries named
	// parsar-daemon-<os>-<arch>. Empty falls back to defaultDaemonBinaryDir.
	BinaryDir string
}

// RegisterParsarDaemonDownloadRoute serves the host-appropriate parsar-daemon
// binary baked into the image, so the one-line connect command can fetch it
// from the minting server (PARSAR_DAEMON_CONNECT_URL) instead of a GitHub
// release. Unauthenticated: the binary is public; the pairing token is what
// gates connecting.
func RegisterParsarDaemonDownloadRoute(r chi.Router, cfg ParsarDaemonDownloadConfig) {
	dir := cfg.BinaryDir
	if dir == "" {
		dir = defaultDaemonBinaryDir
	}
	r.Get("/api/v1/parsar-daemon/download", parsarDaemonDownloadHandler(dir))
}

// parsarDaemonDownloadHandler streams the parsar-daemon binary matching the
// caller's os/arch pair. Extracted so swag can attach annotations to a named
// function.
//
//	@Summary	Download parsar-daemon binary
//	@Description	Serves the parsar-daemon binary for the requested os/arch pair from the image's baked-in binary directory. Unauthenticated — the pairing token is what gates the actual connect.
//	@Tags		runtimes
//	@ID			downloadParsarDaemon
//	@Produce	octet-stream
//	@Param		os query string true "target GOOS" Enums(darwin, linux)
//	@Param		arch query string true "target GOARCH" Enums(amd64, arm64)
//	@Success	200 {file} binary "parsar-daemon binary stream"
//	@Failure	400 {string} string "os/arch not in the accepted allowlist"
//	@Failure	404 {string} string "no binary for that os/arch in this image"
//	@Router		/api/v1/parsar-daemon/download [get]
func parsarDaemonDownloadHandler(dir string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		goos := req.URL.Query().Get("os")
		goarch := req.URL.Query().Get("arch")
		if !daemonBinaryOS[goos] || !daemonBinaryArch[goarch] {
			http.Error(w, "os must be one of [darwin linux] and arch one of [amd64 arm64]", http.StatusBadRequest)
			return
		}

		name := "parsar-daemon-" + goos + "-" + goarch
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			http.Error(w, "no parsar-daemon binary for "+goos+"/"+goarch+" in this image", http.StatusNotFound)
			return
		}
		defer func() { _ = f.Close() }()

		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			http.Error(w, "no parsar-daemon binary for "+goos+"/"+goarch+" in this image", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, req, name, stat.ModTime(), f)
	}
}
