package dev

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func (stubRuntimeStore) RecordAgentCapabilityAudit(store.AgentCapabilityAuditInput) {}

func TestAgentCapabilityRequestsAppearInAgentAudit(t *testing.T) {
	roles := map[string]string{testUserAID: "member", testUserBID: "viewer"}
	_, db := capabilityTestRouter(t, roles, nil)
	s, ingester := newDevRouteAuditStore(t, db)
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capabilityRBACStore{RuntimeStore: s, workspaceRoles: roles})
	workspaceID := store.DefaultDevFixtureIDs().WorkspaceID
	capabilityID, v1, v2 := insertCapabilityVersions(t, db, workspaceID, "Audit MCP")
	publishForeignCapability(t, db, capabilityID)
	agentID := insertAgentForOwner(t, db, testUserBID, "audit-capability-agent")
	base := "/api/v1/workspaces/" + workspaceID + "/agents/" + agentID
	tests := []struct {
		method, path, body, action string
		status                     int
		payload                    map[string]any
	}{
		{http.MethodPost, "/capabilities/" + v1 + "/enable", `{"configuration":{"note":"private configuration"},"pinning_mode":"pinned"}`, "enabled", 200,
			map[string]any{"capability_id": capabilityID, "capability_version_id": v1, "pinning_mode": "pinned", "enabled": true}},
		{http.MethodPost, "/capabilities/" + v1 + "/enable", `{"configuration":{"note":"updated private configuration"},"pinning_mode":"latest"}`, "enabled", 200,
			map[string]any{"capability_id": capabilityID, "capability_version_id": v1, "pinning_mode": "latest", "enabled": true}},
		{http.MethodPost, "/capabilities/" + capabilityID + "/upgrade", `{"new_version_id":"` + v2 + `","pinning_mode":"pinned"}`, "upgraded", 200,
			map[string]any{"capability_id": capabilityID, "capability_version_id": v2, "pinning_mode": "pinned", "enabled": true}},
		{http.MethodPut, "/builtin-capabilities/parsar_chat_history", `{"enabled":false}`, "builtin.updated", 200,
			map[string]any{"builtin_key": "parsar_chat_history", "enabled": false}},
		{http.MethodDelete, "/capabilities/" + v2, "", "removed", 204,
			map[string]any{"capability_reference": v2, "enabled": false}},
	}
	for index, tc := range tests {
		response := serveCapabilityRoute(t, r, tc.method, base+tc.path, tc.body, testUserAID)
		if response.Code != tc.status {
			t.Fatalf("%s: status %d: %s", tc.action, response.Code, response.Body.String())
		}
		flushDevRouteAudit(t, ingester)
		response = serveCapabilityRoute(t, r, http.MethodGet,
			"/api/v1/workspaces/"+workspaceID+"/audit-records?target_type=agent&target_id="+agentID, "", testUserAID)
		var body struct {
			Records []store.AuditRecordRead `json:"audit_records"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK || len(body.Records) != index+1 {
			t.Fatalf("audit response %d: %s", response.Code, response.Body.String())
		}
		event := body.Records[0]
		if event.EventType != "agent.capability."+tc.action || event.ActorType != "user" || event.ActorID != testUserAID || event.TargetID != agentID || event.WorkspaceID != workspaceID || event.OccurredAt.IsZero() {
			t.Fatalf("incorrect audit identity: %+v", event)
		}
		if !reflect.DeepEqual(event.Payload, tc.payload) {
			t.Fatalf("audit payload = %#v, want %#v", event.Payload, tc.payload)
		}
	}
	// Neither a rejected caller nor a failed mutation should claim success.
	response := serveCapabilityRoute(t, r, http.MethodPost, base+"/capabilities/"+v1+"/enable", `{}`, testUserBID)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer enable: %d", response.Code)
	}
	response = serveCapabilityRoute(t, r, http.MethodDelete, base+"/capabilities/"+v2, "", testUserAID)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing binding deletion: %d", response.Code)
	}
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent.capability.enabled", 2)
	assertAuditEventCount(t, db, "agent.capability.removed", 1)
}
