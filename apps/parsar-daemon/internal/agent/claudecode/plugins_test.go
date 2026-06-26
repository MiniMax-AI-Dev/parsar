package claudecode

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginZipFile mirrors the server-side validator test helper.
type pluginZipFile struct {
	Name string
	Body string
	Mode os.FileMode // 0 → default
}

func buildPluginZipBytes(t *testing.T, files []pluginZipFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		hdr := &zip.FileHeader{Name: f.Name, Method: zip.Deflate}
		if f.Mode != 0 {
			hdr.SetMode(f.Mode)
		}
		w, err := zw.CreateHeader(hdr)
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

// validPluginZipBytes is the baseline fixture every install test uses
// unless it explicitly mutates the entry set.
func validPluginZipBytes(t *testing.T) []byte {
	return buildPluginZipBytes(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Body: `{"name":"my-plugin","version":"1.0.0"}`},
		{Name: "commands/hello.md", Body: "---\nname: hello\n---\nbody"},
	})
}

func sha256Hex(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

// pluginServer is a deterministic stand-in for the OSS presigned GET
// endpoint. It counts hits so cache-hit tests can verify the second
// install call did NOT round-trip.
type pluginServer struct {
	*httptest.Server
	hits *int
	body []byte
	stat int
}

func startPluginServer(t *testing.T, body []byte) *pluginServer {
	t.Helper()
	var hits int
	ps := &pluginServer{hits: &hits, body: body, stat: http.StatusOK}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(ps.stat)
		_, _ = w.Write(ps.body)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (s *pluginServer) Hits() int { return *s.hits }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestInstallPlugins_HappyPath_ExtractsAndStampsCacheKey(t *testing.T) {
	t.Parallel()
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("installPlugins: %v", err)
	}
	if len(res.PluginDirs) != 1 {
		t.Fatalf("PluginDirs = %v, want 1 entry", res.PluginDirs)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	dir := res.PluginDirs[0]
	if filepath.Base(dir) != "my-plugin" {
		t.Fatalf("dir basename = %q, want my-plugin", filepath.Base(dir))
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "hello.md")); err != nil {
		t.Fatalf("commands entry missing: %v", err)
	}
	stamped, err := os.ReadFile(filepath.Join(dir, ".cache-key"))
	if err != nil {
		t.Fatalf("read cache-key: %v", err)
	}
	want := "my-plugin@" + sha256Hex(body)
	if string(stamped) != want {
		t.Fatalf("cache-key = %q, want %q", stamped, want)
	}
}

func TestInstallPlugins_CacheHitSkipsDownload(t *testing.T) {
	t.Parallel()
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	desc := []pluginDescriptor{
		{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	}
	if _, err := installPlugins(context.Background(), discardLogger(), workDir, desc); err != nil {
		t.Fatalf("first install: %v", err)
	}
	hitsAfterFirst := srv.Hits()
	if hitsAfterFirst != 1 {
		t.Fatalf("first install hits = %d, want 1", hitsAfterFirst)
	}

	// Second install with the same descriptor — cache-key match
	// short-circuits BEFORE any HTTP call.
	if _, err := installPlugins(context.Background(), discardLogger(), workDir, desc); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if got := srv.Hits(); got != hitsAfterFirst {
		t.Fatalf("second install made %d additional hits; cache should have prevented network", got-hitsAfterFirst)
	}
}

func TestInstallPlugins_CacheInvalidatedBySHA256Change(t *testing.T) {
	t.Parallel()
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()
	logger := discardLogger()

	first := []pluginDescriptor{
		{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	}
	if _, err := installPlugins(context.Background(), logger, workDir, first); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Swap the server payload + sha. Cache key contains the sha so it
	// must be invalidated.
	newBody := buildPluginZipBytes(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Body: `{"name":"my-plugin","version":"1.0.0"}`},
		{Name: "commands/different.md", Body: "---\nname: different\n---\nbody"},
	})
	srv.body = newBody

	second := []pluginDescriptor{
		{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(newBody)},
	}
	if _, err := installPlugins(context.Background(), logger, workDir, second); err != nil {
		t.Fatalf("second install: %v", err)
	}
	dir := filepath.Join(workDir, ".claude", "plugins", "my-plugin")
	if _, err := os.Stat(filepath.Join(dir, "commands", "different.md")); err != nil {
		t.Fatalf("new content not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "hello.md")); err == nil {
		t.Fatal("old content survived the re-install")
	}
}

func TestInstallPlugins_SHA256MismatchDemotesToWarning(t *testing.T) {
	t.Parallel()
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	// Wrong sha → no install, no hard error; rest of the prompt
	// continues without this plugin.
	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: strings.Repeat("0", 64)},
	})
	if err != nil {
		t.Fatalf("installPlugins: %v", err)
	}
	if len(res.PluginDirs) != 0 {
		t.Fatalf("PluginDirs = %v, want empty after sha mismatch", res.PluginDirs)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning on sha mismatch")
	}
	if !strings.Contains(res.Warnings[0], "sha256 mismatch") {
		t.Fatalf("warning text = %q, want sha256-mismatch hint", res.Warnings[0])
	}
	// No .cache-key file must be stamped — would short-circuit future
	// retries with the same bad sha.
	if _, err := os.Stat(filepath.Join(workDir, ".claude", "plugins", "my-plugin", ".cache-key")); err == nil {
		t.Fatal("cache-key stamped despite sha mismatch")
	}
}

