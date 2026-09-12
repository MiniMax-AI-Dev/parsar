package store

import (
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"reflect"
	"testing"
)

func TestFunctionResultEventsAreInputs(t *testing.T) {
	s, _ := testStore(t)
	tenant, session := newTurnSession(t, s)
	turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
	transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
	events := []ExecutionEvent{
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"call","stage":"after","native_item":{"type":"dynamicToolCall","id":"call","tool":"lookup","status":"completed","success":true,"arguments":{},"contentItems":[{"type":"inputText","text":"result"}]}}`)},
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"answer"}`)},
	}
	if err := s.AppendTurnEvents(t.Context(), tenant, session.ID, turn, 1, events); err != nil {
		t.Fatal(err)
	}
	changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	results := 0
	for _, change := range changes {
		event := change.Event
		if event.Item == nil {
			continue
		}
		if event.Item.Type == "function_call_output" {
			results++
			if event.Type != "agent.session.turn.item.added" || event.OutputIndex != nil {
				t.Fatal("function result is not agent output", event)
			}
		}
		if event.Item.Type == "message" && event.Item.Role == "assistant" && (event.OutputIndex == nil || *event.OutputIndex != 1) {
			t.Fatal("function result consumed an output index", event)
		}
	}
	page, err := s.ListItems(t.Context(), tenant, session.ID, "", 100, true)
	if err != nil || results != 1 {
		t.Fatal(page, results, err)
	}
	found := false
	for _, item := range page.Items {
		if item.Type == "function_call_output" {
			found = true
		}
	}
	if !found {
		t.Fatal("function result missing from recovery items")
	}
}

func TestFunctionResultItemsRetainSubmittedFields(t *testing.T) {
	for _, raw := range []string{
		`{"success":true}`,
		`{"success":false,"output":null,"error":null}`,
		`{"success":true,"output":"original"}`,
		`{"success":false,"output":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,AA=="}],"error":"failure"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			s, _ := testStore(t)
			tenant, session := newTurnSession(t, s)
			turn := submitMessage(t, s, tenant, session.ID, "start").TurnID
			transition(t, s, tenant, session.ID, turn, TurnQueued, TurnInProgress)
			call := functionCallFixture(items.Identity(turn, "tool:call"))
			if err := s.RecordFunctionCall(t.Context(), tenant, session.ID, turn, call); err != nil {
				t.Fatal(err)
			}
			if err := s.SubmitFunctionResult(t.Context(), tenant, session.ID, turn, call.CallID, json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
			event := ExecutionEvent{Kind: "tool_call", Payload: json.RawMessage(`{"id":"call","stage":"after","native_item":{"type":"dynamicToolCall","id":"call","tool":"lookup","status":"completed","success":true,"arguments":{},"contentItems":[{"type":"inputText","text":"normalized"}]}}`)}
			if err := s.AppendTurnEvents(t.Context(), tenant, session.ID, turn, 1, []ExecutionEvent{event}); err != nil {
				t.Fatal(err)
			}
			assertFields := func(value any) {
				t.Helper()
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var expected, actual map[string]any
				if json.Unmarshal([]byte(raw), &expected) != nil || json.Unmarshal(encoded, &actual) != nil {
					t.Fatal(string(encoded))
				}
				for _, field := range []string{"output", "error"} {
					wanted, present := expected[field]
					got, exists := actual[field]
					if present != exists || !reflect.DeepEqual(wanted, got) {
						t.Fatalf("%s changed: %s", field, encoded)
					}
				}
			}
			page, err := s.ListItems(t.Context(), tenant, session.ID, "", 100, true)
			if err != nil {
				t.Fatal(err)
			}
			results := 0
			for _, item := range page.Items {
				if item.Type == "function_call_output" {
					assertFields(item)
					results++
				}
			}
			changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range changes {
				if change.Event.Item != nil && change.Event.Item.Type == "function_call_output" {
					assertFields(change.Event.Item)
					results++
				}
			}
			if results != 2 {
				t.Fatal("missing saved or streamed result", results)
			}
		})
	}
}
