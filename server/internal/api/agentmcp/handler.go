// Package agentmcp exposes one Agent to authenticated MCP callers, reusing the
// normal conversation dispatcher. MCP credentials never authenticate Web or runtime APIs.
package agentmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Store interface {
	auth.RoleStore
	GetAgent(context.Context, string) (store.AgentSummary, error)
	GetAgentMCPToken(context.Context, string, string) (*store.AgentMCPToken, error)
	PutAgentMCPToken(context.Context, store.AgentMCPIdentity, string, store.AgentMCPToken) error
	DeleteAgentMCPToken(context.Context, store.AgentMCPIdentity) error
	ResolveAgentMCPToken(context.Context, string, time.Time) (store.AgentMCPIdentity, bool, error)
	CreateWorkspaceConversation(context.Context, store.CreateWorkspaceConversationInput) (store.ConversationRead, error)
	SendUserMessageToConversation(context.Context, store.SendUserMessageToConversationInput) (store.SendUserMessageToConversationResult, error)
	GetAgentRun(context.Context, string) (store.AgentRunDetailRead, error)
}

type Handler struct {
	store  Store
	logger *slog.Logger
}

func New(s Store, logger *slog.Logger) *Handler { return &Handler{store: s, logger: logger} }

const basePath = "/api/v1/workspaces/{workspaceID}/agents/{agentID}"

// RegisterAdminRoutes must be mounted behind the normal session middleware.
func (h *Handler) RegisterAdminRoutes(r chi.Router) {
	r.Get(basePath+"/mcp-token", h.tokenStatus)
	r.Post(basePath+"/mcp-token", h.issueToken)
	r.Delete(basePath+"/mcp-token", h.revokeToken)
}

func (h *Handler) RegisterMCPRoutes(r chi.Router) {
	r.Handle(basePath+"/mcp", http.NewCrossOriginProtection().Handler(http.HandlerFunc(h.serveMCP)))
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	status, message := http.StatusInternalServerError, "MCP request failed"
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		status, message = http.StatusUnauthorized, "Authentication required"
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrNotMember):
		status, message = http.StatusForbidden, "Workspace write permission required"
	case errors.Is(err, store.ErrUnknownAgent):
		status, message = http.StatusNotFound, "Agent not found"
	case errors.Is(err, store.ErrInvalidInput):
		status, message = http.StatusBadRequest, "Invalid request"
	default:
		if h.logger != nil {
			h.logger.Error("agent MCP request failed", "error", err)
		}
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) adminIdentity(r *http.Request) (store.AgentMCPIdentity, error) {
	id := store.AgentMCPIdentity{AgentID: chi.URLParam(r, "agentID"), WorkspaceID: chi.URLParam(r, "workspaceID"), UserID: auth.UserIDFromContext(r.Context())}
	if _, err := uuid.Parse(id.AgentID); err != nil {
		return id, store.ErrInvalidInput
	}
	if _, err := uuid.Parse(id.WorkspaceID); err != nil {
		return id, store.ErrInvalidInput
	}
	if err := auth.RequireWorkspaceRole(r.Context(), h.store, id.WorkspaceID, "owner", "admin", "member"); err != nil {
		return id, err
	}
	a, err := h.store.GetAgent(r.Context(), id.AgentID)
	if err != nil {
		return id, err
	}
	if a.WorkspaceID != id.WorkspaceID {
		return id, store.ErrUnknownAgent
	}
	return id, nil
}

// tokenStatus returns only the current user's credential metadata.
// @Summary Get personal Agent MCP credential status
// @Tags agent-mcp
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Success 200 {object} tokenResponse
// @Failure 400,401,403,404,500 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/mcp-token [get]
func (h *Handler) tokenStatus(w http.ResponseWriter, r *http.Request) {
	id, err := h.adminIdentity(r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	t, err := h.store.GetAgentMCPToken(r.Context(), id.AgentID, id.UserID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{Credential: t})
}

type tokenResponse struct {
	Credential *store.AgentMCPToken `json:"credential"`
	Token      string               `json:"token,omitempty"`
}

// issueToken rotates only the current user's Agent-scoped credential.
// @Summary Create or replace a personal Agent MCP credential
// @Description Returns plaintext once, expires in 30 days, and invalidates the previous credential for this user and Agent.
// @Tags agent-mcp
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Success 201 {object} tokenResponse
// @Failure 400,401,403,404,500 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/mcp-token [post]
func (h *Handler) issueToken(w http.ResponseWriter, r *http.Request) {
	id, err := h.adminIdentity(r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		h.writeError(w, err)
		return
	}
	token := "pmcp_" + base64.RawURLEncoding.EncodeToString(b)
	now := time.Now().UTC()
	t := store.AgentMCPToken{CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
	if err := h.store.PutAgentMCPToken(r.Context(), id, tokenHash(token), t); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, tokenResponse{Credential: &t, Token: token})
}

// revokeToken revokes only the current user's Agent-scoped credential.
// @Summary Revoke a personal Agent MCP credential
// @Tags agent-mcp
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Success 204
// @Failure 400,401,403,404,500 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/mcp-token [delete]
func (h *Handler) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := h.adminIdentity(r)
	if err != nil {
		h.writeError(w, err)
		return
	}
	if err := h.store.DeleteAgentMCPToken(r.Context(), id); err != nil {
		h.writeError(w, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// serveMCP authenticates every HTTP request, including tool calls on an existing client connection.
// @Summary Call an Agent through Streamable HTTP MCP
// @Description Bearer credential is personal and scoped to the Agent. Supports initialize, tools/list and tools/call; no Web or runtime credentials are accepted.
// @Tags agent-mcp
// @Accept json
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param agentID path string true "Agent UUID"
// @Param Authorization header string true "Bearer pmcp_ credential"
// @Param body body object true "MCP JSON-RPC request"
// @Success 200 {object} map[string]interface{}
// @Success 202
// @Failure 400,401,403,405,500 {object} map[string]string
// @Router /api/v1/workspaces/{workspaceID}/agents/{agentID}/mcp [post]
func (h *Handler) serveMCP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) != 48 || !strings.HasPrefix(parts[1], "pmcp_") {
		h.writeError(w, auth.ErrUnauthenticated)
		return
	}
	id, found, err := h.store.ResolveAgentMCPToken(r.Context(), tokenHash(parts[1]), time.Now().UTC())
	if err != nil {
		h.writeError(w, err)
		return
	}
	if !found || id.AgentID != chi.URLParam(r, "agentID") || id.WorkspaceID != chi.URLParam(r, "workspaceID") {
		h.writeError(w, auth.ErrUnauthenticated)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return h.server(id) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}).ServeHTTP(w, r)
}
