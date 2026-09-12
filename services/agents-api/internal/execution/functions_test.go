package execution

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestFunctionDefinitionsRejectUnsupportedConfiguration(t *testing.T) {
	valid := json.RawMessage(`{"type":"function","name":"lookup","description":"Find it","parameters":{"type":"object","properties":{}},"defer_loading":false}`)
	tools, err := functionTools([]json.RawMessage{valid})
	if err != nil || len(tools) != 1 || tools[0].Name != "lookup" || tools[0].Description != "Find it" {
		t.Fatal(tools, err)
	}
	for _, raw := range []string{`{"type":"mcp"}`, `{"type":"function","name":"lookup","parameters":null}`, `{"type":"function","name":"lookup","parameters":{},"defer_loading":true}`, `{"type":"function","name":"lookup","parameters":{},"unknown":true}`} {
		if _, err := functionTools([]json.RawMessage{json.RawMessage(raw)}); err == nil {
			t.Fatal("unsupported configuration admitted", raw)
		}
	}
	if _, err := functionTools([]json.RawMessage{valid, valid}); err == nil {
		t.Fatal("duplicate function names")
	}
}

func TestFunctionResultPreservesCompleteContent(t *testing.T) {
	text := func(value string) proto.FunctionResultContent {
		return proto.FunctionResultContent{Type: "input_text", Text: &value}
	}
	imageURL := "data:image/png;base64,test"
	for _, test := range []struct {
		raw     string
		success bool
		content []proto.FunctionResultContent
	}{
		{`{"success":true,"output":""}`, true, []proto.FunctionResultContent{text("")}},
		{`{"success":true,"output":null,"error":null}`, true, []proto.FunctionResultContent{}},
		{`{"success":false,"error":"failed"}`, false, []proto.FunctionResultContent{text("failed")}},
		{`{"success":false,"output":[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,test"},{"type":"input_text","text":""}],"error":"failed"}`, false, []proto.FunctionResultContent{text("before"), {Type: "input_image", ImageURL: &imageURL}, text(""), text("failed")}},
	} {
		result, err := functionResult(store.FunctionCall{CallID: "public", ExecutorCallID: "native", Result: json.RawMessage(test.raw)})
		if err != nil || result.CallID != "native" || result.DeliveryID != "function:public" || result.Success != test.success || !reflect.DeepEqual(result.Content, test.content) {
			t.Fatal(result, err)
		}
	}
	for _, raw := range []string{`{}`, `{"success":null}`, `{"success":true,"output":{}}`, `{"success":true,"output":[{"type":"input_text"}]}`, `{"success":true,"output":[{"type":"input_audio","audio_url":"a"}]}`} {
		if _, err := functionResult(store.FunctionCall{Result: json.RawMessage(raw)}); err == nil {
			t.Fatal("invalid stored result converted", raw)
		}
	}
}
