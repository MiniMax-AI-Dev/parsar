package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

const testInteractionID = "00000000-0000-0000-0000-000000000901"

type stubInteractionResolver struct {
	request interaction.ResolveRequest
	result  interaction.ResolveResult
	err     error
}

func (s *stubInteractionResolver) Resolve(_ context.Context, req interaction.ResolveRequest) (interaction.ResolveResult, error) {
	s.request = req
	return s.result, s.err
}

type interactionListStore struct {
	stubRuntimeStore
	rows        []store.AgentInteractionRead
	statusGroup string
	limit       int32
}

func (s *interactionListStore) ListWorkspaceAgentInteractions(_ context.Context, _ string, statusGroup string, limit int32) ([]store.AgentInteractionRead, error) {
	s.statusGroup = statusGroup
	s.limit = limit
	return s.rows, nil
}

func TestResolveAgentInteractionMapsStableAnswerArrays(t *testing.T) {
	resolver := &stubInteractionResolver{result: interaction.ResolveResult{
		Applied:     true,
		Interaction: store.AgentInteractionRead{ID: testInteractionID, Status: store.AgentInteractionStatusAnswered},
	}}
	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/interactions/{interactionID}/resolve", resolveAgentInteraction(stubRuntimeStore{}, &routerConfig{interactionService: resolver}))
	req := withTestUser(httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/interactions/"+testInteractionID+"/resolve",
		strings.NewReader(`{"answers":{"environment":["staging"],"checks":["unit","integration"]}}`)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if resolver.request.WorkspaceID != testWorkspaceID || resolver.request.InteractionID != testInteractionID {
		t.Fatalf("request scope = %+v", resolver.request)
	}
	got := map[string][]string{}
	for _, answer := range resolver.request.Decision.QuestionAnswers {
		got[answer.QuestionID] = answer.Answers
	}
	if strings.Join(got["environment"], ",") != "staging" || strings.Join(got["checks"], ",") != "unit,integration" {
		t.Fatalf("answers = %+v", got)
	}
	if resolver.request.Actor.Source != store.AgentInteractionSourceWeb || resolver.request.Actor.UserID == "" {
		t.Fatalf("actor = %+v", resolver.request.Actor)
	}
	var body resolveAgentInteractionResponse
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Applied || body.AlreadyResolved {
		t.Fatalf("response = %+v", body)
	}
}

func TestResolveAgentInteractionRejectsUnknownJSONField(t *testing.T) {
	resolver := &stubInteractionResolver{}
	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/interactions/{interactionID}/resolve", resolveAgentInteraction(stubRuntimeStore{}, &routerConfig{interactionService: resolver}))
	req := withTestUser(httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/interactions/"+testInteractionID+"/resolve",
		strings.NewReader(`{"approved":true,"answer":"legacy-string"}`)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if resolver.request.InteractionID != "" {
		t.Fatal("invalid body reached resolver")
	}
}

func TestResolveAgentInteractionRejectsViewerBeforeResolution(t *testing.T) {
	resolver := &stubInteractionResolver{}
	viewerStore := newRoleStubStore(map[string]string{store.DefaultDevFixtureIDs().UserID: "viewer"})
	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/interactions/{interactionID}/resolve", resolveAgentInteraction(viewerStore, &routerConfig{interactionService: resolver}))
	req := withTestUser(httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/interactions/"+testInteractionID+"/resolve",
		strings.NewReader(`{"approved":true}`)))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if resolver.request.InteractionID != "" {
		t.Fatal("viewer decision reached resolver")
	}
}

func TestResolveAgentInteractionMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid", err: ErrWrap(interaction.ErrInvalidDecision), want: http.StatusBadRequest},
		{name: "missing", err: interaction.ErrNotFound, want: http.StatusNotFound},
		{name: "resolving", err: interaction.ErrAlreadyResolving, want: http.StatusConflict},
		{name: "expired", err: interaction.ErrExpired, want: http.StatusGone},
		{name: "runtime gone", err: interaction.ErrRuntimeGone, want: http.StatusGone},
		{name: "offline", err: interaction.ErrRuntimeUnavailable, want: http.StatusServiceUnavailable},
		{name: "unknown", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			writeInteractionError(res, tt.err)
			if res.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", res.Code, tt.want, res.Body.String())
			}
		})
	}
}

func ErrWrap(err error) error { return errors.Join(errors.New("context"), err) }

func TestListAgentInteractionsIsReadOnlyAndBounded(t *testing.T) {
	listStore := &interactionListStore{rows: []store.AgentInteractionRead{{ID: testInteractionID, Status: store.AgentInteractionStatusPending}}}
	r := chi.NewRouter()
	r.Get("/api/v1/workspaces/{workspaceID}/interactions", listAgentInteractions(listStore))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/interactions?status=pending&limit=999", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	if listStore.statusGroup != "pending" || listStore.limit != 200 {
		t.Fatalf("list arguments = %q/%d", listStore.statusGroup, listStore.limit)
	}
}
