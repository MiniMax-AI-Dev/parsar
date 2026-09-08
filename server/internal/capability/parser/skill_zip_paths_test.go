package parser

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSkillZipRejectsDuplicatePaths(t *testing.T) {
	for _, names := range [][2]string{
		{"SKILL.md", "SKILL.md"},
		{"SKILL.md", "skill.md"},
		{"SKILL.md", "ſKILL.md"},
		{"SKILL.md", "./SKILL.md"},
		{"policy.md", "policy.md"},
		{"references/policy.md", "references/./policy.md"},
		{"references/policy.md", "references//policy.md"},
		{"references/policy.md", `references\policy.md`},
		{"references/policy.md", "References/POLICY.md"},
		{"references/S.md", "references/ſ.md"},
		{"references/Σ.md", "references/ς.md"},
		{"references/café.md", "references/cafe\u0301.md"},
		{"references/\u03b1\u0345\u0301.md", "references/\u03b1\u0301\u0345.md"},
		{"references/.DS_Store/.", "references/.DS_Store"},
		{"references/.DS_Store", "references/.ds_store"},
	} {
		for _, root := range []string{"", "wrapped/"} {
			t.Run(root+names[0]+" and "+names[1], func(t *testing.T) {
				entries := []struct{ name, content string }{
					{root + names[0], minimalSkillMd + "PREVIEW-188"},
					{root + names[1], minimalSkillMd + "RUNTIME-299"},
				}
				if names[0] != "SKILL.md" {
					entries = append(entries, struct{ name, content string }{root + "SKILL.md", minimalSkillMd})
				}
				_, err := ParseSkillZip(buildZip(t, entries))
				if !errors.Is(err, ErrInvalidSkillZip) || !strings.Contains(err.Error(), "duplicate zip path") {
					t.Fatalf("ambiguous archive accepted or wrong error: %v", err)
				}
			})
		}
	}
}

func TestParseSkillZipNormalizesUniqueReferencePath(t *testing.T) {
	parsed, err := ParseSkillZip(buildZip(t, []struct{ name, content string }{
		{"SKILL.md", minimalSkillMd},
		{"references/./policy.md", "pickup on day 3"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Spec.Skill.Files) != 1 || parsed.Spec.Skill.Files[0].Path != "references/policy.md" {
		t.Fatalf("unexpected reference paths: %+v", parsed.Spec.Skill.Files)
	}
}

func TestParseSkillZipWrapperDirectoryIsNotAnEntry(t *testing.T) {
	_, err := ParseSkillZip(buildZip(t, []struct{ name, content string }{
		{"wrapped/", ""},
		{"wrapped/SKILL.md", minimalSkillMd},
		{"wrapped/wrapped/", ""},
		{"wrapped/wrapped/policy.md", "pickup on day 3"},
	}))
	if err != nil {
		t.Fatal(err)
	}
}
