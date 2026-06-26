package parser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// PluginValidationResult is the structured outcome of ValidatePluginZip.
// A non-empty Errors slice means the import handler MUST reject the
// upload. Warnings are advisory and the import is still allowed.
// Manifest is populated even when Warnings is non-empty so the canonical
// Spec can be built without re-parsing.
type PluginValidationResult struct {
	Valid    bool             `json:"valid"`
	Errors   []string         `json:"errors,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
	Manifest PluginManifestV1 `json:"manifest"`
}

// PluginManifestV1 is the subset of Claude Code's plugin.json schema
// that we consume. Fields not listed here are still allowed in the
// source file — Claude Code parses them at runtime.
type PluginManifestV1 struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Author      Author   `json:"author,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	License     string   `json:"license,omitempty"`
}

// Author mirrors the official { name, email, url } shape.
type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// ErrInvalidPluginZip is the sentinel for the "this is not a parseable
// zip at all" case; soft validation failures live in
// PluginValidationResult.Errors instead.
var ErrInvalidPluginZip = errors.New("parser: invalid plugin zip")

// MaxPluginZipBytes is the in-memory cap for a single plugin upload.
// 32 MiB matches the storage-side download cap (storage/oss.MaxDownloadBytes
// is 64 MiB) with 2x headroom for tarball→zip repackaging.
const MaxPluginZipBytes = 32 * 1024 * 1024

// pluginManifestPath is the only place Claude Code reads plugin
// metadata from; the location is hard-coded to surface packaging
// mistakes instead of masking them with a fallback search.
const pluginManifestPath = ".claude-plugin/plugin.json"

const maxPluginNameLen = 128

// invalidPluginNameRune rejects characters that would break the manifest
// when the name flows into JSON, filesystem paths, or shell commands.
// Everything else (Unicode letters, digits, spaces, CJK, punctuation) is
// allowed — matching official Claude Code's relaxed naming rules.
func invalidPluginNameRune(r rune) bool {
	switch r {
	case '/', '\\', '\x00':
		return true
	}
	if r < 0x20 || r == 0x7f {
		return true
	}
	return false
}

// validatePluginName returns "" when name is acceptable or a user-facing
// error string. Mirrors the rules in canonical/plugin.go.
func validatePluginName(name string) string {
	if name == "" {
		return `.claude-plugin/plugin.json is missing required field "name"`
	}
	if len(name) > maxPluginNameLen {
		return fmt.Sprintf(`plugin.json name is too long (%d bytes, max %d)`, len(name), maxPluginNameLen)
	}
	if name == "." || name == ".." {
		return fmt.Sprintf(`plugin.json name %q is reserved`, name)
	}
	for _, r := range name {
		if invalidPluginNameRune(r) {
			return fmt.Sprintf(`plugin.json name %q contains an unsupported character (path separators and control characters are not allowed)`, name)
		}
	}
	return ""
}

// componentDirs is the closed set of subdirectories Claude Code reads
// at plugin load. Used only for the "components-in-.claude-plugin/"
// warning.
var componentDirs = []string{"commands", "agents", "skills", "hooks", "scripts"}

