package claudecode

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/google/uuid"
)

// pluginDescriptor is the daemon-side view of one server-sent plugin
// entry under agent_options["plugins"]:
//
//	{ "name": "...", "version": "...", "download_url": "...", "sha256": "..." }
type pluginDescriptor struct {
	Name        string
	Version     string
	DownloadURL string
	SHA256      string
}

// PluginInstallResult is what installPlugins returns: local directory
// paths to feed into `--plugin-dir`, plus warnings the session should
// surface. Errors that abort install bubble up through the error
// return; warnings cover the "N-1 of N installed" case.
type PluginInstallResult struct {
	PluginDirs []string
	Warnings   []string
}

// pluginInstallTimeout caps a single plugin's download + extract step.
const pluginInstallTimeout = 60 * time.Second

// maxPluginZipBytes mirrors the server-side cap in
// server/internal/capability/parser/plugin_validator.go. Defense in
// depth.
const maxPluginZipBytes int64 = 32 * 1024 * 1024

// pluginsHTTPClient timeout is larger than pluginInstallTimeout so the
// per-call context cancel dominates.
var pluginsHTTPClient = &http.Client{
	Timeout: pluginInstallTimeout + 10*time.Second,
}

// installPlugins materialises every plugin under
// <workDir>/.claude/plugins/<name>/ and returns the local paths. Per
// plugin:
//
//  1. Skip when <dir>/.cache-key matches name+sha256 (recurring prompts
//     avoid the network round-trip).
//  2. Fetch the download URL into a temp file under .tmp/, capping at
//     maxPluginZipBytes.
//  3. Verify SHA-256 against the descriptor before touching the
//     extraction target — mismatch demotes to warning.
//  4. Extract to <workDir>/.claude/plugins/<name>/, stripping a single
//     wrapping directory and ignoring __MACOSX/.
//  5. Stamp .cache-key with "<name>@<sha256>".
//
// Errors during 2-4 demote the plugin to a warning and continue.
// Returning a hard error means we couldn't even create the parent
// directory.
func installPlugins(
	ctx context.Context,
	logger *slog.Logger,
	workDir string,
	plugins []pluginDescriptor,
) (PluginInstallResult, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(plugins) == 0 {
		return PluginInstallResult{}, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return PluginInstallResult{}, errors.New("claudecode plugins: workDir is required")
	}

	root := filepath.Join(workDir, ".claude", "plugins")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return PluginInstallResult{}, fmt.Errorf("claudecode plugins: mkdir %s: %w", root, err)
	}

	result := PluginInstallResult{}
	for _, p := range plugins {
		if err := p.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip plugin (invalid descriptor): %v", err))
			logger.Warn("claudecode plugins: invalid descriptor", "err", err.Error())
			continue
		}

		dir := filepath.Join(root, p.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := p.cacheKey()

		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("claudecode plugins: cache hit",
				"name", p.Name, "version", p.Version, "dir", dir)
			result.PluginDirs = append(result.PluginDirs, dir)
			continue
		}

		perCtx, cancel := context.WithTimeout(ctx, pluginInstallTimeout)
		err := installOnePlugin(perCtx, logger, root, dir, cacheKey, expectedKey, p)
		cancel()
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("plugin %s@%s: %v", p.Name, p.Version, err))
			logger.Warn("claudecode plugins: install failed",
				"name", p.Name, "version", p.Version, "err", err.Error())
			continue
		}
		result.PluginDirs = append(result.PluginDirs, dir)
		logger.Info("claudecode plugins: installed",
			"name", p.Name, "version", p.Version, "dir", dir)
	}
	return result, nil
}

// installOnePlugin: download → verify → extract → stamp cache key.
// On error, best-effort cleanup of any partial extraction.
func installOnePlugin(
	ctx context.Context,
	logger *slog.Logger,
	root, dir, cacheKey, expectedKey string,
	p pluginDescriptor,
) error {
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}

	// Per-call uuid in the temp path so two concurrent installs of the
	// same (name, version) don't truncate each other's bytes, and so
	// nothing on disk between verifyPluginSHA256 and extract can be a
	// different file than the one we just hashed (TOCTOU).
	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", p.Name, p.Version, uuid.NewString()))
	defer func() {
		_ = os.Remove(zipPath)
	}()

	fd, err := fetchPluginZip(ctx, p.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	// Verify and extract BOTH read through the same FD (not the path).
	// Unix file semantics pin the inode, so a swap on disk between
	// hashing and extraction cannot change the bytes we're using.
	if err := verifyPluginSHA256FromFD(fd, p.SHA256); err != nil {
		return err
	}
	if _, err := fd.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	fi, err := fd.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("rm old dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	if err := extractPluginZipFromFD(fd, fi.Size(), dir); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	if err := os.WriteFile(cacheKey, []byte(expectedKey), 0o644); err != nil {
		// Cache miss next time is recoverable — don't fail the install.
		logger.Warn("claudecode plugins: write cache key failed",
			"path", cacheKey, "err", err.Error())
	}
	return nil
}

