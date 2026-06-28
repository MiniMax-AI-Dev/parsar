package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestNewSessionInstallsSkillsAndInjectsSkillFlag is the end-to-end proof
// that agent_options["skills"] flows download → disk → repeated --skill
// argv on the pi subprocess. The fake pi records its argv into
// PI_TESTHELPER_ARGS_FILE (see runFakePi).
func TestNewSessionInstallsSkillsAndInjectsSkillFlag(t *testing.T) {
	body := validSkillZip(t)
	srv := startZipServer(t, body)
	home := t.TempDir()
	t.Setenv("HOME", home)

	argsFile := filepath.Join(t.TempDir(), "argv")
	out := make(chan proto.Envelope, 64)
	req := proto.PromptRequestPayload{
		RunID:          "run_skill",
		ConversationID: "conv-skill",
		Prompt:         "hello",
		AgentOptions: map[string]any{
			"skills": []any{
				map[string]any{
					"name": "code-review", "version": "1.0.0",
					"download_url": srv.URL, "sha256": sha256Hex(body),
				},
			},
			"env": map[string]any{
				"PI_TESTHELPER_ROLE":      "json-success",
				"PI_TESTHELPER_ARGS_FILE": argsFile,
			},
		},
	}
	sess, err := newSession(context.Background(), req, out, sessionConfig{
		piBinary:    os.Args[0],
		extraArgs:   []string{"-test.run=^$"},
		killTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	defer sess.Cancel(context.Background())

	deadline := time.After(5 * time.Second)
	for draining := true; draining; {
		select {
		case _, ok := <-out:
			if !ok {
				draining = false
			}
		case <-deadline:
			t.Fatal("out did not close")
		}
	}

	wantDir := filepath.Join(home, ".parsar", "runtime", "pi", "conv-conv-skill", "skills", "code-review")
	if _, err := os.Stat(filepath.Join(wantDir, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not installed at %s: %v", wantDir, err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file: %v", err)
	}
	argv := strings.Split(string(raw), "\n")
	if !containsArgPair(argv, "--skill", wantDir) {
		t.Fatalf("argv missing --skill %s: %v", wantDir, argv)
	}
}

func containsArgPair(argv []string, flag, val string) bool {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) && argv[i+1] == val {
			return true
		}
	}
	return false
}
