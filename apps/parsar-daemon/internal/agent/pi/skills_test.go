package pi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipFile struct {
	Name string
	Body string
}

func buildZipBytes(t *testing.T, files []zipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate})
		if err != nil {
			t.Fatalf("zip header %q: %v", f.Name, err)
		}
		if _, err := w.Write([]byte(f.Body)); err != nil {
			t.Fatalf("zip write %q: %v", f.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func validSkillZip(t *testing.T) []byte {
	return buildZipBytes(t, []zipFile{
		{Name: "SKILL.md", Body: "---\nname: code-review\ndescription: Review code\n---\nBody"},
	})
}

func sha256Hex(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

type zipServer struct {
	*httptest.Server
	hits *int
	body []byte
	stat int
}

func startZipServer(t *testing.T, body []byte) *zipServer {
	t.Helper()
	var hits int
	zs := &zipServer{hits: &hits, body: body, stat: http.StatusOK}
	zs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(zs.stat)
		_, _ = w.Write(zs.body)
	}))
	t.Cleanup(zs.Close)
	return zs
}

func (s *zipServer) Hits() int { return *s.hits }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInstallSkillsHappyPathReturnsDirAndStampsCacheKey(t *testing.T) {
	body := validSkillZip(t)
	srv := startZipServer(t, body)
	root := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), root, []skillDescriptor{
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("installSkills: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}
	if len(res.SkillDirs) != 1 {
		t.Fatalf("SkillDirs = %v, want 1 entry", res.SkillDirs)
	}
	dir := res.SkillDirs[0]
	if filepath.Base(dir) != "code-review" {
		t.Fatalf("dir basename = %q, want code-review", filepath.Base(dir))
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	stamped, err := os.ReadFile(filepath.Join(dir, ".cache-key"))
	if err != nil {
		t.Fatalf("read cache-key: %v", err)
	}
	if want := "code-review@" + sha256Hex(body); string(stamped) != want {
		t.Fatalf("cache-key = %q, want %q", stamped, want)
	}
}

func TestInstallSkillsCacheHitSkipsDownloadButStillReturnsDir(t *testing.T) {
	body := validSkillZip(t)
	srv := startZipServer(t, body)
	root := t.TempDir()
	desc := []skillDescriptor{
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	}

	first, err := installSkills(context.Background(), discardLogger(), root, desc)
	if err != nil {
		t.Fatalf("first install: %v", err)
	}
	if len(first.SkillDirs) != 1 || srv.Hits() != 1 {
		t.Fatalf("first install dirs=%v hits=%d", first.SkillDirs, srv.Hits())
	}

	second, err := installSkills(context.Background(), discardLogger(), root, desc)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if srv.Hits() != 1 {
		t.Fatalf("cache should prevent second download; hits=%d", srv.Hits())
	}
	// A cache hit must STILL surface the dir so --skill is injected on
	// every turn, not just the first.
	if len(second.SkillDirs) != 1 || second.SkillDirs[0] != first.SkillDirs[0] {
		t.Fatalf("cache hit must still return the dir; got %v", second.SkillDirs)
	}
}

func TestInstallSkillsSHA256MismatchDemotesToWarning(t *testing.T) {
	body := validSkillZip(t)
	srv := startZipServer(t, body)
	root := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), root, []skillDescriptor{
		{Name: "code-review", Version: "1.0.0", DownloadURL: srv.URL, SHA256: strings.Repeat("0", 64)},
	})
	if err != nil {
		t.Fatalf("installSkills should not hard-error on sha mismatch: %v", err)
	}
	if len(res.SkillDirs) != 0 {
		t.Fatalf("SkillDirs = %v, want empty after sha mismatch", res.SkillDirs)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "sha256 mismatch") {
		t.Fatalf("want 1 sha-mismatch warning, got %v", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(root, "code-review", "SKILL.md")); err == nil {
		t.Fatal("SKILL.md should not exist after sha mismatch")
	}
}

func TestInstallSkillsEmptyListIsNoop(t *testing.T) {
	res, err := installSkills(context.Background(), discardLogger(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("empty install: %v", err)
	}
	if len(res.SkillDirs) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("expected empty result; got %+v", res)
	}
}

func TestInstallSkillsStripsWrappingRoot(t *testing.T) {
	body := buildZipBytes(t, []zipFile{
		{Name: "wrapper/SKILL.md", Body: "---\nname: x\n---\nbody"},
		{Name: "wrapper/refs/note.md", Body: "note"},
	})
	srv := startZipServer(t, body)
	root := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), root, []skillDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.SkillDirs) != 1 {
		t.Fatalf("SkillDirs = %v", res.SkillDirs)
	}
	dir := res.SkillDirs[0]
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md at expected path missing (wrapper not stripped?): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wrapper")); err == nil {
		t.Fatal("wrapper survived the strip")
	}
}

