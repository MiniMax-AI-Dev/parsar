package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativeNoExecutionEnvironment(t *testing.T) {
	h, ctx, home := nativeDispatchHarness(t)
	var err error
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
		if body["model"] == "custom-provider-model" {
			t.Error("unsupported verbosity reached model execution")
		}
		for _, value := range body["tools"].([]any) {
			tool := value.(map[string]any)
			if kind, _ := tool["type"].(string); strings.HasPrefix(kind, "web_search") {
				t.Error("undeclared web search reached the model")
			}
		}

		encoded, _ := json.Marshal(body)
		expected := "medium"
		for _, level := range []string{"low", "high"} {
			if strings.Contains(string(encoded), "TEXT-VERBOSITY:"+level) {
				expected = level
			}
		}
		textConfig, _ := body["text"].(map[string]any)
		if textConfig["verbosity"] != expected {
			t.Errorf("effective verbosity = %v, want %s", textConfig["verbosity"], expected)
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
		if item["type"] == "message" {
			initial := map[string]any{"id": item["id"], "type": "message", "role": "assistant", "phase": "final_answer", "status": "in_progress", "content": []any{}}
			send("response.output_item.added", map[string]any{"output_index": 0, "item": initial})
			send("response.content_part.added", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
			send("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "item_id": item["id"], "delta": "NO-ENVIRONMENT-OK"})
			time.Sleep(time.Second)
		} else {
			send("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		}
		send("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		send("response.completed", map[string]any{"response": map[string]any{"id": fmt.Sprintf("response_%d", n), "object": "response", "created_at": time.Now().Unix(), "status": "completed", "model": "gpt-5.5", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 3, "total_tokens": 13, "input_tokens_details": map[string]any{"cached_tokens": 4}, "output_tokens_details": map[string]any{"reasoning_tokens": 2}}}})
	}))
	defer model.Close()
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"model_verbosity": "high", "web_search": "live", "codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-test-token"}, "env": map[string]any{"CODEX_EXEC_SERVER_URL": "ws://127.0.0.1:1"}}, nil
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
