package agentsapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/openai/openai-go/v3"
)

func TestFunctionOutputProjectionAndReplay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decode := func(raw string) openai.AgentSessionItemUnion {
		var item openai.AgentSessionItemUnion
		if err := json.Unmarshal([]byte(raw), &item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	call := decode(`{"id":"item-call","type":"function_call","call_id":"call-1","name":"lookup","arguments":"{}","status":"completed"}`)
	output := decode(`{"id":"item-output","type":"function_call_output","call_id":"call-1","output":[{"type":"text","text":"found"}],"status":"completed"}`)
	p := projection{tools: map[string]bool{}}
	update := func(items []openai.AgentSessionItemUnion) []connector.PromptEvent {
		ch := make(chan connector.PromptEvent)
		done := make(chan error, 1)
		go func() { done <- p.update(ctx, ch, items); close(ch) }()
		var events []connector.PromptEvent
		for ev := range ch {
			events = append(events, ev)
			ev.Persisted <- nil
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		return events
	}
	events := update([]openai.AgentSessionItemUnion{call})
	if len(events) != 1 || events[0].Tool.Stage != "before" {
		t.Fatalf("premature result: %+v", events)
	}
	// Output order does not matter; the protocol's call_id is the join key.
	events = update([]openai.AgentSessionItemUnion{output, call})
	if len(events) != 1 || events[0].Tool.Stage != "after" || events[0].Tool.ID != "item-call" || events[0].Tool.Name != "lookup" {
		t.Fatalf("incorrect tool identity: %+v", events)
	}
	parts := events[0].Tool.Result["output"].([]any)
	if parts[0].(map[string]any)["text"] != "found" {
		t.Fatalf("missing output: %+v", events[0].Tool)
	}
	if got := update([]openai.AgentSessionItemUnion{call, output}); len(got) != 0 {
		t.Fatalf("duplicate projection: %+v", got)
	}
}

func TestIncompleteToolResultRetainsFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var item openai.AgentSessionItemUnion
	if err := json.Unmarshal([]byte(`{"id":"search","type":"web_search_call","status":"incomplete"}`), &item); err != nil {
		t.Fatal(err)
	}
	p := projection{tools: map[string]bool{}}
	ch := make(chan connector.PromptEvent)
	done := make(chan error, 1)
	go func() { done <- p.update(ctx, ch, []openai.AgentSessionItemUnion{item}); close(ch) }()
	event := <-ch
	if event.Tool == nil || event.Tool.Stage != "after" || event.Tool.Result["status"] != "incomplete" || event.Tool.Result["is_error"] != true {
		t.Fatalf("incomplete tool appears successful: %+v", event.Tool)
	}
	// Product persistence serializes this result unchanged, so restored traces
	// receive the same explicit failure marker as the live event.
	raw, err := json.Marshal(event.Tool.Result)
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored["is_error"] != true {
		t.Fatal("restored result lost failure")
	}
	event.Persisted <- nil
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
