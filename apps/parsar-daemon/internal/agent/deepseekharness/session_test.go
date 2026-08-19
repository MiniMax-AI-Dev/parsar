package deepseekharness_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestMain re-execs the test binary as a fake `dsh` when
// DSH_TESTHELPER_ROLE is set, bypassing m.Run so the framework's PASS
// line doesn't pollute fake stdout.
const dshHelperEnvKey = "DSH_TESTHELPER_ROLE"

func TestMain(m *testing.M) {
	if role := os.Getenv(dshHelperEnvKey); role != "" {
		runFakeDsh(role)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeDsh(role string) {
	if argsFile := os.Getenv("DSH_TESTHELPER_ARGS_FILE"); argsFile != "" {
		_ = os.WriteFile(argsFile, []byte(strings.Join(os.Args, "\n")), 0o600)
	}
	switch role {
	case "success":
		_, _ = os.Stdout.WriteString("the final answer\n")
	case "nonzero":
		_, _ = os.Stderr.WriteString("MODEL_ERROR: upstream refused\n")
		os.Exit(1)
	case "hang":
		time.Sleep(10 * time.Minute)
	}
}

func dshHelperConfig() deepseekharness.SessionConfigForTest {
	return deepseekharness.SessionConfigForTest{
		Binary:      os.Args[0],
		ExtraArgs:   []string{"-test.run=^$"},
		KillTimeout: 200 * time.Millisecond,
	}
}

func dshHelperReq(runID, prompt, role string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		RunID:         runID,
		Prompt:        prompt,
		AgentStateKey: "conv1/agent1/deepseek_harness",
		AgentOptions: map[string]any{
			"env": map[string]any{dshHelperEnvKey: role},
		},
	}
}

func drainDsh(t *testing.T, out <-chan proto.Envelope, dl time.Duration) ([]proto.Envelope, bool) {
	t.Helper()
	deadline := time.After(dl)
	var got []proto.Envelope
	for {
		select {
		case env, ok := <-out:
			if !ok {
				return got, true
			}
			got = append(got, env)
		case <-deadline:
			return got, false
		}
	}
}

func TestSessionSuccessEmitsDeltaAndDone(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_ok", "hello", "success"), out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainDsh(t, out, 10*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := envTypes(got)
	if !slices.Contains(types, proto.TypeDelta) || !slices.Contains(types, proto.TypeDone) {
		t.Fatalf("types = %v, want delta+done", types)
	}
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	if slices.Contains(types, proto.TypeError) {
		t.Fatalf("clean exit must not emit an error frame: %v", types)
	}
	done := decodePayload[proto.DonePayload](t, got[len(got)-1])
	if done.Content != "the final answer" {
		t.Fatalf("done content = %q", done.Content)
	}
}

func TestSessionPassesHeadlessProfileAndPatchToCLI(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	argsFile := t.TempDir() + "/argv"
	req := dshHelperReq("run_argv", "hello", "success")
	req.AgentOptions["env"].(map[string]any)["DSH_TESTHELPER_ARGS_FILE"] = argsFile

	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(), req, out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())
	if _, closed := drainDsh(t, out, 10*time.Second); !closed {
		t.Fatal("out did not close")
	}

	body, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	argv := strings.Split(string(body), "\n")
	if !slices.Contains(argv, "--profile") || !slices.Contains(argv, "headless") {
		t.Fatalf("argv missing headless profile: %v", argv)
	}
	if !slices.Contains(argv, "--patch") {
		t.Fatalf("argv missing patch overlay: %v", argv)
	}
	if !slices.Contains(argv, "hello") {
		t.Fatalf("argv missing task: %v", argv)
	}
}

func TestSessionNonZeroExitEmitsErrorAndDone(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_err", "hello", "nonzero"), out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainDsh(t, out, 10*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := envTypes(got)
	if !slices.Contains(types, proto.TypeError) {
		t.Fatalf("types = %v, want error", types)
	}
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	var errPayload proto.ErrorPayload
	for _, env := range got {
		if env.Type == proto.TypeError {
			errPayload = decodePayload[proto.ErrorPayload](t, env)
		}
	}
	if !strings.Contains(errPayload.Error, "upstream refused") {
		t.Fatalf("error payload = %#v", errPayload)
	}
}

func TestSessionCancelClosesOutAndEmitsDone(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_cancel", "hello", "hang"), out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if err := sess.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel: %v", err)
	}

	got, closed := drainDsh(t, out, 10*time.Second)
	if !closed {
		t.Fatalf("out did not close after Cancel, drained %d envs", len(got))
	}
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env = %q, want done; all=%v", got[len(got)-1].Type, envTypes(got))
	}
}

func TestSessionCleansUpPatchFileAfterRun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	argsFile := t.TempDir() + "/argv"
	req := dshHelperReq("run_cleanup", "hello", "success")
	req.AgentOptions["env"].(map[string]any)["DSH_TESTHELPER_ARGS_FILE"] = argsFile

	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(), req, out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())
	if _, closed := drainDsh(t, out, 10*time.Second); !closed {
		t.Fatal("out did not close")
	}

	body, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read argv file: %v", err)
	}
	argv := strings.Split(string(body), "\n")
	patchPath := flagValue(argv, "--patch")
	if patchPath == "" {
		t.Fatalf("argv missing patch path: %v", argv)
	}
	if _, err := os.Stat(patchPath); !os.IsNotExist(err) {
		t.Fatalf("patch file must be removed once the run ends, stat err = %v", err)
	}
}

func TestSessionRejectsPermissionAndAskSubmissions(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 16)
	sess, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_perm", "hello", "hang"), out, dshHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	if err := sess.SubmitPermission(context.Background(), "perm_nope", proto.PermissionDecisionPayload{Approved: true}); !errors.Is(err, agent.ErrUnknownPermission) {
		t.Fatalf("SubmitPermission err = %v, want ErrUnknownPermission", err)
	}
	if err := sess.SubmitPromptForUserChoice(context.Background(), "ask_nope", proto.PromptForUserChoiceDecisionPayload{}); !errors.Is(err, agent.ErrUnknownAsk) {
		t.Fatalf("SubmitPromptForUserChoice err = %v, want ErrUnknownAsk", err)
	}
}

func TestSessionRejectsNilOut(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	_, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_nil", "hello", "success"), nil, dshHelperConfig())
	if err == nil {
		t.Fatal("expected error on nil out")
	}
}

func TestSessionRejectsEmptyPrompt(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 4)
	_, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_empty", "  ", "success"), out, dshHelperConfig())
	if err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestSessionBadBinaryFailsToStart(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	out := make(chan proto.Envelope, 4)
	cfg := dshHelperConfig()
	cfg.Binary = "/nonexistent/binary/that/does/not/resolve"
	cfg.ExtraArgs = nil
	_, err := deepseekharness.NewSessionForTest(context.Background(),
		dshHelperReq("run_bad", "hello", "success"), out, cfg)
	if err == nil {
		t.Fatal("expected start error for bogus binary")
	}
}
