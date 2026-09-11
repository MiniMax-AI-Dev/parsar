package main

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func observeItems(ctx context.Context, s *store.Store, tenant, session, turn, status string) error {
	events := []store.ExecutionEvent{
		{Kind: "delta", Payload: json.RawMessage(`{"item_id":"answer","delta":"partial answer"}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"command","stage":"after","native_item":{"id":"command","type":"commandExecution","command":"exit 7","cwd":"/workspace","status":"failed","aggregatedOutput":"command failed","exitCode":7,"durationMs":8}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"mcp","stage":"after","native_item":{"id":"mcp","type":"mcpToolCall","server":"reference","tool":"lookup","arguments":{"n":9007199254740993},"status":"completed","result":{"structuredContent":{"n":9007199254740993}},"error":null}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"dynamic","stage":"after","native_item":{"id":"dynamic","type":"dynamicToolCall","tool":"lookup","arguments":{},"status":"completed","success":true,"contentItems":[{"type":"inputText","text":""}]}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"patch","stage":"after","native_item":{"id":"patch","type":"fileChange","changes":[{"path":"/workspace/sample","diff":"+example"}],"status":"completed"}}`)},
		{Kind: "tool_call", Payload: json.RawMessage(`{"id":"search","stage":"after","native_item":{"id":"search","type":"webSearch","query":"reference","action":{"type":"search","query":"reference"}}}`)},
	}
	if status == store.TurnCompleted || status == store.TurnFailed {
		events = append(events, store.ExecutionEvent{Kind: "output_message", Payload: json.RawMessage(`{"id":"answer","status":"completed","text":"final answer","phase":"final_answer"}`)})
	}
	return s.AppendTurnEvents(ctx, tenant, session, turn, 1, events)
}
