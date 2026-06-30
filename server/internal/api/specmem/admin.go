package specmem

// Admin-tree handlers — workspace-UI spec/memory CRUD. Mounted via
// RegisterAdminRoutes behind auth.Middleware.Require; every handler
// assumes auth.UserIDFromContext returns the session user.

import (
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/specmemory"
)

// ----- spec fragment CRUD ----------------------------------------------------

type createFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

func (h *handler) createFragment(w http.ResponseWriter, r *http.Request) {
	wid := chiParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceMember(w, r, wid)
	if !ok {
		return
	}
	var body createFragmentRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	frag, err := h.deps.Service.CreateSpecFragment(r.Context(), specmemory.CreateSpecFragmentInput{
		WorkspaceID: wid,
		Title:       body.Title,
		Body:        body.Body,
		Tags:        body.Tags,
		Source:      specmemory.SourceManual,
		Actor:       userActor(userID),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newFragmentDTO(frag))
}

func (h *handler) listFragments(w http.ResponseWriter, r *http.Request) {
	wid := chiParam(r, "workspaceID")
	if _, ok := h.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	sourceFilter := specmemory.Source(urlQuery(r, "source"))
	if sourceFilter != "" && !sourceFilter.Valid() {
		writeError(w, http.StatusBadRequest, "bad_source",
			"source must be one of: manual, agent, import, user, auto-review")
		return
	}
	rows, err := h.deps.Service.ListWorkspaceSpecFragments(r.Context(), specmemory.ListWorkspaceSpecFragmentsInput{
		WorkspaceID:  wid,
		SourceFilter: sourceFilter,
		TagFilter:    parseTags(r),
		Limit:        parseLimit(r, 100, 500),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fragments": newFragmentDTOs(rows)})
}

type updateFragmentRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

func (h *handler) updateFragment(w http.ResponseWriter, r *http.Request) {
	wid := chiParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceMember(w, r, wid)
	if !ok {
		return
	}
	fragID := chiParam(r, "fragmentID")
	// Confirm workspace ownership before applying — the service-layer
	// Update trusts the handler and does NOT re-check, so without this
	// a member of workspace A could blind-write a fragment in B.
	existing, found, err := h.deps.Service.GetSpecFragment(r.Context(), fragID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found || existing.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	var body updateFragmentRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	frag, ok, err := h.deps.Service.UpdateSpecFragment(r.Context(), specmemory.UpdateSpecFragmentInput{
		ID:    fragID,
		Title: body.Title,
		Body:  body.Body,
		Tags:  body.Tags,
		Actor: userActor(userID),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "update_failed", err.Error())
		return
	}
	if !ok {
		// Concurrent delete between the pre-check and the update.
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	writeJSON(w, http.StatusOK, newFragmentDTO(frag))
}

func (h *handler) deleteFragment(w http.ResponseWriter, r *http.Request) {
	wid := chiParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceMember(w, r, wid)
	if !ok {
		return
	}
	fragID := chiParam(r, "fragmentID")
	existing, found, err := h.deps.Service.GetSpecFragment(r.Context(), fragID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	if !found || existing.WorkspaceID != wid {
		writeError(w, http.StatusNotFound, "not_found", "")
		return
	}
	if err := h.deps.Service.DeleteSpecFragment(r.Context(), specmemory.DeleteSpecFragmentInput{
		ID:    fragID,
		Actor: userActor(userID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- spec import ---------------------------------------------------------

type importSpecRequest struct {
	Text string `json:"text"`
	// Confirm=false → preview only (no DB writes). Confirm=true →
	// persist every piece with SourceImport.
	Confirm bool `json:"confirm"`
}

type importSpecResponse struct {
	// Fragments are persisted rows (Confirm=true); empty otherwise.
	Fragments []fragmentDTO `json:"fragments"`
	// Pieces is the slicer preview (Confirm=false) so the UI can
	// show titles before the user commits.
	Pieces []importedFragmentDTO `json:"pieces"`
}

func (h *handler) importSpec(w http.ResponseWriter, r *http.Request) {
	wid := chiParam(r, "workspaceID")
	userID, ok := h.requireWorkspaceMember(w, r, wid)
	if !ok {
		return
	}
	var body importSpecRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "empty_text", "text is required")
		return
	}
	if !body.Confirm {
		pieces := h.deps.Service.PreviewImport(body.Text)
		out := make([]importedFragmentDTO, 0, len(pieces))
		for _, p := range pieces {
			out = append(out, importedFragmentDTO{Title: p.Title, Body: p.Body})
		}
		writeJSON(w, http.StatusOK, importSpecResponse{Pieces: out})
		return
	}
	frags, err := h.deps.Service.ConfirmImport(r.Context(), specmemory.ConfirmImportInput{
		WorkspaceID: wid,
		Text:        body.Text,
		Actor:       userActor(userID),
	})
	if err != nil {
		// ConfirmImport returns rows persisted before the failure;
		// surface both so the client can show "imported N before error".
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":     "import_partial",
			"message":   err.Error(),
			"fragments": newFragmentDTOs(frags),
		})
		return
	}
	writeJSON(w, http.StatusCreated, importSpecResponse{Fragments: newFragmentDTOs(frags)})
}

// ----- memory CRUD ---------------------------------------------------------

type createMemoryRequest struct {
	Scope          string   `json:"scope"`
	WorkspaceID    string   `json:"workspace_id"`
	MemoryType     string   `json:"memory_type"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	Why            string   `json:"why"`
	Tags           []string `json:"tags"`
	ConversationID string   `json:"conversation_id"`
}

func (h *handler) createMemory(w http.ResponseWriter, r *http.Request) {
	var body createMemoryRequest
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	scope := specmemory.Scope(body.Scope)
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "bad_scope", "scope must be user or workspace")
		return
	}
	mtype := specmemory.MemoryType(body.MemoryType)
	if !mtype.Valid() {
		writeError(w, http.StatusBadRequest, "bad_memory_type",
			"memory_type must be one of: user, feedback, workspace, reference")
		return
	}
	userID, ok := h.authorizeMemoryWrite(w, r, scope, body.WorkspaceID)
	if !ok {
		return
	}
	mem, err := h.deps.Service.CreateMemory(r.Context(), specmemory.CreateMemoryInput{
		Scope:          scope,
		UserID:         userID,
		WorkspaceID:    body.WorkspaceID,
		MemoryType:     mtype,
		Title:          body.Title,
		Body:           body.Body,
		Why:            body.Why,
		Tags:           body.Tags,
		Source:         specmemory.SourceUser,
		ConversationID: body.ConversationID,
		Actor:          userActor(userID),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newMemoryDTO(mem))
}

func (h *handler) listMemories(w http.ResponseWriter, r *http.Request) {
	scope := specmemory.Scope(urlQuery(r, "scope"))
	if !scope.Valid() {
		writeError(w, http.StatusBadRequest, "bad_scope", "scope=user|workspace required")
		return
	}
	mtype := specmemory.MemoryType(urlQuery(r, "memory_type"))
	if mtype != "" && !mtype.Valid() {
		writeError(w, http.StatusBadRequest, "bad_memory_type",
			"memory_type must be one of: user, feedback, workspace, reference")
		return
	}
	tags := parseTags(r)
	limit := parseLimit(r, 100, 500)
	switch scope {
	case specmemory.ScopeUser:
		userID, ok := requireSession(w, r)
		if !ok {
			return
		}
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
	case specmemory.ScopeWorkspace:
		workspaceID := urlQuery(r, "workspace_id")
		if workspaceID == "" {
			writeError(w, http.StatusBadRequest, "missing_workspace_id",
				"workspace_id required for scope=workspace")
			return
		}
		if _, ok := h.requireWorkspaceMember(w, r, workspaceID); !ok {
			return
		}
		rows, err := h.deps.Service.ListWorkspaceMemories(r.Context(), specmemory.ListWorkspaceMemoriesInput{
			WorkspaceID:      workspaceID,
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

type updateMemoryRequest struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Why   string   `json:"why"`
	Tags  []string `json:"tags"`
}

func (h *handler) updateMemory(w http.ResponseWriter, r *http.Request) {
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
	userID, ok := h.authorizeMemoryRowAccess(w, r, existing)
	if !ok {
		return
	}
	var body updateMemoryRequest
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
		Actor: userActor(userID),
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

func (h *handler) deleteMemory(w http.ResponseWriter, r *http.Request) {
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
	userID, ok := h.authorizeMemoryRowAccess(w, r, existing)
	if !ok {
		return
	}
	if err := h.deps.Service.DeleteMemory(r.Context(), specmemory.DeleteMemoryInput{
		ID:    memID,
		Actor: userActor(userID),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ----- memory authorization helpers ----------------------------------------

// authorizeMemoryWrite gates the create path before we have a row to
// inspect. user → caller must be authenticated; workspace → caller must
// be a workspace member (viewer excluded — writes are not read-only).
func (h *handler) authorizeMemoryWrite(w http.ResponseWriter, r *http.Request, scope specmemory.Scope, workspaceID string) (string, bool) {
	switch scope {
	case specmemory.ScopeUser:
		return requireSession(w, r)
	case specmemory.ScopeWorkspace:
		if workspaceID == "" {
			writeError(w, http.StatusBadRequest, "missing_workspace_id",
				"workspace_id required for scope=workspace")
			return "", false
		}
		return h.requireWorkspaceMemberNotViewer(w, r, workspaceID)
	default:
		writeError(w, http.StatusBadRequest, "bad_scope", "scope must be user or workspace")
		return "", false
	}
}

// authorizeMemoryRowAccess gates write/delete on an existing row.
// user-scope rows are owner-only; workspace-scope require workspace
// membership.
func (h *handler) authorizeMemoryRowAccess(w http.ResponseWriter, r *http.Request, mem specmemory.Memory) (string, bool) {
	switch mem.Scope {
	case specmemory.ScopeUser:
		userID, ok := requireSession(w, r)
		if !ok {
			return "", false
		}
		if userID != mem.UserID {
			// 404 (not 403) so cross-user IDs aren't enumerable.
			writeError(w, http.StatusNotFound, "not_found", "")
			return "", false
		}
		return userID, true
	case specmemory.ScopeWorkspace:
		if mem.WorkspaceID == "" {
			// The CHECK constraint should make this impossible.
			writeError(w, http.StatusInternalServerError, "data_invariant",
				"workspace-scope memory missing workspace_id")
			return "", false
		}
		return h.requireWorkspaceMemberNotViewer(w, r, mem.WorkspaceID)
	default:
		writeError(w, http.StatusInternalServerError, "bad_scope",
			"unknown memory scope "+mem.Scope.String())
		return "", false
	}
}

// requireSession is the bare "is there a session user" gate (no
// membership lookup). Used by user-scope memory paths.
func requireSession(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
		return "", false
	}
	return userID, true
}
