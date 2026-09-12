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
