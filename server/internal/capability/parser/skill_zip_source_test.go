package parser

import "testing"

func TestParseSkillZipPreservesEntrySource(t *testing.T) {
	source := "---\r\n# Keep this comment\r\nname: quoted-skill\r\ndescription: \"Read a report\"\r\nallowed-tools: Read\r\ndisable-model-invocation: true\r\n---\r\n\r\nRead the report.\r\n"
	for _, name := range []string{"SKILL.md", "wrapped/skill.md"} {
		t.Run(name, func(t *testing.T) {
			buf := buildZip(t, []struct{ name, content string }{{name, source}})
			res, err := ParseSkillZip(buf)
			if err != nil {
				t.Fatal(err)
			}
			if res.EntryMarkdown != source {
				t.Fatalf("entry source changed: %q", res.EntryMarkdown)
			}
			if res.Spec.Skill == nil || len(res.Spec.Skill.Files) != 0 {
				t.Fatal("entry source must stay outside canonical supporting files")
			}
		})
	}
}
