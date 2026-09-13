package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type preparationFrame struct {
	PID    int             `json:"pid"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func preparationFixture(t *testing.T) (proto.PromptRequestPayload, sessionConfig, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	t.Setenv("PARSAR_PREPARATION_FAKE", "1")
	t.Setenv("PARSAR_PREPARATION_FRAMES", filepath.Join(root, "frames.jsonl"))
	t.Setenv("PARSAR_PREPARATION_STATUS", filepath.Join(root, "remote-status"))
	t.Setenv("PARSAR_PREPARATION_BLOCK", "")
	for _, key := range []string{"CODEX_EXEC_SERVER_URL", "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL", "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN"} {
		t.Setenv(key, "")
	}
	binary := filepath.Join(root, "fake-codex")
	executable := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	body := "#!/bin/sh\nexec " + executable + " -test.run=^TestPreparationFakeCodexProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := defaultSessionConfig()
	cfg.codexBinary = binary
	req := proto.PromptRequestPayload{
		AgentKind: "codex", AgentStateKey: "prepared-session", WorkDir: filepath.Join(root, "harness"),
		ReleaseOnCompletion: true, StrictResume: true,
		AgentOptions:      map[string]any{"model": "fixture-model", "model_verbosity": "medium"},
		RemoteEnvironment: &proto.RemoteEnvironment{ID: "fixture-environment", WorkspaceDirectory: "/executor-only", ConnectionURL: "http://127.0.0.1:12345", ConnectionToken: "synthetic-harness-token"},
		FunctionTools:     []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}}}`)}},
	}
	return req, cfg, root
}

func preparationFrames(t *testing.T, root string) []preparationFrame {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "frames.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var frames []preparationFrame
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var frame preparationFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	return frames
}

func waitPreparationMethod(t *testing.T, root, method string) []preparationFrame {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		frames := preparationFrames(t, root)
		for _, frame := range frames {
			if frame.Method == method {
				return frames
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("native request not observed", method)
	return nil
}

func assertPreparationOnly(t *testing.T, root string) {
	t.Helper()
	for _, frame := range preparationFrames(t, root) {
		if strings.HasPrefix(frame.Method, "thread/") || strings.HasPrefix(frame.Method, "turn/") {
			t.Fatal("preparation started native work", frame.Method)
		}
	}
}

func preparedCatalogs(t *testing.T, root string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "parsar-daemon", "agent-sessions", "prepared-session", "model-catalog-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func waitPreparedRelease(t *testing.T, p *Prepared, root string) {
	t.Helper()
	select {
	case <-p.session.rpc.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("prepared child was not released")
	}
	deadline := time.Now().Add(time.Second)
	for len(preparedCatalogs(t, root)) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(preparedCatalogs(t, root)) != 0 {
		t.Fatal("prepared model catalog was not cleaned")
	}
}

func TestPreparationFakeCodexProcess(t *testing.T) {
	if os.Getenv("PARSAR_PREPARATION_FAKE") != "1" {
		return
	}
	for _, arg := range os.Args {
		if arg == "models" {
			_, _ = os.Stdout.WriteString(`{"models":[{"slug":"fixture-model","support_verbosity":true}]}`)
			os.Exit(0)
		}
	}
	log, err := os.OpenFile(os.Getenv("PARSAR_PREPARATION_FRAMES"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	frames := json.NewEncoder(log)
	output := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var frame preparationFrame
		if json.Unmarshal(scanner.Bytes(), &frame) != nil {
			os.Exit(3)
		}
		frame.PID = os.Getpid()
		if frames.Encode(frame) != nil {
			os.Exit(4)
		}
		var result any = map[string]any{}
		switch frame.Method {
		case "initialize":
			result = map[string]string{"userAgent": "fixture-codex"}
		case "environment/info":
			if os.Getenv("PARSAR_PREPARATION_BLOCK") == "1" {
				for {
					time.Sleep(time.Second)
				}
			}
			result = map[string]any{"shell": map[string]string{"path": "/bin/sh"}}
		case "environment/status":
			var params map[string]string
			_ = json.Unmarshal(frame.Params, &params)
			status := "unknown"
			if params["environmentId"] == "remote" {
				status = "ready"
				if data, err := os.ReadFile(os.Getenv("PARSAR_PREPARATION_STATUS")); err == nil {
					status = string(data)
				}
			}
			result = map[string]string{"status": status}
		case "thread/start", "thread/resume":
			result = map[string]any{"thread": map[string]string{"id": "fixture-native-thread"}, "model": "fixture-model"}
		case "turn/start":
			result = map[string]any{"turn": map[string]string{"id": "fixture-native-turn"}}
		}
		if output.Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result}) != nil {
			os.Exit(5)
		}
		if frame.Method == "turn/start" {
			_ = output.Encode(map[string]any{"jsonrpc": "2.0", "method": "turn/started", "params": map[string]any{"threadId": "fixture-native-thread", "turn": map[string]string{"id": "fixture-native-turn"}}})
		}
	}
	_ = log.Close()
	os.Exit(0)
}
