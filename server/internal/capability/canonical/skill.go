package canonical

import (
	"fmt"
	"strings"
)

// SkillSpec is the body for Spec{Kind: KindSkill}.
type SkillSpec struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	// Instruction is the raw markdown body (no frontmatter).
	Instruction string `json:"instruction"`

	Trigger string `json:"trigger,omitempty"`

	// Files carries non-SKILL.md content from a multi-file zip import.
	// The server is authoritative: even when the client posts a fully-populated
	// canonical_spec to /import/commit, the handler re-fetches the source zip
	// from OSS and rebuilds Files from scratch.
	Files []SkillFile `json:"files,omitempty"`
}

type SkillFile struct {
	// Path is relative to the skill root, forward slashes, never absolute or containing "..".
	Path string `json:"path"`

	// Content is the file body as UTF-8 text. Binary blobs are still stored as text.
	Content string `json:"content"`

	// Kind is informational; not load-bearing for validation.
	Kind SkillFileKind `json:"kind"`
}

type SkillFileKind string

const (
	SkillFileKindMarkdown SkillFileKind = "markdown"
	SkillFileKindScript   SkillFileKind = "script"
	SkillFileKindAsset    SkillFileKind = "asset"
)

// Validate enforces non-empty slug + instruction. Title is required by
// convention but only warned-on at the parser level so inline-edited specs
// without a title still validate.
func (s SkillSpec) Validate() error {
	if strings.TrimSpace(s.Slug) == "" {
		return fmt.Errorf("%w: slug is required", ErrInvalidSkill)
	}
	if strings.TrimSpace(s.Instruction) == "" {
		return fmt.Errorf("%w: instruction body is required", ErrInvalidSkill)
	}
	return nil
}
