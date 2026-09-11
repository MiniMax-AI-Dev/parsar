package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type SessionStore interface {
	CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error)
	GetSession(context.Context, string, string) (store.Session, error)
	ListSessions(context.Context, string, string, int, bool) (store.SessionPage, error)
}

type Handler struct {
	store  SessionStore
	auth   *Authenticator
	engine string
}

func NewHandler(s SessionStore, auth *Authenticator, engine string) (http.Handler, error) {
	if s == nil || auth == nil || !store.ValidEngine(engine) {
		return nil, errors.New("session store, authentication and a valid execution engine are required")
	}
	h := &Handler{store: s, auth: auth, engine: engine}
	router := chi.NewRouter()
	router.Use(log.HTTPMiddleware)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Route("/v1", func(r chi.Router) {
		r.Use(h.authenticate)
		r.Post("/agents/sessions", h.createSession)
		r.Get("/agents/sessions", h.listSessions)
		r.Get("/agents/sessions/{session_id}", h.getSession)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "unsupported_operation", "This API operation is not supported.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "unsupported_operation", "This API method is not supported.")
		})
	})
	return router, nil
}

type tenantContextKey struct{}

func (h *Handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant, ok := h.auth.tenant(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "A valid Agents API bearer key is required.")
			return
		}
		if r.Header.Get("OpenAI-Beta") != "agents=v1" {
			writeError(w, http.StatusBadRequest, "invalid_beta_header", "OpenAI-Beta: agents=v1 is required.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantContextKey{}, tenant)))
	})
}

func tenantID(r *http.Request) string { return r.Context().Value(tenantContextKey{}).(string) }

// createSession creates an idle execution Session without submitting a Turn.
// @Summary Create an execution Session
// @Description Supports inline model/instructions and environment type none. Execution input and other options are explicitly unsupported in this slice.
// @Tags Sessions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param Idempotency-Key header string false "Creation retry key, up to 128 bytes"
// @Param body body v1.CreateSessionRequest true "Session configuration"
// @Success 200 {object} v1.Session
// @Failure 400,401,409,413,500 {object} v1.ErrorResponse
// @Router /agents/sessions [post]
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session creation does not accept query parameters.")
		return
	}
	var input v1.CreateSessionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 1 MiB.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		}
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain exactly one JSON object.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	configuration, err := resolve(input, tenantID(r), key)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	session, err := h.store.CreateSession(r.Context(), tenantID(r), store.CreateSessionInput{
		Engine: h.engine, IdempotencyKey: key, Metadata: input.Metadata, Configuration: configuration,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSession(w, r, session)
}

// @Summary Retrieve an execution Session
// @Tags Sessions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.Session
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id} [get]
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session retrieval does not accept query parameters.")
		return
	}
	session, err := h.store.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSession(w, r, session)
}

func (h *Handler) respondSession(w http.ResponseWriter, r *http.Request, session store.Session) {
	response, err := sessionResponse(session)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// @Summary List execution Sessions
// @Description Cursor and results are scoped to the authenticated execution tenant. Saved agent filtering is not supported yet.
// @Tags Sessions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Last Session ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SessionList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions [get]
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	for key, values := range q {
		if (key != "after" && key != "limit" && key != "order") || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "unsupported_parameter", "Supported list parameters are after, limit and order, each supplied once.")
			return
		}
	}
	limit, order := 20, q.Get("order")
	if raw, ok := q["limit"]; ok {
		var err error
		limit, err = strconv.Atoi(raw[0])
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 100.")
			return
		}
	}
	if order != "" && order != "asc" && order != "desc" {
		writeError(w, http.StatusBadRequest, "invalid_request", "order must be asc or desc.")
		return
	}
	page, err := h.store.ListSessions(r.Context(), tenantID(r), strings.TrimSpace(q.Get("after")), limit, order == "asc")
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.SessionList{Data: make([]v1.Session, 0, len(page.Sessions)), HasMore: page.NextCursor != ""}
	for _, session := range page.Sessions {
		item, err := sessionResponse(session)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	writeJSON(w, http.StatusOK, response)
}