func TestInstallPlugins_HTTPErrorDemotesToWarning(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	srv := startPluginServer(t, nil)
	srv.stat = http.StatusForbidden

	res, _ := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "p", Version: "1", DownloadURL: srv.URL, SHA256: strings.Repeat("a", 64)},
	})
	if len(res.PluginDirs) != 0 {
		t.Fatal("expected no installed dirs on 403")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning on 403")
	}
}

func TestInstallPlugins_StripWrappingRoot(t *testing.T) {
	t.Parallel()
	body := buildPluginZipBytes(t, []pluginZipFile{
		{Name: "wrapper-dir/.claude-plugin/plugin.json", Body: `{"name":"x","version":"1"}`},
		{Name: "wrapper-dir/commands/hi.md", Body: "---\nname: hi\n---"},
	})
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.PluginDirs) != 1 {
		t.Fatalf("PluginDirs = %v", res.PluginDirs)
	}
	dir := res.PluginDirs[0]
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("manifest at expected path missing (wrapper not stripped?): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wrapper-dir")); err == nil {
		t.Fatal("wrapper-dir survived the strip")
	}
}

// TestInstallPlugins_StripWrappingRootWithBareDirEntry locks in the
// production fix: real `zip -r my-plugin.zip my-plugin/` archives emit
// a bare directory entry first; naive root-inference sees no internal
// "/" and concludes "no wrapper to strip". The fix skips directory-
// only entries when picking the first candidate.
func TestInstallPlugins_StripWrappingRootWithBareDirEntry(t *testing.T) {
	t.Parallel()
	body := buildPluginZipBytes(t, []pluginZipFile{
		// Bare directory entry — the gotcha.
		{Name: "wrapper-dir/", Body: ""},
		{Name: "wrapper-dir/.claude-plugin/plugin.json", Body: `{"name":"x","version":"1"}`},
		{Name: "wrapper-dir/commands/hi.md", Body: "---\nname: hi\n---"},
	})
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.PluginDirs) != 1 {
		t.Fatalf("PluginDirs = %v", res.PluginDirs)
	}
	dir := res.PluginDirs[0]
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("manifest at expected path missing (bare dir entry confused wrapper detection?): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wrapper-dir")); err == nil {
		t.Fatal("wrapper-dir survived the strip")
	}
}

func TestInstallPlugins_MacOSXMetadataIgnored(t *testing.T) {
	t.Parallel()
	body := buildPluginZipBytes(t, []pluginZipFile{
		{Name: "__MACOSX/._plugin.json", Body: "binary metadata"},
		{Name: ".claude-plugin/plugin.json", Body: `{"name":"x","version":"1"}`},
	})
	srv := startPluginServer(t, body)
	workDir := t.TempDir()
	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	dir := res.PluginDirs[0]
	if _, err := os.Stat(filepath.Join(dir, "__MACOSX")); err == nil {
		t.Fatal("__MACOSX dir was extracted despite filter")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestInstallPlugins_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	// "../../etc/passwd" entry must not write outside the target dir.
	body := buildPluginZipBytes(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Body: `{"name":"x","version":"1"}`},
		{Name: "../../escape", Body: "should never land outside dir"},
	})
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, _ := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if len(res.PluginDirs) != 0 {
		t.Fatalf("PluginDirs = %v, want empty on path-traversal", res.PluginDirs)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning on path-traversal")
	}
	if !strings.Contains(res.Warnings[0], "escapes") {
		t.Fatalf("warning text = %q, want escape hint", res.Warnings[0])
	}
}

