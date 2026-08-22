package skillinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
)

// Descriptor is one server-resolved Skill source.
type Descriptor struct {
	Name        string
	Version     string
	DownloadURL string
	SHA256      string
	Content     string
}

// Decode converts agent_options skills into validated descriptors.
func Decode(raw any) ([]Descriptor, []string) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("agent_options[skills] must be array, got %T", raw)}
	}
	out := make([]Descriptor, 0, len(items))
	warnings := make([]string, 0)
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skills[%d]: not an object", i))
			continue
		}
		s := Descriptor{
			Name: stringField(obj, "name"), Version: stringField(obj, "version"),
			DownloadURL: stringField(obj, "download_url"), SHA256: stringField(obj, "sha256"),
			Content: stringField(obj, "content"),
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

func (s Descriptor) validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if strings.ContainsAny(s.Name, "/\\") || s.Name == "." || s.Name == ".." {
		return fmt.Errorf("name %q contains path separator or dot-ref", s.Name)
	}
	hasInline := strings.TrimSpace(s.Content) != ""
	hasArchive := strings.TrimSpace(s.DownloadURL) != "" || strings.TrimSpace(s.SHA256) != ""
	if hasInline == hasArchive {
		return errors.New("exactly one of content or download_url + sha256 is required")
	}
	if hasArchive {
		if strings.TrimSpace(s.DownloadURL) == "" {
			return errors.New("download_url is required")
		}
		if len(s.SHA256) != 64 {
			return fmt.Errorf("sha256 must be 64 hex chars (got %d)", len(s.SHA256))
		}
		if _, err := hex.DecodeString(s.SHA256); err != nil {
			return errors.New("sha256 must be hexadecimal")
		}
	}
	return nil
}

func (s Descriptor) cacheKey() string {
	digest := strings.ToLower(s.SHA256)
	if s.isInline() {
		sum := sha256.Sum256([]byte(s.Content))
		digest = hex.EncodeToString(sum[:])
	}
	return fmt.Sprintf("%s@%s", strings.TrimSpace(s.Name), digest)
}

func (s Descriptor) isInline() bool { return strings.TrimSpace(s.Content) != "" }

// ResolveRoot returns a state-scoped Skill installation root.
func ResolveRoot(runtimeName, conversationID, runID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("skill install: resolve home: %w", err)
	}
	runtimeName = strings.TrimSpace(runtimeName)
	if runtimeName == "" || strings.ContainsAny(runtimeName, "/\\") || runtimeName == "." || runtimeName == ".." {
		return "", fmt.Errorf("skill install: invalid runtime name %q", runtimeName)
	}
	base := filepath.Join(home, ".parsar", "runtime", runtimeName)
	if id := strings.TrimSpace(conversationID); id != "" {
		return filepath.Join(base, "conv-"+id, "skills"), nil
	}
	return filepath.Join(base, "run-"+strings.TrimSpace(runID), "skills"), nil
}

// MergeDirs appends resolved paths to configured paths without duplicates.
func MergeDirs(existing any, resolved []string) []string {
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

// CloneOptions returns a shallow copy of agent options.
func CloneOptions(opts map[string]any) map[string]any {
	if opts == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(opts))
	maps.Copy(out, opts)
	return out
}
