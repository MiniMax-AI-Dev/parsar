package bootstrap

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/password"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type CreateRequest struct {
	Email         string `json:"email"`
	Name          string `json:"name"`
	WorkspaceName string `json:"workspace_name"`
	Password      string `json:"password"`
}

// CreateResponse is the 201 body. SetupComplete tells the installer
// that the bootstrap door has closed for good.
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

// bootstrapPostLimitPerMin caps unauthenticated POST /api/v1/bootstrap
// per client IP. Legitimate use fires this endpoint exactly once per
// install, so the ceiling can be tight; attackers spraying while
// owner_count==0 would otherwise trigger a bcrypt cost=12 burn per
// request (~250 ms of CPU). 20/min is a comfortable margin above
// hand-retry noise (double-click, back button) while capping the
// worst-case CPU spend during the setup window at ~5 s/min/IP.
const bootstrapPostLimitPerMin = 20

// RegisterRoutes mounts /api/v1/bootstrap{,/status} on r. Both routes
// are unauthenticated: status is safe by construction; create gates
// itself on owner_count == 0 in the store layer under a Postgres
// advisory lock, and is additionally rate-limited by IP to bound the
// setup-window DoS surface (bcrypt cost=12 per attempt).
//
// sessions and secure control the auto-login side effect: when the
// caller supplies a password, a fresh session is issued and set as
// the parsar_session cookie so the browser is logged in immediately.
// Pass sessions=nil to disable auto-login (bootstrap CLI path).
func RegisterRoutes(r chi.Router, svc *Service, devAuth DevAuthLookup, sessions auth.SessionStore, secure bool) {
	if devAuth == nil {
		devAuth = func() bool { return false }
	}
	r.Get("/api/v1/bootstrap/status", statusHandler(svc, devAuth))
	// Status is deliberately outside the rate-limit group: the SPA
	// polls it on every mount before deciding whether to render
	// SetupPage, and blocking that would break the setup UI itself.
	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitBy(bootstrapPostLimitPerMin, time.Minute, httprate.KeyByIP))
		r.Post("/api/v1/bootstrap", createHandler(svc, sessions, secure))
	})
}

// statusHandler reports whether the bootstrap door is still open.
// Safe to call without any credential.
//
//	@Summary	Bootstrap status
//	@Description	Reports whether the bootstrap door is still open (no owner exists yet).
//	@Tags		bootstrap
//	@ID			getBootstrapStatus
//	@Produce	json
//	@Success	200 {object} StatusResult
//	@Failure	500 {object} ErrorResponse
//	@Failure	503 {object} ErrorResponse
//	@Router		/api/v1/bootstrap/status [get]
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

// createHandler provisions the first owner user, their workspace, and
// (when password is supplied) their local email/password identity.
// Runs at most once per installation — subsequent calls return 409.
//
// On success with a password, a session cookie is set so the browser
// is authenticated immediately.
//
//	@Summary	Provision first owner
//	@Description	Creates the first user, their workspace, and (if password provided) an email/password identity. Auto-issues a session cookie on success.
//	@Tags		bootstrap
//	@ID			provisionBootstrap
//	@Accept		json
//	@Produce	json
//	@Param		body body CreateRequest true "owner email, password, workspace name"
//	@Success	201 {object} CreateResponse
//	@Failure	400 {object} ErrorResponse
//	@Failure	409 {object} ErrorResponse "bootstrap already closed"
//	@Failure	500 {object} ErrorResponse
//	@Failure	503 {object} ErrorResponse
//	@Router		/api/v1/bootstrap [post]
func createHandler(svc *Service, sessions auth.SessionStore, secure bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeError(w, http.StatusServiceUnavailable, "bootstrap_unavailable", "bootstrap service not wired")
			return
		}
		var req CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", "request body is not valid JSON: "+err.Error())
			return
		}
		in := store.ProvisionFirstOwnerInput{
			Email:         strings.TrimSpace(req.Email),
			Name:          strings.TrimSpace(req.Name),
			WorkspaceName: strings.TrimSpace(req.WorkspaceName),
		}
		if req.Password != "" {
			if err := password.Validate(req.Password); err != nil {
				writeError(w, http.StatusBadRequest, "bootstrap_weak_password", err.Error())
				return
			}
			h, err := password.Hash(req.Password)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "bootstrap_hash_failed", err.Error())
				return
			}
			in.PasswordHash = h
		}
		out, err := svc.Create(r.Context(), in)
		if err != nil {
			switch {
			case errors.Is(err, ErrInvalidInput):
				writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", err.Error())
			case errors.Is(err, store.ErrBootstrapClosed):
				writeError(w, http.StatusConflict, "bootstrap_closed", err.Error())
			case errors.Is(err, store.ErrInvalidWorkspaceInput):
				writeError(w, http.StatusBadRequest, "bootstrap_invalid_input", err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "bootstrap_internal_error", err.Error())
			}
			return
		}
		// Auto-login when we have both a session issuer and a password
		// was set: mint a session and hand the browser the cookie so
		// the user lands directly on the app after registration.
		if sessions != nil && in.PasswordHash != "" {
			sid, sErr := sessions.Create(r.Context(), auth.CreateSessionInput{
				UserID:    out.UserID,
				UserAgent: r.UserAgent(),
				IP:        r.RemoteAddr,
			})
			if sErr == nil {
				auth.IssueCookie(w, sid, 0, secure)
			}
			// Session issue failure is non-fatal: the client can still
			// POST /api/v1/auth/login with the credentials it just used.
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}
