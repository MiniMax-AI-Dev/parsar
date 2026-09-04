package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// ManagedSkillsRoot returns an adapter-owned skill directory scoped to one
// agent state. It never derives runtime state from the subprocess cwd.
func ManagedSkillsRoot(agentKind, agentStateKey, conversationID, runID string) (string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", fmt.Errorf("agent: resolve managed skills root: %w", err)
	}
	kind := safeRuntimePathPart(agentKind)
	if kind == "" {
		return "", fmt.Errorf("agent: invalid agent kind %q", agentKind)
	}
	base := filepath.Join(root, "runtime", kind)
	if key := strings.TrimSpace(agentStateKey); key != "" {
		parts := safeRuntimePathParts(key)
		if len(parts) == 0 {
			return "", fmt.Errorf("agent: invalid agent state key %q", agentStateKey)
		}
		return filepath.Join(append([]string{base, "state"}, append(parts, "skills")...)...), nil
	}
	if id := safeRuntimePathPart(conversationID); id != "" {
		return filepath.Join(base, "conv-"+id, "skills"), nil
	}
	if id := safeRuntimePathPart(runID); id != "" {
		return filepath.Join(base, "run-"+id, "skills"), nil
	}
	return "", fmt.Errorf("agent: agent state key, conversation id, or run id is required")
}

func safeRuntimePathParts(value string) []string {
	raw := strings.Split(value, "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		if safe := safeRuntimePathPart(part); safe != "" {
			parts = append(parts, safe)
		}
	}
	return parts
}

func safeRuntimePathPart(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	value = b.String()
	if value == "." || value == ".." {
		return ""
	}
	return value
}
