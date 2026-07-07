package claudecode_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestMain re-execs the test binary as a fake `claude` when
// CLAUDECODE_TESTHELPER_ROLE is set, bypassing m.Run so the test
// framework's PASS line never pollutes the fake stdout.
const helperEnvKey = "CLAUDECODE_TESTHELPER_ROLE"

func TestMain(m *testing.M) {
	if role := os.Getenv(helperEnvKey); role != "" {
		runFakeClaude(role)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runFakeClaude pretends to be the `claude` CLI in stream-json mode.
func runFakeClaude(role string) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	stdin := bufio.NewScanner(os.Stdin)
	stdin.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	// Wait for the daemon's initial user message so stream-json frame
	// ordering is deterministic.
	_ = stdin.Scan()

	switch role {
	case "echo-success":
		_ = enc.Encode(map[string]any{
			"type": "system", "subtype": "init",
			"session_id": "sess_echo",
		})
		_ = enc.Encode(map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"role": "assistant",
				"content": []map[string]any{
					{"type": "text", "text": "hi there"},
				},
			},
		})
		_ = enc.Encode(map[string]any{
			"type": "result", "subtype": "success",
			"result":     "hi there",
			"session_id": "sess_echo",
			"usage":      map[string]int{"input_tokens": 5, "output_tokens": 2},
		})

	case "echo-error":
		_ = enc.Encode(map[string]any{
			"type": "result", "subtype": "error_during_execution",
			"is_error": true, "error": "boom from fake",
		})

	case "permission":
		_ = enc.Encode(map[string]any{
			"type": "control_request", "request_id": "req_cc_42",
			"request": map[string]any{
				"subtype": "can_use_tool", "tool_name": "Bash",
				"input": map[string]any{"command": "ls"},
			},
		})
		// Wait for the daemon's control_response.
		approved := false
		ccID := ""
		if stdin.Scan() {
			var decision struct {
				Type     string `json:"type"`
				Response struct {
					RequestID string `json:"request_id"`
					Response  struct {
						Behavior string `json:"behavior"`
					} `json:"response"`
				} `json:"response"`
			}
			if err := json.Unmarshal(stdin.Bytes(), &decision); err == nil {
				approved = decision.Response.Response.Behavior == "allow"
				ccID = decision.Response.RequestID
			}
		}
		text := "denied for " + ccID
		if approved {
			text = "allowed for " + ccID
		}
		_ = enc.Encode(map[string]any{
			"type": "result", "subtype": "success",
			"result": text,
		})

	case "hang":
		_ = enc.Encode(map[string]any{
			"type": "system", "subtype": "init",
			"session_id": "sess_hang",
		})
		// Long sleep keeps a runtime timer alive so Go's deadlock
		// detector doesn't panic to stderr. SIGTERM still kills it.
		time.Sleep(10 * time.Minute)

	case "ask-question":
		// Stream an AskUserQuestion as a control_request (the path
		// claude-code takes under --permission-prompt-tool stdio).
		// Block on stdin waiting for the daemon's control_response
		// carrying the human's answer; echo it back via the final
		// result frame so the test can assert the round-trip text.
		_ = enc.Encode(map[string]any{
			"type": "system", "subtype": "init",
			"session_id": "sess_ask",
		})
		_ = enc.Encode(map[string]any{
			"type":       "control_request",
			"request_id": "cc_req_ask_1",
			"request": map[string]any{
				"subtype":   "can_use_tool",
				"tool_name": "AskUserQuestion",
				"input": map[string]any{
					"questions": []map[string]any{{
						"header":      "Confirm delete",
						"question":    "Delete /tmp directory?",
						"multiSelect": false,
						"options": []map[string]any{
							{"label": "Confirm delete", "description": "Run rm -rf"},
							{"label": "Cancel", "description": "Do not run"},
						},
					}},
				},
			},
		})

		// Wait for the daemon's control_response. Body shape:
		//   {type:"control_response", response:{subtype:"success",
		//    request_id:"...", response:{behavior:"deny", message:"..."}}}
		answerText := ""
		if stdin.Scan() {
			var cr struct {
				Type     string `json:"type"`
				Response struct {
					Subtype  string `json:"subtype"`
					Response struct {
						Behavior string `json:"behavior"`
						Message  string `json:"message"`
					} `json:"response"`
				} `json:"response"`
			}
			if err := json.Unmarshal(stdin.Bytes(), &cr); err == nil {
				answerText = cr.Response.Response.Message
			}
		}
		_ = enc.Encode(map[string]any{
			"type": "result", "subtype": "success",
			"result":     "echoed:" + answerText,
			"session_id": "sess_ask",
		})
	}
}

func helperConfig() claudecode.SessionConfigForTest {
	return claudecode.SessionConfigForTest{
		ClaudeBinary: os.Args[0],
		// belt-and-braces: -test.run=^$ stops any tests from running
		// if TestMain forgets to short-circuit.
		ExtraArgs:   []string{"-test.run=^$"},
		KillTimeout: 200 * time.Millisecond,
	}
}

