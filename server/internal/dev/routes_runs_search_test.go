package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type runSearchCaptureStore struct {
	stubRuntimeStore
	workspaceID, search string
	statuses            []string
	limit, offset       int32
}

func (s *runSearchCaptureStore) ListWorkspaceAgentRuns(_ context.Context, workspaceID string, statuses []string, limit, offset int32, search string) (store.ListWorkspaceAgentRunsResult, error) {
	s.workspaceID, s.statuses, s.limit, s.offset, s.search = workspaceID, statuses, limit, offset, search
	return store.ListWorkspaceAgentRunsResult{Runs: []store.AgentRunBriefRead{}}, nil
}

func TestWorkspaceAgentRunsRoutePassesSearchAndPagination(t *testing.T) {
	s := &runSearchCaptureStore{}
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, s)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/agent-runs?q=%20Backend%25_%20&status=running,queued&limit=20&offset=40", nil)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	requireStatus(t, response, http.StatusOK)
	if s.workspaceID != testWorkspaceID || s.search != "Backend%_" || s.limit != 20 || s.offset != 40 || !reflect.DeepEqual(s.statuses, []string{"running", "queued"}) {
		t.Fatalf("unexpected search arguments: %+v", s)
	}
}
