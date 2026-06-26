// SPA static-asset serving for the production / internal-deployment
// artifact.
//
// In `make dev` Vite serves the SPA on a different origin; the Go
// process never touches static files. In the production image the
// Vite dist is baked in and the Go process MUST serve it.
//
// MountStaticAssets is intentionally a no-op when the directory does
// not exist or is empty, so a misconfigured path still lets /healthz
// answer instead of refusing to boot.
//
// The handler re-checks the API prefix list every request (instead of
// using a chi middleware) so an unmatched `/api/v1/...` surfaces as a
// JSON 404 rather than an HTML index.html that breaks every API client.
package api

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// StaticAssetsOptions configures the SPA fallback handler. Dir must
// contain an index.html — MountStaticAssets verifies this once at
// startup so a typo'd path fails fast at boot.
type StaticAssetsOptions struct {
	// Dir is the absolute filesystem path holding the Vite build
	// artefacts. Empty disables the handler (`make dev` path).
	Dir string

	// Logger is called when the handler decides NOT to mount so the
	// operator sees a pointer to the problem at boot. nil is fine.
	Logger func(format string, args ...any)
}

// apiPathPrefixes lists routes the Go API owns. A request to any of
// these that misses a more specific chi route must surface as a JSON
// 404, NOT the SPA shell — API clients (curl, fetch, monitoring) need
// a real 404. Add new top-level route groups here in the same commit
// that registers the chi handler so the SPA can never shadow them.
//
// `/v1/` is defensive: today versioned routes live under `/api/v1/`,
// but reserving the bare form shields any future top-level v1 route.
// The OTLP receiver also exposes `/v1/...` but on its own port (:4318).
var apiPathPrefixes = []string{
	"/api/",
	"/dev/",
	"/v1/",
	"/healthz",
	"/readyz",
}

// MountStaticAssets installs the SPA fallback as chi's NotFound
// handler. Call AFTER every API route has been registered so the
// fallback only fires for paths the API does not own. Returns true
// when the handler was actually installed. A missing dir, non-dir,
// or missing index.html disables the handler and emits a warning via
// opts.Logger so the process stays up and /healthz still answers.
func MountStaticAssets(r chi.Router, opts StaticAssetsOptions) bool {
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if strings.TrimSpace(opts.Dir) == "" {
		return false
	}
	dir := opts.Dir
	info, err := os.Stat(dir)
	if err != nil {
		logf("static assets disabled: stat %s: %v", dir, err)
		return false
	}
	if !info.IsDir() {
		logf("static assets disabled: %s is not a directory", dir)
		return false
	}
	indexPath := filepath.Join(dir, "index.html")
	if _, err := os.Stat(indexPath); err != nil {
		logf("static assets disabled: %s missing (Vite build did not emit it?): %v", indexPath, err)
		return false
	}
	handler := newSPAHandler(dir, indexPath)
	r.NotFound(handler)
	return true
}

// newSPAHandler is split out so it can be unit-tested with httptest
// without spinning a full chi router.
func newSPAHandler(dir, indexPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Unmatched routes under an API prefix are real 404s, not
		// SPA routes — returning index.html here would break every
		// fetch() consumer the moment they hit a typo.
		if isAPIPath(r.URL.Path) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}` + "\n"))
			return
		}
		// Only GET/HEAD are served from the SPA; other methods on
		// an unknown path are method-level mistakes.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		// path.Clean collapses `..` so a curl `/etc/passwd`-style
		// payload can't escape `dir`. isInsideDir re-checks the
		// resolved absolute path as belt-and-braces against future
		// filepath quirks (Windows separators, symlinks).
		urlPath := path.Clean("/" + r.URL.Path)
		rel := strings.TrimPrefix(urlPath, "/")
		if rel == "" {
			http.ServeFile(w, r, indexPath)
			return
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if !isInsideDir(dir, target) {
			http.NotFound(w, r)
			return
		}
		info, err := os.Stat(target)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// SPA route — hand the React app its shell so
				// client-side routing can resolve the URL.
				http.ServeFile(w, r, indexPath)
				return
			}
			// Other errors (perm, EIO) are deployment failures,
			// not 404s; surface a 500 so the operator notices.
			http.Error(w, "static asset read failed", http.StatusInternalServerError)
			return
		}
		if info.IsDir() {
			// Asking for a directory is ambiguous; do not
			// auto-list. Let client routing decide what to render.
			http.ServeFile(w, r, indexPath)
			return
		}
		http.ServeFile(w, r, target)
	}
}

// isAPIPath returns true when path belongs to one of the API prefixes
// the Go server owns. Prefix-based.
func isAPIPath(p string) bool {
	for _, prefix := range apiPathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
		// Bare-prefix form covers `/api`, `/dev`, `/v1` (without
		// trailing slash) which aren't in the prefix table.
		if p == strings.TrimSuffix(prefix, "/") {
			return true
		}
	}
	return false
}

// isInsideDir defends against `../` traversal: a request like
// `/..%2f..%2fetc%2fpasswd` decodes + cleans into `/etc/passwd`, and
// filepath.Join would dutifully build `<dir>/etc/passwd`. On layouts
// where `<dir>` is a symlink or a relative path, the joined path can
// escape. Compare absolute forms with a strict prefix match.
func isInsideDir(dir, target string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	// Append a separator so `/srv/web` does NOT accidentally permit
	// `/srv/web-evil/...`.
	prefix := absDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return absTarget == absDir || strings.HasPrefix(absTarget, prefix)
}