// helperReq points the helper at a specific role via env passthrough.
func helperReq(runID, prompt, role string) proto.PromptRequestPayload {
	return proto.PromptRequestPayload{
		RunID:  runID,
		Prompt: prompt,
		AgentOptions: map[string]any{
			"env": map[string]any{
				helperEnvKey: role,
			},
		},
	}
}

// drain reads envelopes until out closes or dl fires. Second return
// is true on a clean close, false on timeout.
func drain(t *testing.T, out <-chan proto.Envelope, dl time.Duration) ([]proto.Envelope, bool) {
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

func TestSessionEndToEndSuccess(t *testing.T) {
	out := make(chan proto.Envelope, 32)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_s", "hello", "echo-success"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	if len(got) == 0 {
		t.Fatal("got no envelopes")
	}
	if got[len(got)-1].Type != "done" {
		t.Errorf("last env type = %q, want done", got[len(got)-1].Type)
	}

	types := envTypes(got)
	mustContain(t, types, "delta")
	mustContain(t, types, "usage")
	mustContain(t, types, "done")
	for _, e := range got {
		if e.ID != "run_s" {
			t.Errorf("env type=%s ID=%q, want run_s", e.Type, e.ID)
		}
	}
}

func TestSessionEndToEndError(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_e", "hello", "echo-error"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	got, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close, drained %d envs", len(got))
	}
	types := envTypes(got)
	mustContain(t, types, "error")
	mustContain(t, types, "done")
	if got[len(got)-1].Type != "done" {
		t.Errorf("last env not done: %s", got[len(got)-1].Type)
	}
}

func TestSessionCancelClosesOut(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_c", "hello", "hang"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}

	// Give the helper a moment to emit the system init line.
	time.Sleep(150 * time.Millisecond)
	if err := sess.Cancel(context.Background()); err != nil {
		t.Errorf("Cancel: %v", err)
	}

	got, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatalf("out did not close after Cancel, drained %d envs", len(got))
	}
	types := envTypes(got)
	// Synthesised cancel terminal: error + done.
	mustContain(t, types, "done")
}

func TestSessionCancelIsIdempotent(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_c2", "hello", "hang"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	_ = sess.Cancel(context.Background())
	_ = sess.Cancel(context.Background())
	_ = sess.Cancel(context.Background())
	_, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatal("out did not close after redundant Cancels")
	}
}

func TestSessionSubmitPermissionUnknownReturnsErrUnknown(t *testing.T) {
	out := make(chan proto.Envelope, 16)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_p0", "hello", "hang"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	err = sess.SubmitPermission(context.Background(), "perm_neverseen",
		proto.PermissionDecisionPayload{Approved: true})
	if !errors.Is(err, agent.ErrUnknownPermission) {
		t.Errorf("SubmitPermission for unknown id err = %v, want ErrUnknownPermission", err)
	}
}

func TestSessionPermissionRoundTrip(t *testing.T) {
	out := make(chan proto.Envelope, 32)
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_p", "approve me", "permission"), out, helperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	// Drain until we see the permission_request, then approve it.
	permID := ""
	deadline := time.After(5 * time.Second)
	var collected []proto.Envelope
	for permID == "" {
		select {
		case env, ok := <-out:
			if !ok {
				t.Fatalf("out closed before permission_request; collected %d", len(collected))
			}
			collected = append(collected, env)
			if env.Type == "permission_request" {
				permID = env.ID
			}
		case <-deadline:
			t.Fatalf("timeout waiting for permission_request; collected %d", len(collected))
		}
	}
	if !strings.HasPrefix(permID, "perm_") {
		t.Errorf("perm id wrong shape: %q", permID)
	}

	if err := sess.SubmitPermission(context.Background(), permID,
		proto.PermissionDecisionPayload{Approved: true}); err != nil {
		t.Fatalf("SubmitPermission: %v", err)
	}

	// Drain the rest. Expect to land at done with "allowed" content.
	rest, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatal("out did not close after approval")
	}
	all := append(collected, rest...)
	final := all[len(all)-1]
	if final.Type != "done" {
		t.Errorf("final env type = %q, want done", final.Type)
	}
	var done struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(final.Payload, &done); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if !strings.HasPrefix(done.Content, "allowed for ") {
		t.Errorf("done.content = %q, want 'allowed for ...'", done.Content)
	}

	// After resolution the perm id should no longer be known.
	err = sess.SubmitPermission(context.Background(), permID,
		proto.PermissionDecisionPayload{Approved: true})
	if !errors.Is(err, agent.ErrUnknownPermission) {
		t.Errorf("second SubmitPermission err = %v, want ErrUnknownPermission", err)
	}
}