func TestInstallPlugins_SymlinkEntrySkipped(t *testing.T) {
	t.Parallel()
	// archive/zip's SetMode() drops file-type bits, so to test a real
	// symlink entry we set ExternalAttrs by hand — upper 16 bits are
	// the Unix mode (S_IFLNK | perm), which is how attacker-crafted
	// zips actually mark symlinks.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	{
		hdr := &zip.FileHeader{Name: ".claude-plugin/plugin.json", Method: zip.Deflate}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create manifest: %v", err)
		}
		if _, err := w.Write([]byte(`{"name":"x","version":"1"}`)); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	{
		hdr := &zip.FileHeader{Name: "commands/evil.md", Method: zip.Deflate}
		// 0xA1ED = S_IFLNK (0xA000) | 0755. External attrs are
		// (unix_mode << 16) per the zip spec.
		hdr.ExternalAttrs = 0xA1ED << 16
		hdr.CreatorVersion = 3 << 8 // UNIX host system
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create symlink: %v", err)
		}
		if _, err := w.Write([]byte("/etc/passwd")); err != nil {
			t.Fatalf("write symlink target: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	body := buf.Bytes()
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "x", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.PluginDirs) != 1 {
		t.Fatalf("PluginDirs = %v, want 1", res.PluginDirs)
	}
	dir := res.PluginDirs[0]
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "evil.md")); err == nil {
		t.Fatal("symlink entry was extracted as a regular file; symlinks must be skipped")
	}
}

func TestInstallPlugins_ConcurrentSamePluginNoTruncation(t *testing.T) {
	t.Parallel()
	// Per-call uuid in the temp filename makes each install's temp
	// file independent; concurrent installs of the same (name,
	// version) must both succeed.
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	workDir := t.TempDir()

	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
				{Name: "my-plugin", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
			})
			if err != nil {
				done <- err
				return
			}
			if len(res.PluginDirs) != 1 {
				done <- fmt.Errorf("PluginDirs = %v", res.PluginDirs)
				return
			}
			done <- nil
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent install failed: %v", err)
		}
	}
}

func TestInstallPlugins_PartialInstall(t *testing.T) {
	t.Parallel()
	// Good descriptor installs; bad-sha descriptor demotes to warning.
	bodyOK := buildPluginZipBytes(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Body: `{"name":"good","version":"1"}`},
	})
	srvOK := startPluginServer(t, bodyOK)
	srvBAD := startPluginServer(t, bodyOK)
	workDir := t.TempDir()

	res, err := installPlugins(context.Background(), discardLogger(), workDir, []pluginDescriptor{
		{Name: "good", Version: "1", DownloadURL: srvOK.URL, SHA256: sha256Hex(bodyOK)},
		{Name: "bad", Version: "1", DownloadURL: srvBAD.URL, SHA256: strings.Repeat("0", 64)},
	})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.PluginDirs) != 1 {
		t.Fatalf("PluginDirs = %v, want 1 (only the good one)", res.PluginDirs)
	}
	if !strings.HasSuffix(res.PluginDirs[0], "/good") {
		t.Fatalf("PluginDirs[0] = %q, want trailing /good", res.PluginDirs[0])
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning for the bad plugin")
	}
}

func TestInstallPlugins_EmptyListIsNoop(t *testing.T) {
	t.Parallel()
	res, err := installPlugins(context.Background(), discardLogger(), t.TempDir(), nil)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(res.PluginDirs) != 0 || len(res.Warnings) != 0 {
		t.Fatalf("expected empty result; got %+v", res)
	}
}

func TestInstallPlugins_DescriptorValidatorRejectsBadNames(t *testing.T) {
	t.Parallel()
	body := validPluginZipBytes(t)
	srv := startPluginServer(t, body)
	res, _ := installPlugins(context.Background(), discardLogger(), t.TempDir(), []pluginDescriptor{
		{Name: "../escape", Version: "1", DownloadURL: srv.URL, SHA256: sha256Hex(body)},
	})
	if len(res.PluginDirs) != 0 {
		t.Fatalf("PluginDirs = %v; bad name should be rejected", res.PluginDirs)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning")
	}
}

func TestDecodePluginDescriptors_ArrayShape(t *testing.T) {
	t.Parallel()
	raw := []any{
		map[string]any{"name": "a", "version": "1", "download_url": "https://x/a.zip", "sha256": strings.Repeat("a", 64)},
		map[string]any{"name": "", "version": "1", "download_url": "https://x/b.zip", "sha256": strings.Repeat("b", 64)},
		"not an object",
	}
	got, warns := decodePluginDescriptors(raw)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got = %v, want 1 valid entry", got)
	}
	if len(warns) != 2 {
		t.Fatalf("warns = %v, want 2", warns)
	}
}

func TestDecodePluginDescriptors_NilAndWrongType(t *testing.T) {
	t.Parallel()
	got, warns := decodePluginDescriptors(nil)
	if got != nil || warns != nil {
		t.Fatalf("nil input should produce nil output; got=%v warns=%v", got, warns)
	}
	_, warns = decodePluginDescriptors("not an array")
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning on wrong type, got %v", warns)
	}
}

