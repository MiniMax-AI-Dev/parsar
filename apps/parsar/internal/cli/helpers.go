package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// emitJSON marshals v as 2-space-indented JSON with a trailing newline.
// HTML escaping is disabled because injection blocks contain literal
// `<spec>` / `<memory>` tags that hook scripts forward verbatim.
func emitJSON(w io.Writer, v any) error {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	if _, err := io.WriteString(w, buf.String()); err != nil {
		return err
	}
	return nil
}

// splitTags parses a CSV tag value. Empty input returns nil (no
// filter), not []string{} (which would serialize as `?tag=`).
func splitTags(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// truncate clips s to n runes (not bytes), appending "…" when clipped.
func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// resolveBody returns raw, unless raw == "-" in which case it reads
// the body from stdin. Empty raw is passed through; callers decide
// whether empty is allowed.
func resolveBody(raw string) (string, error) {
	if raw != "-" {
		return raw, nil
	}
	buf, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read body from stdin: %w", err)
	}
	return string(buf), nil
}
