package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestAgentCRUDRoutesWithRealStore(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)

	settingsPatch := serveDevRoute(t, r, http.MethodPatch, "/api/v1/workspaces/"+ids.WorkspaceID+"/settings", `{}`)
	if settingsPatch.Code != http.StatusOK || !strings.Contains(settingsPatch.Body.String(), `"workspace_id"`) {
		t.Fatalf("settings PATCH expected 200, got %d: %s", settingsPatch.Code, settingsPatch.Body.String())
	}
	settingsGet := serveDevRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+ids.WorkspaceID+"/settings", "")
	if settingsGet.Code != http.StatusOK || !strings.Contains(settingsGet.Body.String(), `"workspace_id"`) {
		t.Fatalf("settings GET expected 200, got %d: %s", settingsGet.Code, settingsGet.Body.String())
	}

	createBody := `{"name":"Route Agent","connector_type":"agent_daemon","system_prompt":"be useful","config":{"daemon_mode":"sandbox","agent_kind":"opencode"}}`
	created := serveDevRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/projects/"+ids.ProjectID+"/agents", createBody)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"project_agent"`) {
		t.Fatalf("create expected 201, got %d: %s", created.Code, created.Body.String())
	}
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "agent.created", 1)
	assertAuditEventCount(t, db, "project_agent.attached", 1)
	assertAuditActor(t, db, "agent.created", ids.UserID)

	duplicateAuto := serveDevRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/projects/"+ids.ProjectID+"/agents", createBody)
	if duplicateAuto.Code != http.StatusCreated || !strings.Contains(duplicateAuto.Body.String(), `"slug":"agent-`) {
		t.Fatalf("duplicate auto slug expected 201 with random agent slug, got %d: %s", duplicateAuto.Code, duplicateAuto.Body.String())
	}
	if strings.Contains(duplicateAuto.Body.String(), `"slug":"route-agent`) {
		t.Fatalf("duplicate auto slug should not derive from name, got %d: %s", duplicateAuto.Code, duplicateAuto.Body.String())
	}

	conflictBody := `{"name":"Route Agent Explicit","slug":"backend-agent","connector_type":"agent_daemon","system_prompt":"explicit","config":{"daemon_mode":"sandbox","agent_kind":"opencode"}}`
	conflict := serveDevRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/projects/"+ids.ProjectID+"/agents", conflictBody)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "slug_conflict") {
		t.Fatalf("explicit duplicate slug expected 409 slug_conflict, got %d: %s", conflict.Code, conflict.Body.String())
	}
	agentID, projectAgentID := lookupAgentAndProjectAgent(t, db, "Route Agent")

	updated := serveDevRoute(t, r, http.MethodPatch, "/api/v1/agents/"+agentID, `{"name":"Route Agent Renamed","capabilities":["unknown-capability"]}`)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "Route Agent Renamed") {
		t.Fatalf("update expected 200, got %d: %s", updated.Code, updated.Body.String())
	}

	globalRuntime := serveDevRoute(t, r, http.MethodPatch, "/api/v1/agents/"+agentID, `{"runtime":"local"}`)
	if globalRuntime.Code != http.StatusUnprocessableEntity || !strings.Contains(globalRuntime.Body.String(), "runtime is immutable post-create") {
		t.Fatalf("global PATCH runtime expected 422 immutable, got %d: %s", globalRuntime.Code, globalRuntime.Body.String())
	}
	flushDevRouteAudit(t, ingester)
	immutable := serveDevRoute(t, r, http.MethodPatch, "/api/v1/agents/"+agentID, `{"slug":"changed"}`)
	if immutable.Code != http.StatusUnprocessableEntity || !strings.Contains(immutable.Body.String(), "slug is immutable") {
		t.Fatalf("immutable slug expected 422, got %d: %s", immutable.Code, immutable.Body.String())
	}

	detached := serveDevRoute(t, r, http.MethodDelete, "/api/v1/project-agents/"+projectAgentID, "")
	if detached.Code != http.StatusOK || !strings.Contains(detached.Body.String(), `"project_agent"`) {
		t.Fatalf("project-agent delete expected 200, got %d: %s", detached.Code, detached.Body.String())
	}
	missingDetach := serveDevRoute(t, r, http.MethodDelete, "/api/v1/project-agents/00000000-0000-0000-0000-000000099999", "")
	if missingDetach.Code != http.StatusNotFound {
		t.Fatalf("unknown project-agent delete expected 404, got %d: %s", missingDetach.Code, missingDetach.Body.String())
	}

	created = serveDevRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+ids.WorkspaceID+"/projects/"+ids.ProjectID+"/agents", `{"name":"Delete Guard Agent","connector_type":"agent_daemon","config":{"daemon_mode":"sandbox","agent_kind":"opencode"}}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create delete guard agent expected 201, got %d: %s", created.Code, created.Body.String())
	}
	deleteAgentID, deleteProjectAgentID := lookupAgentAndProjectAgent(t, db, "Delete Guard Agent")
	insertQueuedRun(t, db, ids, deleteProjectAgentID)
	blocked := serveDevRoute(t, r, http.MethodDelete, "/api/v1/agents/"+deleteAgentID, "")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), "in_flight_runs") {
		t.Fatalf("agent delete with in-flight run expected 409, got %d: %s", blocked.Code, blocked.Body.String())
	}
	if _, err := db.Exec(ctx, `update agent_runs set status = 'completed', finished_at = $1 where project_agent_id = $2`, time.Now().UTC(), deleteProjectAgentID); err != nil {
		t.Fatal(err)
	}
	deleted := serveDevRoute(t, r, http.MethodDelete, "/api/v1/agents/"+deleteAgentID, "")
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), "detached_project_agent_ids") {
		t.Fatalf("agent delete expected 200, got %d: %s", deleted.Code, deleted.Body.String())
	}
}

func openDevRouteTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}
	db, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	lockConn, err := db.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConn.Exec(context.Background(), `select pg_advisory_lock(8675309)`); err != nil {
		lockConn.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.Background(), `select pg_advisory_unlock(8675309)`)
		lockConn.Release()
	})
	if _, err := db.Exec(context.Background(), `truncate table sandboxes, agent_run_events, usage_logs, audit_records, agent_run_artifacts, agent_runs, messages, conversations, project_agents, agents, models, secrets, projects, workspace_members, workspaces, auth_identities, users restart identity cascade`); err != nil {
		t.Fatal(err)
	}
	return db
}

func newDevRouteAuditStore(t *testing.T, db *pgxpool.Pool) (*store.Store, *audit.Ingester) {
	t.Helper()
	ingester := audit.NewIngester(audit.NewPostgresSink(sqlc.New(db)), audit.Options{BufferCapacity: 64})
	ingester.Start(context.Background())
	t.Cleanup(func() { _ = ingester.Stop(context.Background()) })
	return store.New(db, store.WithAudit(ingester)), ingester
}

func serveDevRoute(t *testing.T, r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = withTestUser(req)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func flushDevRouteAudit(t *testing.T, ingester *audit.Ingester) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ingester.Flush(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertAuditEventCount(t *testing.T, db *pgxpool.Pool, eventType string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(context.Background(), `select count(*) from audit_records where event_type = $1`, eventType).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("audit %s count = %d, want %d", eventType, got, want)
	}
}

func assertAuditActor(t *testing.T, db *pgxpool.Pool, eventType string, actorID string) {
	t.Helper()
	var actorType, gotActorID string
	if err := db.QueryRow(context.Background(), `select actor_type, actor_id::text from audit_records where event_type = $1 order by occurred_at desc limit 1`, eventType).Scan(&actorType, &gotActorID); err != nil {
		t.Fatal(err)
	}
	if actorType != "user" || gotActorID != actorID {
		t.Fatalf("audit %s actor = %s/%s, want user/%s", eventType, actorType, gotActorID, actorID)
	}
}

func lookupAgentAndProjectAgent(t *testing.T, db *pgxpool.Pool, name string) (string, string) {
	t.Helper()
	var agentID, projectAgentID string
	if err := db.QueryRow(context.Background(), `select a.id::text, pa.id::text from agents a join project_agents pa on pa.agent_id = a.id where a.name = $1 and a.deleted_at is null and pa.deleted_at is null order by a.created_at desc limit 1`, name).Scan(&agentID, &projectAgentID); err != nil {
		t.Fatal(err)
	}
	return agentID, projectAgentID
}

func insertQueuedRun(t *testing.T, db *pgxpool.Pool, ids store.DevFixtureIDs, projectAgentID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(context.Background(), `insert into agent_runs(id, workspace_id, project_id, conversation_id, trigger_source, trigger_channel, requested_by_type, requested_by_id, project_agent_id, connector_type, status, visibility, metadata, created_at, updated_at) values ('00000000-0000-0000-0000-000000009999', $1, $2, $3, 'manual', 'web', 'user', $4, $5, 'agent_daemon', 'queued', 'project', '{}', $6, $6)`, ids.WorkspaceID, ids.ProjectID, ids.ConversationID, ids.UserID, projectAgentID, now); err != nil {
		t.Fatal(err)
	}
}
