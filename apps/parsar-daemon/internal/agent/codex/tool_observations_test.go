package codex

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestToolObservationDetails(t *testing.T) {
	cases := []struct{ native, expected string }{
		{`{"id":"x","type":"commandExecution","command":"exit 2","cwd":"/work","status":"failed","aggregatedOutput":"","exitCode":2,"durationMs":37}`, `{"kind":"command","status":"failed","command":"exit 2","cwd":"/work","exit_code":2,"duration_ms":37,"output":""}`},
		{`{"id":"x","type":"mcpToolCall","server":"reference","tool":"lookup","status":"failed","arguments":{"number":9007199254740993},"result":null,"error":{"message":"failed"}}`, `{"kind":"mcp","status":"failed","name":"lookup","server":"reference","arguments":{"number":9007199254740993},"output":null,"error":{"message":"failed"}}`},
		{`{"id":"x","type":"dynamicToolCall","tool":"lookup","namespace":"reference","status":"completed","success":false,"arguments":[1],"contentItems":[{"type":"inputText","text":""},{"type":"inputImage","imageUrl":"data:image/png;base64,abc"}]}`, `{"kind":"function","status":"failed","name":"reference::lookup","arguments":[1],"content":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}`},
		{`{"id":"x","type":"dynamicToolCall","tool":"lookup","contentItems":[]}`, `{"kind":"function","status":"completed","name":"lookup","content":[]}`},
		{`{"id":"x","type":"fileChange","changes":[{"diff":"after","number":9007199254740993}]}`, `{"kind":"function","status":"completed","name":"apply_patch","arguments":{"changes":[{"diff":"after","number":9007199254740993}]}}`},
		{`{"id":"x","type":"webSearch","action":{"type":"openPage","url":"https://example.com"}}`, `{"kind":"web_search","status":"completed","action":{"type":"open_page","url":"https://example.com"}}`},
	}
	for _, c := range cases {
		value, err := normalizeToolObservation("x", "after", []byte(c.native))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(value)
		if err != nil || !bytes.Equal(raw, []byte(c.expected)) {
			t.Fatalf("got %s; want %s; error %v", raw, c.expected, err)
		}
	}
}

func TestToolObservationRejectsUnrepresentableDetails(t *testing.T) {
	for _, raw := range []string{
		`{"id":"other","type":"commandExecution","command":"pwd"}`,
		`{"id":"x","type":"dynamicToolCall","tool":"lookup","contentItems":[{"type":"inputAudio"}]}`,
	} {
		if _, err := normalizeToolObservation("x", "after", []byte(raw)); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
}
