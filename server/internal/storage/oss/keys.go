package oss

import (
	"path"
	"strings"

	"github.com/google/uuid"
)

// PluginObjectPrefix is the bucket directory for capability-plugin
// zips. Trailing slash is added by JoinKey.
const PluginObjectPrefix = "capabilities/plugins"

// SkillObjectPrefix is the per-tenant directory for multi-file
// skill zips. Layout mirrors PluginObjectPrefix so the auth +
// presign machinery treats them symmetrically.
const SkillObjectPrefix = "capabilities/skills"

// capabilityObjectPrefixes is the closed set of legal prefixes for
// workspace-scoped capability blobs. KeyBelongsToWorkspace iterates
// over this so a new capability kind just adds an entry.
var capabilityObjectPrefixes = []string{PluginObjectPrefix, SkillObjectPrefix}

// NewPluginObjectKey mints a fresh object key:
//
//	capabilities/plugins/<workspaceID>/<uuid>/<filename>
//
// The workspaceID prefix makes the key self-describing — download
// can refuse a key that doesn't start with the caller's workspace
// without an extra storage lookup. Pure; safe for concurrent use.
func NewPluginObjectKey(workspaceID, filename string) string {
	return newCapabilityObjectKey(PluginObjectPrefix, workspaceID, filename)
}

// NewSkillObjectKey is the Skill-zip counterpart with identical
// layout under SkillObjectPrefix.
func NewSkillObjectKey(workspaceID, filename string) string {
	return newCapabilityObjectKey(SkillObjectPrefix, workspaceID, filename)
}

func newCapabilityObjectKey(prefix, workspaceID, filename string) string {
	workspaceID = sanitizeWorkspaceID(workspaceID)
	filename = sanitizeFilename(filename)
	return path.Join(prefix, workspaceID, uuid.NewString(), filename)
}

// KeyBelongsToWorkspace reports whether the given OSS key was
// minted for the given workspace. Shape-based: a key with extra
// leading slashes, traversal segments, or a different prefix gets
// false. path.Clean only collapses adjacent separators; it does
// NOT promote a rooted-out path back to a valid relative one, so
// `../capabilities/...` survives Clean and is rejected here.
func KeyBelongsToWorkspace(key, workspaceID string) bool {
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(key) == "" {
		return false
	}
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, "../") || key == ".." {
		return false
	}
	if strings.Contains(key, "/..") || strings.Contains(key, "../") {
		return false
	}
	cleaned := path.Clean(key)
	wid := sanitizeWorkspaceID(workspaceID)
	for _, prefix := range capabilityObjectPrefixes {
		expected := prefix + "/" + wid + "/"
		if strings.HasPrefix(cleaned, expected) && len(cleaned) > len(expected) {
			return true
		}
	}
	return false
}

// sanitizeWorkspaceID rejects anything containing a path separator,
// control char, or dot-traversal. Empty → "_unknown" so the key
// still has a valid shape; the auth layer is the real check.
func sanitizeWorkspaceID(wid string) string {
	wid = strings.TrimSpace(wid)
	if wid == "" {
		return "_unknown"
	}
	if strings.ContainsAny(wid, "/\\\x00") || wid == "." || wid == ".." {
		return "_invalid"
	}
	return wid
}

// JoinKey stitches an object key from segments, stripping leading
// slashes from each (OSS rejects keys with leading "/").
func JoinKey(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		cleaned = append(cleaned, p)
	}
	return strings.Join(cleaned, "/")
}

// sanitizeFilename reduces the filename to a safe basename. Empty
// or traversal values ("..", ".", "/") normalize to "plugin.zip"
// (path.Base("..") returns ".." verbatim, hence the explicit list).
func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "plugin.zip"
	}
	name = path.Base(name)
	if name == "." || name == ".." || name == "/" || name == "" {
		return "plugin.zip"
	}
	return name
}
