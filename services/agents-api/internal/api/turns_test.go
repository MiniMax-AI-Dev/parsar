package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type turnReadStore struct {
	SessionStore
	tenant, sessionID, turnID, cursor string
	limit                             int
	ascending                         bool
	session                           store.Session
	turn                              store.Turn
}

func (s *turnReadStore) GetSession(_ context.Context, tenant, id string) (store.Session, error) {
	s.tenant, s.sessionID = tenant, id
	return s.session, nil
}
func (s *turnReadStore) GetTurn(_ context.Context, tenant, session, id string) (store.Turn, error) {
	s.tenant, s.sessionID, s.turnID = tenant, session, id
	return s.turn, nil
}
func (s *turnReadStore) ListTurns(_ context.Context, tenant, session, cursor string, limit int, asc bool) (store.TurnPage, error) {
	s.tenant, s.sessionID, s.cursor, s.limit, s.ascending = tenant, session, cursor, limit, asc
	return store.TurnPage{Turns: []store.Turn{s.turn}, NextCursor: s.turn.ID}, nil
}

func TestTurnRoutesUseAuthenticatedScopeAndSafeProjection(t *testing.T) {
	h, record, tenant := testHandler(t)
	s := &turnReadStore{session: store.Session{Configuration: json.RawMessage(`{"agent":{"id":"agent_snapshot"}}`)}, turn: store.Turn{ID: "turn", SessionID: "session", Status: store.TurnFailed, CreatedAt: time.Unix(1700000000, 999), Outcome: json.RawMessage(`{"error":"Bearer SECRET","done":{"metadata":{"password":"SECRET"}}}`)}}
	record.SessionStore = s
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("/v1/agents/sessions/session/turns/turn")
	if w.Code != 200 || s.tenant != tenant || s.sessionID != "session" || s.turnID != "turn" || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("unsafe response: %d %s", w.Code, w.Body)
	}
	var got v1.Turn
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AgentID != "agent_snapshot" || got.Error == nil || got.Error.Code != "internal_error" || got.CreatedAt != 1700000000 || got.StartedAt != nil || got.CompletedAt != nil || string(got.Usage) != "null" {
		t.Fatalf("bad projection: %+v", got)
	}
	if w := request("/v1/agents/sessions/session/turns?after=last&limit=2&order=asc"); w.Code != 200 || s.cursor != "last" || s.limit != 2 || !s.ascending {
		t.Fatalf("bad list: %d %s", w.Code, w.Body)
	}
	if w := request("/v1/agents/sessions/session/turns"); w.Code != 200 || s.limit != 20 || s.ascending {
		t.Fatalf("bad defaults: %d", w.Code)
	}
	for _, query := range []string{"limit=0", "limit=101", "limit=2&limit=3", "order=random", "agent_id=other"} {
		if w := request("/v1/agents/sessions/session/turns?" + query); w.Code != 400 {
			t.Fatalf("accepted %s: %d", query, w.Code)
		}
	}
	if w := request("/v1/agents/sessions/session/turns/turn?unknown=1"); w.Code != 400 {
		t.Fatalf("accepted retrieve query: %d", w.Code)
	}
	s.session.Configuration = json.RawMessage(`{}`)
	if w := request("/v1/agents/sessions/session/turns/turn"); w.Code != 500 || strings.Contains(w.Body.String(), "snapshot") {
		t.Fatalf("missing identity: %d %s", w.Code, w.Body)
	}
}

func TestTurnProjectionPreservesLifecycle(t *testing.T) {
	session := store.Session{Configuration: json.RawMessage(`{"agent":{"id":"agent_snapshot"}}`)}
	for _, status := range []string{store.TurnQueued, store.TurnInProgress, store.TurnWaiting, store.TurnCompleted, store.TurnFailed, store.TurnCancelled} {
		turn := store.Turn{Status: status, CreatedAt: time.Unix(1700000000, 0), StartedAt: time.Unix(1700000001, 0), CompletedAt: time.Unix(1700000002, 0)}
		got, err := turnResponse(session, turn)
		if err != nil || got.Status != status || *got.StartedAt != 1700000001 || *got.CompletedAt != 1700000002 || (got.Error != nil) != (status == store.TurnFailed) {
			t.Fatalf("lifecycle %s: %+v %v", status, got, err)
		}
	}
}