// fetchPluginZip GETs url into dst, capping the body at
// maxPluginZipBytes. Returns an OPEN file descriptor positioned at
// offset 0; the caller closes it. Holding the FD across verify +
// extract closes the TOCTOU between hashing the on-disk bytes and
// reading them for extract — even if someone swaps the file, the open
// FD points at the original inode.
//
// Only http/https are accepted to defend against a future
// canonical_spec letting attacker-supplied download_url reach this
// code with file:// or http://internal-ip/... values.
func fetchPluginZip(ctx context.Context, downloadURL, dst string) (*os.File, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		// Don't include downloadURL — it carries the signature query
		// string.
		return nil, errors.New("download_url must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, errors.New("build request failed")
	}
	resp, err := pluginsHTTPClient.Do(req)
	if err != nil {
		// Strip embedded URL via sanitizeHTTPClientError —
		// OSSAccessKeyId + Signature would otherwise leak into the
		// daemon log.
		return nil, fmt.Errorf("get failed: %s", sanitizeHTTPClientError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("get: status %d", resp.StatusCode)
	}

	// O_EXCL — the per-call uuid in the path makes a collision a
	// programmer error, not an attacker condition. Failing fast is
	// safer than silent truncation.
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open dst: %w", err)
	}

	limited := io.LimitReader(resp.Body, maxPluginZipBytes+1)
	written, err := io.Copy(f, limited)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("copy body: %w", err)
	}
	if written > maxPluginZipBytes {
		_ = f.Close()
		return nil, fmt.Errorf("zip exceeds %d byte cap", maxPluginZipBytes)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seek after write: %w", err)
	}
	return f, nil
}

// sanitizeHTTPClientError strips the URL embedded by *url.Error.
// net/http returns errors that include the full request URL — for
// presigned OSS URLs that's OSSAccessKeyId + Signature + Expires.
// Without redaction those credentials land in the daemon log via
// PluginInstallResult.Warnings → session.go logger.Warn.
//
// Format is `<method> "<url>": <inner>` — keep the method + inner
// message, drop the URL.
func sanitizeHTTPClientError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	open := strings.Index(msg, `"`)
	if open < 0 {
		return msg
	}
	close := strings.Index(msg[open+1:], `"`)
	if close < 0 {
		return msg
	}
	closeAbs := open + 1 + close
	if closeAbs+2 > len(msg) {
		return msg
	}
	return msg[:open] + "<redacted-url>" + msg[closeAbs+1:]
}

// verifyPluginSHA256FromFD hashes the bytes the open FD points at and
// compares against want (lowercase hex). Rewinds the FD afterwards.
func verifyPluginSHA256FromFD(fd *os.File, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return errors.New("verify: empty expected sha256")
	}
	if _, err := fd.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("verify: seek: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, fd); err != nil {
		return fmt.Errorf("verify: hash: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("verify: sha256 mismatch (want=%s got=%s)", want, got)
	}
	return nil
}

