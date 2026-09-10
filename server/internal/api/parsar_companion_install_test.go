package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestDeviceInstallerCompanion(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		pair, cli    bool
		wantSuccess  bool
	}{
		{"server pairing", "server", true, true, true},
		{"server missing CLI", "server", true, false, false},
		{"release pairing", "release", true, true, true},
		{"release missing CLI", "release", true, false, false},
		{"download only", "server", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			binaries := filepath.Join(root, "binaries")
			out := filepath.Join(root, "output with spaces")
			tools := filepath.Join(root, "tools")
			for _, dir := range []string{binaries, out, tools} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, body string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			platform := runtime.GOOS + "-" + runtime.GOARCH
			write(filepath.Join(binaries, "parsar-daemon-"+platform), `#!/bin/sh
set -eu
[ "$*" = "connect -b" ]
[ "$PARSAR_DAEMON_CONNECT_TOKEN" = "synthetic-pairing" ]
cli="$(dirname "$0")/parsar"
[ "$("$cli")" = "companion-ok" ]
printf paired > "$PARSAR_DAEMON_OUT_DIR/paired"
`)
			if tc.cli {
				write(filepath.Join(binaries, "parsar-"+platform), "#!/bin/sh\nprintf companion-ok\n")
			}
			r := chi.NewRouter()
			if tc.source == "server" {
				RegisterParsarDaemonDownloadRoute(r, ParsarDaemonDownloadConfig{BinaryDir: binaries})
			}
			srv := httptest.NewServer(r)
			defer srv.Close()
			curl, err := exec.LookPath("curl")
			if err != nil {
				t.Skip("curl is required for installer execution")
			}
			write(filepath.Join(tools, "curl"), `#!/bin/sh
set -eu
url= out=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift ;;
    http*) url="$1" ;;
  esac
  shift
done
case "$url" in
  https://github.com/*/releases/download/parsar-daemon-v1.2.3/*)
    name="${url##*/}"
    name="$(printf %s "$name" | sed 's/-v1.2.3-/-/')"
    cp "$TEST_BINARIES/$name" "$out"
    ;;
  http://127.0.0.1:*) exec "$TEST_CURL" -fsSL "$url" -o "$out" ;;
  *) exit 90 ;;
esac
`)
			cmd := exec.Command("bash")
			cmd.Dir = root
			cmd.Stdin = strings.NewReader(installParsarDaemonScript)
			cmd.Env = append(os.Environ(),
				"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
				"PARSAR_DAEMON_OUT_DIR="+out,
				"PARSAR_DAEMON_CONNECT_URL="+srv.URL,
				"PARSAR_DAEMON_VERSION=parsar-daemon-v1.2.3",
				"TEST_BINARIES="+binaries, "TEST_CURL="+curl,
				"PARSAR_DAEMON_CONNECT_TOKEN=",
			)
			if tc.pair {
				cmd.Env = append(cmd.Env, "PARSAR_DAEMON_CONNECT_TOKEN=synthetic-pairing")
				write(filepath.Join(out, "parsar"), "old-cli")
				write(filepath.Join(out, "parsar-daemon"), "old-daemon")
			}
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.wantSuccess {
				t.Fatalf("success=%v, want %v: %s", err == nil, tc.wantSuccess, output)
			}
			_, pairedErr := os.Stat(filepath.Join(out, "paired"))
			if got := pairedErr == nil; got != (tc.pair && tc.wantSuccess) {
				t.Fatalf("paired=%v", got)
			}
			if !tc.wantSuccess {
				for _, binary := range []string{"parsar", "parsar-daemon"} {
					body, _ := os.ReadFile(filepath.Join(out, binary))
					want := "old-cli"
					if binary == "parsar-daemon" {
						want = "old-daemon"
					}
					if string(body) != want {
						t.Fatalf("failed download replaced %s", binary)
					}
				}
			}
			if !tc.pair {
				info, err := os.Stat(filepath.Join(out, "parsar-daemon-"+platform))
				if err != nil || info.Mode().Perm()&0o111 != 0 {
					t.Fatalf("download-only file should not be executable: %v", err)
				}
			}
			staging, _ := filepath.Glob(filepath.Join(out, ".install.*"))
			if len(staging) != 0 {
				t.Fatalf("left staging directories: %v", staging)
			}
		})
	}
}

func TestCompanionBinaryDownload(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "parsar-linux-amd64"), []byte("cli"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterParsarDaemonDownloadRoute(r, ParsarDaemonDownloadConfig{BinaryDir: dir})
	for _, tc := range []struct {
		binary string
		status int
	}{
		{"parsar", http.StatusOK},
		{"parsar-daemon", http.StatusNotFound},
		{"..%2F..%2Fetc", http.StatusBadRequest},
		{"other", http.StatusBadRequest},
	} {
		rec := daemonDownloadGet(r, fmt.Sprintf("/api/v1/parsar-daemon/download?os=linux&arch=amd64&binary=%s", tc.binary))
		if rec.Code != tc.status {
			t.Fatalf("binary=%s: status=%d", tc.binary, rec.Code)
		}
		if tc.status == http.StatusOK && (rec.Body.String() != "cli" || !strings.Contains(rec.Header().Get("Content-Disposition"), "parsar-linux-amd64")) {
			t.Fatal("companion download returned the wrong binary or filename")
		}
	}
}
