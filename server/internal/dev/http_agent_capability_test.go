package dev

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestExternalHTTPAgentCannotEnableParsarCapability(t *testing.T) {
	router, db := capabilityTestRouter(t, map[string]string{testUserAID: "member", testUserBID: "viewer"}, nil)
	agentID := insertAgentForOwner(t, db, testUserAID, "external-http")
	if _, err := db.Exec(context.Background(), `UPDATE agents SET connector_type='http' WHERE id=$1`, agentID); err != nil {
		t.Fatal(err)
	}
	_, versionID, _ := insertCapabilityVersions(t, db, "00000000-0000-0000-0000-000000000002", "External capability")
	path := "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/" + agentID + "/capabilities/" + versionID + "/enable"
	response := serveCapabilityRoute(t, router, http.MethodPost, path, `{}`, testUserAID)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "manage their own capabilities") {
		t.Fatalf("external binding response: %d %s", response.Code, response.Body.String())
	}
	response = serveCapabilityRoute(t, router, http.MethodPost, path, `{}`, testUserBID)
	if response.Code != http.StatusForbidden {
		t.Fatalf("viewer response: %d", response.Code)
	}
}
