package dev

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"net/http"
	"strings"
	"testing"
)

func TestKnowledgeImportVersionBindingAndPermissions(t *testing.T) {
	ids := store.DefaultDevFixtureIDs()
	router, db := capabilityTestRouter(t, map[string]string{ids.UserID: "admin", testUserAID: "member"}, nil)
	st := store.New(db)
	ctx := context.Background()
	spec := canonical.Spec{SchemaVersion: 1, Kind: canonical.KindKnowledge, Knowledge: &canonical.KnowledgeSpec{Documents: []canonical.KnowledgeDocument{{Name: "policy.md", Content: "THIRD-DAY"}}}}
	body := mustJSON(t, map[string]any{"kind": "knowledge", "name": "QA knowledge", "canonical_spec": spec})
	base := "/api/v1/workspaces/" + ids.WorkspaceID + "/capabilities"
	denied := serveCapabilityRoute(t, router, http.MethodPost, base+"/import/commit", body, testUserAID)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("member write: %d", denied.Code)
	}
	created := serveCapabilityRoute(t, router, http.MethodPost, base+"/import/commit", body, ids.UserID)
	if created.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", created.Code, created.Body.String())
	}
	var imported commitCapabilityImportResponse
	if err := json.Unmarshal(created.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Capability.Type != "knowledge" {
		t.Fatal("wrong type persisted")
	}
	listed := serveCapabilityRoute(t, router, http.MethodGet, base+"?type=knowledge", "", ids.UserID)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), imported.Capability.ID) {
		t.Fatalf("knowledge not listed: %s", listed.Body.String())
	}
	agent, err := st.CreateAgent(ctx, store.CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "Knowledge reader", ConnectorType: "agent_daemon", CreatedBy: ids.UserID, InitialCapabilities: []store.InitialAgentCapabilityInput{{CapabilityVersionID: imported.CapabilityVersion.ID, PinningMode: store.PinningModeLatest}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateAgent(ctx, store.CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "No knowledge", ConnectorType: "agent_daemon", CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	spec.Knowledge.Documents[0].Content = "FOURTH-DAY"
	versioned := serveCapabilityRoute(t, router, http.MethodPost, base+"/"+imported.Capability.ID+"/versions/import/commit", mustJSON(t, map[string]any{"canonical_spec": spec}), ids.UserID)
	if versioned.Code != http.StatusCreated {
		t.Fatalf("version %d: %s", versioned.Code, versioned.Body.String())
	}
	rows, err := st.GetEnabledCapabilitiesForAgent(ctx, agent.Agent.ID)
	if err != nil || len(rows) != 1 || !strings.Contains(string(rows[0].LatestCanonicalSpec), "FOURTH-DAY") || !strings.Contains(string(rows[0].CanonicalSpec), "THIRD-DAY") {
		t.Fatalf("version resolution: %v %+v", err, rows)
	}
	absent, err := st.GetEnabledCapabilitiesForAgent(ctx, other.Agent.ID)
	if err != nil || len(absent) != 0 {
		t.Fatal("knowledge leaked to unbound Agent")
	}
	if err := st.DeleteAgentCapability(ctx, agent.Agent.ID, imported.CapabilityVersion.ID); err != nil {
		t.Fatal(err)
	}
	absent, err = st.GetEnabledCapabilitiesForAgent(ctx, agent.Agent.ID)
	if err != nil || len(absent) != 0 {
		t.Fatal("knowledge still bound after removal")
	}
	foreign := serveCapabilityRoute(t, router, http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000099/capabilities/"+imported.Capability.ID, "", testUserAID)
	if foreign.Code != http.StatusForbidden && foreign.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace read: %d", foreign.Code)
	}
}

func TestKnowledgeUnpublishKeepsPrivateRevisionsInSourceWorkspace(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		name := "source workspace"
		if foreign {
			name = "other workspace"
		}
		t.Run(name, func(t *testing.T) {
			ids := store.DefaultDevFixtureIDs()
			router, db := capabilityTestRouter(t, map[string]string{ids.UserID: "admin"}, nil)
			ctx := context.Background()
			st := store.New(db)
			sourceID := ids.WorkspaceID
			if foreign {
				sourceID = "00000000-0000-0000-0000-000000000099"
				insertForeignWorkspace(t, db, sourceID)
			}
			spec := canonical.Spec{SchemaVersion: 1, Kind: canonical.KindKnowledge, Knowledge: &canonical.KnowledgeSpec{Documents: []canonical.KnowledgeDocument{{Name: "policy.md", Content: "PUBLIC-FACT"}}}}
			encoded, _ := json.Marshal(spec)
			created, err := st.CreateCapability(ctx, store.CreateCapabilityInput{WorkspaceID: sourceID, Type: "knowledge", Name: "Shared knowledge", Visibility: "public", CreatorID: ids.UserID, InitialVersion: &store.CreateCapabilityVersionInput{Version: "1.0.0", CanonicalSpec: encoded, CreatorID: ids.UserID}})
			if err != nil {
				t.Fatal(err)
			}
			created, err = st.GetCapability(ctx, created.ID)
			if err != nil {
				t.Fatal(err)
			}
			agentID := insertAgentForOwner(t, db, ids.UserID, "knowledge-privacy-agent")
			base := "/api/v1/workspaces/" + ids.WorkspaceID + "/agents/" + agentID + "/capabilities/"
			enabled := serveCapabilityRoute(t, router, http.MethodPost, base+created.LatestVersionID+"/enable", `{"pinning_mode":"latest"}`, ids.UserID)
			if enabled.Code != http.StatusOK {
				t.Fatalf("enable: %d %s", enabled.Code, enabled.Body.String())
			}
			visibility := "workspace"
			if _, err := st.UpdateCapability(ctx, store.UpdateCapabilityInput{CapabilityID: created.ID, Visibility: &visibility}); err != nil {
				t.Fatal(err)
			}
			spec.Knowledge.Documents[0].Content = "PRIVATE-FACT"
			encoded, _ = json.Marshal(spec)
			if _, err := st.CreateCapabilityVersion(ctx, store.CreateCapabilityVersionInput{CapabilityID: created.ID, Version: "1.0.1", CanonicalSpec: encoded, CreatorID: ids.UserID}); err != nil {
				t.Fatal(err)
			}
			rows, err := st.GetEnabledCapabilitiesForAgent(ctx, agentID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("resolve: %v (%d bindings)", err, len(rows))
			}
			want := "PRIVATE-FACT"
			if foreign {
				want = "PUBLIC-FACT"
			}
			if !strings.Contains(string(rows[0].LatestCanonicalSpec), want) {
				t.Fatalf("latest knowledge must contain %s, got %s", want, rows[0].LatestCanonicalSpec)
			}
		})
	}
}
