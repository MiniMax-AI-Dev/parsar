package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestConversationUserMessageRouteWithRealStore(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, ingester := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)

	sendPath := "/api/v1/conversations/" + ids.ConversationID + "/messages"
	res := serveDevRoute(t, r, http.MethodPost, sendPath, `{"content":"please help me out @Product Agent"}`)
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"dispatched_agent_count":1`) || !strings.Contains(res.Body.String(), `"agent_run_id"`) {
		t.Fatalf("admin send expected 201 with one run, got %d: %s", res.Code, res.Body.String())
	}
	assertConversationMessageCounts(t, db, ids.ConversationID, 1, 1)
	flushDevRouteAudit(t, ingester)
	assertAuditEventCount(t, db, "user.message.sent", 1)
	assertAuditActor(t, db, "user.message.sent", ids.UserID)

	memberID := "00000000-0000-0000-0000-000000000101"
	insertConversationMessageUser(t, db, ids, memberID, "member@example.com", "member")
	memberReq := newConversationMessageRequest(http.MethodPost, sendPath, `{"content":"plain member msg"}`, memberID)
	memberRes := serveConversationMessageRequest(r, memberReq)
	if memberRes.Code != http.StatusCreated || !strings.Contains(memberRes.Body.String(), `"agent_run_id":null`) || !strings.Contains(memberRes.Body.String(), `"dispatched_agent_count":0`) {
		t.Fatalf("member send expected 201 without run, got %d: %s", memberRes.Code, memberRes.Body.String())
	}

	outsiderID := "00000000-0000-0000-0000-000000000102"
	insertConversationMessageUser(t, db, ids, outsiderID, "outsider@example.com", "")
	outsiderReq := newConversationMessageRequest(http.MethodPost, sendPath, `{"content":"no access"}`, outsiderID)
	outsiderRes := serveConversationMessageRequest(r, outsiderReq)
	if outsiderRes.Code != http.StatusForbidden {
		t.Fatalf("outsider expected 403, got %d: %s", outsiderRes.Code, outsiderRes.Body.String())
	}

	missing := serveDevRoute(t, r, http.MethodPost, "/api/v1/conversations/00000000-0000-0000-0000-000000099999/messages", `{"content":"hello"}`)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing conversation expected 404, got %d: %s", missing.Code, missing.Body.String())
	}
	empty := serveDevRoute(t, r, http.MethodPost, sendPath, `{"content":""}`)
	if empty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty content expected 422, got %d: %s", empty.Code, empty.Body.String())
	}
	unknownAgent := serveDevRoute(t, r, http.MethodPost, sendPath, `{"content":"override","mentioned_agent_ids":["00000000-0000-0000-0000-000000099999"]}`)
	if unknownAgent.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown mentioned_agent_ids expected 422, got %d: %s", unknownAgent.Code, unknownAgent.Body.String())
	}
}

func TestConversationUserMessageRouteMentionOverride(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)

	path := "/api/v1/conversations/" + ids.ConversationID + "/messages"
	override := serveDevRoute(t, r, http.MethodPost, path, `{"content":"no textual mention","mentioned_agent_ids":["`+ids.ProductAgentID+`"]}`)
	if override.Code != http.StatusCreated || !strings.Contains(override.Body.String(), `"dispatched_agent_count":1`) {
		t.Fatalf("override expected 201 with one run, got %d: %s", override.Code, override.Body.String())
	}
}

// TestConversationUserMessageRouteAgentDaemonReturns201 exercises the
// full chi route so the connector_type dispatch gate, agent CRUD
// acceptance, and handler stay wired together.
//
// agent_daemon agents require a device_id at create time; the test
// seeds a runtimes row + passes AgentConfig to CreateAgent,
// then asserts the chosen device_id round-trips to agents.config —
// the connector reads this JSON key on first prompt to lazy-bind the
// conversation.
func TestConversationUserMessageRouteAgentDaemonReturns201(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	ids := store.DefaultDevFixtureIDs()
	s, _ := newDevRouteAuditStore(t, db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	// Seed an agent_daemon runtime in the dev fixture workspace. This
	// has to exist BEFORE CreateAgent because the new store-side
	// validation does a GetRuntime(deviceID) + type/workspace check.
	deviceID := seedAgentDaemonRuntimeDevRoute(t, db, ids.WorkspaceID, "daemon-route-probe-runtime")

	// CreateAgent(agent_daemon) with a real device → post-fix this must
	// succeed and the persisted agents.config must omit runtime so
	// dispatch.go:108 takes the no-op branch (we re-verify below).
	created, err := s.CreateAgent(ctx, store.CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Daemon Route Probe",
		Slug:          "daemon-route-probe",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"device_id":   deviceID,
			"daemon_mode": "local",
			"agent_kind":  "claude_code",
		},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent(agent_daemon, device_id=%q): %v", deviceID, err)
	}

	// New persistence pin: device_id must land on agents.config
	// so connector.streamPrompt's configuredDeviceBinding path can
	// pick it up on first prompt. If this regresses, the dispatch
	// pipeline still queues runs but the daemon never gets called.
	var storedDeviceID string
	if err := db.QueryRow(ctx, `select config->>'device_id' from agents where id = $1::uuid`, created.Agent.ID).Scan(&storedDeviceID); err != nil {
		t.Fatal(err)
	}
	if storedDeviceID != deviceID {
		t.Fatalf("agents.config->>'device_id' = %q, want %q", storedDeviceID, deviceID)
	}

	conv, err := s.CreateWorkspaceConversation(ctx, store.CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "daemon route",
		PrimaryAgentID: created.Agent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)
	path := "/api/v1/conversations/" + conv.ID + "/messages"
	res := serveDevRoute(t, r, http.MethodPost, path, `{"content":"hi daemon"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("daemon-primary POST expected 201, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), `"dispatched_agent_count":1`) || strings.Contains(res.Body.String(), `"agent_run_id":null`) {
		t.Fatalf("daemon-primary POST should return a non-null agent_run_id with dispatched_agent_count=1, got %s", res.Body.String())
	}

	// The created run must NOT be bound through the retired local runtime path — that
	// binding was written by the old local runtime dispatch path
	// and what produced the original 500 when applied to agent_daemon.
	var boundRuntimeID *string
	if err := db.QueryRow(ctx, `select runtime_id::text from agent_runs where conversation_id = $1::uuid limit 1`, conv.ID).Scan(&boundRuntimeID); err != nil {
		t.Fatal(err)
	}
	if boundRuntimeID != nil {
		t.Fatalf("agent_daemon run must not get a retired local runtime binding, got runtime_id=%q", *boundRuntimeID)
	}
}