// extractPluginZipFromFD reads via io.NewSectionReader rather than
// re-opening the path so the byte stream stays identical to the
// verified one (TOCTOU defense).
func extractPluginZipFromFD(fd *os.File, size int64, dst string) error {
	zr, err := zip.NewReader(io.NewSectionReader(fd, 0, size), size)
	if err != nil {
		return fmt.Errorf("extract: open zip: %w", err)
	}

	root := detectSingleZipRoot(zr.File)
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("extract: abs dst: %w", err)
	}

	for _, f := range zr.File {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		// Skip non-regular zip entries (symlinks, devices, named
		// pipes). Symlink entries flagged with Unix lrwxrwxrwx mode
		// bits would otherwise be written as plain files containing
		// the link target string — an exfil vector.
		mode := f.Mode()
		if !f.FileInfo().IsDir() && !mode.IsRegular() {
			continue
		}
		if root != "" {
			if !strings.HasPrefix(name, root) {
				continue
			}
			name = strings.TrimPrefix(name, root)
			if name == "" {
				continue
			}
		}

		target := filepath.Join(absDst, name)
		rel, err := filepath.Rel(absDst, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("extract: entry %q escapes target", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extract: mkdir %s: %w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("extract: mkdir parent of %s: %w", target, err)
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry streams one zip entry into target preserving the
// entry's mode bits (executables stay executable — hook scripts need
// this). 0644 default when no mode is set.
func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("extract: open entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("extract: open target %s: %w", target, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("extract: copy %s: %w", target, err)
	}
	return nil
}

// detectSingleZipRoot returns the common root directory (with trailing
// slash) shared by every non-MACOSX entry, or "" when there is none.
// Hidden directories (".*") are NOT treated as wrappers because
// `.claude-plugin/` is a legitimate plugin component.
//
// `normaliseZipPath` strips trailing slashes, so a bare directory
// entry like `my-plugin/` arrives as `my-plugin` —
// indistinguishable from a top-level file. Skipping entries without an
// internal "/" lets us pick a real file path and infer the wrapper.
// Without this, `zip -r foo foo/` would short-circuit on the leading
// `foo/` directory entry and leave the manifest nested.
// (Mirrors server-side plugin_validator.detectSingleRoot — the two
// must agree.)
func detectSingleZipRoot(files []*zip.File) string {
	var first string
	for _, f := range files {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		if !strings.Contains(name, "/") {
			continue
		}
		first = name
		break
	}
	if first == "" {
		return ""
	}
	idx := strings.Index(first, "/")
	if idx <= 0 {
		return ""
	}
	root := first[:idx+1]
	if strings.HasPrefix(root, ".") {
		return ""
	}
	for _, f := range files {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		// Bare directory entries (e.g. `my-plugin`) arrive without a
		// trailing slash. If the entry equals the root with the slash
		// trimmed, it's the wrapping dir itself.
		if name+"/" == root {
			continue
		}
		if !strings.HasPrefix(name, root) {
			return ""
		}
	}
	return root
}

// normaliseZipPath converts back-slashes to forward slashes (some
// Windows zip writers emit `\`) and strips trailing slashes that
// directory entries may carry.
func normaliseZipPath(name string) string {
	p := strings.ReplaceAll(name, "\\", "/")
	return strings.TrimSuffix(p, "/")
}

// decodePluginDescriptors converts the raw agent_options["plugins"]
// value into a typed slice. Entries that fail to decode are dropped
// with a warning string returned alongside — the rest of the plugins
// might still be installable.
func decodePluginDescriptors(raw any) ([]pluginDescriptor, []string) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("agent_options[plugins] must be array, got %T", raw)}
	}
	out := make([]pluginDescriptor, 0, len(items))
	warnings := make([]string, 0)
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("plugins[%d]: not an object", i))
			continue
		}
		p := pluginDescriptor{
			Name:        stringField(obj, "name"),
			Version:     stringField(obj, "version"),
			DownloadURL: stringField(obj, "download_url"),
			SHA256:      stringField(obj, "sha256"),
		}
		if err := p.validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("plugins[%d] (%s): %v", i, p.Name, err))
			continue
		}
		out = append(out, p)
	}
	return out, warnings
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// validate is the daemon-side analogue of canonical.PluginSpec.Validate
// with a narrower contract — defense in depth, server-side validator
// is authoritative.
func (p pluginDescriptor) validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is required")
	}
	// Block path-traversal-ish names before they hit filepath.Join.
	if strings.ContainsAny(p.Name, "/\\") || p.Name == "." || p.Name == ".." {
		return fmt.Errorf("name %q contains path separator or dot-ref", p.Name)
	}
	if strings.TrimSpace(p.DownloadURL) == "" {
		return errors.New("download_url is required")
	}
	if len(p.SHA256) != 64 {
		return fmt.Errorf("sha256 must be 64 hex chars (got %d)", len(p.SHA256))
	}
	return nil
}

// cacheKey is what we stamp into <dir>/.cache-key. Including the
// sha256 means a re-published version with the same name+version (but
// rebuilt zip content) invalidates the cache.
func (p pluginDescriptor) cacheKey() string {
	return fmt.Sprintf("%s@%s", path.Clean(p.Name), strings.ToLower(p.SHA256))
}

// cloneAgentOptions returns a shallow copy of agent_options. Shallow
// is fine — we only overwrite the top-level "plugin_dirs" key.
func cloneAgentOptions(opts map[string]any) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(opts))
	for k, v := range opts {
		out[k] = v
	}
	return out
}

// mergePluginDirs combines a caller-supplied plugin_dirs override
// (accepted as []string OR []any) with the capability-resolved list,
// preserving order and deduplicating. Override wins on collision.
func mergePluginDirs(existing any, resolved []string) []string {
	preset := coerceStringSlice(existing)
	seen := make(map[string]bool, len(preset)+len(resolved))
	out := make([]string, 0, len(preset)+len(resolved))
	for _, d := range preset {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, d := range resolved {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// coerceStringSlice accepts the two wire shapes opts["plugin_dirs"]
// can take: a pre-typed []string or a JSON-decoded []any of strings.
// BuildArgs' stringSlice errors on bad shapes downstream, so a clean
// degradation here is fine.
func coerceStringSlice(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
