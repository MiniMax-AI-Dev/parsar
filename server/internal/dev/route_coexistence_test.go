package dev

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
)

// TestAPIV1RouteCoexistence is the regression guard for the /dev →
// /api/v1 migration. The dev package mounts its business surface as a
// catch-all subrouter via r.Route("/api/v1", ...). In cmd/server that
// same mux ALSO carries:
//
//   - flat routes registered first: /api/v1/health (api.RegisterHealthRoutes),
//     /api/v1/bootstrap{,/status} (bootstrap.RegisterRoutes);
//   - a deeper subrouter registered last:
//     /api/v1/workspaces/{workspaceID}/runtimes (runtimeapi.RegisterAdminRoutes).
//
// Historically this raised two fears: (1) chi panicking when Mounting
// "/api/v1" next to pre-existing flat "/api/v1/..." routes, and (2) the
// dev catch-all swallowing the deeper runtime subrouter (or vice versa).
// This test reproduces main.go's exact registration ORDER and asserts
// both that registration does not panic and that requests dispatch to
// the right place across all three groups.
func TestAPIV1RouteCoexistence(t *testing.T) {
	r := chi.NewRouter()

	// (1) Flat routes first — mirrors api.RegisterHealthRoutes +
	// bootstrap.RegisterRoutes, which run before dev in main.go.
	r.Get("/api/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("health"))
	})
	r.Get("/api/v1/bootstrap/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bootstrap"))
	})

	// (2) The real dev surface under test: /dev fakes + /api/v1 business
	// catch-all. Panics here (e.g. a chi Mount conflict) fail the test.
	RegisterRoutesWithStore(r, stubRuntimeStore{})

	// (3) Deeper runtime-style subrouter registered LAST, exactly as
	// runtimeapi.RegisterAdminRoutes does in main.go.
	r.Route("/api/v1/workspaces/{workspaceID}/runtimes", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("runtimes"))
		})
	})

	const workspaceID = "00000000-0000-0000-0000-000000000002"

	serve := func(method, path string, userID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if userID != "" {
			req = req.WithContext(auth.WithUserID(req.Context(), userID))
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	t.Run("flat health route resolves", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/health", "")
		if rec.Code != http.StatusOK || rec.Body.String() != "health" {
			t.Fatalf("GET /api/v1/health => %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("flat bootstrap route resolves", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/bootstrap/status", "")
		if rec.Code != http.StatusOK || rec.Body.String() != "bootstrap" {
			t.Fatalf("GET /api/v1/bootstrap/status => %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("dev fake stays under /dev", func(t *testing.T) {
		rec := serve(http.MethodGet, "/dev/seed", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /dev/seed => %d (fakes must remain under /dev)", rec.Code)
		}
	})

	t.Run("dev business route dispatches via /api/v1 catch-all", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/me", "00000000-0000-0000-0000-0000000000aa")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/me => %d %q (dev catch-all not reached)", rec.Code, rec.Body.String())
		}
	})

	t.Run("deeper runtime subrouter wins over dev catch-all", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/runtimes", "")
		if rec.Code != http.StatusOK || rec.Body.String() != "runtimes" {
			t.Fatalf("GET /api/v1/workspaces/{id}/runtimes => %d %q (deeper mount shadowed)", rec.Code, rec.Body.String())
		}
	})

	t.Run("sibling dev workspaces route still resolves alongside runtimes mount", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/v1/workspaces/"+workspaceID+"/settings", "00000000-0000-0000-0000-0000000000aa")
		if rec.Code == http.StatusNotFound {
			t.Fatalf("GET /api/v1/workspaces/{id}/settings => 404 (swallowed by runtimes mount)")
		}
	})
}
