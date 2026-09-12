package store

import (
	"encoding/json"
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
