package dev

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func TestMeHandlerReturnsContextUser(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), "00000000-0000-0000-0000-0000000000aa"))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	requireStatus(t, res, http.StatusOK)
	body := res.Body.String()
	if !strings.Contains(body, `"user_id":"00000000-0000-0000-0000-0000000000aa"`) || !strings.Contains(body, `"email":"bob@example.com"`) {
		t.Fatalf("expected context user profile, got %s", body)
	}
	if strings.Contains(body, "is_dev_seed") {
		t.Fatalf("/api/v1/me must not expose is_dev_seed after dev shim cut, got %s", body)
	}
}

func TestMeHandlerMissingContextUserReturnsInternalError(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	requireStatus(t, res, http.StatusInternalServerError)
	if !strings.Contains(res.Body.String(), "authenticated user missing from request context") {
		t.Fatalf("expected missing context user error, got %s", res.Body.String())
	}
}

func TestMeHandlerUnknownUserReturnsInternalError(t *testing.T) {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, unknownUserStore{stubRuntimeStore{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), "00000000-0000-0000-0000-0000000000ff"))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	requireStatus(t, res, http.StatusInternalServerError)
	if !strings.Contains(res.Body.String(), "resolved user does not exist") {
		t.Fatalf("expected resolved user error, got %s", res.Body.String())
	}
}

type unknownUserStore struct {
	stubRuntimeStore
}

func (unknownUserStore) GetUserByID(ctx context.Context, userID string) (store.UserRead, error) {
	return store.UserRead{}, errors.Join(store.ErrUnknownUser, errors.New(userID))
}
