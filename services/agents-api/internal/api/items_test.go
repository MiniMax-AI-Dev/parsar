package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type itemReadStore struct {
	SessionStore
	tenant, session, cursor string
	limit                   int
	ascending               bool
}

func (s *itemReadStore) ListItems(_ context.Context, tenant, session, cursor string, limit int, asc bool) (store.ItemPage, error) {
	s.tenant, s.session, s.cursor, s.limit, s.ascending = tenant, session, cursor, limit, asc
	return store.ItemPage{Items: []v1.Item{}, HasMore: false}, nil
}
func TestItemRouteUsesAuthenticationAndSharedPagination(t *testing.T) {
	h, record, tenant := testHandler(t)
	s := &itemReadStore{}
	record.SessionStore = s
	request := func(query, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/agents/sessions/session/items"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("?after=last&limit=2&order=asc", "test-api-key"); w.Code != 200 || s.tenant != tenant || s.session != "session" || s.cursor != "last" || s.limit != 2 || !s.ascending {
		t.Fatal(w.Code, w.Body, s)
	}
	if w := request("", "test-api-key"); w.Code != 200 || s.limit != 20 || s.ascending || w.Body.String() != "{\"data\":[],\"has_more\":false}\n" {
		t.Fatal(w.Code, w.Body, s)
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?order=bad", "?limit=2&limit=3", "?tenant_id=other"} {
		if w := request(q, "test-api-key"); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w := request("", "invalid"); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