func TestInstallSkillsPathTraversalRejected(t *testing.T) {
	body := buildZipBytes(t, []zipFile{
		{Name: "SKILL.md", Body: "---\nname: x\n---"},
		{Name: "../../escape", Body: "should never land outside dir"},
	})
	srv := startZipServer(t, body)
	root := t.TempDir()

	res, _ := installSkills(context.Background(), discardLogger(), root, []skillDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if len(res.SkillDirs) != 0 {
		t.Fatalf("SkillDirs = %v, want empty on path-traversal", res.SkillDirs)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "escapes") {
		t.Fatalf("want escape warning, got %v", res.Warnings)
	}
}

func TestInstallSkillsPartialInstall(t *testing.T) {
	bodyOK := validSkillZip(t)
	srvOK := startZipServer(t, bodyOK)
	srvBAD := startZipServer(t, bodyOK)
	root := t.TempDir()

	res, err := installSkills(context.Background(), discardLogger(), root, []skillDescriptor{
		{Name: "good", Version: "1", DownloadURL: srvOK.URL, SHA256: sha256Hex(bodyOK)},
		{Name: "bad", Version: "1", DownloadURL: srvBAD.URL, SHA256: strings.Repeat("0", 64)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.SkillDirs) != 1 || filepath.Base(res.SkillDirs[0]) != "good" {
		t.Fatalf("SkillDirs = %v, want only the good one", res.SkillDirs)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning for the bad skill")
	}
}

func TestInstallSkillsDescriptorValidatorRejectsBadNames(t *testing.T) {
	body := validSkillZip(t)
	srv := startZipServer(t, body)
	res, _ := installSkills(context.Background(), discardLogger(), t.TempDir(), []skillDescriptor{
		{Name: "../escape", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if len(res.SkillDirs) != 0 {
		t.Fatalf("SkillDirs = %v; bad name should be rejected", res.SkillDirs)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning")
	}
}

func TestDecodeSkillDescriptorsArrayShape(t *testing.T) {
	raw := []any{
		map[string]any{"name": "a", "version": "1", "download_url": "https://x/a.zip", "sha256": strings.Repeat("a", 64)},
		map[string]any{"name": "", "version": "1", "download_url": "https://x/b.zip", "sha256": strings.Repeat("b", 64)},
		"not an object",
	}
	got, warns := decodeSkillDescriptors(raw)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got = %v, want 1 valid entry", got)
	}
	if len(warns) != 2 {
		t.Fatalf("warns = %v, want 2", warns)
	}
}

func TestDecodeSkillDescriptorsNilAndWrongType(t *testing.T) {
	if got, warns := decodeSkillDescriptors(nil); got != nil || warns != nil {
		t.Fatalf("nil raw should be (nil, nil), got (%v, %v)", got, warns)
	}
	if got, warns := decodeSkillDescriptors("not-array"); got != nil || len(warns) != 1 {
		t.Fatalf("string raw should warn, got got=%v warns=%v", got, warns)
	}
}

func TestMergeSkillDirsOverrideWinsAndDedupes(t *testing.T) {
	got := mergeSkillDirs([]any{"/a", "/b"}, []string{"/b", "/c"})
	want := []string{"/a", "/b", "/c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeSkillDirsNilExisting(t *testing.T) {
	got := mergeSkillDirs(nil, []string{"/x"})
	if len(got) != 1 || got[0] != "/x" {
		t.Fatalf("got %v, want [/x]", got)
	}
}

func TestResolveSkillsRootConversationScoped(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := resolveSkillsRoot("conv-abc", "run-1")
	if err != nil {
		t.Fatalf("resolveSkillsRoot: %v", err)
	}
	want := filepath.Join(tmp, ".parsar", "runtime", "pi", "conv-conv-abc", "skills")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveSkillsRootRunScopedFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	got, err := resolveSkillsRoot("", "run-9")
	if err != nil {
		t.Fatalf("resolveSkillsRoot: %v", err)
	}
	want := filepath.Join(tmp, ".parsar", "runtime", "pi", "run-run-9", "skills")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
