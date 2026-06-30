// Package specmem hosts the HTTP surface for spec fragments + memories.
//
// Two trees share this file set:
//
//   - Admin tree (admin.go) — workspace UI / session-authed CRUD.
//     Behind auth.Middleware.Require; uses auth.UserIDFromContext.
//   - Agent-runtime tree (runtime.go) — bearer-authed endpoints the
//     in-sandbox `parsar` CLI and hook scripts call. Behind
//     auth.RunnerCredential; uses auth.RuntimeIdentityFromContext.
//
// One package because DTOs / validation / audit-actor mapping are
// shared, so drift between user and agent write paths is visible in
// code review.
package specmem

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/specmemory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// MembershipStore is the narrow RBAC surface the admin tree needs.
// *store.Store satisfies it implicitly; tests pass a fake without
// touching pgx.
type MembershipStore interface {
	GetWorkspaceMemberRole(ctx context.Context, workspaceID, userID string) (string, error)
}

// Deps bundles what the handlers need from cmd/server. Logger is
// optional (nil silently drops internal-error logs).
type Deps struct {
	Service    *specmemory.Service
	Membership MembershipStore
	Logger     *slog.Logger
}

type handler struct {
	deps Deps
}

func newHandler(deps Deps) *handler {
	if deps.Service == nil {
		panic("specmem: Service is required")
	}
	return &handler{deps: deps}
}

// RegisterAdminRoutes mounts the workspace-UI tree. Caller MUST wrap
// this with auth.Middleware.Require so auth.UserIDFromContext returns
// the session user. Per-handler workspace/project membership checks
// run inside.
func RegisterAdminRoutes(r chi.Router, deps Deps) {
	if deps.Membership == nil {
		panic("specmem: Membership is required for admin routes")
	}
	h := newHandler(deps)
	r.Route("/api/v1/workspaces/{workspaceID}/spec", func(r chi.Router) {
		r.Route("/fragments", func(r chi.Router) {
			r.Get("/", h.listFragments)
			r.Post("/", h.createFragment)
			r.Patch("/{fragmentID}", h.updateFragment)
			r.Delete("/{fragmentID}", h.deleteFragment)
		})
		r.Post("/import", h.importSpec)
	})
	r.Route("/api/v1/memories", func(r chi.Router) {
		r.Get("/", h.listMemories)
		r.Post("/", h.createMemory)
		r.Patch("/{memoryID}", h.updateMemory)
		r.Delete("/{memoryID}", h.deleteMemory)
	})
}

// RegisterRuntimeRoutes mounts the agent-runtime tree. Caller MUST
// wrap this with auth.RunnerCredential so auth.RuntimeIdentityFromContext
// returns the resolved runtime. Membership checks DON'T run here — the
// bearer credential is the authorization signal.
//
// Scoping rule (enforced inside each handler): all reads/writes are
// silently constrained by the resolved runtime identity. Cross-boundary
// access returns 404, never 403, to prevent ID enumeration.
func RegisterRuntimeRoutes(r chi.Router, deps Deps) {
	h := newHandler(deps)
	r.Route("/api/v1/agent-runtime", func(r chi.Router) {
		r.Get("/injection/snapshot", h.runtimeSnapshot)
		r.Get("/injection/incremental", h.runtimeIncremental)
		r.Route("/spec/fragments", func(r chi.Router) {
			r.Get("/", h.runtimeListFragments)
			r.Post("/", h.runtimeCreateFragment)
			r.Patch("/{fragmentID}", h.runtimeUpdateFragment)
			r.Delete("/{fragmentID}", h.runtimeDeleteFragment)
		})
		r.Route("/memories", func(r chi.Router) {
			r.Get("/", h.runtimeListMemories)
			r.Post("/", h.runtimeCreateMemory)
			r.Patch("/{memoryID}", h.runtimeUpdateMemory)
			r.Delete("/{memoryID}", h.runtimeDeleteMemory)
		})
	})
}

// ----- response helpers -----------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": code, "message": message})
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

// parseLimit clamps ?limit=N into [1,max]; defaults to def when absent
// or unparseable.
func parseLimit(r *http.Request, def, max int) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return int32(def)
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return int32(def)
	}
	if n > max {
		return int32(max)
	}
	return int32(n)
}

// parseTags reads a CSV `?tag=a,b,c` query parameter. Empty / absent
// returns nil so the store treats it as "no filter".
func parseTags(r *http.Request) []string {
	raw := strings.TrimSpace(r.URL.Query().Get("tag"))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ----- RBAC helpers ---------------------------------------------------------

// requireWorkspaceMember checks the session user against workspace_members.
// Any role passes — read-side check. On failure writes the response and
// returns ("", false) so the caller MUST return.
func (h *handler) requireWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
		return "", false
	}
	if _, err := h.deps.Membership.GetWorkspaceMemberRole(r.Context(), workspaceID, userID); err != nil {
		writeError(w, http.StatusForbidden, "not_member", "not a workspace member")
		return "", false
	}
	return userID, true
}

