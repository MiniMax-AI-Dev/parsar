package bootstrap

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type CreateRequest struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
}

// CreateResponse is the 201 body. SetupComplete signals the
// installer that the bootstrap door has closed and the operator
// should remove PARSAR_BOOTSTRAP_TOKEN from the env.
type CreateResponse struct {
	UserID        string `json:"user_id"`
	UserCreated   bool   `json:"user_created"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	WorkspaceName string `json:"workspace_name"`
	MemberID      string `json:"member_id"`
	SetupComplete bool   `json:"setup_complete"`
}

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// DevAuthLookup reads the live dev_auth flag at request time.
// cmd/server passes a closure backed by config.Auth.DevAuth so this
// package does not import config.
type DevAuthLookup func() bool

// RegisterRoutes mounts /api/v1/bootstrap{,/status} on r. Both
// routes are unauthenticated at the chi level; POST does its own
// token check inside the service. The rest of /api/v1 uses cookie
// sessions, but bootstrap pre-dates any user, so cookie auth would
// be a chicken-and-egg loop.
func RegisterRoutes(r chi.Router, svc *Service, devAuth DevAuthLookup) {
	if devAuth == nil {
		devAuth = func() bool { return false }
	}
	r.Get("/api/v1/bootstrap/status", statusHandler(svc, devAuth))
	r.Post("/api/v1/bootstrap", createHandler(svc))
}

func statusHandler(svc *Service, devAuth DevAuthLookup) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "bootstrap_unavailable", "bootstrap service not wired")
			return
		}
		res, err := svc.Status(r.Context(), devAuth())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "bootstrap_status_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func createHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "bootstrap_unavailable", "bootstrap service not wired")
			return
		}
		token, err := bearerToken(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "bootstrap_unauthorized", err.Error())
			return
		}
		var req CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", "request body is not valid JSON: "+err.Error())
			return
		}
		out, err := svc.Create(r.Context(), token, store.ProvisionFirstOwnerInput{
			Email:         req.Email,
			Name:          req.Name,
			WorkspaceName: req.WorkspaceName,
		})
		if err != nil {
			switch {
			case errors.Is(err, ErrHTTPDisabled):
				writeError(w, http.StatusServiceUnavailable, "bootstrap_http_disabled", err.Error())
			case errors.Is(err, ErrUnauthorized):
				writeError(w, http.StatusUnauthorized, "bootstrap_unauthorized", err.Error())
			case errors.Is(err, ErrInvalidInput):
				writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", err.Error())
			case errors.Is(err, store.ErrBootstrapClosed):
				writeError(w, http.StatusConflict, "bootstrap_closed", err.Error())
			case errors.Is(err, store.ErrInvalidWorkspaceInput):
				// Store-level validation re-asserts handler invariants;
				// if it fires anyway treat as 400, not 500.
				writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "bootstrap_internal_error", err.Error())
			}
			return
		}
		writeJSON(w, http.StatusCreated, CreateResponse{
			UserID:        out.UserID,
			UserCreated:   out.UserCreated,
			WorkspaceID:   out.WorkspaceID,
			WorkspaceSlug: out.WorkspaceSlug,
			WorkspaceName: out.WorkspaceName,
			MemberID:      out.MemberID,
			SetupComplete: true,
		})
	}
}

// bearerToken extracts the token from "Authorization: Bearer ...".
// Surrounding whitespace is trimmed; missing/malformed headers
// return plain-English errors suitable for installer UI surfaces.
func bearerToken(r *http.Request) (string, error) {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if h == "" {
		return "", errMissingAuthHeader
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return "", errMalformedAuthHeader
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix)), nil
}

var (
	errMissingAuthHeader   = errors.New("missing Authorization header (expected: \"Bearer <token>\")")
	errMalformedAuthHeader = errors.New("Authorization header must use the Bearer scheme (expected: \"Bearer <token>\")")
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}
