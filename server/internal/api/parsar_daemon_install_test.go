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
