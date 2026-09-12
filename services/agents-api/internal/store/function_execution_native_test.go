package store_test

import (
	"bytes"
	"context"
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

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativeFunctionExecutionPersistsCallsResultsAndContinuity(t *testing.T) {
	h, ctx, home := nativeDispatchHarness(t)
	var err error
	h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "native-functions", Configuration: json.RawMessage(functionConfiguration)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.s.BindSessionDevice(ctx, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 1, 1))
	picture.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	output := []any{map[string]any{"type": "input_text", "text": "before"}, map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}, map[string]any{"type": "input_text", "text": "after"}}
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		n := requests.Add(1)
		raw, _ := json.MarshalIndent(body, "", "  ")
		_ = os.WriteFile(filepath.Join(home, fmt.Sprintf("functions-model-%d.json", n)), raw, 0600)
		if !strings.Contains(string(raw), "lookup_ticket") {
			t.Error("configured function missing")
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
				expected := append([]any(nil), output...)
				// Codex adds its default image detail at the model transport boundary.
				expected[1] = map[string]any{"type": "input_image", "image_url": output[1].(map[string]any)["image_url"], "detail": "high"}
				if n == 4 {
					expected = append(expected, map[string]any{"type": "input_text", "text": "synthetic failure"})
				}
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
	defer model.Close()
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"codex_provider": map[string]any{"base_url": model.URL + "/v1", "bearer_token": "synthetic-test-token"}}, nil
	}
	nativeID := ""
	for index := range 3 {
		input := h.message(fmt.Sprint(index), "Look up ticket 42")
		running := h.run(ctx, input.TurnID)
		state := functionState(t, h, 1)
		action := state.RequiredActions[0]
		if action.Name != "lookup_ticket" || action.TurnID != input.TurnID || state.LastTurn.Status != store.TurnWaiting {
			t.Fatal(action, state.LastTurn)
		}
		if index == 2 {
			if _, err := h.s.RequestCancel(ctx, h.tenant, h.session.ID, "native-cancel"); err != nil {
				t.Fatal(err)
			}
			h.finished(running, store.TurnCancelled)
			break
		}
		value := map[string]any{"success": index == 0, "output": output}
		if index == 1 {
			value["error"] = "synthetic failure"
		}
		raw, _ := json.Marshal(value)
		for range 2 {
			if err := h.s.SubmitFunctionResult(ctx, h.tenant, h.session.ID, input.TurnID, action.CallID, raw); err != nil {
				t.Fatal(err)
			}
		}
		h.finished(running, store.TurnCompleted)
		saved, err := h.s.GetFunctionCall(ctx, h.tenant, h.session.ID, input.TurnID, action.CallID)
		if err != nil || !saved.Applied {
			t.Fatal(saved, err)
		}
		page, err := h.s.ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, item := range page.Items {
			if item.Type == "function_call" && item.CallID == action.CallID {
				found = true
			}
		}
		if !found {
			t.Fatal("required action identity differs from recovered function item")
		}
		bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
		if err != nil || bound.NativeSessionID == "" || (nativeID != "" && bound.NativeSessionID != nativeID) {
			t.Fatal(bound, err)
		}
		nativeID = bound.NativeSessionID
	}
	functionState(t, h, 0)
	if requests.Load() != 5 {
		t.Fatal("function replay or missing model continuation", requests.Load())
	}
	if t.Failed() {
		return
	}
	t.Logf("Native daemon/engine functions, complete text/image/error results, receipts, Items identity, resume and cancellation passed; evidence %s", home)
}
