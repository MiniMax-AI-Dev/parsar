package parser

import (
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestParseSkill_FullFrontmatter(t *testing.T) {
	raw := "---\n" +
		"slug: code-review\n" +
		"title: Code Review\n" +
		"description: Run a quick PR sanity check\n" +
		"trigger: keyword\n" +
		"---\n" +
		"# Code Review\n\nWalk through the diff and flag risk.\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.Kind != canonical.KindSkill {
		t.Fatalf("kind: want skill, got %q", res.Spec.Kind)
	}
	sk := res.Spec.Skill
	if sk.Slug != "code-review" {
		t.Fatalf("slug: %q", sk.Slug)
	}
	if sk.Title != "Code Review" {
		t.Fatalf("title: %q", sk.Title)
	}
	if sk.Description != "Run a quick PR sanity check" {
		t.Fatalf("description: %q", sk.Description)
	}
	if sk.Trigger != "keyword" {
		t.Fatalf("trigger: %q", sk.Trigger)
	}
	if !strings.Contains(sk.Instruction, "Walk through the diff") {
		t.Fatalf("instruction missing body content: %q", sk.Instruction)
	}
	if strings.HasSuffix(sk.Instruction, "\n") {
		t.Fatalf("instruction must be trimmed of trailing newlines, got %q", sk.Instruction)
	}
	if res.SuggestedName != "Code Review" {
		t.Fatalf("suggested name: want \"Code Review\", got %q", res.SuggestedName)
	}
}

// TestParseSkill_SlugDerivedFromName confirms the kebabFromName fallback
// when frontmatter omits slug.
func TestParseSkill_SlugDerivedFromName(t *testing.T) {
	raw := "---\n" +
		"name: \"My Cool Skill!! v2\"\n" +
		"---\n" +
		"body content\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := res.Spec.Skill.Slug; got != "my-cool-skill-v2" {
		t.Fatalf("derived slug: want my-cool-skill-v2, got %q", got)
	}
	if res.Spec.Skill.Title != "My Cool Skill!! v2" {
		t.Fatalf("title fallback wrong: %q", res.Spec.Skill.Title)
	}
}

// TestParseSkill_SlugDerivedFromCJKName pins the kebabFromName fallback
// for pure non-ASCII names — the trimmed name is returned verbatim so
// canonical Validate passes without manual intervention.
func TestParseSkill_SlugDerivedFromCJKName(t *testing.T) {
	raw := "---\n" +
		"name: \"面试题目生成助手\"\n" +
		"---\n" +
		"body content\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := res.Spec.Skill.Slug; got != "面试题目生成助手" {
		t.Fatalf("derived slug: want 面试题目生成助手, got %q", got)
	}
}

// TestParseSkill_NoFrontmatterStillParses: lenient path — no
// frontmatter yields a warning + empty slug instead of an error.
func TestParseSkill_NoFrontmatterStillParses(t *testing.T) {
	raw := "Just a plain note with no frontmatter."
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.Skill.Slug != "" {
		t.Fatalf("slug should be empty when no frontmatter, got %q", res.Spec.Skill.Slug)
	}
	if res.Spec.Skill.Instruction == "" {
		t.Fatalf("instruction body should be populated even without frontmatter")
	}
	hasFrontmatterWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "frontmatter") {
			hasFrontmatterWarning = true
			break
		}
	}
	if !hasFrontmatterWarning {
		t.Fatalf("expected a frontmatter-related warning, got %v", res.Warnings)
	}
}

// TestParseSkill_FrontmatterPresentButEmpty: empty frontmatter ==
// "no fields recovered" — parser succeeds with empty Slug + warning.
func TestParseSkill_FrontmatterPresentButEmpty(t *testing.T) {
	raw := "---\n\n---\nbody\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.Skill.Slug != "" {
		t.Fatalf("slug should be empty when frontmatter has no fields, got %q", res.Spec.Skill.Slug)
	}
	if res.Spec.Skill.Instruction != "body" {
		t.Fatalf("instruction body should be 'body' (frontmatter stripped), got %q", res.Spec.Skill.Instruction)
	}
}

// TestParseSkill_EmptyBodyIsError: an instruction-less skill is rejected.
func TestParseSkill_EmptyBodyIsError(t *testing.T) {
	raw := "---\nslug: x\n---\n\n   \n"
	_, err := ParseSkill(raw, SourceFormatMarkdown)
	if err == nil || !strings.Contains(err.Error(), "instruction body is empty") {
		t.Fatalf("want empty-body error, got %v", err)
	}
}

// TestParseSkill_MalformedYAMLDegradesToWarning: YAML decode errors
// downgrade to warning; the line-based fallback recovers usable
// `key: value` lines.
func TestParseSkill_MalformedYAMLDegradesToWarning(t *testing.T) {
	raw := "---\nthis: is: not: valid: yaml:\n---\nbody\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	hasYAMLWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "YAML") {
			hasYAMLWarning = true
			break
		}
	}
	if !hasYAMLWarning {
		t.Fatalf("expected a YAML-failure warning, got %v", res.Warnings)
	}
	if res.Spec.Skill.Instruction != "body" {
		t.Fatalf("body should be cleanly stripped of frontmatter, got %q", res.Spec.Skill.Instruction)
	}
}

