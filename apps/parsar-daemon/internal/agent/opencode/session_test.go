package opencode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestMain re-execs the test binary as a fake `opencode` when
// OPENCODE_TESTHELPER_ROLE is set, bypassing m.Run so the test
// framework's PASS line doesn't pollute fake stdout.
const opencodeHelperEnvKey = "OPENCODE_TESTHELPER_ROLE"

func TestMain(m *testing.M) {
	if role := os.Getenv(opencodeHelperEnvKey); role != "" {
		runFakeOpenCode(role)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeOpenCode(role string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	sawRun := false
	sawJSONFormat := false
	for i, arg := range os.Args[1:] {
		if arg == "run" {
			sawRun = true
		}
		if arg == "--format" && i+2 <= len(os.Args[1:]) && os.Args[i+2] == "json" {
			sawJSONFormat = true
		}
	}

	switch role {
	case "json-success":
		if !sawRun || !sawJSONFormat {
			_, _ = os.Stderr.WriteString("missing opencode run --format json\n")
			os.Exit(64)
		}
		_ = enc.Encode(map[string]any{
			"type": "message.part.delta",
			"properties": map[string]any{
				"field": "text",
				"delta": "hi ",
			},
		})
		_ = enc.Encode(map[string]any{
			"type": "message.part.delta",
			"properties": map[string]any{
				"field": "text",
				"delta": "there",
			},
		})
		_ = enc.Encode(map[string]any{
			"type": "message.updated",
			"properties": map[string]any{
				"info": map[string]any{
					"cost": 0.12,
					"tokens": map[string]any{
						"input":  4,
						"output": 2,
						"total":  6,
					},
				},
			},
		})

	case "plain-success":
		_, _ = os.Stdout.WriteString("plain line one\nplain line two\n")

	case "nonzero":
		_, _ = os.Stderr.WriteString("bad auth from fake opencode\n")
		os.Exit(17)

	case "hang":
		_ = enc.Encode(map[string]any{
			"type": "message.part.delta",
			"properties": map[string]any{
				"field": "text",
				"delta": "started",
			},
		})
		time.Sleep(10 * time.Minute)
	}
}

func opencodeHelperConfig() opencode.SessionConfigForTest {
	return opencode.SessionConfigForTest{
		OpenCodeBinary: os.Args[0],
		ExtraArgs:      []string{"-test.run=^$"},
		KillTimeout:    200 * time.Millisecond,
	}
}

func opencodeHelperReq(runID, prompt, role string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		RunID:  runID,
		Prompt: prompt,
		AgentOptions: map[string]any{
			"env": map[string]any{
				opencodeHelperEnvKey: role,
			},
		},
	}
}

func drainOpenCode(t *testing.T, out <-chan proto.Envelope, dl time.Duration) ([]proto.Envelope, bool) {
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

func TestSessionJSONSuccessEmitsDeltaUsageAndDone(t *testing.T) {
	out := make(chan proto.Envelope, 32)
	sess, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_json", "hello", "json-success"), out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainOpenCode(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := opencodeEnvTypes(got)
	mustContainOpenCode(t, types, proto.TypeDelta)
	mustContainOpenCode(t, types, proto.TypeUsage)
	mustContainOpenCode(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	for _, env := range got {
		if env.ID != "run_json" {
			t.Errorf("env type=%s ID=%q, want run_json", env.Type, env.ID)
		}
	}
	done := decodePayload[proto.DonePayload](t, got[len(got)-1])
	if done.Content != "hi there" {
		t.Fatalf("done content = %q, want hi there", done.Content)
	}
	if done.Usage.Provider != "opencode" || done.Usage.InputTokens != 4 || done.Usage.OutputTokens != 2 {
		t.Fatalf("done usage = %#v", done.Usage)
	}
}

func TestSessionPlainStdoutFallsBackToDeltaAndDone(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_plain", "hello", "plain-success"), out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainOpenCode(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := opencodeEnvTypes(got)
	if len(got) < 2 || got[0].Type != proto.TypeDelta || got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("plain envs = %v", types)
	}
	done := decodePayload[proto.DonePayload](t, got[len(got)-1])
	if done.Content != "plain line one\nplain line two" {
		t.Fatalf("done content = %q", done.Content)
	}
}

func TestSessionNonZeroExitEmitsErrorAndDone(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_err", "hello", "nonzero"), out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainOpenCode(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := opencodeEnvTypes(got)
	mustContainOpenCode(t, types, proto.TypeError)
	mustContainOpenCode(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	var errPayload proto.ErrorPayload
	for _, env := range got {
		if env.Type == proto.TypeError {
			errPayload = decodePayload[proto.ErrorPayload](t, env)
		}
	}
	if !strings.Contains(errPayload.Error, "bad auth from fake opencode") {
		t.Fatalf("error payload = %#v", errPayload)
	}
}

func TestSessionCancelClosesOutAndEmitsTerminalFrames(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_cancel", "hello", "hang"), out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}

	// Let the helper emit its first delta before cancellation.
	time.Sleep(150 * time.Millisecond)
	if err := sess.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel: %v", err)
	}

	got, closed := drainOpenCode(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close after Cancel, drained %d envs", len(got))
	}
	types := opencodeEnvTypes(got)
	mustContainOpenCode(t, types, proto.TypeError)
	mustContainOpenCode(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
}

func TestSessionSubmitPermissionUnknownReturnsErrUnknown(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_perm", "hello", "hang"), out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	err = sess.SubmitPermission(context.Background(), "perm_nope", proto.PermissionDecisionPayload{Approved: true})
	if !errors.Is(err, agent.ErrUnknownPermission) {
		t.Fatalf("SubmitPermission err = %v, want ErrUnknownPermission", err)
	}
}

func TestSessionRejectsNilOut(t *testing.T) {
	_, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_nil", "hello", "json-success"), nil, opencodeHelperConfig())
	if err == nil {
		t.Fatal("expected error on nil out")
	}
}

func TestSessionRejectsEmptyPrompt(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	_, err := opencode.NewSessionForTest(context.Background(),
		proto.PromptRequestPayload{RunID: "run_empty", Prompt: ""}, out, opencodeHelperConfig())
	if err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestSessionBadBinaryFailsToStart(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	cfg := opencodeHelperConfig()
	cfg.OpenCodeBinary = "/nonexistent/binary/that/does/not/resolve"
	cfg.ExtraArgs = nil
	_, err := opencode.NewSessionForTest(context.Background(),
		opencodeHelperReq("run_bad", "hello", "json-success"), out, cfg)
	if err == nil {
		t.Fatal("expected start error for bogus binary")
	}
}

func opencodeEnvTypes(envs []proto.Envelope) []string {
	out := make([]string, len(envs))
	for i, env := range envs {
		out[i] = env.Type
	}
	return out
}

func mustContainOpenCode(t *testing.T, haystack []string, needle string) {
	t.Helper()
	if !slices.Contains(haystack, needle) {
		t.Fatalf("expected %q in %v", needle, haystack)
	}
}