// ValidatePluginZip parses buf as a zip and checks every structural
// rule. Returns a PluginValidationResult whose Errors slice is non-empty
// iff the zip cannot be safely imported.
//
// Pure — no I/O beyond reading buf. Safe to call from any goroutine.
func ValidatePluginZip(buf []byte) (*PluginValidationResult, error) {
	result := &PluginValidationResult{Valid: true}

	if len(buf) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "empty upload (0 bytes)")
		return result, nil
	}
	if int64(len(buf)) > MaxPluginZipBytes {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("plugin zip exceeds %d byte cap (got %d)", MaxPluginZipBytes, len(buf)))
		return result, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "not a valid zip file: "+err.Error())
		return result, nil
	}

	// detectSingleRoot strips a well-formed wrapping directory
	// ("my-plugin/.claude-plugin/...") so both layouts validate the
	// same way. Real-world zips from `zip -r my-plugin/` include the
	// wrapper.
	rawPaths := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		rawPaths = append(rawPaths, normalizeZipPath(f.Name))
	}
	root := detectSingleRoot(rawPaths)
	stripped := make([]string, 0, len(rawPaths))
	pathSet := make(map[string]bool, len(rawPaths))
	for _, p := range rawPaths {
		if root != "" {
			p = strings.TrimPrefix(p, root)
		}
		if p == "" {
			continue
		}
		stripped = append(stripped, p)
		pathSet[p] = true
	}

	if !pathSet[pluginManifestPath] {
		result.Valid = false
		result.Errors = append(result.Errors, "missing .claude-plugin/plugin.json (plugin manifest)")
		return result, nil
	}

	manifestBytes, err := readZipEntry(zr, root, pluginManifestPath)
	if err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "could not read .claude-plugin/plugin.json: "+err.Error())
		return result, nil
	}
	var manifest PluginManifestV1
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ".claude-plugin/plugin.json is not valid JSON: "+err.Error())
		return result, nil
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if msg := validatePluginName(manifest.Name); msg != "" {
		result.Valid = false
		result.Errors = append(result.Errors, msg)
		return result, nil
	}
	result.Manifest = manifest

	// Component dirs nested inside .claude-plugin/ indicate a packaging
	// mistake — Claude Code reads .claude-plugin/ for the manifest only.
	for _, dir := range componentDirs {
		nested := ".claude-plugin/" + dir + "/"
		for _, p := range stripped {
			if strings.HasPrefix(p, nested) {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%q directory is nested under .claude-plugin/; move it to the plugin root", dir))
				break
			}
		}
	}

	// commands/*.md must carry YAML frontmatter — otherwise Claude Code
	// treats them as plain text and ignores the declared name /
	// allowed-tools. Nested dirs are out of scope.
	for _, p := range stripped {
		if !strings.HasPrefix(p, "commands/") || !strings.HasSuffix(p, ".md") {
			continue
		}
		rel := strings.TrimPrefix(p, "commands/")
		if strings.Contains(rel, "/") {
			continue // skip nested
		}
		content, err := readZipEntry(zr, root, p)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("could not read %s: %s", p, err.Error()))
			continue
		}
		trimmed := strings.TrimLeftFunc(string(content), func(r rune) bool {
			return r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
		if !strings.HasPrefix(trimmed, "---") {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: missing YAML frontmatter (file should start with ---)", p))
		}
	}

	// skills/<name>/ must contain SKILL.md or Claude Code skips loading it.
	skillDirs := map[string]bool{}
	for _, p := range stripped {
		if !strings.HasPrefix(p, "skills/") {
			continue
		}
		rest := strings.TrimPrefix(p, "skills/")
		idx := strings.Index(rest, "/")
		if idx <= 0 {
			continue
		}
		skillDirs[rest[:idx]] = true
	}
	for name := range skillDirs {
		want := "skills/" + name + "/SKILL.md"
		if !pathSet[want] {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skills/%s/: missing SKILL.md", name))
		}
	}

	// hooks/hooks.json must be valid JSON when present (field-shape
	// validation is out of scope).
	if pathSet["hooks/hooks.json"] {
		if content, err := readZipEntry(zr, root, "hooks/hooks.json"); err == nil {
			if !json.Valid(content) {
				result.Warnings = append(result.Warnings, "hooks/hooks.json is not valid JSON")
			}
		} else {
			result.Warnings = append(result.Warnings, "could not read hooks/hooks.json: "+err.Error())
		}
	}

	// .mcp.json must be valid JSON when present.
	if pathSet[".mcp.json"] {
		if content, err := readZipEntry(zr, root, ".mcp.json"); err == nil {
			if !json.Valid(content) {
				result.Warnings = append(result.Warnings, ".mcp.json is not valid JSON")
			}
		} else {
			result.Warnings = append(result.Warnings, "could not read .mcp.json: "+err.Error())
		}
	}

	return result, nil
}

// normalizeZipPath converts back-slashes to forward slashes (some
// Windows zip writers emit `\`) and strips trailing slashes that
// directory entries may carry.
func normalizeZipPath(name string) string {
	p := strings.ReplaceAll(name, "\\", "/")
	return strings.TrimSuffix(p, "/")
}

// detectSingleRoot returns the common directory prefix that every
// non-empty path shares, or "" if there isn't one. Strips a wrapping
// `my-plugin/` directory that `zip -r my-plugin/` produces.
//
// Hidden-dir-only zips (entries all starting with `.claude-plugin/`)
// are NOT treated as having a root — that's a malformed plugin, not a
// wrapper.
func detectSingleRoot(paths []string) string {
	// `normalizeZipPath` strips trailing slashes, so a bare directory
	// entry like "vela-dev/" arrives here as "vela-dev" —
	// indistinguishable from a top-level file. Skipping entries without
	// an internal "/" lets us pick the first real file path and infer
	// the wrapper; otherwise `zip -r foo foo/` zips on macOS would fail
	// to unwrap.
	var first string
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "__MACOSX/") || p == "__MACOSX" {
			continue
		}
		if !strings.Contains(p, "/") {
			continue
		}
		first = p
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
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "__MACOSX/") || p == "__MACOSX" {
			continue
		}
		// A bare directory entry equal to the root with the slash
		// trimmed is the wrapping dir itself.
		if p+"/" == root {
			continue
		}
		if !strings.HasPrefix(p, root) {
			return ""
		}
	}
	return root
}

// readZipEntry locates the named entry inside zr (accounting for an
// optional wrapping root directory) and returns its decompressed bytes.
//
// The 1 MiB per-entry cap defends against a zip declaring a tiny
// compressed size that expands to gigabytes.
func readZipEntry(zr *zip.Reader, root, relPath string) ([]byte, error) {
	target := relPath
	if root != "" {
		target = path.Join(root, relPath)
	}
	for _, f := range zr.File {
		if normalizeZipPath(f.Name) != target {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		const perEntryCap int64 = 1 << 20 // 1 MiB
		limited := io.LimitReader(rc, perEntryCap+1)
		buf, err := io.ReadAll(limited)
		if err != nil {
			return nil, err
		}
		if int64(len(buf)) > perEntryCap {
			return nil, fmt.Errorf("entry %s exceeds %d byte read cap", relPath, perEntryCap)
		}
		return buf, nil
	}
	return nil, fmt.Errorf("entry %s not found in zip", relPath)
}
