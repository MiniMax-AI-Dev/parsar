package specmemory

import (
	"bufio"
	"strings"
)

const importHeadingPrefix = "## "

// ImportedFragment is the slicer output. The caller stamps
// ID / WorkspaceID / Source / CreatedBy / AgentActor / timestamps.
type ImportedFragment struct {
	Title string
	Body  string
}

// ImportFragmentsFromMarkdown splits a markdown blob into fragments at
// every level-2 heading. Content before the first H2 and empty-body
// sections are dropped. Body keeps internal blank lines.
func ImportFragmentsFromMarkdown(text string) []ImportedFragment {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var (
		out          []ImportedFragment
		currentSet   bool
		currentTitle string
		currentBody  strings.Builder
	)
	flush := func() {
		if !currentSet {
			return
		}
		body := strings.TrimRight(currentBody.String(), " \t\n")
		if body == "" {
			currentSet = false
			currentBody.Reset()
			return
		}
		out = append(out, ImportedFragment{
			Title: currentTitle,
			Body:  body,
		})
		currentSet = false
		currentBody.Reset()
	}

	scanner := bufio.NewScanner(strings.NewReader(text))
	// Pasted spec docs can have wide tables / code blocks beyond
	// bufio's 64KB default token cap.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if isImportHeading(line) {
			flush()
			currentTitle = strings.TrimSpace(strings.TrimPrefix(line, importHeadingPrefix))
			currentSet = true
			continue
		}
		if currentSet {
			currentBody.WriteString(line)
			currentBody.WriteString("\n")
		}
	}
	flush()
	return out
}

// isImportHeading reports whether the line is a level-2 markdown
// heading. Rejects level-3+ and headings with no title.
func isImportHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, importHeadingPrefix) {
		return false
	}
	if strings.HasPrefix(trimmed, "### ") {
		return false
	}
	return strings.TrimSpace(trimmed[len(importHeadingPrefix):]) != ""
}
