package codex

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostedSkillDoesNotActivateAdditionalNativeResources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		files    []string
		rejected bool
	}{
		{"ordinary supporting files", []string{"SKILL.md", "scripts/check.py", "references/guide.md"}, false},
		{"native dependency configuration", []string{"SKILL.md", "agents/openai.yaml"}, true},
		{"nested discovery", []string{"SKILL.md", "references/other/SKILL.md"}, true},
		{"nested dependencies", []string{"SKILL.md", "references/other/SKILL.md", "references/other/agents/openai.yaml"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range tc.files {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0400); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyHostedSkillLayout(root); (err != nil) != tc.rejected {
				t.Fatalf("rejected=%v err=%v", tc.rejected, err)
			}
		})
	}
}
