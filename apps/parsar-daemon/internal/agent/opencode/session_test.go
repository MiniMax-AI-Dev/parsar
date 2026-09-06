package opencode_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if dumpPath := os.Getenv("OPENCODE_TESTHELPER_CONFIG_DUMP"); dumpPath != "" {
		body, err := json.Marshal(map[string]string{
			"config_dir":      os.Getenv("OPENCODE_CONFIG_DIR"),
			"xdg_config_home": os.Getenv("XDG_CONFIG_HOME"),
		})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "encode managed config env: %v\n", err)
			os.Exit(65)
		}
		if err := os.WriteFile(dumpPath, body, 0o600); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "dump managed config: %v\n", err)
			os.Exit(65)
		}
	}
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

func TestSessionInstallsAndRegistersManagedSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARSAR_HOME", home)
	t.Setenv("OPENCODE_CONFIG_DIR", "")
	userConfigHome := filepath.Join(t.TempDir(), "user-config")
	t.Setenv("XDG_CONFIG_HOME", userConfigHome)
	body := openCodeSkillZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dumpPath := filepath.Join(t.TempDir(), "opencode.json")
	out := make(chan proto.Envelope, 32)
	req := opencodeHelperReq("run_skills", "hello", "json-success")
	req.ConversationID = "conv-skills"
	req.AgentStateKey = "conv-skills/agent-1/opencode"
	req.AgentOptions["skills"] = []any{map[string]any{
		"name": "find-skills", "version": "1.0.0", "download_url": srv.URL,
		"sha256": fmt.Sprintf("%x", sha256.Sum256(body)),
	}}
	req.AgentOptions["env"].(map[string]any)["OPENCODE_TESTHELPER_CONFIG_DUMP"] = dumpPath

	sess, err := opencode.NewSessionForTest(context.Background(), req, out, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("NewSessionForTest: %v", err)
	}
	defer sess.Cancel(context.Background())
	if _, closed := drainOpenCode(t, out, 5*time.Second); !closed {
		t.Fatal("out did not close")
	}

	skillRoot := filepath.Join(home, "runtime", "opencode", "state", "conv-skills", "agent-1", "opencode", "skills")
	if _, err := os.Stat(filepath.Join(skillRoot, "find-skills", "SKILL.md")); err != nil {
		t.Fatalf("managed skill missing: %v", err)
	}
	dumped, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read dumped config: %v", err)
	}
	var configEnv map[string]string
	if err := json.Unmarshal(dumped, &configEnv); err != nil {
		t.Fatalf("decode dumped config env: %v", err)
	}
	if got, want := configEnv["config_dir"], filepath.Dir(skillRoot); got != want {
		t.Fatalf("OPENCODE_CONFIG_DIR = %q, want %q", got, want)
	}
	if got := configEnv["xdg_config_home"]; got != userConfigHome {
		t.Fatalf("XDG_CONFIG_HOME = %q, want inherited %q", got, userConfigHome)
	}

	cleanupReq := opencodeHelperReq("run_skills_cleanup", "hello", "json-success")
	cleanupReq.ConversationID = req.ConversationID
	cleanupReq.AgentStateKey = req.AgentStateKey
	cleanupOut := make(chan proto.Envelope, 32)
	cleanupSession, err := opencode.NewSessionForTest(context.Background(), cleanupReq, cleanupOut, opencodeHelperConfig())
	if err != nil {
		t.Fatalf("cleanup session: %v", err)
	}
	defer cleanupSession.Cancel(context.Background())
	if _, closed := drainOpenCode(t, cleanupOut, 5*time.Second); !closed {
		t.Fatal("cleanup out did not close")
	}
	if _, err := os.Stat(filepath.Join(skillRoot, "find-skills")); !os.IsNotExist(err) {
		t.Fatalf("unbound skill still exists: %v", err)
	}
}

func openCodeSkillZip(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("---\nname: find-skills\ndescription: Find skills\n---\nUse the catalog.")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
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
