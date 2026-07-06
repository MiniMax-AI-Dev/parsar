// OpenAPI spec + human-readable docs UI.
//
// docs/openapi/openapi.yaml is the contract source of truth (hand-maintained,
// reviewed in PRs). This file mounts two always-on routes so the contract
// stops being a hidden artefact:
//
//   GET /api/v1/openapi.yaml — raw spec, primary source for tooling
//                              (openapi-typescript, oapi-codegen, curl).
//   GET /docs                — Stoplight Elements single-page viewer that
//                              fetches the spec above. Zero build step,
//                              no node_modules in the Go image.
//
// The spec path is resolved at mount time so a missing or stale file fails
// at boot rather than 404ing in production. Empty PARSAR_OPENAPI_SPEC plus
// no fallback file leaves both routes unmounted — a deliberate no-op that
// keeps a misconfigured deployment from crashing.
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// DocsOptions configures OpenAPI exposure.
type DocsOptions struct {
	// SpecPath is the absolute path to openapi.yaml. Empty disables both
	// routes. Resolved at RegisterDocsRoutes time; the file is read on
	// every request so an in-place edit during `make dev` is visible
	// without restarting the server.
	SpecPath string

	// Title overrides the docs page <title>. Empty falls back to
	// "Parsar API".
	Title string

	// Logger receives a one-line notice when the routes do not mount
	// (missing spec, unreadable file). nil is fine.
	Logger func(format string, args ...any)
}

// ResolveOpenAPISpecPath finds the spec file using the same precedence
// the rest of the dev tooling follows: explicit env var, then the
// repo-relative default. Returns "" when nothing exists so callers can
// disable the routes cleanly.
func ResolveOpenAPISpecPath() string {
	if p := strings.TrimSpace(os.Getenv("PARSAR_OPENAPI_SPEC")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Walk upward from CWD looking for docs/openapi/openapi.yaml. This
	// makes `make server` (CWD=server/) and a binary launched from the
	// repo root both work without per-environment config.
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "docs", "openapi", "openapi.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// RegisterDocsRoutes mounts /api/v1/openapi.yaml and /docs. Returns true
// when the routes were installed (spec resolved + readable).
func RegisterDocsRoutes(r chi.Router, opts DocsOptions) bool {
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	spec := strings.TrimSpace(opts.SpecPath)
	if spec == "" {
		logf("openapi docs disabled: spec path is empty")
		return false
	}
	if _, err := os.Stat(spec); err != nil {
		logf("openapi docs disabled: stat %s: %v", spec, err)
		return false
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "Parsar API"
	}
	r.Get("/api/v1/openapi.yaml", serveOpenAPISpec(spec))
	r.Get("/docs", serveDocsUI(title))
	return true
}

func serveOpenAPISpec(path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Re-read on every request: cheap (6k lines), and lets the
		// human-in-the-loop iterate on the yaml without restarting Go.
		// In production the file is read-only inside the image so this
		// is still effectively cached by the page cache.
		data, err := os.ReadFile(path)
		if err != nil {
			http.Error(w, "openapi spec unavailable", http.StatusInternalServerError)
			return
		}
		// `application/yaml` is the RFC 9512 media type. Stoplight
		// Elements and openapi-typescript both accept it.
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

// docsHTML is the Swagger UI single-page viewer. Pinned to a concrete
// version so a CDN upgrade cannot silently change the rendering. Left
// sidebar shows METHOD + PATH for every operation, which is what
// developers scan for when hunting an endpoint.
const docsHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width,initial-scale=1" />
<title>{{TITLE}}</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" />
<style>html,body{margin:0;height:100%;}#ui{height:100%;}</style>
</head>
<body>
<div id="ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
<script>
  window.ui = SwaggerUIBundle({
    url: "/api/v1/openapi.yaml",
    dom_id: "#ui",
    deepLinking: true,
    docExpansion: "list",
    operationsSorter: "alpha",
    tagsSorter: "alpha",
  });
</script>
</body>
</html>`

func serveDocsUI(title string) http.HandlerFunc {
	body := []byte(renderDocsHTML(title))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The HTML is tiny and references a versioned CDN bundle that
		// is itself cache-busted by version, so a short browser cache
		// is fine.
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
	}
}

// renderDocsHTML is split out so unit tests can assert on the wired
// title without parsing HTML at request time.
func renderDocsHTML(title string) string {
	// Minimal escape: title is operator-supplied, never user-supplied,
	// but treat it carefully anyway. The only character that can break
	// out of the <title> tag is `<`.
	safe := strings.ReplaceAll(title, "<", "&lt;")
	return strings.Replace(docsHTML, "{{TITLE}}", safe, 1)
}
