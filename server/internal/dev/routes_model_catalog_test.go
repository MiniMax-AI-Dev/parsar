package dev

import (
	"encoding/json"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogRoutesPermissionsAndAgentSelection(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-master-key-test-master-key-")
	db := openDevRouteTestDB(t)
	st, audit := newDevRouteAuditStore(t, db)
	_, err := st.SeedDevFixture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ids := store.DefaultDevFixtureIDs()
	insertCapabilityExtraUser(t, db, testUserAID, "catalog-reader@example.com")
	insertWorkspaceMember(t, db, testUserAID, "member")
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, st)
	path := "/api/v1/workspaces/" + ids.WorkspaceID
	providerBody := `{"name":"Provider","protocol":"responses","base_url":"https://example.com/v1","api_key":"catalog-http-private-canary"}`
	for _, role := range []string{"member", "viewer"} {
		if _, err := db.Exec(t.Context(), "UPDATE workspace_members SET role=$1 WHERE workspace_id=$2 AND user_id=$3", role, ids.WorkspaceID, testUserAID); err != nil {
			t.Fatal(err)
		}
		for _, resource := range []string{"models", "model-providers"} {
			response := serveCapabilityRoute(t, r, "GET", path+"/"+resource, "", testUserAID)
			if response.Code != 200 {
				t.Fatalf("%s read: %d", role, response.Code)
			}
			response = serveCapabilityRoute(t, r, "POST", path+"/"+resource, providerBody, testUserAID)
			if response.Code != 403 {
				t.Fatalf("%s write: %d", role, response.Code)
			}
		}
	}
	response := serveCapabilityRoute(t, r, "POST", path+"/model-providers", providerBody, ids.UserID)
	if response.Code != 201 || strings.Contains(response.Body.String(), "canary") {
		t.Fatalf("provider create: %d %s", response.Code, response.Body)
	}
	var provider struct {
		Provider store.CatalogProvider `json:"provider"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &provider); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"Catalog model","model_key":"exact-model","provider_id":%q}`, provider.Provider.ID)
	response = serveCapabilityRoute(t, r, "POST", path+"/models", body, ids.UserID)
	if response.Code != 201 {
		t.Fatal(response.Body)
	}
	var model struct {
		Model store.CatalogModel `json:"model"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	body = fmt.Sprintf(`{"name":"Catalog Agent","config":{"model_id":%q,"model":"stale","x_agents_core":{"harness":"codex"},"environment":{"type":"openai_hosted"}}}`, model.Model.ID)
	response = serveCapabilityRoute(t, r, "POST", path+"/agents", body, ids.UserID)
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"model":"exact-model"`) || !strings.Contains(response.Body.String(), model.Model.ID) {
		t.Fatalf("agent resolution: %d %s", response.Code, response.Body)
	}
	response = serveCapabilityRoute(t, r, "PATCH", path+"/models/"+model.Model.ID, `{"name":"renamed","model_key":"swapped"}`, ids.UserID)
	if response.Code != 400 {
		t.Fatal("model identity changed", response.Code)
	}
	response = serveCapabilityRoute(t, r, "GET", path+"/model-providers", "", testUserBID)
	if response.Code != 404 {
		t.Fatal("nonmember access", response.Code)
	}
	flushDevRouteAudit(t, audit)
	assertAuditActor(t, db, "model_catalog.provider.created", ids.UserID)
	var unsafe int
	if err := db.QueryRow(t.Context(), "SELECT count(*) FROM audit_records WHERE payload::text LIKE '%catalog-http-private-canary%'").Scan(&unsafe); err != nil || unsafe != 0 {
		t.Fatal("key in audit", err)
	}
}

func TestCatalogRoutesWithoutDatabase(t *testing.T) {
	r := chi.NewRouter()
	registerModelCatalogRoutes(r, nil)
	request := httptest.NewRequest("GET", "/workspaces/00000000-0000-0000-0000-000000000002/models", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != 503 {
		t.Fatalf("missing store: %d", response.Code)
	}
}
