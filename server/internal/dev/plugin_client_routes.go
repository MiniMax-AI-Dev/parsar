package dev

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

// WithPluginsDir sets the on-disk directory where installed plugin bundles
// live. The GET /api/v1/plugins/{pluginName}/client.js handler reads from
// this directory. Pass empty string to disable plugin client serving.
func WithPluginsDir(dir string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.pluginsDir = dir
	}
}

// servePluginClient handles GET /api/v1/plugins/{pluginName}/client.js.
// Reads the built client bundle from <pluginsDir>/<pluginName>/dist/client.js
// and returns it with Content-Type: application/javascript.
//
//	@Summary		Serve plugin client bundle
//	@Description	Returns the built client.js for a plugin. Used by the web UI to load plugin UI components at runtime.
//	@Tags			plugins
//	@ID				servePluginClientBundle
//	@Produce		application/javascript
//	@Param			pluginName	path	string	true	"Plugin directory name (scope-stripped, e.g. hotel-ops)"
//	@Success		200	{string}	string	"JavaScript bundle"
//	@Failure		404	{object}	map[string]string	"Plugin or client bundle not found"
//	@Failure		503	{object}	map[string]string	"Plugin serving not configured"
//	@Router			/api/v1/plugins/{pluginName}/client.js [get]
func servePluginClient(pluginsDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pluginsDir == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "plugin client serving is not configured",
			})
			return
		}

		pluginName := strings.TrimSpace(chi.URLParam(r, "pluginName"))
		if pluginName == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "plugin name is required",
			})
			return
		}

		// Security: ensure pluginName doesn't escape the plugins directory.
		if strings.Contains(pluginName, "..") || strings.Contains(pluginName, "/") || strings.Contains(pluginName, "\\") {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid plugin name",
			})
			return
		}

		// Try dist/client.js first (built output), then client/dist/client.js.
		candidates := []string{
			filepath.Join(pluginsDir, pluginName, "dist", "client.js"),
			filepath.Join(pluginsDir, pluginName, "client", "dist", "client.js"),
		}

		for _, path := range candidates {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, max-age=0")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}

		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "plugin client bundle not found",
		})
	}
}