// requireWorkspaceMemberNotViewer is the write twin of
// requireWorkspaceMember — viewer is locked out, owner/admin/member
// allowed.
func (h *handler) requireWorkspaceMemberNotViewer(w http.ResponseWriter, r *http.Request, workspaceID string) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
		return "", false
	}
	role, err := h.deps.Membership.GetWorkspaceMemberRole(r.Context(), workspaceID, userID)
	if err != nil {
		writeError(w, http.StatusForbidden, "not_member", "not a workspace member")
		return "", false
	}
	if role == "viewer" {
		writeError(w, http.StatusForbidden, "viewer_readonly", "viewer is read-only")
		return "", false
	}
	return userID, true
}

// userActor builds the audit Actor for a UI-driven write.
func userActor(userID string) specmemory.Actor {
	return specmemory.Actor{Type: audit.ActorTypeUser, UserID: userID}
}

// agentActor builds the audit Actor for a runtime-credential write.
// Format mirrors what the parsar CLI passes through env vars:
// "connector:agent_id". Falls back to "runtime:<runtime_id>"
// when there's no connector/agent on the runtime config.
func agentActor(id store.RuntimeIdentity) specmemory.Actor {
	connector := derefString(id.ConnectorName)
	aid := derefString(id.AgentID)
	var actor string
	switch {
	case connector != "" && aid != "":
		actor = connector + ":" + aid
	case connector != "":
		actor = connector + ":runtime:" + id.RuntimeID
	default:
		actor = "runtime:" + id.RuntimeID
	}
	return specmemory.Actor{Type: audit.ActorTypeAgent, AgentActor: actor}
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ----- DTOs -----------------------------------------------------------------

// fragmentDTO is the wire view of a spec fragment. Source is exposed
// so the UI can badge "agent" vs "manual" rows.
type fragmentDTO struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Tags        []string  `json:"tags"`
	Source      string    `json:"source"`
	CreatedBy   string    `json:"created_by,omitempty"`
	AgentActor  string    `json:"agent_actor,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func newFragmentDTO(f specmemory.Fragment) fragmentDTO {
	tags := f.Tags
	if tags == nil {
		tags = []string{}
	}
	return fragmentDTO{
		ID:          f.ID,
		WorkspaceID: f.WorkspaceID,
		Title:       f.Title,
		Body:        f.Body,
		Tags:        tags,
		Source:      f.Source.String(),
		CreatedBy:   f.CreatedBy,
		AgentActor:  f.AgentActor,
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}

func newFragmentDTOs(fs []specmemory.Fragment) []fragmentDTO {
	out := make([]fragmentDTO, 0, len(fs))
	for _, f := range fs {
		out = append(out, newFragmentDTO(f))
	}
	return out
}

// memoryDTO is the wire view. Why is exposed because the UI shows it
// inline (especially for feedback/workspace memories).
type memoryDTO struct {
	ID             string    `json:"id"`
	Scope          string    `json:"scope"`
	UserID         string    `json:"user_id"`
	WorkspaceID    string    `json:"workspace_id,omitempty"`
	MemoryType     string    `json:"memory_type"`
	Title          string    `json:"title,omitempty"`
	Body           string    `json:"body"`
	Why            string    `json:"why,omitempty"`
	Tags           []string  `json:"tags"`
	Source         string    `json:"source"`
	AgentActor     string    `json:"agent_actor,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func newMemoryDTO(m specmemory.Memory) memoryDTO {
	tags := m.Tags
	if tags == nil {
		tags = []string{}
	}
	return memoryDTO{
		ID:             m.ID,
		Scope:          m.Scope.String(),
		UserID:         m.UserID,
		WorkspaceID:    m.WorkspaceID,
		MemoryType:     m.MemoryType.String(),
		Title:          m.Title,
		Body:           m.Body,
		Why:            m.Why,
		Tags:           tags,
		Source:         m.Source.String(),
		AgentActor:     m.AgentActor,
		ConversationID: m.ConversationID,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

func newMemoryDTOs(ms []specmemory.Memory) []memoryDTO {
	out := make([]memoryDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, newMemoryDTO(m))
	}
	return out
}

// injectionDTO is the wire view of a built Injection bundle. All four
// fields are always present — empty strings let the hook adapter elide
// blocks without re-parsing.
type injectionDTO struct {
	SpecBlock         string `json:"spec_block"`
	MemoryBlock       string `json:"memory_block"`
	MemoryWriteGuide  string `json:"memory_write_guide"`
	IncrementalMemory string `json:"incremental_memory"`
}

func newInjectionDTO(in specmemory.Injection) injectionDTO {
	return injectionDTO{
		SpecBlock:         in.SpecBlock,
		MemoryBlock:       in.MemoryBlock,
		MemoryWriteGuide:  in.MemoryWriteGuide,
		IncrementalMemory: in.IncrementalMemory,
	}
}

// importedFragmentDTO is the preview/result view for spec import.
type importedFragmentDTO struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func chiParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}

func urlQuery(r *http.Request, key string) string {
	return strings.TrimSpace(r.URL.Query().Get(key))
}
