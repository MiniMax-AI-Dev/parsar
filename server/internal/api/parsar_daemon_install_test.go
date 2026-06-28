package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestParsarDaemonInstallRoute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		cfg         ParsarDaemonInstallConfig
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "default_keeps_placeholder",
			cfg:         ParsarDaemonInstallConfig{},
			wantContain: `DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`,
		},
		{
			name:        "configured_repo_substituted",
			cfg:         ParsarDaemonInstallConfig{Repo: "acme/parsar"},
			wantContain: `DEFAULT_REPO="acme/parsar"`,
			wantAbsent:  `DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`,
		},
		{
			name:        "whitespace_repo_treated_as_empty",
			cfg:         ParsarDaemonInstallConfig{Repo: "   "},
			wantContain: `DEFAULT_REPO="__PARSAR_DAEMON_REPO__"`,
		},
		{
			name:        "configured_repo_preserves_fallback_marker",
			cfg:         ParsarDaemonInstallConfig{Repo: "acme/parsar"},
			wantContain: "__PARSAR_DAEMON_REPO__*",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := chi.NewRouter()
			RegisterParsarDaemonInstallRoute(r, tc.cfg)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/parsar-daemon/install.sh", nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/x-shellscript") {
				t.Fatalf("Content-Type = %q, want text/x-shellscript prefix", got)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
			}
			body, err := io.ReadAll(rec.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			s := string(body)
			if !strings.HasPrefix(s, "#!/usr/bin/env bash") {
				t.Fatalf("body must start with shebang, got %q", s[:min(40, len(s))])
			}
			if !strings.Contains(s, tc.wantContain) {
				t.Fatalf("body missing %q", tc.wantContain)
			}
			if tc.wantAbsent != "" && strings.Contains(s, tc.wantAbsent) {
				t.Fatalf("body should NOT contain %q", tc.wantAbsent)
			}
		})
	}
}

// TestInstallScriptSupportsOneLineConnect pins the north-star one-liner:
// the web "copy one command" button pipes this script with the pairing
// env vars set, and the script must finish the job (chmod + hand off to
// `connect`). If a future edit drops either marker the one-liner silently
// regresses to the old two-step flow, so fail loudly here.
func TestInstallScriptSupportsOneLineConnect(t *testing.T) {
	t.Parallel()
	for _, want := range []string{
		`PARSAR_DAEMON_CONNECT_TOKEN`,
		`exec "$OUT_FILE" connect -b`,
	} {
		if !strings.Contains(installParsarDaemonScript, want) {
			t.Fatalf("install script missing one-line connect marker %q", want)
		}
	}
}
