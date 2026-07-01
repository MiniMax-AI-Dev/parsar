package agentdaemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestIMHistoryMCPScript_EndToEnd drives the real `sh -c` front door through a
// full MCP handshake and asserts a tools/call round-trips to the stub endpoint.
// Skipped when node is absent (the no-node degraded path is covered by the
// script's sh branch, exercised in TestIMHistoryMCPScript_NoNode).
func TestIMHistoryMCPScript_EndToEnd(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed; skipping node-path e2e")
	}

	var gotAuth, gotConv, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotConv = r.URL.Query().Get("conversation_id")
		gotLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"messages":[{"id":"om_a","text":"hi"}],"cap":50,"platform":"feishu"}`)
	}))
	defer srv.Close()

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"PARSAR_IM_HISTORY_URL=" + srv.URL,
		"PARSAR_IM_HISTORY_TOKEN=tok-xyz",
		"PARSAR_CONVERSATION_ID=conv-1",
	}
	resp := runMCPScript(t, env, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch_chat_history","arguments":{"limit":10}}}`,
	}, 3)

	// initialize echoes the client's protocol version.
	if pv, _ := resp[1]["result"].(map[string]any)["protocolVersion"].(string); pv != "2025-06-18" {
		t.Fatalf("initialize protocolVersion = %q, want echo 2025-06-18", pv)
	}
	// tools/list advertises fetch_chat_history.
	tools, _ := resp[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "fetch_chat_history" {
		t.Fatalf("tools/list = %#v", resp[2]["result"])
	}
	// tools/call routed to the stub with the token + scoped conversation +
	// limit, and returned the body verbatim as text content.
	if gotAuth != "Bearer tok-xyz" || gotConv != "conv-1" || gotLimit != "10" {
		t.Fatalf("request: auth=%q conv=%q limit=%q", gotAuth, gotConv, gotLimit)
	}
	content, _ := resp[3]["result"].(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("call content = %#v", resp[3]["result"])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &body); err != nil {
		t.Fatalf("call body not JSON: %v", err)
	}
	if body["platform"] != "feishu" {
		t.Fatalf("call body = %#v", body)
	}
}

// TestIMHistoryMCPScript_NoNode forces the no-node branch (PATH stripped so
// `command -v node` fails) and asserts the sh responder still initializes,
// lists the tool, and returns a friendly install-node error on call — the
// never-block-startup contract.
func TestIMHistoryMCPScript_NoNode(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	// Empty PATH makes `command -v node` fail even on a machine with node.
	env := []string{"PATH="}
	resp := runMCPScript(t, env, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch_chat_history"}}`,
	}, 3)

	if _, ok := resp[1]["result"]; !ok {
		t.Fatalf("no-node initialize failed: %#v", resp[1])
	}
	tools, _ := resp[2]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("no-node tools/list = %#v", resp[2]["result"])
	}
	res, _ := resp[3]["result"].(map[string]any)
	if res == nil || res["isError"] != true {
		t.Fatalf("no-node call must be isError: %#v", resp[3])
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] == "" {
		t.Fatalf("no-node call missing error text: %#v", res)
	}
}

// runMCPScript spawns `sh -c <script>` with env, writes the given JSON-RPC
// lines to stdin, and returns responses keyed by their integer id (until
// wantIDs distinct ids are seen or the process ends). Env inherits nothing but
// what is passed, so PATH tests are deterministic.
func runMCPScript(t *testing.T, env, lines []string, maxID int) map[int]map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", imHistoryMCPScript)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	go func() {
		for _, l := range lines {
			_, _ = io.WriteString(stdin, l+"\n")
		}
		// Give the async node HTTP call time to flush before EOF closes the
		// process; the scanner below is the real synchronizer.
	}()

	out := map[int]map[string]any{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if idf, ok := msg["id"].(float64); ok {
			out[int(idf)] = msg
			if len(out) >= maxID {
				break
			}
		}
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	return out
}
