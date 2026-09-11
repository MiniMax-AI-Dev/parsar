package items

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

const testTurn = "bff31a40-9a63-4a49-aebe-89dfe9dd5268"

func TestMessageSnapshotsReplaceDeltasAndDoNotRegress(t *testing.T) {
	var previous v1.Item
	for _, event := range []struct{ kind, body string }{
		{"output_message", `{"id":"native","status":"in_progress","phase":"commentary"}`},
		{"delta", `{"item_id":"native","delta":"draft"}`},
		{"output_message", `{"id":"native","status":"completed","phase":"final_answer","text":"Revised answer"}`},
		{"delta", `{"item_id":"native","delta":"late"}`},
		{"output_message", `{"id":"native","status":"in_progress"}`},
	} {
		updates, err := Project(testTurn, event.kind, 1, []byte(event.body))
		if err != nil {
			t.Fatal(err)
		}
		previous = Merge(updates[0], previous)
	}
	if previous.Status != "completed" || previous.Phase != "final_answer" || *previous.Content[0].Text != "Revised answer" {
		t.Fatalf("%+v", previous)
	}
	raw, _ := json.Marshal(previous)
	if strings.Contains(string(raw), "native") {
		t.Fatal("native identity leaked")
	}
}

func TestToolProjectionPreservesResultsAndPublicFields(t *testing.T) {
	cases := []struct{ native, kind, status string }{
		{`{"type":"commandExecution","id":"x","command":"exit 7","cwd":"/work","status":"failed","aggregatedOutput":"error\n","exitCode":7,"durationMs":9,"privateField":"SECRET"}`, "command_execution", "failed"},
		{`{"type":"mcpToolCall","id":"x","server":"reference","tool":"lookup","arguments":{"id":9007199254740993},"status":"completed","result":{"structuredContent":{"number":9007199254740993}},"error":null}`, "mcp_call", "completed"},
		{`{"type":"mcpToolCall","id":"x","server":"reference","tool":"lookup","arguments":{},"status":"failed","error":{"message":"failed"}}`, "mcp_call", "failed"},
		{`{"type":"fileChange","id":"x","status":"completed","changes":[{"diff":"-before\n+after"}]}`, "function_call", "completed"},
		{`{"type":"webSearch","id":"x","action":{"type":"search","query":"sample"}}`, "web_search_call", "completed"},
	}
	for _, c := range cases {
		updates, err := Project(testTurn, "tool_call", 1, []byte(`{"id":"x","stage":"after","native_item":`+c.native+`}`))
		if err != nil {
			t.Fatal(err)
		}
		item := updates[0].Item
		if item.Type != c.kind || item.Status != c.status {
			t.Fatalf("%+v", item)
		}
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatal("private native field leaked")
		}
		var restored v1.Item
		if err = json.Unmarshal(raw, &restored); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(restored)
		if strings.Contains(c.native, "9007199254740993") && !strings.Contains(string(again), "9007199254740993") {
			t.Fatal("integer precision lost")
		}
		if c.kind == "mcp_call" && !strings.Contains(string(again), `"output":`) {
			t.Fatal("required nullable result missing")
		}
	}
}

func TestDynamicResultsHaveSeparateLinkedIdentity(t *testing.T) {
	raw := []byte(`{"id":"x","stage":"after","native_item":{"type":"dynamicToolCall","id":"x","tool":"lookup","namespace":"reference","status":"completed","success":false,"arguments":[1],"contentItems":[{"type":"inputText","text":""},{"type":"inputImage","imageUrl":"data:image/png;base64,abc"}]}}`)
	updates, err := Project(testTurn, "tool_call", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Item.Name != "reference::lookup" || updates[0].Item.ID == updates[1].Item.ID || updates[0].Item.ID != updates[1].Item.CallID || updates[1].Item.Status != "failed" {
		t.Fatalf("%+v", updates)
	}
	result, _ := json.Marshal(updates[1].Item)
	if !strings.Contains(string(result), `"text":""`) || !strings.Contains(string(result), `"input_image"`) {
		t.Fatal(string(result))
	}
}

func TestNativeWebNavigationUsesPublicDiscriminators(t *testing.T) {
	for native, public := range map[string]string{"openPage": "open_page", "findInPage": "find_in_page"} {
		updates, err := Project(testTurn, "tool_call", 1, []byte(`{"id":"web","stage":"after","native_item":{"id":"web","type":"webSearch","action":{"type":"`+native+`","url":"https://example.com","pattern":"needle"}}}`))
		if err != nil || len(updates) != 1 || updates[0].Item.Action.Type != public || updates[0].Item.Action.URL == nil || *updates[0].Item.Action.URL != "https://example.com" {
			t.Fatal(updates, err)
		}
	}
}

func TestLegacyDoneDoesNotConfirmSuccessfulAnswer(t *testing.T) {
	var previous v1.Item
	for _, event := range []struct{ kind, body string }{
		{"delta", `{"delta":"partial answer"}`},
		{"error", `{"error":"provider failure"}`},
		{"done", `{"content":"provider failure"}`},
		{"execution_failed", `{"done":{"content":"provider failure"}}`},
	} {
		updates, err := Project(testTurn, event.kind, 1, []byte(event.body))
		if err != nil {
			t.Fatal(err)
		}
		for _, update := range updates {
			previous = Merge(update, previous)
		}
	}
	if previous.Status != "in_progress" || *previous.Content[0].Text != "partial answer" {
		t.Fatal(previous)
	}
	updates, err := Project(testTurn, "execution_completed", 1, []byte(`{"done":{"content":"complete answer"}}`))
	if err != nil {
		t.Fatal(err)
	}
	previous = Merge(updates[0], previous)
	if previous.Status != "completed" || *previous.Content[0].Text != "complete answer" {
		t.Fatal(previous)
	}
}