// seedAgentDaemonRuntimeDevRoute mirrors the helper in store_test.go
// but lives here so the dev package's integration tests don't have to
// reach into another package's test types. Inserts a minimal
// liveness='offline' runtimes row of type='agent_daemon' and returns
// its id. Pairing-token columns left null on purpose — the new
// CreateAgent validation only checks (id, type, workspace_id), not
// pairing state.
func seedAgentDaemonRuntimeDevRoute(t *testing.T, db *pgxpool.Pool, workspaceID, name string) string {
	t.Helper()
	now := time.Now().UTC()
	var id string
	if err := db.QueryRow(context.Background(),
		`insert into runtimes(id, workspace_id, type, name, liveness, provider, version, hostname, config, created_at, updated_at)
		 values (gen_random_uuid(), $1::uuid, 'agent_daemon', $2, 'offline', 'agent_daemon', '', '', '{}'::jsonb, $3, $3)
		 returning id::text`,
		workspaceID, name, now,
	).Scan(&id); err != nil {
		t.Fatalf("seed agent_daemon runtime: %v", err)
	}
	return id
}

func insertConversationMessageUser(t *testing.T, db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, ids store.DevFixtureIDs, userID string, email string, wsRole string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := db.Exec(context.Background(), `insert into users(id, email, name, status, created_at, updated_at) values ($1, $2, $3, 'active', $4, $4)`, userID, email, email, now); err != nil {
		t.Fatal(err)
	}
	if wsRole != "" {
		if _, err := db.Exec(context.Background(), `insert into workspace_members(id, workspace_id, user_id, role, created_at, updated_at) values (gen_random_uuid(), $1, $2, $3, $4, $4)`, ids.WorkspaceID, userID, wsRole, now); err != nil {
			t.Fatal(err)
		}
	}
}

func newConversationMessageRequest(method, path, body, userID string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.WithUserID(req.Context(), userID))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func serveConversationMessageRequest(r http.Handler, req *http.Request) *httptest.ResponseRecorder {
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func assertConversationMessageCounts(t *testing.T, db *pgxpool.Pool, conversationID string, wantMessages int, wantRuns int) {
	t.Helper()
	var messages, runs int
	if err := db.QueryRow(context.Background(), `select count(*) from messages where conversation_id = $1`, conversationID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `select count(*) from agent_runs where conversation_id = $1`, conversationID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if messages != wantMessages || runs != wantRuns {
		t.Fatalf("conversation counts messages/runs = %d/%d, want %d/%d", messages, runs, wantMessages, wantRuns)
	}
}
