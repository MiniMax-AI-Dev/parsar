package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
	res := serveDevRoute(t, r, http.MethodPost, sendPath, `{"content":"please help me out","mentioned_agent_ids":["`+ids.ProductAgentID+`"]}`)
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

// Core-backed Agents enqueue through the same authorized product route.
func TestConversationUserMessageRouteCoreReturns201(t *testing.T) {
	db := openDevRouteTestDB(t)
	ctx := context.Background()
	s, _ := newDevRouteAuditStore(t, db)
	ids := store.DefaultDevFixtureIDs()
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateAgent(ctx, store.CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "Core route", ConnectorType: "agents_api", AgentConfig: map[string]any{"model": "test-model"}, CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateWorkspaceConversation(ctx, store.CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, PrimaryAgentID: created.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)
	res := serveDevRoute(t, r, http.MethodPost, "/api/v1/conversations/"+conv.ID+"/messages", `{"content":"hello Core"}`)
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"dispatched_agent_count":1`) {
		t.Fatalf("Core enqueue: %d %s", res.Code, res.Body.String())
	}
	var kind string
	var runtimeID *string
	if err := db.QueryRow(ctx, `select connector_type,runtime_id::text from agent_runs where conversation_id=$1`, conv.ID).Scan(&kind, &runtimeID); err != nil {
		t.Fatal(err)
	}
	if kind != "agents_api" || runtimeID != nil {
		t.Fatalf("unexpected execution binding: %s %v", kind, runtimeID)
	}
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