func TestSessionRejectsEmptyPrompt(t *testing.T) {
	// Pure-image inbound (empty Prompt, non-empty Attachments) is a
	// valid prompt today and must NOT be rejected.
	out := make(chan proto.Envelope, 4)
	_, err := claudecode.NewSessionForTest(context.Background(),
		proto.PromptRequestPayload{RunID: "r0", Prompt: ""},
		out, helperConfig())
	if err == nil {
		t.Fatal("expected error on empty prompt + no attachments")
	}
}

func TestSessionRejectsNilOut(t *testing.T) {
	_, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("r0", "hi", "echo-success"),
		nil, helperConfig())
	if err == nil {
		t.Fatal("expected error on nil out")
	}
}

func TestSessionBadBinaryFailsToStart(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	cfg := helperConfig()
	cfg.ClaudeBinary = "/nonexistent/binary/that/does/not/resolve"
	cfg.ExtraArgs = nil
	_, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("r0", "hi", "echo-success"), out, cfg)
	if err == nil {
		t.Fatal("expected start error for bogus binary")
	}
}

func envTypes(envs []proto.Envelope) []string {
	out := make([]string, len(envs))
	for i, e := range envs {
		out[i] = e.Type
	}
	return out
}

func mustContain(t *testing.T, haystack []string, needle string) {
	t.Helper()
	if !slices.Contains(haystack, needle) {
		t.Errorf("expected %q in %v", needle, haystack)
	}
}

// TestSessionAskUserQuestionRoundTrip drives the full intercept →
// answer → tool_result loop through a real subprocess, so the test
// also catches stdin write / NDJSON-framing regressions the unit
// tests can't see.
func TestSessionAskUserQuestionRoundTrip(t *testing.T) {
	out := make(chan proto.Envelope, 32)
	cfg := helperConfig()
	cfg.AskTimeout = 30 * time.Second
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_a", "ask me", "ask-question"), out, cfg)
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	askID := ""
	deadline := time.After(5 * time.Second)
	var collected []proto.Envelope
	for askID == "" {
		select {
		case env, ok := <-out:
			if !ok {
				t.Fatalf("out closed before prompt_for_user_choice; collected %d", len(collected))
			}
			collected = append(collected, env)
			if env.Type == proto.TypePromptForUserChoice {
				// env.ID is the run id; the ask id rides on the payload.
				var p proto.PromptForUserChoicePayload
				if err := env.DecodePayload(&p); err != nil {
					t.Fatalf("decode prompt_for_user_choice payload: %v", err)
				}
				askID = p.AskID
			}
		case <-deadline:
			t.Fatalf("timeout waiting for prompt_for_user_choice; collected %d", len(collected))
		}
	}
	if !strings.HasPrefix(askID, "ask_") {
		t.Errorf("ask id wrong shape: %q", askID)
	}

	if err := sess.SubmitPromptForUserChoiceForTest(askID, proto.PromptForUserChoiceDecisionPayload{
		Answers: []string{"Confirm delete"},
	}); err != nil {
		t.Fatalf("SubmitPromptForUserChoice: %v", err)
	}

	rest, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatal("out did not close after ask answer")
	}
	all := append(collected, rest...)
	final := all[len(all)-1]
	if final.Type != "done" {
		t.Fatalf("final env type = %q, want done; types=%v", final.Type, envTypes(all))
	}
	var done struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(final.Payload, &done); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if !strings.Contains(done.Content, "Confirm delete") {
		t.Errorf("answer not echoed back through the fake claude loop: %q", done.Content)
	}

	// Second submit must look unknown.
	err = sess.SubmitPromptForUserChoiceForTest(askID, proto.PromptForUserChoiceDecisionPayload{Answers: []string{"x"}})
	if !errors.Is(err, agent.ErrUnknownAsk) {
		t.Errorf("second SubmitPromptForUserChoice err = %v, want ErrUnknownAsk", err)
	}
}

// TestSessionAskUserQuestionTimeoutSubmitsCancelled exercises the
// daemon-side timer. AskTimeout is set short so the watchdog fires
// before the human responds; the test then asserts the fake claude
// resumed with the canned "timeout" message (i.e. the timer wrote a
// successful tool_result, not an error).
func TestSessionAskUserQuestionTimeoutSubmitsCancelled(t *testing.T) {
	out := make(chan proto.Envelope, 32)
	cfg := helperConfig()
	cfg.AskTimeout = 150 * time.Millisecond
	sess, err := claudecode.NewSessionForTest(context.Background(),
		helperReq("run_to", "ask me", "ask-question"), out, cfg)
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())

	envs, closed := drain(t, out, 5*time.Second)
	if !closed {
		t.Fatal("out did not close after timer fired")
	}
	final := envs[len(envs)-1]
	if final.Type != "done" {
		t.Fatalf("final env type = %q, want done; types=%v", final.Type, envTypes(envs))
	}
	var done struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(final.Payload, &done); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if !strings.Contains(done.Content, "10 minutes") {
		t.Errorf("timeout sentence not echoed: %q", done.Content)
	}
}
