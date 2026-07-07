package parser

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

type SkillParseResult struct {
	Spec          canonical.Spec
	Warnings      []string
	SuggestedName string
}

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Slug        string `yaml:"slug"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Trigger     string `yaml:"trigger"`
}

// frontmatterPattern matches the multi-line `---\n...\n---\n` block at the
// start of the document. Single-line frontmatter is intentionally unsupported.
var frontmatterPattern = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// ParseSkill converts a pasted markdown skill file into a canonical.Spec.
// Frontmatter handling is best-effort: yaml.v3 first, then a line-based
// fallback for values like `description: The flow is: parse input` that confuse the
// YAML mapping detector. Slug is no longer required at parse time —
// commitCapabilityImport derives one from the form name (or a random
// fallback) so an unparseable frontmatter never blocks import.
func ParseSkill(raw string, format SourceFormat) (SkillParseResult, error) {
	if strings.TrimSpace(raw) == "" {
		return SkillParseResult{}, ErrEmptyInput
	}
	if format != SourceFormatMarkdown {
		return SkillParseResult{}, fmt.Errorf("%w: %q", ErrUnsupportedSourceFormat, format)
	}
	front, body, warnings := splitSkillDoc(raw)

	if strings.TrimSpace(body) == "" {
		return SkillParseResult{}, fmt.Errorf("skill parse: instruction body is empty")
	}

	slug := strings.TrimSpace(front.Slug)
	if slug == "" {
		// Empty slug here is fine; the commit handler tries the form-provided
		// name next, then an auto-generated suffix.
		slug = kebabFromName(front.Name)
	}
	if slug == "" {
		warnings = append(warnings, "frontmatter provided no slug or name — on submit the slug will be derived from the name entered in the form")
	}

	title := strings.TrimSpace(front.Title)
	if title == "" {
		title = strings.TrimSpace(front.Name)
	}

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        slug,
			Title:       title,
			Description: strings.TrimSpace(front.Description),
			Instruction: strings.TrimRight(body, " \t\r\n"),
			Trigger:     strings.TrimSpace(front.Trigger),
		},
	}
	if title == "" {
		warnings = append(warnings, "title is empty — recommend filling in a human-friendly name before committing")
	}
	return SkillParseResult{
		Spec:          spec,
		Warnings:      warnings,
		SuggestedName: firstNonEmpty(title, slug),
	}, nil
}

// splitSkillDoc parses frontmatter and returns the body. The body is ALWAYS
// stripped of the leading `---\n...\n---\n` block — even when YAML decode
// fails — so the agent's system prompt never contains raw frontmatter.
//
// YAML decode is tried first; on failure (typically `description: A: B`
// looking like a nested mapping) we fall back to line-based extraction.
func splitSkillDoc(raw string) (skillFrontmatter, string, []string) {
	var front skillFrontmatter
	var warnings []string
	loc := frontmatterPattern.FindStringSubmatchIndex(raw)
	if loc == nil {
		warnings = append(warnings, "no YAML frontmatter detected — using entire content as instruction body and synthesizing slug/title from defaults")
		return front, raw, warnings
	}
	yamlBlock := raw[loc[2]:loc[3]]
	body := raw[loc[1]:]
	if err := yaml.Unmarshal([]byte(yamlBlock), &front); err != nil {
		// YAML failed; fall back to line-based extraction of the five known keys.
		fallback, recovered := parseFrontmatterLines(yamlBlock)
		if recovered {
			warnings = append(warnings, fmt.Sprintf("frontmatter YAML parse failed, fell back to simple field extraction: %v", err))
			return fallback, body, warnings
		}
		warnings = append(warnings, fmt.Sprintf("frontmatter YAML parse failed and field extraction fallback also failed, please fill the name manually in the dialog: %v", err))
		return skillFrontmatter{}, body, warnings
	}
	return front, body, warnings
}

// frontmatterLinePattern matches the five keys SkillSpec consumes; nested
// mappings, lists, anchors, and other YAML features are out of scope.
var frontmatterLinePattern = regexp.MustCompile(`^(name|slug|title|description|trigger):[ \t]*(.*)$`)

// parseFrontmatterLines is the line-based fallback used when yaml.v3 chokes.
// Values get a light unquote pass (matching wrapper `"`/`'` stripped) but are
// otherwise verbatim — so `description: A: B` returns `A: B`. The bool
// reports whether anything was recovered so the warning text can distinguish
// success from a fully unparseable block.
func parseFrontmatterLines(yamlBlock string) (skillFrontmatter, bool) {
	var front skillFrontmatter
	var recovered bool
	for _, line := range strings.Split(yamlBlock, "\n") {
		line = strings.TrimRight(line, "\r")
		m := frontmatterLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		value := unquoteFrontmatterValue(strings.TrimSpace(m[2]))
		switch key {
		case "name":
			front.Name = value
		case "slug":
			front.Slug = value
		case "title":
			front.Title = value
		case "description":
			front.Description = value
		case "trigger":
			front.Trigger = value
		}
		recovered = true
	}
	return front, recovered
}

// unquoteFrontmatterValue strips one matching pair of surrounding `"` or `'`.
// Anything else passes through untouched.
func unquoteFrontmatterValue(v string) string {
	if len(v) < 2 {
		return v
	}
	first, last := v[0], v[len(v)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

// kebabFromName mirrors the opencode capability runtime's kebabCase so slugs
// stay stable across the import + skill-clone paths.
//
// When the input contains no ASCII alphanumerics at all (e.g. a pure
// Chinese title), we return the trimmed input unchanged so the caller
// still gets a usable slug instead of an empty string. Canonical slug
// validation only requires non-empty, so a CJK slug is acceptable.
func kebabFromName(name string) string {
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		return ""
	}
	var b strings.Builder
	lastWasDash := true
	hasASCIIAlnum := false
	for _, r := range clean {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastWasDash = false
			hasASCIIAlnum = true
		default:
			if !lastWasDash {
				b.WriteByte('-')
				lastWasDash = true
			}
		}
	}
	if !hasASCIIAlnum {
		return clean
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
