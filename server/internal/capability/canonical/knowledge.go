package canonical

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Knowledge documents are inline reference data, never executable files or paths.
const MaxKnowledgeBytes = 32 * 1024
const MaxKnowledgeDocuments = 16

type KnowledgeDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type KnowledgeSpec struct {
	Documents []KnowledgeDocument `json:"documents"`
}

func (s KnowledgeSpec) Validate() error {
	if len(s.Documents) == 0 || len(s.Documents) > MaxKnowledgeDocuments {
		return fmt.Errorf("%w: knowledge requires 1 to %d documents", ErrInvalidSpec, MaxKnowledgeDocuments)
	}
	seen := map[string]bool{}
	size := 0
	for _, doc := range s.Documents {
		name := strings.TrimSpace(doc.Name)
		if name == "" || len(name) > 255 || !utf8.ValidString(name) || strings.ContainsAny(name, "\x00\r\n") {
			return fmt.Errorf("%w: document name must contain 1 to 255 UTF-8 bytes on one line", ErrInvalidSpec)
		}
		if seen[name] {
			return fmt.Errorf("%w: duplicate document name %q", ErrInvalidSpec, name)
		}
		seen[name] = true
		if strings.TrimSpace(doc.Content) == "" || !utf8.ValidString(doc.Content) || strings.ContainsRune(doc.Content, 0) {
			return fmt.Errorf("%w: document %q must contain non-empty UTF-8 text", ErrInvalidSpec, name)
		}
		size += len(doc.Name) + len(doc.Content)
	}
	if size > MaxKnowledgeBytes {
		return fmt.Errorf("%w: knowledge documents exceed %d bytes", ErrInvalidSpec, MaxKnowledgeBytes)
	}
	return nil
}
