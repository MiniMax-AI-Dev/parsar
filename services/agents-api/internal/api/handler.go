package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type ResourceStore interface {
	AgentStore
	EnvironmentTemplateStore
	VaultStore
	CredentialStore
	GetEnvironment(context.Context, string, string) (store.Environment, error)
	ListItems(context.Context, string, string, string, int, bool) (store.ItemPage, error)
	GetTurn(context.Context, string, string, string) (store.Turn, error)
	ListTurns(context.Context, string, string, string, int, bool) (store.TurnPage, error)
	CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error)
	FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (store.SessionCreation, error)
	GetSession(context.Context, string, string) (store.Session, error)
	DeleteSession(context.Context, string, string) error
	UpdateSessionMetadata(context.Context, string, string, map[string]string) (store.Session, error)
	ListSessions(context.Context, string, string, int, bool, *string) (store.SessionPage, error)
}

type Handler struct {
	policy             execution.Policy
	store              ResourceStore
	auth               *Authenticator
	engine             string
	inputs             InputSubmitter
	executorURL        string
	hostedEnvironments bool
	directoryReader    EnvironmentDirectoryReader
	fileWriter         EnvironmentFileWriter
	sourceFiles        SourceFileStore
	artifacts          SessionArtifactStore
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
	router.Group(func(r chi.Router) {
		r.Use(h.authenticateProject)
		r.Post("/v1/files", h.createSourceFile)
		r.Get("/v1/files", h.listSourceFiles)
		r.Get("/v1/files/{file_id}", h.getSourceFile)
		r.Get("/v1/files/{file_id}/content", h.sourceFileContent)
		r.Delete("/v1/files/{file_id}", h.deleteSourceFile)
	})
	router.Route("/v1", func(r chi.Router) {
		r.Use(h.authenticate)
		r.Post("/vaults", h.createVault)
		r.Get("/vaults", h.listVaults)
		r.Get("/vaults/{vault_id}", h.getVault)
		r.Delete("/vaults/{vault_id}", h.deleteVault)
		r.Post("/vaults/{vault_id}/credentials", h.createCredential)
		r.Get("/vaults/{vault_id}/credentials", h.listCredentials)
		r.Get("/vaults/{vault_id}/credentials/{credential_id}", h.getCredential)
		r.Post("/vaults/{vault_id}/credentials/{credential_id}", h.updateCredential)
		r.Delete("/vaults/{vault_id}/credentials/{credential_id}", h.deleteCredential)
		r.Post("/agents", h.createAgent)
		r.Get("/agents", h.listAgents)
		r.Get("/agents/{agent_id}", h.getAgent)
		r.Post("/agents/{agent_id}", h.updateAgent)
		r.Delete("/agents/{agent_id}", h.deleteAgent)
		r.Post("/agents/environments/templates", h.createEnvironmentTemplate)
		r.Get("/agents/environments/templates", h.listEnvironmentTemplates)
		r.Get("/agents/environments/templates/{environment_template_id}", h.getEnvironmentTemplate)
		r.Post("/agents/environments/templates/{environment_template_id}", h.updateEnvironmentTemplate)
		r.Delete("/agents/environments/templates/{environment_template_id}", h.deleteEnvironmentTemplate)
		r.Get("/agents/environments/{environment_id}", h.getEnvironment)
		r.Get("/agents/environments/{environment_id}/files", h.listEnvironmentFiles)
		r.Post("/agents/environments/{environment_id}/files", h.createEnvironmentFile)
		r.Post("/agents/sessions", h.createSession)
		r.Get("/agents/sessions", h.listSessions)
		r.Get("/agents/sessions/{session_id}", h.getSession)
		r.Post("/agents/sessions/{session_id}", h.updateSession)
		r.Delete("/agents/sessions/{session_id}", h.deleteSession)
		r.Post("/agents/sessions/{session_id}/events", h.createEvents)
		r.Get("/agents/sessions/{session_id}/events", h.streamEvents)
		r.Get("/agents/sessions/{session_id}/items", h.listItems)
		r.Get("/agents/sessions/{session_id}/turns", h.listTurns)
		r.Get("/agents/sessions/{session_id}/turns/{turn_id}", h.getTurn)
		r.Get("/agents/sessions/{session_id}/artifacts", h.listSessionArtifacts)
		r.Get("/agents/sessions/{session_id}/artifacts/{artifact_id}", h.getSessionArtifact)
		r.Get("/agents/sessions/{session_id}/artifacts/{artifact_id}/content", h.sessionArtifactContent)
		r.Delete("/agents/sessions/{session_id}/artifacts/{artifact_id}", h.deleteSessionArtifact)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "unsupported_operation", "This API operation is not supported.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "unsupported_operation", "This API method is not supported.")
		})
	})
	return router, nil
}

