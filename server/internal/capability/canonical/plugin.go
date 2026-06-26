package canonical

import (
	"fmt"
	"regexp"
	"strings"
)

// PluginSpec is the body for Spec{Kind: KindPlugin} — a Claude Code
// plugin zip stored in Aliyun OSS. Name must match the `name` field
// inside the zip's plugin.json and be unique within a workspace. Only
// the Claude Code renderer consumes plugins; OpenCode / Codex renderers
// return ErrUnsupportedKindForTarget.
type PluginSpec struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`

	// OssKey is duplicated on the capability_version row so SQL queries
	// can filter without jsonb path access; the row column is
	// authoritative and the spec copy is a snapshot.
	OssKey string `json:"oss_key"`
	SHA256 string `json:"sha256"`

	UploadSource UploadSource `json:"upload_source"`
	// GitHub* fields are populated only when UploadSource ==
	// UploadSourceGitHub and power re-sync from upstream.
	GitHubRepo string `json:"github_repo,omitempty"`
	GitHubRef  string `json:"github_ref,omitempty"`
	GitHubPath string `json:"github_path,omitempty"`
}

// UploadSource discriminates how a plugin version landed in OSS.
type UploadSource string

const (
	// UploadSourceZip is a direct user upload via OSS presigned PUT.
	UploadSourceZip UploadSource = "zip"

	// UploadSourceGitHub is a server-side sync from a public GitHub
	// repo (tarball API → extract → repackage → OSS).
	UploadSourceGitHub UploadSource = "github"
)

// maxPluginNameLen mirrors parser/plugin_validator.go; both layers must
// agree. The parser is the first line of defence, Validate the second.
const maxPluginNameLen = 128

// invalidPluginNameRune forbids only path separators and control chars,
// matching official Claude Code which has no kebab-case requirement.
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

// sha256HexRe matches 64 lowercase hex characters. Strict because
// daemon-side verification uses byte comparison; mixed case or truncated
// digests would create a silent verification bypass.
var sha256HexRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Validate enforces structural sanity. Pure: no DB / network access.
func (p PluginSpec) Validate() error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidPlugin)
	}
	if len(name) > maxPluginNameLen {
		return fmt.Errorf("%w: name is too long (%d bytes, max %d)", ErrInvalidPlugin, len(name), maxPluginNameLen)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: name %q is reserved", ErrInvalidPlugin, name)
	}
	for _, r := range name {
		if invalidPluginNameRune(r) {
			return fmt.Errorf("%w: name %q contains an unsupported character (path separators and control characters are not allowed)", ErrInvalidPlugin, name)
		}
	}
	if strings.TrimSpace(p.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidPlugin)
	}
	if strings.TrimSpace(p.OssKey) == "" {
		return fmt.Errorf("%w: oss_key is required", ErrInvalidPlugin)
	}
	if !sha256HexRe.MatchString(p.SHA256) {
		return fmt.Errorf("%w: sha256 must be 64 lowercase hex chars (got %d chars)", ErrInvalidPlugin, len(p.SHA256))
	}
	switch p.UploadSource {
	case UploadSourceZip:
		if p.GitHubRepo != "" || p.GitHubRef != "" || p.GitHubPath != "" {
			return fmt.Errorf("%w: upload_source=zip must not set github_* fields", ErrInvalidPlugin)
		}
	case UploadSourceGitHub:
		if strings.TrimSpace(p.GitHubRepo) == "" {
			return fmt.Errorf("%w: upload_source=github requires github_repo", ErrInvalidPlugin)
		}
	default:
		return fmt.Errorf("%w: upload_source must be %q or %q (got %q)", ErrInvalidPlugin, UploadSourceZip, UploadSourceGitHub, p.UploadSource)
	}
	return nil
}
