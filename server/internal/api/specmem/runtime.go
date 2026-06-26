package specmem

// Runtime-tree handlers — the agent-runtime endpoints the in-sandbox
// `parsar` CLI and Claude/OpenCode hook scripts call. Mounted via
// RegisterRuntimeRoutes behind auth.RunnerCredential; every handler
// assumes auth.RuntimeIdentityFromContext returns a resolved identity.
//
// The runtime identity is the ONLY authorization signal. Workspace,
// owning user, project binding and conversation_id come from the
// pre-resolved sandbox row — handlers MUST NOT accept client-side
// overrides for these (a leaked token could otherwise write to any
// workspace).

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/specmemory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// runtimeIdentity pulls the middleware-injected identity from ctx.
// Missing identity is a wiring bug (RunnerCredential should have
// rejected earlier), so we 500 — operators notice 500 spikes; a silent
// 401 would hide the misconfiguration.
func (h *handler) runtimeIdentity(w http.ResponseWriter, r *http.Request) (store.RuntimeIdentity, bool) {
	id, ok := auth.RuntimeIdentityFromContext(r.Context())
	if !ok {
		if h.deps.Logger != nil {
			h.deps.Logger.Error("specmem: runtime handler invoked without RuntimeIdentity in ctx — RunnerCredential middleware likely missing",
				"path", r.URL.Path)
		}
		writeError(w, http.StatusInternalServerError, "wiring_error",
			"runtime identity missing from context")
		return store.RuntimeIdentity{}, false
	}
	return id, true
}

// requireOwnerUserID extracts the owning user. Spec fragment lookups
// can run without one (workspace-scoped), but every memory path needs
// it. A runtime row missing OwnerUserID is a provisioning bug.
func requireOwnerUserID(w http.ResponseWriter, id store.RuntimeIdentity) (string, bool) {
	uid := derefString(id.OwnerUserID)
	if uid == "" {
		writeError(w, http.StatusInternalServerError, "identity_incomplete",
			"runtime is not bound to an owner user")
		return "", false
	}
	return uid, true
}

// parseRuntimeLimit reads an optional positive-integer limit override.
// Returns 0 ("let the service pick its default") when absent or
// unparseable. Capped at 5000 so a typo can't request a million rows.
// Distinct from parseLimit() in handler.go: the runtime tree wants 0
// to be a valid "no override" signal.
func parseRuntimeLimit(r *http.Request, key string) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	if n > 5000 {
		return 5000
	}
	return int32(n)
}

// ----- snapshot -------------------------------------------------------------

