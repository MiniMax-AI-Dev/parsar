package store_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func nativeFunctionModel(t *testing.T, home string) (*httptest.Server, []any, *atomic.Int32) {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	output := []any{map[string]any{"type": "input_text", "text": "before"}, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}, map[string]any{"type": "input_text", "text": "after"}}
	expected := append([]any(nil), output...)
	// Codex adds its default image detail at the model transport boundary.
	expected[1] = map[string]any{"type": "input_image", "image_url": output[1].(map[string]any)["image_url"], "detail": "high"}
	failure := append(append([]any(nil), expected...), map[string]any{"type": "input_text", "text": "synthetic failure"})
	model, requests := nativeFunctionResultsModel(t, home, []any{expected, failure})
	return model, output, requests
}

func nativeFunctionResultsModel(t *testing.T, home string, results []any) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		assertNativeSubagentsDisabled(t, body)
		n := requests.Add(1)
		raw, _ := json.MarshalIndent(body, "", "  ")
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("functions-model-%d.json", n)), raw, 0600)
		var config struct {
			Agent struct{ Tools []map[string]any }
		}
		if err := json.Unmarshal([]byte(functionConfiguration), &config); err != nil {
			t.Error(err)
			return
		}
		expectedTool := config.Agent.Tools[0]
		delete(expectedTool, "defer_loading")
		expectedTool["strict"] = false
		foundTool := false
		for _, tool := range body["tools"].([]any) {
			if reflect.DeepEqual(tool, expectedTool) {
				foundTool = true
			}
		}
		if !foundTool {
			t.Error("configured function changed at the model boundary")
		}
		var entry map[string]any
		if n%2 == 1 {
			entry = map[string]any{"id": fmt.Sprintf("fc_%d", n), "type": "function_call", "call_id": fmt.Sprintf("call_%d", n), "name": "lookup_ticket", "arguments": `{"ticket":"42"}`, "status": "completed"}
		} else {
			found := false
			for _, value := range body["input"].([]any) {
				item := value.(map[string]any)
				if item["type"] != "function_call_output" || item["call_id"] != fmt.Sprintf("call_%d", n-1) {
					continue
				}
				found = true
				if int(n/2) > len(results) {
					t.Error("unexpected native result continuation", n)
					return
				}
				expected := results[n/2-1]
				if !reflect.DeepEqual(item["output"], expected) {
					t.Errorf("complete result changed: %v", item["output"])
				}
			}
			if !found {
				t.Error("native model did not receive stored result")
			}
			entry = map[string]any{"id": fmt.Sprintf("message_%d", n), "type": "message", "role": "assistant", "phase": "final_answer", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "FUNCTION-EXECUTION-OK", "annotations": []any{}}}}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(kind string, data map[string]any) {
			data["type"] = kind
			raw, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
			w.(http.Flusher).Flush()
		}
		send("response.created", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "status": "in_progress", "output": []any{}}})
		send("response.output_item.added", map[string]any{"output_index": 0, "item": entry})
		send("response.output_item.done", map[string]any{"output_index": 0, "item": entry})
		send("response.completed", map[string]any{"response": map[string]any{"id": fmt.Sprintf("r_%d", n), "object": "response", "created_at": 0, "status": "completed", "model": "gpt-5.5", "output": []any{entry}}})
	}))
	return model, &requests
}

func assertNativeSubagentsDisabled(t *testing.T, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body["tools"])
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Multi-agent tools:", "spawn_agent", "send_input", "wait_agent", "resume_agent", "close_agent", "send_message_to_agent"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("disabled subagent tool remains discoverable: %s", forbidden)
		}
	}
}
