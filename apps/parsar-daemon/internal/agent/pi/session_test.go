package pi_test

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
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestMain re-execs the test binary as a fake `pi` when
// PI_TESTHELPER_ROLE is set, bypassing m.Run so the framework's PASS
// line doesn't pollute fake stdout.
const piHelperEnvKey = "PI_TESTHELPER_ROLE"

func TestMain(m *testing.M) {
	if role := os.Getenv(piHelperEnvKey); role != "" {
		runFakePi(role)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakePi(role string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	// Record argv so the skill-injection test can assert --skill <dir>
	// reached the subprocess.
	if argsFile := os.Getenv("PI_TESTHELPER_ARGS_FILE"); argsFile != "" {
		_ = os.WriteFile(argsFile, []byte(strings.Join(os.Args, "\n")), 0o644)
	}

	sawModeJSON := false
	for i, arg := range os.Args {
		if arg == "--mode" && i+1 < len(os.Args) && os.Args[i+1] == "json" {
			sawModeJSON = true
		}
	}

	header := map[string]any{"type": "session", "id": "sess-xyz", "cwd": "/", "timestamp": "t"}
	delta := func(s string) map[string]any {
		return map[string]any{
			"type":                  "message_update",
			"message":               map[string]any{"role": "assistant"},
			"assistantMessageEvent": map[string]any{"type": "text_delta", "contentIndex": 0, "delta": s},
		}
	}

	switch role {
	case "json-success":
		if !sawModeJSON {
			_, _ = os.Stderr.WriteString("missing --mode json\n")
			os.Exit(64)
		}
		_ = enc.Encode(header)
		_ = enc.Encode(delta("hi "))
		_ = enc.Encode(delta("there"))
		_ = enc.Encode(map[string]any{
			"type": "message_end",
			"message": map[string]any{
				"role":       "assistant",
				"content":    []any{map[string]any{"type": "text", "text": "hi there"}},
				"provider":   "anthropic",
				"model":      "claude-x",
				"stopReason": "stop",
				"usage": map[string]any{
					"input": 4, "output": 2, "cacheRead": 0, "cacheWrite": 0,
					"totalTokens": 6,
					"cost":        map[string]any{"input": 0.1, "output": 0.02, "cacheRead": 0, "cacheWrite": 0, "total": 0.12},
				},
			},
		})

	case "error-stop":
		// pi exits 0 even when the model errors — it just emits a
		// message_end with stopReason "error".
		_ = enc.Encode(header)
		_ = enc.Encode(map[string]any{
			"type": "message_end",
			"message": map[string]any{
				"role":         "assistant",
				"content":      []any{},
				"provider":     "anthropic",
				"model":        "claude-x",
				"stopReason":   "error",
				"errorMessage": "model boom",
				"usage": map[string]any{
					"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
					"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
				},
			},
		})

	case "nonzero":
		_, _ = os.Stderr.WriteString("bad auth from fake pi\n")
		os.Exit(17)

	case "hang":
		_ = enc.Encode(header)
		_ = enc.Encode(delta("started"))
		time.Sleep(10 * time.Minute)
	}
}

func piHelperConfig() pi.SessionConfigForTest {
	return pi.SessionConfigForTest{
		PiBinary:    os.Args[0],
		ExtraArgs:   []string{"-test.run=^$"},
		KillTimeout: 200 * time.Millisecond,
	}
}

func piHelperReq(runID, prompt, role string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		RunID:  runID,
		Prompt: prompt,
		AgentOptions: map[string]any{
			"env": map[string]any{
				piHelperEnvKey: role,
			},
		},
	}
}

func drainPi(t *testing.T, out <-chan proto.Envelope, dl time.Duration) ([]proto.Envelope, bool) {
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
	sess, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_json", "hello", "json-success"), out, piHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainPi(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := piEnvTypes(got)
	mustContainPi(t, types, proto.TypeDelta)
	mustContainPi(t, types, proto.TypeUsage)
	mustContainPi(t, types, proto.TypeDone)
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
	if done.Usage.Provider != "anthropic" || done.Usage.InputTokens != 4 || done.Usage.OutputTokens != 2 {
		t.Fatalf("done usage = %#v", done.Usage)
	}
	if done.Metadata["pi_session_id"] != "sess-xyz" {
		t.Fatalf("done metadata = %#v, want pi_session_id sess-xyz", done.Metadata)
	}
}

// pi exits 0 on a model error: the session must still surface TypeError
// (from stopReason) and Done, and close out.
func TestSessionModelErrorStopReasonStillEmitsErrorAndDone(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_mod_err", "hello", "error-stop"), out, piHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainPi(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := piEnvTypes(got)
	mustContainPi(t, types, proto.TypeError)
	mustContainPi(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	var errPayload proto.ErrorPayload
	for _, env := range got {
		if env.Type == proto.TypeError {
			errPayload = decodePayload[proto.ErrorPayload](t, env)
		}
	}
	if !strings.Contains(errPayload.Error, "model boom") {
		t.Fatalf("error payload = %#v, want model boom", errPayload)
	}
}

func TestSessionNonZeroExitEmitsErrorAndDone(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_err", "hello", "nonzero"), out, piHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drainPi(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := piEnvTypes(got)
	mustContainPi(t, types, proto.TypeError)
	mustContainPi(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
	var errPayload proto.ErrorPayload
	for _, env := range got {
		if env.Type == proto.TypeError {
			errPayload = decodePayload[proto.ErrorPayload](t, env)
		}
	}
	if !strings.Contains(errPayload.Error, "bad auth from fake pi") {
		t.Fatalf("error payload = %#v", errPayload)
	}
}

func TestSessionCancelClosesOutAndEmitsTerminalFrames(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_cancel", "hello", "hang"), out, piHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}

	time.Sleep(150 * time.Millisecond)
	if err := sess.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel: %v", err)
	}

	got, closed := drainPi(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close after Cancel, drained %d envs", len(got))
	}
	types := piEnvTypes(got)
	mustContainPi(t, types, proto.TypeDone)
	if got[len(got)-1].Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done; all=%v", got[len(got)-1].Type, types)
	}
}

func TestSessionSubmitPermissionUnknownReturnsErrUnknown(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_perm", "hello", "hang"), out, piHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	err = sess.SubmitPermission(context.Background(), "perm_nope", proto.PermissionDecisionPayload{Approved: true})
	if !errors.Is(err, agent.ErrUnknownPermission) {
		t.Fatalf("SubmitPermission err = %v, want ErrUnknownPermission", err)
	}
	err = sess.SubmitPromptForUserChoice(context.Background(), "ask_nope", proto.PromptForUserChoiceDecisionPayload{})
	if !errors.Is(err, agent.ErrUnknownAsk) {
		t.Fatalf("SubmitPromptForUserChoice err = %v, want ErrUnknownAsk", err)
	}
}

func TestSessionRejectsNilOut(t *testing.T) {
	_, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_nil", "hello", "json-success"), nil, piHelperConfig())
	if err == nil {
		t.Fatal("expected error on nil out")
	}
}

func TestSessionRejectsEmptyPrompt(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	_, err := pi.NewSessionForTest(context.Background(),
		proto.PromptRequestPayload{RunID: "run_empty", Prompt: ""}, out, piHelperConfig())
	if err == nil {
		t.Fatal("expected error on empty prompt")
	}
}

func TestSessionBadBinaryFailsToStart(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	cfg := piHelperConfig()
	cfg.PiBinary = "/nonexistent/binary/that/does/not/resolve"
	cfg.ExtraArgs = nil
	_, err := pi.NewSessionForTest(context.Background(),
		piHelperReq("run_bad", "hello", "json-success"), out, cfg)
	if err == nil {
		t.Fatal("expected start error for bogus binary")
	}
}

func piEnvTypes(envs []proto.Envelope) []string {
	out := make([]string, len(envs))
	for i, env := range envs {
		out[i] = env.Type
	}
	return out
}

func mustContainPi(t *testing.T, haystack []string, needle string) {
	t.Helper()
	if !slices.Contains(haystack, needle) {
		t.Fatalf("expected %q in %v", needle, haystack)
	}
}
