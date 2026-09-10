package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type agentStatusActorStore struct {
	roleStubStore
	agentID, actorID string
	calls            int
}

func (s *agentStatusActorStore) DisableAgent(ctx context.Context, agentID, actorID string) (store.AgentStatusRead, error) {
	s.agentID, s.actorID = agentID, actorID
	s.calls++
	return s.stubRuntimeStore.DisableAgent(ctx, agentID, actorID)
}

func (s *agentStatusActorStore) EnableAgent(ctx context.Context, agentID, actorID string) (store.AgentStatusRead, error) {
	s.agentID, s.actorID = agentID, actorID
	s.calls++
	return s.stubRuntimeStore.EnableAgent(ctx, agentID, actorID)
}

func TestAgentStatusRequestActorAndAuthorization(t *testing.T) {
	const actorID = "00000000-0000-0000-0000-0000000000aa"
	agentID := store.DefaultDevFixtureIDs().BackendAgentID
	for _, action := range []string{"enable", "disable"} {
		for _, tc := range []struct {
			role string
			code int
		}{
			{"owner", http.StatusOK},
			{"admin", http.StatusOK},
			{"member", http.StatusForbidden},
			{"viewer", http.StatusForbidden},
			{"", http.StatusNotFound},
		} {
			t.Run(action+"/"+tc.role, func(t *testing.T) {
				roles := map[string]string{}
				if tc.role != "" {
					roles[actorID] = tc.role
				}
				s := &agentStatusActorStore{roleStubStore: newRoleStubStore(roles)}
				r := chi.NewRouter()
				RegisterRoutesWithStore(r, s)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/"+action, nil)
				req = req.WithContext(auth.WithUserID(req.Context(), actorID))
				res := httptest.NewRecorder()
				r.ServeHTTP(res, req)
				requireStatus(t, res, tc.code)
				if tc.code != http.StatusOK {
					if s.calls != 0 {
						t.Fatal("unauthorized request reached the status mutation")
					}
					return
				}
				if s.calls != 1 || s.agentID != agentID || s.actorID != actorID {
					t.Fatalf("unexpected mutation: calls=%d agent=%s actor=%s", s.calls, s.agentID, s.actorID)
				}
			})
		}
	}
}
