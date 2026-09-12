package main

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func observeItems(ctx context.Context, s *store.Store, tenant, session, turn, status string) error {
	events := []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"partial answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"command","stage":"after","observation":{"status":"failed","kind":"command","command":"exit 7","cwd":"/workspace","output":"command failed","exit_code":7,"duration_ms":8}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"mcp","stage":"after","observation":{"status":"completed","kind":"mcp","server":"reference","name":"lookup","arguments":{"n":9007199254740993},"output":{"structuredContent":{"n":9007199254740993}},"error":null}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"dynamic","stage":"after","observation":{"status":"completed","kind":"function","name":"reference::lookup","arguments":{},"content":[{"type":"input_text","text":""}]}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"patch","stage":"after","observation":{"status":"completed","kind":"function","name":"apply_patch","arguments":{"changes":[{"path":"/workspace/sample","diff":"+example"}]}}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"search","stage":"after","observation":{"status":"completed","kind":"web_search","action":{"type":"search","query":"reference"}}}`)},
	}
	if status == store.TurnCompleted || status == store.TurnFailed {
		events = append(events, store.ExecutionEvent{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"final answer","phase":"final_answer"}`)})
	}
	return s.AppendTurnEvents(ctx, tenant, session, turn, 1, events)
}
