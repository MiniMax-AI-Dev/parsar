package api

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

const environmentOrigin = "https://executor.example"

func environmentSession() store.Session {
	return store.Session{
		ID: "session", TenantID: "tenant", CreatedAt: time.Unix(1700000000, 0), Metadata: map[string]string{},
		Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"self_hosted"},"daemon":{"credential":"private"}}`),
		Environment: &store.Environment{
			ID: "environment", SessionID: "session", TenantID: "tenant", Status: "pending",
			Configuration: json.RawMessage(`{"type":"self_hosted","workspace_directory":"/remote/workspace","capability_directories":["/remote/capabilities"],"id":"forged","remote_url":"https://secret@private","env":{"SECRET":"private"},"setup_commands":["private"]}`),
		},
	}
}

func TestSessionEnvironmentUsesSafeStoredAssociation(t *testing.T) {
	session := environmentSession()
	response, err := sessionResponse(session, environmentOrigin)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(response.Environment)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || !reflect.DeepEqual(fields, map[string]any{
		"id": "environment", "type": "self_hosted", "remote_url": environmentOrigin,
		"capability_directories": []any{"/remote/capabilities"}, "workspace_directory": "/remote/workspace",
	}) {
		t.Fatal("unsafe or inaccurate environment output", string(raw))
	}
	if response.Status != "idle" || len(response.RequiredActions) != 0 {
		t.Fatal("offline idle environment requested compute", response)
	}
	session.Environment.Configuration = json.RawMessage(`{"type":"self_hosted","workspace_directory":"/remote/workspace","capability_directories":null}`)
	response, err = sessionResponse(session, environmentOrigin)
	if err != nil || response.Environment.CapabilityDirectories == nil || *response.Environment.CapabilityDirectories == nil {
		t.Fatal("missing capability paths must project as an empty array", response, err)
	}
	for _, change := range []func(*store.Session){
		func(s *store.Session) { s.Environment = nil },
		func(s *store.Session) { s.Environment.ID = "" },
		func(s *store.Session) { s.Environment.TenantID = "foreign" },
		func(s *store.Session) { s.Environment.SessionID = "other" },
		func(s *store.Session) { s.Environment.Configuration = json.RawMessage(`{"type":"self_hosted"}`) },
		func(s *store.Session) {
			s.Configuration = json.RawMessage(`{"agent":{"id":"agent","model":"model"},"environment":{"type":"openai_hosted"}}`)
		},
	} {
		invalid := environmentSession()
		change(&invalid)
		if _, err := sessionResponse(invalid, environmentOrigin); err == nil {
			t.Fatal("invalid environment association or unsupported hosted output accepted")
		}
	}
	if _, err := sessionResponse(session, ""); err == nil {
		t.Fatal("self-hosted output invented a remote URL")
	}
}

func TestEnvironmentInputActivitySnapshotsIgnoreCurrentTurnAndActivity(t *testing.T) {
	session := environmentSession()
	session.LastTurn = &store.Turn{ID: "old-failure", Status: store.TurnFailed, CompletedAt: time.Unix(1700000200, 0)}
	session.EnvironmentInputActivity = &store.EnvironmentInputActivity{Status: "requires_action", EnvironmentID: "environment", LastActiveAt: time.Unix(1700000300, 0)}
	session.RequiredActions = []v1.FunctionCallAction{{Type: "function_call", CallID: "stale"}}
	session.Usage = json.RawMessage(`{"input_tokens":999,"output_tokens":0,"total_tokens":999}`)
	for _, status := range []string{"requires_action", "idle", "failed"} {
		activity := &store.EnvironmentInputActivity{Status: status, LastActiveAt: time.Unix(1700000100, 0)}
		if status == "requires_action" {
			activity.EnvironmentID = "environment"
		}
		change := store.SessionChange{
			Event:                    v1.SessionEvent{Type: "agent.session." + status, EventID: "event", SessionID: session.ID},
			EnvironmentInputActivity: activity,
		}
		event, err := streamResponse(session, change, environmentOrigin)
		if err != nil || event.Session == nil {
			t.Fatal(event, err)
		}
		value := event.Session
		if value.Status != status || (value.Error != nil) != (status == "failed") || value.Usage != nil || value.LastActiveAt != 1700000100 || value.RequiredActions == nil {
			t.Fatal("activity borrowed current Session state", value)
		}
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if json.Unmarshal(raw, &wire) != nil || len(wire) != 3 || wire["session"] == nil || wire["turn_id"] != nil || wire["session_id"] != nil {
			t.Fatal("invalid pre-Turn event fields", string(raw))
		}
		actions, _ := json.Marshal(value.RequiredActions)
		if status == "requires_action" && string(actions) != `[{"environment_id":"environment","type":"environment_connection"}]` {
			t.Fatal("function fields leaked into environment action", string(actions))
		}
		if status != "requires_action" && string(actions) != `[]` {
			t.Fatal("settled action leaked", string(actions))
		}
		if status == "failed" && *value.Error != "The initial input timed out waiting for the environment connection." {
			t.Fatal("unsafe or missing initial failure message", value.Error)
		}
	}
	change := store.SessionChange{
		Event: v1.SessionEvent{Type: "agent.session.in_progress", EventID: "promotion"},
		Turn:  &store.Turn{ID: "promoted", Status: store.TurnInProgress, CreatedAt: time.Unix(1700000400, 0)},
	}
	event, err := streamResponse(session, change, environmentOrigin)
	if err != nil || event.Session.Status != "in_progress" || len(event.Session.RequiredActions) != 0 || event.Session.LastActiveAt != 1700000400 {
		t.Fatal("current pending activity leaked into promoted Turn snapshot", event, err)
	}
}
