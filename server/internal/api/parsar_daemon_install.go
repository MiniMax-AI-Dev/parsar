package api

import (
	_ "embed"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

//go:embed install_parsar_daemon.sh
var installParsarDaemonScript string

type ParsarDaemonInstallConfig struct {
	// Repo is the GitHub `owner/name` slug whose Releases the script
	// downloads from. Empty keeps the script's built-in placeholder.
	Repo string
}

// RegisterParsarDaemonInstallRoute serves install-parsar-daemon.sh verbatim.
// Unauthenticated: the script has no secrets and only pulls public
// GitHub Release artifacts.
func RegisterParsarDaemonInstallRoute(r chi.Router, cfg ParsarDaemonInstallConfig) {
	script := installParsarDaemonScript
	if repo := strings.TrimSpace(cfg.Repo); repo != "" {
		// Replace only the DEFAULT_REPO assignment, not the case-pattern
		// marker that gates the fallback. A blanket ReplaceAll would
		// rewrite both and re-trigger the fallback path.
		script = strings.Replace(script,
			`DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`,
			fmt.Sprintf(`DEFAULT_REPO=%q`, repo),
			1)
	}
	r.Get("/api/v1/parsar-daemon/install.sh", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(script))
	})
}
