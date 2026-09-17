//go:build unix

package claudesdk

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

func preparationFixture(t *testing.T, mode string) Config {
	t.Helper()
	config := workspaceFixture(t)
	binary, err := filepath.EvalSymlinks(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexport GO_CLAUDE_PREPARATION_HELPER=1 SDK_HELPER_MODE='" + mode + "' GORACE=atexit_sleep_ms=0\nexec '" + strings.ReplaceAll(binary, "'", "'\\''") + "' \"$@\"\n"
	if err := os.WriteFile(config.Node, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return config
}

func preparationRequest() proto.PromptRequestPayload {
	req := workspaceRequest()
	req.RunID, req.Prompt = "", ""
	req.AgentSessionID = "native-session"
	return req
}

func runPreparationHelper() {
	mode := os.Getenv("SDK_HELPER_MODE")
	if len(os.Args) > 1 && strings.HasSuffix(os.Args[1], "runtime_check.js") {
		features := []string{"workspace_tools", "workspace_prepare", "workspace_command_observations", "workspace_read", "workspace_directory"}
		if mode == "old-runtime" {
			features = []string{"workspace_tools"}
		} else if mode == "old-command-runtime" {
			features = []string{"workspace_tools", "workspace_prepare"}
		}
		_ = json.NewEncoder(os.Stdout).Encode(RuntimeInfo{Type: "runtime_ready", Protocol: 1, Node: "fixture", SDK: "fixture", MCP: "fixture", Native: "fixture", Features: features})
		return
	}
	state := os.Getenv("CLAUDE_CONFIG_DIR")
	_ = os.WriteFile(filepath.Join(state, "launched"), nil, 0o600)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(2)
	}
	raw := append([]byte{}, scanner.Bytes()...)
	_ = os.WriteFile(filepath.Join(state, "prepare.json"), raw, 0o600)
	var fields map[string]json.RawMessage
	var request startRequest
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(raw, &request) != nil || request.Type != "prepare" || fields["prompt"] != nil || fields["run_id"] != nil || request.Workspace == nil {
		os.Exit(3)
	}
	emit := func(event bridgeEvent) { _ = json.NewEncoder(os.Stdout).Encode(event) }
	if mode == "history-missing" {
		emit(bridgeEvent{Type: "error", Code: "history_unavailable"})
		return
	}
	if mode == "invalid-receipt" {
		emit(bridgeEvent{Type: "delta", Delta: "unexpected model work"})
		time.Sleep(time.Hour)
		return
	}
	if mode == "delayed-ready" {
		for {
			if _, err := os.Stat(filepath.Join(state, "ready")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	emit(bridgeEvent{Type: "prepared"})
	if strings.HasPrefix(mode, "directory-") {
		runWorkspaceDirectoryHelper(scanner, state, mode)
		return
	}
	if strings.HasPrefix(mode, "read-") {
		runWorkspaceReadHelper(scanner, state, mode)
		return
	}
	if !scanner.Scan() {
		return
	}
	raw = append([]byte{}, scanner.Bytes()...)
	_ = os.WriteFile(filepath.Join(state, "start.json"), raw, 0o600)
	fields = nil
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || string(fields["type"]) != `"start"` || string(fields["prompt"]) != `"hello"` {
		os.Exit(4)
	}
	if mode == "cancellation" {
		runCancellationHelper(request, "cancellation-wait", emit)
		return
	}
	if strings.HasPrefix(mode, "commands") {
		runCommandsHelper(request, mode, emit)
		return
	}
	emit(bridgeEvent{Type: "input_ready", SessionID: request.Resume})
	emit(bridgeEvent{Type: "delta", Delta: "partial"})
	emit(bridgeEvent{Type: "usage", ResultID: "native-result", SessionID: request.Resume, Usage: json.RawMessage(usageFixture)})
	emit(bridgeEvent{Type: "input_closed", SessionID: request.Resume})
	emit(bridgeEvent{Type: "result", SessionID: request.Resume, Text: "completed"})
	_ = os.WriteFile(filepath.Join(state, "released"), nil, 0o600)
}

func waitPreparationFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if raw, err := os.ReadFile(path); err == nil && json.Valid(raw) {
			return raw
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", filepath.Base(path))
		case <-ticker.C:
		}
	}
}
