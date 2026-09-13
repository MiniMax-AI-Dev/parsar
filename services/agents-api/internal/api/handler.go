package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ResourceStore interface {
	AgentStore
	ListItems(context.Context, string, string, string, int, bool) (store.ItemPage, error)
	GetTurn(context.Context, string, string, string) (store.Turn, error)
	ListTurns(context.Context, string, string, string, int, bool) (store.TurnPage, error)
	CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error)
	FindSessionCreation(context.Context, string, string, json.RawMessage) (store.SessionCreation, error)
	GetSession(context.Context, string, string) (store.Session, error)
	UpdateSessionMetadata(context.Context, string, string, map[string]string) (store.Session, error)
	ListSessions(context.Context, string, string, int, bool) (store.SessionPage, error)
}

type Handler struct {
	store  ResourceStore
	auth   *Authenticator
	engine string
	inputs InputSubmitter
}

func NewHandler(s ResourceStore, auth *Authenticator, engine string, options ...Option) (http.Handler, error) {
	if s == nil || auth == nil || !store.ValidEngine(engine) {
		return nil, errors.New("resource store, authentication and a valid execution engine are required")
	}
	h := &Handler{store: s, auth: auth, engine: engine}
	for _, option := range options {
		option(h)
	}
	router := chi.NewRouter()
	router.Use(log.HTTPMiddleware)
	router.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	router.Route("/v1", func(r chi.Router) {
		r.Use(h.authenticate)
		r.Post("/agents", h.createAgent)
		r.Get("/agents", h.listAgents)
		r.Get("/agents/{agent_id}", h.getAgent)
		r.Post("/agents/{agent_id}", h.updateAgent)
		r.Delete("/agents/{agent_id}", h.deleteAgent)
		r.Post("/agents/sessions", h.createSession)
		r.Get("/agents/sessions", h.listSessions)
		r.Get("/agents/sessions/{session_id}", h.getSession)
		r.Post("/agents/sessions/{session_id}", h.updateSession)
		r.Post("/agents/sessions/{session_id}/events", h.createEvents)
		r.Get("/agents/sessions/{session_id}/events", h.streamEvents)
		r.Get("/agents/sessions/{session_id}/items", h.listItems)
		r.Get("/agents/sessions/{session_id}/turns", h.listTurns)
		r.Get("/agents/sessions/{session_id}/turns/{turn_id}", h.getTurn)
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

// createSession optionally admits initial text in the same transaction as the Session.
// @Summary Create an execution Session
// @Description Supports inline configuration or a tenant-owned saved agent_id with per-Session field replacements. Execution supports model/instructions, text verbosity, non-deferred function tools, disabled multi_agent, implicit reasoning, service tier auto and environment type none. Omitted stream defaults to false; stream and agent_id cannot be null. Metadata may be null, but its values must be strings. Initial input accepts a string or user-message array containing text and atomically starts a Turn; omitted or null input creates an idle Session. With stream=true, returns live Session events starting at creation; disconnect does not cancel execution. New saved-Agent creation retries retain caller identity independently of later Agent changes; existing records without that identity retain their previous retry rules. Creation retries observe future events without replay; retry with stream=false to retrieve the Session. Non-text initial input remains unsupported.
// @Tags Sessions
// @Accept json
// @Produce json,text/event-stream
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param Idempotency-Key header string false "Creation retry key, up to 128 bytes"
// @Param body body v1.CreateSessionRequest true "Session configuration"
// @Success 200 {object} v1.Session
// @Failure 400,401,404,409,413,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions [post]
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session creation does not accept query parameters.")
		return
	}
	var request decodedSessionRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
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
	input, err := request.validated()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request fields have invalid types or null values.")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	initialInputs, err := initialSessionInputs(input.Input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	creationRequest, err := sessionCreationRequest(input, initialInputs)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
		return
	}
	var saved *v1.SavedAgent
	if input.AgentID != nil {
		resource, err := h.lookupAgent(r.Context(), tenantID(r), *input.AgentID)
		if err != nil {
			if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
				return
			}
			writeStoreError(w, r, err)
			return
		}
		saved = &v1.SavedAgent{ID: resource.ID}
		if err := json.Unmarshal(resource.Configuration, &saved.SavedAgentConfiguration); err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	configuration, err := resolve(input, tenantID(r), key, saved)
	if err != nil {
		if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
			return
		}
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	createInput := store.CreateSessionInput{
		Engine: h.engine, IdempotencyKey: key, Metadata: input.Metadata, Configuration: configuration, InitialInputs: initialInputs, CreationRequest: creationRequest,
	}
	if input.Stream {
		h.createSessionStream(w, r, createInput)
		return
	}
	create := h.store.CreateSession
	if len(initialInputs) > 0 {
		if h.inputs == nil {
			writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution input is not enabled on this service.")
			return
		}
		create = h.inputs.CreateSession
	}
	session, err := create(r.Context(), tenantID(r), createInput)
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
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListSessions(r.Context(), tenantID(r), options.after, options.limit, options.ascending)
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
