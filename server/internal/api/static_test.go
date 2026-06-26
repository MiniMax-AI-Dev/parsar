package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// buildSPAFixture lays down the minimal Vite-style dist tree:
// index.html, assets/app.js, favicon.ico.
func buildSPAFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<html><body>SPA shell</body></html>")
	writeFile(t, filepath.Join(dir, "assets", "app.js"), "console.log('hello');")
	writeFile(t, filepath.Join(dir, "favicon.ico"), "icon-bytes")
	return dir
}

func TestMountStaticAssetsDisabledWhenDirEmpty(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mounted := MountStaticAssets(r, StaticAssetsOptions{Dir: ""})
	if mounted {
		t.Fatalf("expected MountStaticAssets to skip when dir is empty")
	}
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/conversations/42")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 from default chi handler, got %d", resp.StatusCode)
	}
}

func TestMountStaticAssetsDisabledWhenDirMissing(t *testing.T) {
	r := chi.NewRouter()
	calls := 0
	mounted := MountStaticAssets(r, StaticAssetsOptions{
		Dir: "/definitely/not/a/real/path/parsar-prod-image-test",
		Logger: func(string, ...any) {
			calls++
		},
	})
	if mounted {
		t.Fatalf("expected MountStaticAssets to skip when dir does not exist")
	}
	if calls == 0 {
		t.Fatalf("expected Logger to be called explaining why mount was skipped")
	}
}

func TestMountStaticAssetsDisabledWhenIndexMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "assets", "app.js"), "console.log('hello');")
	r := chi.NewRouter()
	calls := 0
	mounted := MountStaticAssets(r, StaticAssetsOptions{
		Dir:    dir,
		Logger: func(string, ...any) { calls++ },
	})
	if mounted {
		t.Fatalf("expected MountStaticAssets to skip when index.html is absent")
	}
	if calls == 0 {
		t.Fatalf("expected Logger to be called explaining why mount was skipped")
	}
}

func TestSPAHandlerServesIndexAtRoot(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	if mounted := MountStaticAssets(r, StaticAssetsOptions{Dir: dir}); !mounted {
		t.Fatalf("MountStaticAssets returned false; expected true")
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from root, got %d", resp.StatusCode)
	}
	body := readAllString(t, resp.Body)
	if !strings.Contains(body, "SPA shell") {
		t.Fatalf("expected SPA shell in body, got %q", body)
	}
}

func TestSPAHandlerServesHashedAsset(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	MountStaticAssets(r, StaticAssetsOptions{Dir: dir})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("get asset: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for asset, got %d", resp.StatusCode)
	}
	body := readAllString(t, resp.Body)
	if !strings.Contains(body, "console.log") {
		t.Fatalf("expected asset body, got %q", body)
	}
}

func TestSPAHandlerFallsBackToIndexForUnknownPath(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	MountStaticAssets(r, StaticAssetsOptions{Dir: dir})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/conversations/abc-123")
	if err != nil {
		t.Fatalf("get SPA route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 SPA fallback, got %d", resp.StatusCode)
	}
	body := readAllString(t, resp.Body)
	if !strings.Contains(body, "SPA shell") {
		t.Fatalf("expected SPA shell fallback, got %q", body)
	}
}

func TestSPAHandlerDoesNotShadowAPIPrefixes(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	MountStaticAssets(r, StaticAssetsOptions{Dir: dir})
	srv := httptest.NewServer(r)
	defer srv.Close()

	cases := []string{
		"/api/v1/something-unknown",
		"/dev/seed-but-unknown",
		"/v1/anything",
		"/healthz/extra-path",
		"/readyz/extra-path",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			resp, err := http.Get(srv.URL + p)
			if err != nil {
				t.Fatalf("get %s: %v", p, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("expected 404, got %d", resp.StatusCode)
			}
			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("expected json 404 for API prefix path, got %q", ct)
			}
			body := readAllString(t, resp.Body)
			if strings.Contains(body, "SPA shell") {
				t.Fatalf("SPA shell leaked into API 404: %q", body)
			}
		})
	}
}

func TestSPAHandlerKeepsAPIRoutesLive(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	RegisterHealthRoutes(r, HealthDeps{DB: nil})
	if mounted := MountStaticAssets(r, StaticAssetsOptions{Dir: dir}); !mounted {
		t.Fatalf("MountStaticAssets returned false")
	}
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected /healthz still 200 after mounting SPA, got %d", resp.StatusCode)
	}
	body := readAllString(t, resp.Body)
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("expected real liveness JSON, got %q", body)
	}
}

func TestSPAHandlerRejectsNonGETMethod(t *testing.T) {
	dir := buildSPAFixture(t)
	r := chi.NewRouter()
	MountStaticAssets(r, StaticAssetsOptions{Dir: dir})
	srv := httptest.NewServer(r)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/something", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for POST on unknown SPA path, got %d", resp.StatusCode)
	}
	body := readAllString(t, resp.Body)
	if strings.Contains(body, "SPA shell") {
		t.Fatalf("SPA shell must not be served for non-GET, body=%q", body)
	}
}

func TestSPAHandlerRejectsPathTraversal(t *testing.T) {
	dir := buildSPAFixture(t)
	// Sibling file outside the SPA dir; handler must NOT serve it.
	parent := filepath.Dir(dir)
	secretPath := filepath.Join(parent, "secret.txt")
	writeFile(t, secretPath, "private-secret-bytes")
	defer os.Remove(secretPath)

	r := chi.NewRouter()
	MountStaticAssets(r, StaticAssetsOptions{Dir: dir})
	srv := httptest.NewServer(r)
	defer srv.Close()

	// Go's http client path.Cleans before sending, so /../secret.txt
	// arrives as /secret.txt. The handler should NOT expose any file
	// outside dir.
	resp, err := http.Get(srv.URL + "/../secret.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body := readAllString(t, resp.Body)
	if strings.Contains(body, "private-secret-bytes") {
		t.Fatalf("path traversal leaked sibling file: %q", body)
	}
}

func TestIsAPIPath(t *testing.T) {
	yes := []string{
		"/api/v1/conversations",
		"/dev/seed",
		"/v1/whatever",
		"/healthz",
		"/readyz",
		"/api",
		"/dev",
		"/v1",
	}
	for _, p := range yes {
		if !isAPIPath(p) {
			t.Errorf("expected %q classified as API path", p)
		}
	}
	no := []string{
		"/",
		"/conversations/42",
		"/assets/app.js",
		"/login",
		"/admin/projects/abc",
	}
	for _, p := range no {
		if isAPIPath(p) {
			t.Errorf("expected %q NOT classified as API path", p)
		}
	}
}

func TestIsInsideDirSeparatorEdgeCase(t *testing.T) {
	if isInsideDir("/srv/web", "/srv/web-evil/x") {
		t.Fatal("isInsideDir incorrectly accepted sibling-prefix escape")
	}
	if !isInsideDir("/srv/web", "/srv/web/index.html") {
		t.Fatal("isInsideDir incorrectly rejected legitimate child path")
	}
}

func readAllString(t *testing.T, body interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	buf := make([]byte, 0, 1024)
	chunk := make([]byte, 1024)
	for {
		n, err := body.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
