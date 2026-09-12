package items

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolProjectionPreservesResultsAndPublicFields(t *testing.T) {
	cases := []struct{ observation, kind, status string }{
		{`{"kind":"command","command":"exit 7","cwd":"/work","status":"failed","output":"error\n","exit_code":7,"duration_ms":9,"privateField":"SECRET"}`, "command_execution", "failed"},
		{`{"kind":"mcp","server":"reference","name":"lookup","arguments":{"id":9007199254740993},"status":"completed","output":{"structuredContent":{"number":9007199254740993}},"error":null}`, "mcp_call", "completed"},
		{`{"kind":"mcp","server":"reference","name":"lookup","arguments":{},"status":"failed","error":{"message":"failed"}}`, "mcp_call", "failed"},
		{`{"kind":"function","name":"apply_patch","status":"completed","arguments":{"changes":[{"diff":"-before\n+after"}]}}`, "function_call", "completed"},
		{`{"kind":"web_search","status":"failed","action":{"type":"search","query":"sample"}}`, "web_search_call", "incomplete"},
	}
	for _, c := range cases {
		updates, err := Project(testTurn, "tool_call", 1, []byte(`{"id":"x","stage":"after","observation":`+c.observation+`}`))
		if err != nil {
			t.Fatal(err)
		}
		item := updates[0].Item
		if item.Type != c.kind || item.Status != c.status {
			t.Fatalf("%+v", item)
		}
		raw, _ := json.Marshal(item)
		if strings.Contains(string(raw), "SECRET") {
			t.Fatal("private field leaked")
		}
		if strings.Contains(c.observation, "9007199254740993") && !strings.Contains(string(raw), "9007199254740993") {
			t.Fatal("integer precision lost")
		}
		if c.kind == "mcp_call" && !strings.Contains(string(raw), `"output":`) {
			t.Fatal("required nullable output missing")
		}
	}
}

func TestFunctionResultsHaveSeparateLinkedIdentity(t *testing.T) {
	raw := []byte(`{"id":"x","stage":"after","observation":{"kind":"function","name":"reference::lookup","status":"failed","arguments":[1],"content":[{"type":"input_text","text":""},{"type":"input_image","image_url":"data:image/png;base64,abc"}]}}`)
	updates, err := Project(testTurn, "tool_call", 1, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 || updates[0].Item.Name != "reference::lookup" || updates[0].Item.ID != Identity(testTurn, "tool:x") || updates[1].Item.ID != Identity(testTurn, "result:x") || updates[0].Item.ID != updates[1].Item.CallID || updates[1].Item.Status != "failed" {
		t.Fatalf("%+v", updates)
	}
	result, _ := json.Marshal(updates[1].Item)
	if !strings.Contains(string(result), `"text":""`) || !strings.Contains(string(result), `"input_image"`) {
		t.Fatal(string(result))
	}
	for _, content := range []string{"", `,"content":null`, `,"content":[]`} {
		updates, err = Project(testTurn, "tool_call", 1, []byte(`{"id":"x","stage":"after","observation":{"kind":"function","name":"lookup","status":"completed"`+content+`}}`))
		if err != nil {
			t.Fatal(err)
		}
		want := 1
		if strings.Contains(content, "[]") {
			want = 2
		}
		if len(updates) != want {
			t.Fatal(updates)
		}
		if want == 2 && string(encoded(updates[1].Item.Output)) != "[]" {
			t.Fatal(updates)
		}
	}
}

func TestToolProjectionRejectsUntranslatedOrInvalidObservations(t *testing.T) {
	for _, raw := range []string{
		`{"id":"x","name":"Bash","stage":"after","result":"ok"}`,
		`{"id":"x","stage":"after","native_item":{"id":"x","type":"commandExecution","command":"pwd"}}`,
		`{"id":"x","stage":"before","observation":{"kind":"command","status":"completed","command":"pwd"}}`,
		`{"id":"x","stage":"after","observation":{"kind":"command","status":"completed","command":"pwd","output":{}}}`,
		`{"id":"x","stage":"after","observation":{"kind":"function","status":"completed","name":"f","content":[{"type":"unknown"}]}}`,
		`{"id":"x","stage":"after","observation":{"kind":"web_search","status":"completed","action":{"type":"openPage"}}}`,
	} {
		if _, err := Project(testTurn, "tool_call", 1, []byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestWebNavigationUsesNeutralDiscriminators(t *testing.T) {
	for _, kind := range []string{"open_page", "find_in_page"} {
		updates, err := Project(testTurn, "tool_call", 1, []byte(`{"id":"web","stage":"after","observation":{"kind":"web_search","status":"completed","action":{"type":"`+kind+`","url":"https://example.com","pattern":"needle"}}}`))
		if err != nil || len(updates) != 1 || updates[0].Item.Action.Type != kind || updates[0].Item.Action.URL == nil || *updates[0].Item.Action.URL != "https://example.com" {
			t.Fatal(updates, err)
		}
	}
}
