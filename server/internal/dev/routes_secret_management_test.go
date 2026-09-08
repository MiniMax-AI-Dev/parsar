package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type secretManagementCaptureStore struct {
	stubRuntimeStore
	input store.CreateSecretInput
}

func (s *secretManagementCaptureStore) CreateSecret(_ context.Context, input store.CreateSecretInput, _ []byte) (store.SecretRead, error) {
	s.input = input
	return store.SecretRead{ID: "00000000-0000-0000-0000-000000000099"}, nil
}

func TestAgentInlineSecretRecordsManagementWithoutChangingSharing(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-secret-management-key")
	s := &secretManagementCaptureStore{}
	ids := store.DefaultDevFixtureIDs()
	_, ok := materialiseInlineSecrets(context.Background(), s, map[string]any{},
		[]createAgentInlineSecretBody{{Kind: "github_pat", Plaintext: "synthetic"}}, ids.UserID, ids.WorkspaceID)
	if !ok || s.input.ManagementWorkspaceID != ids.WorkspaceID || s.input.WorkspaceID != "" {
		t.Fatal("inline credential must retain its sharing scope and record its management workspace")
	}
}

func TestSecretDisableRequiresItsManagementWorkspace(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", "test-secret-management-key")
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	st := store.New(db)
	ids := store.DefaultDevFixtureIDs()
	_, err := st.InsertDevFixture(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	employee, err := st.AddWorkspaceMember(ctx, store.AddWorkspaceMemberInput{
		WorkspaceID: ids.WorkspaceID, Email: "secret-viewer@example.com", Role: "viewer", Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, st)
	create := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/secrets",
		`{"name":"Shared test credential","kind":"runtime","provider":"test","auth_type":"api_key","payload":{"api_key":"synthetic"}}`, ids.UserID)
	var secret store.SecretRead
	if err := json.Unmarshal(create.Body.Bytes(), &secret); err != nil || create.Code != http.StatusCreated || secret.ManagementWorkspaceID != ids.WorkspaceID {
		t.Fatalf("create secret: %d %s", create.Code, create.Body.String())
	}
	personal, err := st.CreateWorkspace(ctx, store.CreateWorkspaceInput{Name: "Viewer-owned workspace", CreatedBy: employee.Member.UserID, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	disable := func(workspace, user string, want int) {
		t.Helper()
		res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+workspace+"/secrets/"+secret.ID+"/disable", "", user)
		if res.Code != want {
			t.Fatalf("disable: got %d, want %d: %s", res.Code, want, res.Body.String())
		}
	}
	disable(ids.WorkspaceID, employee.Member.UserID, http.StatusForbidden)
	disable(personal.Workspace.ID, employee.Member.UserID, http.StatusNotFound)
	shared, err := st.GetSecretPayload(ctx, personal.Workspace.ID, secret.ID)
	if err != nil || shared.Status != "active" || shared.ManagementWorkspaceID != ids.WorkspaceID {
		t.Fatalf("shared credential changed or unavailable: %v", err)
	}
	list := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+personal.Workspace.ID+"/secrets", "", employee.Member.UserID)
	var listed struct {
		Secrets []store.SecretRead `json:"secrets"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || list.Code != http.StatusOK {
		t.Fatalf("list shared secrets: %d", list.Code)
	}
	found := false
	for _, item := range listed.Secrets {
		if item.ID == secret.ID {
			found = item.ManagementWorkspaceID == ids.WorkspaceID
		}
	}
	if !found {
		t.Fatal("shared credential or its management workspace missing from list")
	}
	disable(ids.WorkspaceID, ids.UserID, http.StatusOK)
	if _, err := db.Exec(ctx, `update secrets set status='active', management_workspace_id=null where id=$1`, secret.ID); err != nil {
		t.Fatal(err)
	}
	disable(ids.WorkspaceID, ids.UserID, http.StatusNotFound)
	legacy, err := st.GetSecretPayload(ctx, ids.WorkspaceID, secret.ID)
	if err != nil || legacy.Status != "active" {
		t.Fatalf("legacy credential should remain usable: %v", err)
	}
}
