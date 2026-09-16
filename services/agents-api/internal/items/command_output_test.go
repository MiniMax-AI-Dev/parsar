package items

import (
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func TestCommandOutputKeepsIdentityAndReplacesDraftAtCompletion(t *testing.T) {
	previous := v1.Item{ID: Identity(testTurn, "tool:cmd"), TurnID: testTurn, Type: "command_execution", Command: "run", Status: "in_progress"}
	for _, delta := range []string{"same\n", "same\n", "结束\n"} {
		raw, _ := json.Marshal(map[string]string{"id": "cmd", "delta": delta})
		updates, err := Project(testTurn, "command_output", 1, raw)
		if err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(previous)
		merged := mustMerge(t, updates[0], previous)
		after, _ := json.Marshal(previous)
		if string(before) != string(after) || *updates[0].CommandOutputDelta != delta || merged.ID != previous.ID || merged.Command != "run" {
			t.Fatal("delta merge changed its input or command identity")
		}
		previous = merged
	}
	if string(encoded(previous.Output)) != `"same\nsame\n结束\n"` {
		t.Fatal(string(encoded(previous.Output)))
	}
	for _, snapshot := range []struct{ raw, want string }{
		{``, `"same\nsame\n结束\n"`}, {`""`, `""`}, {`"authoritative"`, `"authoritative"`},
	} {
		final := previous
		final.Status, final.Output = "completed", nil
		if snapshot.raw != "" {
			final.Output = json.RawMessage(snapshot.raw)
		}
		merged := mustMerge(t, Update{Item: final}, previous)
		if string(encoded(merged.Output)) != snapshot.want {
			t.Fatal(string(encoded(merged.Output)))
		}
		late := "late"
		got := mustMerge(t, Update{Item: final, CommandOutputDelta: &late}, merged)
		if !reflect.DeepEqual(got, merged) {
			t.Fatal("late output changed completed command")
		}
	}
}

func TestCommandOutputRequiresMatchingCommand(t *testing.T) {
	updates, err := Project(testTurn, "command_output", 1, []byte(`{"id":"cmd","delta":"text"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, previous := range []v1.Item{
		{},
		{ID: updates[0].Item.ID, TurnID: testTurn, Type: "mcp_call"},
		{ID: updates[0].Item.ID, TurnID: "other", Type: "command_execution"},
		{ID: updates[0].Item.ID, TurnID: testTurn, Type: "command_execution", Status: "in_progress", Output: json.RawMessage(`{}`)},
	} {
		if _, err := Merge(updates[0], previous); err == nil {
			t.Fatal("invalid prior command accepted")
		}
	}
	for _, raw := range []string{`{}`, `{"id":"cmd","delta":null}`, `{"id":"cmd","delta":""}`, `{"id":"cmd","delta":7}`, `{"delta":"text"}`} {
		if _, err := Project(testTurn, "command_output", 1, []byte(raw)); err == nil {
			t.Fatal("invalid delta accepted", raw)
		}
	}
}
