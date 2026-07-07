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

//	@Summary	Create a workspace spec fragment
//	@Description	Persists a manual (UI-authored) spec fragment for the workspace. Caller must be a workspace member.
//	@Tags		memories
//	@ID			createWorkspaceSpecFragment
//	@Accept		json
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		body body createFragmentRequest true "fragment content"
//	@Success	201 {object} fragmentDTO
//	@Failure	400 {object} map[string]string
//	@Failure	401 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/spec/fragments [post]
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

//	@Summary	List workspace spec fragments
//	@Description	Returns spec fragments visible to the workspace. Optional source/tag filters narrow the list.
//	@Tags		memories
//	@ID			listWorkspaceSpecFragments
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		source query string false "filter by source" Enums(manual, agent, import, user, auto-review)
//	@Param		tag query string false "comma-separated tags"
//	@Param		limit query int false "page size (1-500, default 100)"
//	@Success	200 {object} map[string]interface{}
//	@Failure	400 {object} map[string]string
//	@Failure	401 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/spec/fragments [get]
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

//	@Summary	Update a workspace spec fragment
//	@Description	Mutates a fragment's content. Cross-workspace access is masked as 404 so ids aren't enumerable.
//	@Tags		memories
//	@ID			updateWorkspaceSpecFragment
//	@Accept		json
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		fragmentID path string true "fragment id"
//	@Param		body body updateFragmentRequest true "new content"
//	@Success	200 {object} fragmentDTO
//	@Failure	400 {object} map[string]string
//	@Failure	404 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/spec/fragments/{fragmentID} [patch]
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

//	@Summary	Delete a workspace spec fragment
//	@Description	Deletes a spec fragment. Cross-workspace deletes are masked as 404.
//	@Tags		memories
//	@ID			deleteWorkspaceSpecFragment
//	@Param		workspaceID path string true "workspace id"
//	@Param		fragmentID path string true "fragment id"
//	@Success	204
//	@Failure	404 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/spec/fragments/{fragmentID} [delete]
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

//	@Summary	Import a spec document
//	@Description	Slices a spec document into fragments. confirm=false previews; confirm=true persists every piece with SourceImport.
//	@Tags		memories
//	@ID			importWorkspaceSpec
//	@Accept		json
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		body body importSpecRequest true "spec text and confirm flag"
//	@Success	200 {object} importSpecResponse "preview (confirm=false)"
//	@Success	201 {object} importSpecResponse "persisted (confirm=true)"
//	@Failure	400 {object} map[string]string
//	@Failure	500 {object} map[string]interface{} "partial import (rows persisted before failure)"
//	@Router		/api/v1/workspaces/{workspaceID}/spec/import [post]
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

//	@Summary	Create a memory
//	@Description	Persists a user- or workspace-scope memory. scope=user requires only a session; scope=workspace requires non-viewer workspace membership.
//	@Tags		memories
//	@ID			createMemory
//	@Accept		json
//	@Produce	json
//	@Param		body body createMemoryRequest true "memory payload"
//	@Success	201 {object} memoryDTO
//	@Failure	400 {object} map[string]string
//	@Failure	401 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Router		/api/v1/memories [post]
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

//	@Summary	List memories
//	@Description	Returns memories in the requested scope. scope=user returns the caller's private bucket; scope=workspace requires membership and a workspace_id query param.
//	@Tags		memories
//	@ID			listMemories
//	@Produce	json
//	@Param		scope query string true "memory scope" Enums(user, workspace)
//	@Param		workspace_id query string false "workspace id (required when scope=workspace)"
//	@Param		memory_type query string false "memory type filter" Enums(user, feedback, workspace, reference)
//	@Param		tag query string false "comma-separated tag filter"
//	@Param		limit query int false "page size (1-500, default 100)"
//	@Success	200 {object} map[string]interface{}
//	@Failure	400 {object} map[string]string
//	@Failure	401 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Router		/api/v1/memories [get]
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

//	@Summary	Update a memory
//	@Description	Mutates a memory's content. Cross-owner or cross-workspace access is masked as 404.
//	@Tags		memories
//	@ID			updateMemory
//	@Accept		json
//	@Produce	json
//	@Param		memoryID path string true "memory id"
//	@Param		body body updateMemoryRequest true "new content"
//	@Success	200 {object} memoryDTO
//	@Failure	400 {object} map[string]string
//	@Failure	404 {object} map[string]string
//	@Router		/api/v1/memories/{memoryID} [patch]
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

//	@Summary	Delete a memory
//	@Description	Deletes a memory. Cross-owner or cross-workspace deletes are masked as 404.
//	@Tags		memories
//	@ID			deleteMemory
//	@Param		memoryID path string true "memory id"
//	@Success	204
//	@Failure	404 {object} map[string]string
//	@Router		/api/v1/memories/{memoryID} [delete]
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
