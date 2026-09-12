package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestFunctionStateEventsUseTheirOwnSnapshot(t *testing.T) {
	session := store.Session{ID: "session", CreatedAt: time.Now(), Metadata: map[string]string{},
		Configuration:   json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"none"}}`),
		RequiredActions: []v1.FunctionCallAction{{CallID: "stale"}},
	}
	for _, arguments := range []string{`{"n":9007199254740993}`, `null`, `[1,"value"]`, `"value"`, `false`} {
		action := v1.FunctionCallAction{Type: "function_call", CallID: "call", Name: "lookup", TurnID: "turn", Arguments: json.RawMessage(arguments)}
		change := store.SessionChange{Event: v1.SessionEvent{Type: "agent.session.requires_action", EventID: "event", SessionID: "session"}, Turn: &store.Turn{ID: "turn", Status: store.TurnWaiting}, RequiredActions: []v1.FunctionCallAction{action}}
		event, err := streamResponse(session, change)
		if err != nil || event.Session.Status != "requires_action" || !reflect.DeepEqual(event.Session.RequiredActions, change.RequiredActions) {
			t.Fatal(event, err)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire) != 3 || wire["session_id"] != nil || wire["turn_id"] != nil || wire["session"] == nil {
			t.Fatal("unexpected public fields", string(raw))
		}
		change.RequiredActions = nil
		change.Turn.Status = store.TurnInProgress
		change.Event.Type = "agent.session.in_progress"
		event, err = streamResponse(session, change)
		if err != nil || event.Session.Status != "in_progress" || event.Session.RequiredActions == nil || len(event.Session.RequiredActions) != 0 {
			t.Fatal("stale actions leaked", event, err)
		}
	}
}
