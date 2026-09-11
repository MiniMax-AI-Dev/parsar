package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativeNoExecutionEnvironment(t *testing.T) {
	binary, root := os.Getenv("PARSAR_NATIVE_DAEMON_BIN"), os.Getenv("PARSAR_NATIVE_PROOF_DIR")
	if binary == "" || root == "" {
		t.Skip("explicit native daemon binary and evidence directory required")
	}
	h := newDispatchHarness(t)
	oldPeer, _ := h.registry.LookupDevice(h.device.ID)
	h.conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	home, err := os.MkdirTemp(root, "no-environment-native-")
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "parsar-daemon", "execution")
	if err = os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]string{"server_url": h.url + "/api/v1", "runtime_id": h.device.ID, "runner_credential": h.credential, "device_name": "native proof"})
	if err = os.WriteFile(filepath.Join(profile, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	daemonLog, err := os.Create(filepath.Join(home, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemonLog.Close()
	cmd := exec.Command(binary, "connect", "--profile", "execution")
	cmd.Env = append(os.Environ(), "PARSAR_HOME="+home)
	cmd.Stdout, cmd.Stderr = daemonLog, daemonLog
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-stopped:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-stopped
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		peer, e := h.registry.LookupDevice(h.device.ID)
		if e == nil && peer != oldPeer {
			if info, found, known := peer.AgentKindStatus("codex"); known && found && info.Available && info.Capabilities.EnvironmentNone {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("native daemon not ready; logs %s", home)
		}
		time.Sleep(50 * time.Millisecond)
	}
	config, _ := json.Marshal(map[string]any{"agent": map[string]string{"model": "gpt-5.5", "instructions": "Keep this instruction."}, "environment": map[string]string{"type": "none"}})
	h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "native-session", Configuration: config})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.BindSessionDevice(ctx, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	marker := filepath.Join(home, "must-not-exist")
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/responses") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := requests.Add(1)
		raw, _ := json.MarshalIndent(body, "", "  ")
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("model-request-%d.json", n)), raw, 0600)
		if strings.Contains(string(raw), "PUBLIC-CANCEL") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"cancel_response\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		var item map[string]any
		if n == 1 {
			args, _ := json.Marshal(map[string]string{"cmd": "touch " + marker})
			item = map[string]any{"id": "fc_forbidden", "type": "function_call", "call_id": "call_forbidden", "name": "exec_command", "arguments": string(args), "status": "completed"}
		} else {
			item = map[string]any{"id": fmt.Sprintf("message_%d", n), "type": "message", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "NO-ENVIRONMENT-OK", "annotations": []any{}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(kind string, data map[string]any) {
			data["type"] = kind
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
			w.(http.Flusher).Flush()
		}
		send("response.created", map[string]any{"response": map[string]any{"id": fmt.Sprintf("response_%d", n), "status": "in_progress", "output": []any{}}})
		send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		send("response.completed", map[string]any{"response": map[string]any{"id": fmt.Sprintf("response_%d", n), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "gpt-5.5", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 3, "total_tokens": 13, "input_tokens_details": map[string]any{"cached_tokens": 4}, "output_tokens_details": map[string]any{"reasoning_tokens": 2}}}})
	}))
	defer model.Close()
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-test-token"}, "env": map[string]any{"CODEX_EXEC_SERVER_URL": "ws://127.0.0.1:1"}}, nil
	}
	first := h.message("first", "Return an answer.")
	h.finished(h.run(ctx, first.TurnID), store.TurnCompleted)
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID == "" {
		t.Fatal(bound, err)
	}
	second := h.message("second", "Continue the same conversation.")
	h.finished(h.run(ctx, second.TurnID), store.TurnCompleted)
	again, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || again.NativeSessionID != bound.NativeSessionID {
		t.Fatal(again, err)
	}
	if requests.Load() != 3 {
		t.Fatalf("expected rejected command and two answers; requests=%d; evidence %s", requests.Load(), home)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("forbidden command may have executed: %v", err)
	}
	page, err := h.s.ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	answers := 0
	for _, item := range page.Items {
		if item.Role == "assistant" && item.Status == "completed" && len(item.Content) > 0 && item.Content[0].Text != nil && *item.Content[0].Text == "NO-ENVIRONMENT-OK" {
			answers++
		}
	}
	if answers != 2 {
		t.Fatal(page)
	}
	verifyNativePublicExecution(t, h, ctx, home)
	t.Logf("Native environment none: command rejected, caller override ignored, two Turns resumed and recovered. Evidence: %s", home)
}
