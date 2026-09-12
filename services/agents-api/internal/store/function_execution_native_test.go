package store_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	model, output, requests := nativeFunctionModel(t, home)
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
