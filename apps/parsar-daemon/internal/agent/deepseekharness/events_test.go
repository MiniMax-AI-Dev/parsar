package deepseekharness

import (
	"encoding/json"
	"testing"
)

func TestDecodeFrameKeepsOnlyThisSessionsEvents(t *testing.T) {
	frame := func(method, sessionID string) []byte {
		body, err := json.Marshal(map[string]any{
			"type":   "server-request",
			"rpcId":  "r1",
			"method": method,
			"payload": map[string]any{
				"type":      method,
				"sessionId": sessionID,
				"event":     map[string]any{"type": eventTurnStart, "seq": 1, "data": map[string]any{"turn": 1}},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return body
	}

	if _, ok := decodeFrame(frame(frameMethodSessionEvent, "s1"), "s1"); !ok {
		t.Error("a matching session/event frame should be accepted")
	}
	if _, ok := decodeFrame(frame(frameMethodSessionEvent, "s2"), "s1"); ok {
		t.Error("another session's event must be ignored")
	}
	// The mux carries other frame families (projections, queue snapshots,
	// subscription acks). None of them are durable events.
	if _, ok := decodeFrame(frame("session/projection", "s1"), "s1"); ok {
		t.Error("a projection frame must be ignored")
	}
	if _, ok := decodeFrame(frame(frameMethodSessionSubscribed, "s1"), "s1"); ok {
		t.Error("a subscription ack must be ignored")
	}
	// Malformed input is ignored rather than failing a turn.
	if _, ok := decodeFrame([]byte("not json"), "s1"); ok {
		t.Error("unparseable frames must be ignored")
	}
	if _, ok := decodeFrame([]byte(`{"method":"session/event","payload":"not an object"}`), "s1"); ok {
		t.Error("a frame with an undecodable payload must be ignored")
	}
}

func TestArgsFromJSONStringPreservesUnparseableArguments(t *testing.T) {
	got := argsFromJSONString(`{"file_path":"a.txt","limit":10}`)
	if got["file_path"] != "a.txt" {
		t.Errorf("parsed args = %#v", got)
	}

	// A model can emit invalid JSON. Losing it would leave the operator
	// with a tool call and no idea what was asked.
	broken := argsFromJSONString(`  {"file_path":  `)
	if broken["raw"] != `{"file_path":` {
		t.Errorf("unparseable args = %#v, want the raw text preserved", broken)
	}

	if got := argsFromJSONString("   "); got != nil {
		t.Errorf("empty args = %#v, want nil", got)
	}
}

func TestToolResultTextFlattensNestedContent(t *testing.T) {
	var data toolResultData
	raw := `{"message":{"source":{"kind":"tool","callId":"c1"},"content":[
		{"type":"tool-result","toolCallId":"c1","isError":true,"content":[
			{"type":"text","text":"line one"},
			{"type":"text","text":"line two"}]}]}}`
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	text, isError := data.textFromToolResult()
	if text != "line one\nline two" {
		t.Errorf("text = %q", text)
	}
	if !isError {
		t.Error("isError was not carried through")
	}
	if data.Message.Source.CallID != "c1" {
		t.Errorf("call id = %q", data.Message.Source.CallID)
	}
}

func TestAssistantTextExcludesReasoning(t *testing.T) {
	var data assistantMessageData
	raw := `{"turn":1,"step":1,"message":{"role":"assistant","content":[
		{"type":"reasoning","text":"internal deliberation"},
		{"type":"text","text":"the answer"},
		{"type":"tool-call","text":""}]}}`
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := data.assistantText(); got != "the answer" {
		t.Errorf("assistantText = %q, want just the text block", got)
	}
}
