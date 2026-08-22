package paths

import "strings"

// StateKeyParts splits an AgentStateKey into filesystem-safe path parts for
// an adapter state directory under Root(). It is the traversal guard for a
// server-supplied key: every part is reduced to [A-Za-z0-9._-], and "." /
// ".." parts are dropped rather than sanitized into a name that still
// escapes. An empty result means the key carried nothing usable.
func StateKeyParts(key string) []string {
	rawParts := strings.Split(key, "/")
	parts := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if safe := SafePathPart(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	return parts
}

// SafePathPart reduces one path component to filesystem-safe characters,
// returning "" for a component that cannot be used as a directory name.
func SafePathPart(part string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(part) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "." || out == ".." {
		return ""
	}
	return out
}
