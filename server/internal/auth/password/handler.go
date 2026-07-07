package password

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// Querier is the narrow subset of sqlc.Queries the login handler needs.
// Declared as an interface so tests can stub it without spinning up
// Postgres.
type Querier interface {
	GetPasswordHashByEmail(ctx context.Context, email string) (sqlc.GetPasswordHashByEmailRow, error)
	TouchEmailIdentityLastUsed(ctx context.Context, arg sqlc.TouchEmailIdentityLastUsedParams) error
}

// LoginHandler serves POST /api/v1/auth/login and POST /api/v1/auth/logout.
type LoginHandler struct {
	q        Querier
	sessions auth.SessionStore
	secure   bool
	logger   *slog.Logger
	now      func() time.Time
}

// NewLoginHandler builds a handler.
func NewLoginHandler(q Querier, sessions auth.SessionStore, secure bool, logger *slog.Logger) *LoginHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &LoginHandler{
		q:        q,
		sessions: sessions,
		secure:   secure,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// RegisterRoutes mounts the two endpoints on r. Callers should nest
// login under a rate-limited chi.Group; logout is safe unlimited (it
// only revokes the cookie's own session).
func (h *LoginHandler) RegisterRoutes(r chi.Router) {
	r.Post("/api/v1/auth/login", h.Login)
	r.Post("/api/v1/auth/logout", h.Logout)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Login validates email+password and, on success, issues a session
// cookie. Failures always return a single opaque 401 payload so a
// probe cannot distinguish "unknown email" from "wrong password".
//
//	@Summary	Log in with email and password
//	@ID			login-email-password
//	@Accept		json
//	@Produce	json
//	@Param		body body loginRequest true "credentials"
//	@Success	200 {object} loginResponse
//	@Failure	400 {object} errorResponse
//	@Failure	401 {object} errorResponse
//	@Router		/api/v1/auth/login [post]
func (h *LoginHandler) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "request body is not valid JSON")
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" || req.Password == "" {
		// Still burn dummy bcrypt so the attacker cannot use empty-body
		// probes to distinguish "no email in DB" from "empty request".
		_ = Compare("", req.Password)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	row, err := h.q.GetPasswordHashByEmail(ctx, email)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		h.logger.ErrorContext(ctx, "login: query password", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "login_lookup_failed", "internal error")
		return
	}

	// Compare BEFORE reading row.UserID / row.Status: the empty-hash
	// dummy-bcrypt path preserves timing even when the user is missing
	// or has no email identity bound.
	pwErr := Compare(row.PasswordHash, req.Password)
	if pwErr != nil || row.UserID == "" || row.Status != "active" {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}

	sid, err := h.sessions.Create(ctx, auth.CreateSessionInput{
		UserID:    row.UserID,
		UserAgent: r.UserAgent(),
		IP:        r.RemoteAddr,
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "login: create session", slog.String("err", err.Error()))
		writeError(w, http.StatusInternalServerError, "session_issue_failed", "internal error")
		return
	}
	auth.IssueCookie(w, sid, 0, h.secure)

	// Best-effort last_used_at bump. Failure only logs; the user is
	// already authenticated at this point.
	nowT := h.now()
	if err := h.q.TouchEmailIdentityLastUsed(ctx, sqlc.TouchEmailIdentityLastUsedParams{
		NowStr: nowT.Format(time.RFC3339),
		Now:    pgtype.Timestamptz{Time: nowT, Valid: true},
		Email:  email,
	}); err != nil {
		h.logger.WarnContext(ctx, "login: touch last_used_at", slog.String("err", err.Error()))
	}

	writeJSON(w, http.StatusOK, loginResponse{
		UserID: row.UserID,
		Email:  row.Email,
		Name:   row.Name,
	})
}

// Logout revokes the current session (identified by the cookie) and
// clears the cookie. Idempotent — no cookie -> just clears it.
//
//	@Summary	Log out
//	@ID			logout
//	@Success	204
//	@Router		/api/v1/auth/logout [post]
func (h *LoginHandler) Logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		if rErr := h.sessions.Revoke(ctx, c.Value, h.now()); rErr != nil {
			h.logger.WarnContext(ctx, "logout: revoke session", slog.String("err", rErr.Error()))
		}
	}
	auth.ClearCookie(w, h.secure)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Code: code, Message: msg})
}
