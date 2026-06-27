package pi

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/google/uuid"
)

// skillDescriptor is the daemon-side view of one server-sent skill entry
// under agent_options["skills"]:
//
//	{ "name": "...", "version": "...", "download_url": "...", "sha256": "..." }
type skillDescriptor struct {
	Name        string
	Version     string
	DownloadURL string
	SHA256      string
}

// SkillInstallResult carries the per-skill directories to feed into
// repeated `--skill <dir>` flags plus warnings the session surfaces.
// Unlike Claude Code (which auto-scans .claude/skills/), pi needs an
// explicit flag per skill, so SkillDirs is populated even on a cache hit.
type SkillInstallResult struct {
	SkillDirs []string
	Warnings  []string
}

const skillInstallTimeout = 60 * time.Second

// maxSkillZipBytes mirrors the server-side cap. Defense in depth.
const maxSkillZipBytes int64 = 32 * 1024 * 1024

var skillsHTTPClient = &http.Client{Timeout: skillInstallTimeout + 10*time.Second}

// installSkills materialises every skill under <root>/<name>/ and returns
// the local paths. Per skill:
//
//  1. Cache hit (<dir>/.cache-key == name@sha256) returns the dir without
//     a network round-trip — but still returns it, so --skill is injected
//     on every turn.
//  2. Fetch → verify SHA-256 → extract (single wrapping dir stripped,
//     __MACOSX/ ignored) → stamp .cache-key.
//
// Errors during fetch/verify/extract demote one skill to a warning and
// continue. A hard error means the root dir itself was uncreatable.
func installSkills(
	ctx context.Context,
	logger *slog.Logger,
	root string,
	skills []skillDescriptor,
) (SkillInstallResult, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(skills) == 0 {
		return SkillInstallResult{}, nil
	}
	if strings.TrimSpace(root) == "" {
		return SkillInstallResult{}, errors.New("pi skills: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SkillInstallResult{}, fmt.Errorf("pi skills: mkdir %s: %w", root, err)
	}

	result := SkillInstallResult{}
	for _, s := range skills {
		if err := s.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip skill (invalid descriptor): %v", err))
			logger.Warn("pi skills: invalid descriptor", "err", err.Error())
			continue
		}

		dir := filepath.Join(root, s.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := s.cacheKey()

		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("pi skills: cache hit", "name", s.Name, "version", s.Version, "dir", dir)
			result.SkillDirs = append(result.SkillDirs, dir)
			continue
		}

		perCtx, cancel := context.WithTimeout(ctx, skillInstallTimeout)
		err := installOneSkill(perCtx, logger, root, dir, cacheKey, expectedKey, s)
		cancel()
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skill %s@%s: %v", s.Name, s.Version, err))
			logger.Warn("pi skills: install failed", "name", s.Name, "version", s.Version, "err", err.Error())
			continue
		}
		result.SkillDirs = append(result.SkillDirs, dir)
		logger.Info("pi skills: installed", "name", s.Name, "version", s.Version, "dir", dir)
	}
	return result, nil
}

func installOneSkill(
	ctx context.Context,
	logger *slog.Logger,
	root, dir, cacheKey, expectedKey string,
	s skillDescriptor,
) error {
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}

	// Per-call uuid so concurrent installs of the same (name, version)
	// don't truncate each other's bytes, and nothing on disk between
	// verify and extract can be a different file than the one hashed.
	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", s.Name, s.Version, uuid.NewString()))
	defer func() { _ = os.Remove(zipPath) }()

	fd, err := fetchSkillZip(ctx, s.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	// Verify and extract BOTH read through the same FD (not the path):
	// Unix file semantics pin the inode, so a swap on disk between
	// hashing and extraction cannot change the bytes we use.
	if err := verifySHA256FromFD(fd, s.SHA256); err != nil {
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
	if err := extractSkillZipFromFD(fd, fi.Size(), dir); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	if err := os.WriteFile(cacheKey, []byte(expectedKey), 0o644); err != nil {
		logger.Warn("pi skills: write cache key failed", "path", cacheKey, "err", err.Error())
	}
	return nil
}

// fetchSkillZip GETs url into dst, capping the body at maxSkillZipBytes.
// Returns an OPEN file descriptor at offset 0; the caller closes it.
// Holding the FD across verify + extract closes the TOCTOU between
// hashing the on-disk bytes and reading them for extract.
//
// Only http/https are accepted to defend against a future download_url
// reaching this code with file:// or http://internal-ip/... values.
func fetchSkillZip(ctx context.Context, downloadURL, dst string) (*os.File, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("download_url must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, errors.New("build request failed")
	}
	resp, err := skillsHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get failed: %s", sanitizeHTTPClientError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("get: status %d", resp.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open dst: %w", err)
	}

	limited := io.LimitReader(resp.Body, maxSkillZipBytes+1)
	written, err := io.Copy(f, limited)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("copy body: %w", err)
	}
	if written > maxSkillZipBytes {
		_ = f.Close()
		return nil, fmt.Errorf("zip exceeds %d byte cap", maxSkillZipBytes)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seek after write: %w", err)
	}
	return f, nil
}