func TestParseSkill_RejectsNonMarkdownFormat(t *testing.T) {
	_, err := ParseSkill(`{"slug":"x"}`, SourceFormatJSON)
	if !errors.Is(err, ErrUnsupportedSourceFormat) {
		t.Fatalf("want ErrUnsupportedSourceFormat, got %v", err)
	}
}

func TestParseSkill_EmptyInput(t *testing.T) {
	_, err := ParseSkill("\n\t\n", SourceFormatMarkdown)
	if !errors.Is(err, ErrEmptyInput) {
		t.Fatalf("want ErrEmptyInput, got %v", err)
	}
}

func TestParseSkill_TitleFallbackEmitsWarning(t *testing.T) {
	raw := "---\nslug: just-a-slug\n---\nbody\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Spec.Skill.Title != "" {
		t.Fatalf("title should be empty when name+title missing: %q", res.Spec.Skill.Title)
	}
	hasTitleWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "title is empty") {
			hasTitleWarning = true
			break
		}
	}
	if !hasTitleWarning {
		t.Fatalf("expected a 'title is empty' warning, got %v", res.Warnings)
	}
	if res.SuggestedName != "just-a-slug" {
		t.Fatalf("suggested name fallback: %q", res.SuggestedName)
	}
}

// TestParseSkill_DescriptionWithColonSpace: `description:` value with a
// `: ` (common in CJK text) used to make yaml.v3 reject the whole block;
// the line-based fallback now recovers the `name:` line.
func TestParseSkill_DescriptionWithColonSpace(t *testing.T) {
	raw := "---\n" +
		"name: parsar-dev\n" +
		"description: 流程是: 解析输入 → worktree → make check → push\n" +
		"---\n" +
		"body content\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse should not fail on description with colon-space, got %v", err)
	}
	if got := res.Spec.Skill.Slug; got != "parsar-dev" {
		t.Fatalf("slug fallback should recover name=parsar-dev, got %q", got)
	}
	if got := res.Spec.Skill.Title; got != "parsar-dev" {
		t.Fatalf("title fallback should mirror name, got %q", got)
	}
	sawYAMLWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "YAML") {
			sawYAMLWarning = true
			break
		}
	}
	if !sawYAMLWarning {
		t.Fatalf("expected a YAML-fallback warning, got %v", res.Warnings)
	}
}

// TestParseSkill_FrontmatterAlwaysStrippedFromBody enforces the
// load-bearing invariant: the instruction body must never contain the
// leading `---\n...\n---\n` block, regardless of YAML decode outcome.
// Letting it through would corrupt --append-system-prompt silently.
func TestParseSkill_FrontmatterAlwaysStrippedFromBody(t *testing.T) {
	cases := map[string]string{
		"well-formed":             "---\nname: ok\n---\nthe body\n",
		"yaml-rejected-recovered": "---\nname: ok\ndesc: x: y\n---\nthe body\n",
		"yaml-rejected-empty":     "---\nthis: is: not: yaml:\n---\nthe body\n",
	}
	for label, raw := range cases {
		t.Run(label, func(t *testing.T) {
			res, err := ParseSkill(raw, SourceFormatMarkdown)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if strings.Contains(res.Spec.Skill.Instruction, "---") {
				t.Fatalf("instruction must not contain the frontmatter delimiter, got %q", res.Spec.Skill.Instruction)
			}
			if !strings.Contains(res.Spec.Skill.Instruction, "the body") {
				t.Fatalf("instruction should still carry the markdown body, got %q", res.Spec.Skill.Instruction)
			}
		})
	}
}

// TestParseSkill_LineBasedFallback_QuotedValues: the fallback strips
// matching wrapping quotes but leaves internal punctuation untouched.
func TestParseSkill_LineBasedFallback_QuotedValues(t *testing.T) {
	raw := "---\n" +
		"name: \"My Skill\"\n" +
		"slug: 'kebab-slug'\n" +
		"description: contains: colon and \"quoted\" pieces\n" +
		"---\n" +
		"body\n"
	res, err := ParseSkill(raw, SourceFormatMarkdown)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := res.Spec.Skill.Slug; got != "kebab-slug" {
		t.Fatalf("slug: want kebab-slug, got %q", got)
	}
	if got := res.Spec.Skill.Title; got != "My Skill" {
		t.Fatalf("title (from quoted name): want \"My Skill\", got %q", got)
	}
	// Internal `: ` and embedded quotes survive (only outer wrapping
	// quotes are stripped).
	if got := res.Spec.Skill.Description; !strings.Contains(got, "colon and") || !strings.Contains(got, "\"quoted\"") {
		t.Fatalf("description should preserve internal punctuation, got %q", got)
	}
}