func TestMergePluginDirs_OverrideWinsAndDedupes(t *testing.T) {
	t.Parallel()
	got := mergePluginDirs([]any{"/a", "/b"}, []string{"/b", "/c"})
	want := []string{"/a", "/b", "/c"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergePluginDirs_AcceptsTypedStringSlice(t *testing.T) {
	t.Parallel()
	got := mergePluginDirs([]string{"/x"}, []string{"/y"})
	want := []string{"/x", "/y"}
	if !equalStrings(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergePluginDirs_NilExisting(t *testing.T) {
	t.Parallel()
	got := mergePluginDirs(nil, []string{"/x"})
	if !equalStrings(got, []string{"/x"}) {
		t.Fatalf("got %v", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestResolveSessionWorkDir_RespectsExplicitDir locks in "caller wins":
// when req.WorkDir is set we must use exactly that path (mkdir -p if it
// doesn't exist yet) and never fall back to the conversation scratch
// dir.
func TestResolveSessionWorkDir_RespectsExplicitDir(t *testing.T) {
	t.Parallel()
	explicit := filepath.Join(t.TempDir(), "some-explicit-dir")
	got, err := resolveSessionWorkDir(explicit, "conv-ignored")
	if err != nil {
		t.Fatalf("resolveSessionWorkDir: %v", err)
	}
	if got != explicit {
		t.Fatalf("got %q, want explicit dir verbatim", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat explicit dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("explicit dir %q is not a directory", got)
	}
}

// TestResolveSessionWorkDir_RejectsRelativeDir: relative paths are
// ambiguous (resolved against daemon cwd, which is not a stable anchor
// for user-facing config). The user gets a clear error instead of a
// chdir failure later.
func TestResolveSessionWorkDir_RejectsRelativeDir(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"foo", "./bar", "../baz", "a/b/c"} {
		if _, err := resolveSessionWorkDir(rel, "conv-x"); err == nil {
			t.Fatalf("relative path %q: expected error, got nil", rel)
		}
	}
}

// TestResolveSessionWorkDir_ExplicitDirCreated: an absolute path whose
// parents don't exist yet still works — daemon mkdir -p's it. This is
// the "user named a fresh project root" case.
func TestResolveSessionWorkDir_ExplicitDirCreated(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "missing", "parents", "leaf")
	got, err := resolveSessionWorkDir(target, "conv-ignored")
	if err != nil {
		t.Fatalf("resolveSessionWorkDir: %v", err)
	}
	if got != target {
		t.Fatalf("got %q, want %q", got, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target %q is not a directory", target)
	}
}

// TestResolveSessionWorkDir_FallbackCreatesDir: empty req.WorkDir with
// conversation_id must yield a real on-disk per-conversation directory
// under daemon HOME used for BOTH plugin install AND claude cwd.
// Overrides HOME to keep test inside t.TempDir().
func TestResolveSessionWorkDir_FallbackCreatesDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	got, err := resolveSessionWorkDir("", "conv-abc-123")
	if err != nil {
		t.Fatalf("resolveSessionWorkDir: %v", err)
	}
	want := filepath.Join(tmp, ".parsar", "runtime", "claudecode", "conv-conv-abc-123")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat fallback dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("fallback %q is not a directory", got)
	}
}

// TestResolveSessionWorkDir_BothEmptyFallsBackToCwd: when neither
// req.WorkDir nor conversation_id is provided, degrade to daemon cwd
// rather than refuse.
func TestResolveSessionWorkDir_BothEmptyFallsBackToCwd(t *testing.T) {
	t.Parallel()
	got, err := resolveSessionWorkDir("", "")
	if err != nil {
		t.Fatalf("resolveSessionWorkDir: %v", err)
	}
	wantCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	if got != wantCwd {
		t.Fatalf("got %q, want daemon cwd %q", got, wantCwd)
	}
	// Whitespace-only inputs must be treated as empty.
	got, err = resolveSessionWorkDir("  ", "  ")
	if err != nil {
		t.Fatalf("whitespace inputs: %v", err)
	}
	if got != wantCwd {
		t.Fatalf("whitespace inputs: got %q, want %q", got, wantCwd)
	}
}

// TestResolveSessionWorkDir_FallbackIsIdempotent: a second call with
// the same conversation_id must succeed (MkdirAll on existing dir).
func TestResolveSessionWorkDir_FallbackIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	first, err := resolveSessionWorkDir("", "conv-x")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := resolveSessionWorkDir("", "conv-x")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if first != second {
		t.Fatalf("non-deterministic fallback: %q vs %q", first, second)
	}
}
