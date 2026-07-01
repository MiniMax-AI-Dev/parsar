package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestBuiltinCapabilityListAndToggle exercises the synthetic built-in surface
// end to end over HTTP:
//   - the agent capability list includes the parsar_chat_history built-in,
//     defaulting to enabled (no row => ON);
//   - PUT builtin-capabilities/{key} flips it and the next list reflects the
//     new state;
//   - a non-owner is rejected with 403.
func TestBuiltinCapabilityListAndToggle(t *testing.T) {
	r, db := capabilityTestRouter(t, nil, map[string]string{testUserAID: "member", testUserBID: "member"})
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	agentID := insertAgentForOwner(t, db, testUserAID, "builtin-toggle-agent")

	const key = "parsar_chat_history"
	listPath := "/api/v1/workspaces/" + wid + "/agents/" + agentID + "/capabilities"
	togglePath := "/api/v1/workspaces/" + wid + "/agents/" + agentID + "/builtin-capabilities/" + key

	// 1. Default list: built-in present and enabled.
	list := serveCapabilityRoute(t, r, http.MethodGet, listPath, "", testUserAID)
	if list.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d: %s", list.Code, list.Body.String())
	}
	if !builtinEnabledInList(t, list.Body.Bytes(), key) {
		t.Fatalf("expected built-in %q enabled by default in list: %s", key, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"built_in":true`) {
		t.Fatalf("list missing built_in flag: %s", list.Body.String())
	}

	// 2. Toggle OFF.
	off := serveCapabilityRoute(t, r, http.MethodPut, togglePath, `{"enabled":false}`, testUserAID)
	if off.Code != http.StatusOK || !strings.Contains(off.Body.String(), `"enabled":false`) {
		t.Fatalf("toggle off expected 200/enabled=false, got %d: %s", off.Code, off.Body.String())
	}

	// 3. List reflects disabled.
	list2 := serveCapabilityRoute(t, r, http.MethodGet, listPath, "", testUserAID)
	if list2.Code != http.StatusOK {
		t.Fatalf("list2 expected 200, got %d: %s", list2.Code, list2.Body.String())
	}
	if builtinEnabledInList(t, list2.Body.Bytes(), key) {
		t.Fatalf("built-in %q should be disabled after toggle off: %s", key, list2.Body.String())
	}

	// 4. Toggle back ON.
	on := serveCapabilityRoute(t, r, http.MethodPut, togglePath, `{"enabled":true}`, testUserAID)
	if on.Code != http.StatusOK || !strings.Contains(on.Body.String(), `"enabled":true`) {
		t.Fatalf("toggle on expected 200/enabled=true, got %d: %s", on.Code, on.Body.String())
	}
	list3 := serveCapabilityRoute(t, r, http.MethodGet, listPath, "", testUserAID)
	if !builtinEnabledInList(t, list3.Body.Bytes(), key) {
		t.Fatalf("built-in %q should be re-enabled after toggle on: %s", key, list3.Body.String())
	}

	// 5. Non-owner rejected.
	forbidden := serveCapabilityRoute(t, r, http.MethodPut, togglePath, `{"enabled":false}`, testUserBID)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-owner toggle expected 403, got %d: %s", forbidden.Code, forbidden.Body.String())
	}

	// 6. Unknown key rejected with 404.
	unknown := serveCapabilityRoute(t, r, http.MethodPut,
		"/api/v1/workspaces/"+wid+"/agents/"+agentID+"/builtin-capabilities/does_not_exist", `{"enabled":false}`, testUserAID)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown builtin key expected 404, got %d: %s", unknown.Code, unknown.Body.String())
	}
}

// builtinEnabledInList decodes the agent capability list and reports the
// enabled state of the built-in entry with the given key. Fails the test if the
// built-in entry is absent.
func builtinEnabledInList(t *testing.T, body []byte, key string) bool {
	t.Helper()
	var payload struct {
		Installed []struct {
			BuiltIn    bool   `json:"built_in"`
			BuiltinKey string `json:"builtin_key"`
			Enabled    bool   `json:"enabled"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("list body not valid json: %v / %s", err, string(body))
	}
	for _, entry := range payload.Installed {
		if entry.BuiltIn && entry.BuiltinKey == key {
			return entry.Enabled
		}
	}
	t.Fatalf("built-in %q not found in installed list: %s", key, string(body))
	return false
}
