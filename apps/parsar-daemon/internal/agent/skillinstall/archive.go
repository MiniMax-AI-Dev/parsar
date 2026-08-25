package skillinstall

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxSkillZipBytes mirrors the server-side upload cap as daemon-side defence in depth.
const maxSkillZipBytes int64 = 32 * 1024 * 1024

var skillsHTTPClient = &http.Client{Timeout: skillInstallTimeout + 10*time.Second}

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

// sanitizeHTTPClientError removes credentials embedded in presigned URLs.
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

// verifySHA256FromFD keeps verification and extraction on the same inode.
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
		mode := f.Mode()
		// Symlinks and devices must never materialise from a capability archive.
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

func detectSingleZipRoot(files []*zip.File) string {
	var first string
	for _, f := range files {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" || !strings.Contains(name, "/") {
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
	return strings.TrimSuffix(strings.ReplaceAll(name, "\\", "/"), "/")
}