// sanitizeHTTPClientError strips the URL embedded by *url.Error so a
// presigned download_url (OSSAccessKeyId + Signature) never lands in the
// daemon log. Format is `<method> "<url>": <inner>`.
func sanitizeHTTPClientError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	open := strings.Index(msg, `"`)
	if open < 0 {
		return msg
	}
	closeRel := strings.Index(msg[open+1:], `"`)
	if closeRel < 0 {
		return msg
	}
	closeAbs := open + 1 + closeRel
	if closeAbs+2 > len(msg) {
		return msg
	}
	return msg[:open] + "<redacted-url>" + msg[closeAbs+1:]
}

func verifySHA256FromFD(fd *os.File, want string) error {
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

// extractSkillZipFromFD reads via io.NewSectionReader rather than
// re-opening the path so the byte stream stays identical to the verified
// one (TOCTOU defense).
func extractSkillZipFromFD(fd *os.File, size int64, dst string) error {
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
		// Skip non-regular entries (symlinks, devices). A symlink entry
		// would otherwise be written as a plain file holding the link
		// target string — an exfil vector.
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

// detectSingleZipRoot returns the common wrapping directory (with
// trailing slash) shared by every non-MACOSX entry, or "" when there is
// none. Bare directory entries (no internal "/") are skipped when picking
// the first candidate so `zip -r skill skill/` doesn't short-circuit on
// its own leading directory entry. Hidden roots (".*") are NOT treated as
// wrappers.
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
		if name+"/" == root {
			continue
		}
		if !strings.HasPrefix(name, root) {
			return ""
		}
	}
	return root
}

func normaliseZipPath(name string) string {
	p := strings.ReplaceAll(name, "\\", "/")
	return strings.TrimSuffix(p, "/")
}

// decodeSkillDescriptors converts agent_options["skills"] into typed
// descriptors. Entries that fail to decode are dropped with a warning;
// the rest may still be installable.
func decodeSkillDescriptors(raw any) ([]skillDescriptor, []string) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("agent_options[skills] must be array, got %T", raw)}
	}
	out := make([]skillDescriptor, 0, len(items))
	warnings := make([]string, 0)
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skills[%d]: not an object", i))
			continue
		}
		s := skillDescriptor{
			Name:        stringField(obj, "name"),
			Version:     stringField(obj, "version"),
			DownloadURL: stringField(obj, "download_url"),
			SHA256:      stringField(obj, "sha256"),
		}
		if err := s.validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("skills[%d] (%s): %v", i, s.Name, err))
			continue
		}
		out = append(out, s)
	}
	return out, warnings
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func (s skillDescriptor) validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	// Block path-traversal names before they hit filepath.Join.
	if strings.ContainsAny(s.Name, "/\\") || s.Name == "." || s.Name == ".." {
		return fmt.Errorf("name %q contains path separator or dot-ref", s.Name)
	}
	if strings.TrimSpace(s.DownloadURL) == "" {
		return errors.New("download_url is required")
	}
	if len(s.SHA256) != 64 {
		return fmt.Errorf("sha256 must be 64 hex chars (got %d)", len(s.SHA256))
	}
	return nil
}

func (s skillDescriptor) cacheKey() string {
	return fmt.Sprintf("%s@%s", strings.TrimSpace(s.Name), strings.ToLower(s.SHA256))
}

// resolveSkillsRoot returns the absolute directory under which managed
// skills install, one subdir per skill. Kept under ~/.parsar/ (runtime
// state lives there, not the user's project tree) and scoped per
// conversation so consecutive turns reuse .cache-key files without two
// conversations racing the same skill dir. runID scopes the one-shot
// fallback when there is no conversation.
func resolveSkillsRoot(conversationID, runID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("pi skills: resolve home: %w", err)
	}
	base := filepath.Join(home, ".parsar", "runtime", "pi")
	if id := strings.TrimSpace(conversationID); id != "" {
		return filepath.Join(base, "conv-"+id, "skills"), nil
	}
	return filepath.Join(base, "run-"+strings.TrimSpace(runID), "skills"), nil
}

// mergeSkillDirs combines a caller-supplied skill_dirs override (accepted
// as []string OR []any) with the install-resolved list, preserving order
// and deduplicating. Override wins on collision.
func mergeSkillDirs(existing any, resolved []string) []string {
	preset := coerceStringSlice(existing)
	seen := make(map[string]bool, len(preset)+len(resolved))
	out := make([]string, 0, len(preset)+len(resolved))
	for _, d := range append(append([]string{}, preset...), resolved...) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

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

// cloneAgentOptions returns a shallow copy so we never mutate the
// caller's map when overwriting the top-level "skill_dirs" key.
func cloneAgentOptions(opts map[string]any) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(opts))
	maps.Copy(out, opts)
	return out
}