// createSession atomically reserves or admits initial text with the Session.
// @Summary Create an execution Session
// @Description Supports inline configuration or a tenant-owned saved agent_id with per-Session field replacements. Execution supports model/instructions, text verbosity, non-deferred function tools, disabled multi_agent, implicit reasoning, service tier auto and environment type none, subject to the configured engine. Codex additionally supports HTTP MCP with explicit service origin, native allowed_tools and boolean required defaulting to false. Session vault_ids attach only project-owned Vaults; credential_id selects an attached static bearer credential for the exact HTTPS URL, while null/omission selects a unique match or remains anonymous. Ambiguous selection rejects creation. Frozen private selections never populate an omitted public credential_id; missing decryption configuration fails dispatch without anonymous fallback. Required initialization uses native startup before the first native Turn, including cold resume, and requires a separately advertised capability; exact hosted creation timing and error parity remain unverified. Other MCP origins and OAuth remain unsupported. The self_hosted profile requires Codex, an absolute workspace_directory and empty capability_directories, with optional non-deferred function tools and HTTP MCP using explicit service origin, optionally authenticated by the attached Vault rules. Remote MCP and remote Bearer authentication each require separately advertised combination support; old peers cannot receive unsupported work. Omitted/null capability_directories use the empty-list default; self_hosted requires configured execution plus executor registry. Claude SDK currently requires medium verbosity and object-root function schemas. It supports anonymous or attached static-bearer service-origin HTTP MCP on none with boolean required and separately advertised MCP/bearer/required runtime support. Required servers must be connected before the first native input is released; pending or failed startup rejects execution. The shared Vault selection and immutable binding rules apply; unsupported native labels/tool names reject before persistence. An attached Vault with no matching credential may remain anonymous; missing keys or failed credential lookup/decryption never fall back to anonymous execution. Omitted stream defaults to false; stream and agent_id cannot be null. Metadata may be null, but its values must be strings. Initial input accepts a string or user-message array containing text. None initial input atomically starts a Turn; self_hosted initial input is reserved while returning its Environment connection target, with execution deferred to native readiness and Session failure on initial timeout. Omitted or null input creates an idle Session. With stream=true, returns live Session events starting at creation; disconnect does not cancel execution. New Sessions retain their authenticated creator; all creation retries require the same typed subject, including across key rotation. Saved-Agent retries and inline requests using Vault attachments or credential references retain caller intent independently of later resource changes; unrelated inline retries preserve resolved/default equivalences. Unknown historical creators reject retries; known creators without recorded intent retain resolved-snapshot retry rules. These conflict policies are local and not verified hosted parity. Creation retries observe future events without replay; retry with stream=false to retrieve the Session. Non-text initial input remains unsupported. Basic Codex and Claude SDK openai_hosted creation requires an explicitly configured managed provider. The Claude workspace profile supports non-deferred function tools with text results alongside native workspace tools; HTTP MCP remains unsupported. Idle Sessions provision automatically; initial provisioning has no caller connection action. Network defaults to enabled; disabled is also supported, while restricted domains and remaining unsupported startup installations are rejected. Confidential env, system/npm/Python packages and ordered setup commands use the shared initialization lifecycle; requested network applies after setup. Initial inline and tenant-owned file_id files freeze encrypted bytes before provisioning, then install through the common Core lifecycle before native execution or live Files access. Referenced files/env/packages/setup overrides are rejected pending semantic verification. Tenant-owned environment_template_id references inherit omitted network and allow only narrowing overrides. Referenced network:null is explicitly unsupported pending semantic verification. Core freezes effective configuration; template updates/deletion do not alter Session snapshots or same-intent creation retries.
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
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 16 MiB.")
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
	if err := h.resolveTemplateEnvironment(r.Context(), tenantID(r), &input); err != nil {
		if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
			writeStoreError(w, r, err)
		}
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
	if err == nil {
		configuration, err = h.bindSessionCredentials(r.Context(), tenantID(r), configuration)
		if err != nil {
			if !h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
				writeStoreError(w, r, err)
			}
			return
		}
	}
	if err == nil {
		err = h.policy.ValidateSessionConfiguration(h.engine, configuration)
	}
	if err != nil {
		if h.recoverSessionCreation(w, r, key, creationRequest, input.Stream) {
			return
		}
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	if input.Environment.Type == "self_hosted" && (h.inputs == nil || h.executorURL == "") {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Self-hosted execution is not configured on this service.")
		return
	}
	if input.Environment.Type == "openai_hosted" && (!h.hostedEnvironments || h.inputs == nil) {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Hosted execution is not configured on this service.")
		return
	}
	createInput := store.CreateSessionInput{
		Creator: sessionCreator(r), InitialFiles: input.initialFiles, Initialization: input.initialization,
		Engine: h.engine, IdempotencyKey: key, Metadata: input.Metadata, Configuration: configuration, InitialInputs: initialInputs, CreationRequest: creationRequest,
	}
	if input.Stream {
		h.createSessionStream(w, r, createInput)
		return
	}
	create := h.store.CreateSession
	if len(initialInputs) > 0 || input.Environment.Type == "openai_hosted" {
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
// @Description Returns supported none, self_hosted and basic openai_hosted Session environments. Self-hosted pending input can require a caller connection before a Turn exists. Hosted initial provisioning remains idle until a Turn starts; connection observations are not native execution readiness.
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
	response, err := sessionResponse(session, h.executorURL)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// @Summary List execution Sessions
// @Description Cursor and results are scoped to the authenticated execution tenant. Optional agent_id matches the immutable root Agent ID, including inline Agents and historical Sessions whose saved source was updated or deleted. Omission lists all Agents. Returns the same Environment and pending-input activity projection as Session retrieval, including self_hosted Sessions.
// @Tags Sessions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id query string false "Root Agent ID whose Sessions to return"
// @Param after query string false "Last Session ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SessionList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions [get]
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r, "agent_id")
	if !ok {
		return
	}
	var agentID *string
	if values, present := r.URL.Query()["agent_id"]; present {
		agentID = &values[0]
	}
	page, err := h.store.ListSessions(r.Context(), tenantID(r), options.after, options.limit, options.ascending, agentID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.SessionList{Data: make([]v1.Session, 0, len(page.Sessions)), HasMore: page.NextCursor != ""}
	for _, session := range page.Sessions {
		item, err := sessionResponse(session, h.executorURL)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	writeJSON(w, http.StatusOK, response)
}