// runtimeSnapshot serves the SessionStart injection bundle. Hook
// scripts call this on platform startup, get back SpecBlock +
// MemoryBlock + MemoryWriteGuide, and stitch them into the system
// prompt. ProjectID comes from identity (empty for workspace-only
// runtimes — those have no project memory bucket).
func (h *handler) runtimeSnapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	userID, ok := requireOwnerUserID(w, id)
	if !ok {
		return
	}
	out, err := h.deps.Service.BuildSnapshot(r.Context(), specmemory.SnapshotInput{
		WorkspaceID: id.WorkspaceID,
		// WorkspaceName is not part of RuntimeIdentity; the renderer
		// accepts the empty string and emits `<spec workspace="">`.
		WorkspaceName:      "",
		UserID:             userID,
		ProjectID:          derefString(id.ProjectID),
		SpecLimit:          parseRuntimeLimit(r, "spec_limit"),
		UserMemoryLimit:    parseRuntimeLimit(r, "user_memory_limit"),
		ProjectMemoryLimit: parseRuntimeLimit(r, "project_memory_limit"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newInjectionDTO(out))
}

// ----- incremental ----------------------------------------------------------

// runtimeIncremental serves the per-turn memory delta. Hooks persist
// the cursor (timestamp of last seen row) and pass it as ?since=
// next time. Empty delta is normal — the hook treats empty
// IncrementalMemory as "skip injection this turn".
func (h *handler) runtimeIncremental(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	userID, ok := requireOwnerUserID(w, id)
	if !ok {
		return
	}
	sinceRaw := urlQuery(r, "since")
	if sinceRaw == "" {
		writeError(w, http.StatusBadRequest, "missing_since",
			"since (RFC3339 timestamp) is required")
		return
	}
	since, err := time.Parse(time.RFC3339, sinceRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_since",
			"since must be RFC3339: "+err.Error())
		return
	}
	out, err := h.deps.Service.BuildIncremental(r.Context(), specmemory.IncrementalInput{
		UserID:    userID,
		ProjectID: derefString(id.ProjectID),
		Since:     since,
		Limit:     parseRuntimeLimit(r, "limit"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "incremental_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newInjectionDTO(out))
}

// ----- spec fragment write-back --------------------------------------------

// runtimeCreateFragmentRequest is the agent-side payload. NO
// workspace_id, source, or actor fields — those come from the runtime
// identity. Accepting them here would let the agent attribute writes to
// another workspace or impersonate a user.
type runtimeCreateFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// runtimeCreateFragment handles `parsar spec add` from inside a sandbox.
// Source is fixed to SourceAgent so the UI can badge agent rows; the
// agent_actor column captures connector + project_agent_id.
func (h *handler) runtimeCreateFragment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	var body runtimeCreateFragmentRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	frag, err := h.deps.Service.CreateSpecFragment(r.Context(), specmemory.CreateSpecFragmentInput{
		WorkspaceID: id.WorkspaceID,
		Title:       body.Title,
		Body:        body.Body,
		Tags:        body.Tags,
		Source:      specmemory.SourceAgent,
		Actor:       agentActor(id),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newFragmentDTO(frag))
}

// ----- memory write-back ----------------------------------------------------

// runtimeCreateMemoryRequest is the agent-side payload. Scope defaults
// to user when absent. ProjectID is NOT accepted — for scope=project
// the project comes from the runtime identity binding so an agent in
// sandbox A can't write to project B's memory bucket by guessing IDs.
type runtimeCreateMemoryRequest struct {
	Scope      string   `json:"scope"`
	MemoryType string   `json:"memory_type"`
	Title      string   `json:"title"`
	Body       string   `json:"body"`
	Why        string   `json:"why"`
	Tags       []string `json:"tags"`
}

// runtimeCreateMemory handles `parsar memory add` from inside a sandbox.
// Source is fixed to SourceAgent; agent_actor identifies the writer;
// conversation_id is sourced from the runtime config.
//
// scope=project requires a project binding; a workspace-only sandbox
// gets 400 (not 500) so the CLI surfaces a clear message.
func (h *handler) runtimeCreateMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	userID, ok := requireOwnerUserID(w, id)
	if !ok {
		return
	}
	var body runtimeCreateMemoryRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	rawScope := body.Scope
	if rawScope == "" {
		rawScope = string(specmemory.ScopeUser)
	}
	scope := specmemory.Scope(rawScope)
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "bad_scope",
			"scope must be user or project")
		return
	}
	mtype := specmemory.MemoryType(body.MemoryType)
	if !mtype.Valid() {
		writeError(w, http.StatusBadRequest, "bad_memory_type",
			"memory_type must be one of: user, feedback, project, reference")
		return
	}
	var projectID string
	if scope == specmemory.ScopeProject {
		projectID = derefString(id.ProjectID)
		if projectID == "" {
			writeError(w, http.StatusBadRequest, "no_project_binding",
				"runtime is not bound to a project; use scope=user")
			return
		}
	}
	mem, err := h.deps.Service.CreateMemory(r.Context(), specmemory.CreateMemoryInput{
		Scope:          scope,
		UserID:         userID,
		ProjectID:      projectID,
		MemoryType:     mtype,
		Title:          body.Title,
		Body:           body.Body,
		Why:            body.Why,
		Tags:           body.Tags,
		Source:         specmemory.SourceAgent,
		ConversationID: derefString(id.ConversationID),
		Actor:          agentActor(id),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newMemoryDTO(mem))
}

// ----- spec fragment list / update / delete --------------------------------

// runtimeListFragments serves `parsar spec list`. Workspace scope comes
// from identity — agents cannot enumerate other workspaces' fragments
// by guessing query params. Hooks usually consume the snapshot endpoint
// instead.
func (h *handler) runtimeListFragments(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	sourceFilter := specmemory.Source(urlQuery(r, "source"))
	if sourceFilter != "" && !sourceFilter.Valid() {
		writeError(w, http.StatusBadRequest, "bad_source",
			"source must be one of: manual, agent, import, user, auto-review")
		return
	}
	rows, err := h.deps.Service.ListWorkspaceSpecFragments(r.Context(), specmemory.ListWorkspaceSpecFragmentsInput{
		WorkspaceID:  id.WorkspaceID,
		SourceFilter: sourceFilter,
		TagFilter:    parseTags(r),
		Limit:        parseRuntimeLimit(r, "limit"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fragments": newFragmentDTOs(rows)})
}

// runtimeUpdateFragmentRequest mirrors the admin updateFragmentRequest.
// The runtime caller can only mutate content fields; the audit actor
// is derived from runtime identity.
type runtimeUpdateFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

// runtimeUpdateFragment handles `parsar spec edit <id>`. The pre-fetch +
// WorkspaceID check is the only thing standing between a leaked agent
// token and a cross-workspace write. 404 (not 403) so workspace
// boundaries aren't enumerable.
func (h *handler) runtimeUpdateFragment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	fragID := chiParam(r, "fragmentID")
	existing, found, err := h.deps.Service.GetSpecFragment(r.Context(), fragID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found || existing.WorkspaceID != id.WorkspaceID {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	var body runtimeUpdateFragmentRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	frag, ok, err := h.deps.Service.UpdateSpecFragment(r.Context(), specmemory.UpdateSpecFragmentInput{
		ID:    fragID,
		Title: body.Title,
		Body:  body.Body,
		Tags:  body.Tags,
		Actor: agentActor(id),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, newFragmentDTO(frag))
}

// runtimeDeleteFragment handles `parsar spec rm <id>` with the same
// cross-workspace check the update path uses.
func (h *handler) runtimeDeleteFragment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	fragID := chiParam(r, "fragmentID")
	existing, found, err := h.deps.Service.GetSpecFragment(r.Context(), fragID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found || existing.WorkspaceID != id.WorkspaceID {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := h.deps.Service.DeleteSpecFragment(r.Context(), specmemory.DeleteSpecFragmentInput{
		ID:    fragID,
		Actor: agentActor(id),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- memory list / update / delete ---------------------------------------

// runtimeListMemories serves `parsar memory list`. Scope is required so
// user / project lists never silently mix. Identity supplies user_id
// (always) and project_id (when scope=project).
func (h *handler) runtimeListMemories(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	userID, ok := requireOwnerUserID(w, id)
	if !ok {
		return
	}
	scope := specmemory.Scope(urlQuery(r, "scope"))
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "bad_scope",
			"scope=user|project required")
		return
	}
	mtype := specmemory.MemoryType(urlQuery(r, "memory_type"))
	if mtype != "" && !mtype.Valid() {
		writeError(w, http.StatusBadRequest, "bad_memory_type",
			"memory_type must be one of: user, feedback, project, reference")
		return
	}
	tags := parseTags(r)
	limit := parseRuntimeLimit(r, "limit")
	switch scope {
	case specmemory.ScopeUser:
		rows, err := h.deps.Service.ListUserMemories(r.Context(), specmemory.ListUserMemoriesInput{
			UserID:           userID,
			MemoryTypeFilter: mtype,
			TagFilter:        tags,
			Limit:            limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": newMemoryDTOs(rows)})
	case specmemory.ScopeProject:
		projectID := derefString(id.ProjectID)
		if projectID == "" {
			writeError(w, http.StatusBadRequest, "no_project_binding",
				"runtime is not bound to a project; use scope=user")
			return
		}
		rows, err := h.deps.Service.ListProjectMemories(r.Context(), specmemory.ListProjectMemoriesInput{
			ProjectID:        projectID,
			MemoryTypeFilter: mtype,
			TagFilter:        tags,
			Limit:            limit,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"memories": newMemoryDTOs(rows)})
	}
}

// runtimeUpdateMemoryRequest is the wire payload. user_id / project_id
// / scope are absent — they're read from the existing row after the
// runtime ownership check. Structural fields are immutable from the
// runtime tree.
type runtimeUpdateMemoryRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Why   string   `json:"why"`
	Tags  []string `json:"tags"`
}

// runtimeUpdateMemory handles `parsar memory edit <id>`. Mirrors
// authorizeMemoryRowAccess but uses runtime identity and never falls
// back to membership lookups.
func (h *handler) runtimeUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	memID := chiParam(r, "memoryID")
	existing, found, err := h.deps.Service.GetMemory(r.Context(), memID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if !runtimeOwnsMemory(id, existing) {
		// 404 (not 403) so cross-user/project IDs aren't enumerable.
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	var body runtimeUpdateMemoryRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	mem, ok, err := h.deps.Service.UpdateMemory(r.Context(), specmemory.UpdateMemoryInput{
		ID:    memID,
		Title: body.Title,
		Body:  body.Body,
		Why:   body.Why,
		Tags:  body.Tags,
		Actor: agentActor(id),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, newMemoryDTO(mem))
}

// runtimeDeleteMemory handles `parsar memory rm <id>` with the same
// ownership gate as update.
func (h *handler) runtimeDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id, ok := h.runtimeIdentity(w, r)
	if !ok {
		return
	}
	memID := chiParam(r, "memoryID")
	existing, found, err := h.deps.Service.GetMemory(r.Context(), memID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if !runtimeOwnsMemory(id, existing) {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := h.deps.Service.DeleteMemory(r.Context(), specmemory.DeleteMemoryInput{
		ID:    memID,
		Actor: agentActor(id),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runtimeOwnsMemory is the runtime-side counterpart of
// authorizeMemoryRowAccess: user-scope rows must match owner_user_id;
// project-scope must match the project binding. Anything else is a
// hard no (surface as 404).
func runtimeOwnsMemory(id store.RuntimeIdentity, mem specmemory.Memory) bool {
	switch mem.Scope {
	case specmemory.ScopeUser:
		uid := derefString(id.OwnerUserID)
		return uid != "" && uid == mem.UserID
	case specmemory.ScopeProject:
		pid := derefString(id.ProjectID)
		return pid != "" && mem.ProjectID == pid
	default:
		return false
	}
}
