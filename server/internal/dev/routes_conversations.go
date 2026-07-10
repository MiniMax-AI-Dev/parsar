package dev

import (
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type configureConversationExternalRefBody struct {
	Gateway          string `json:"gateway"`
	ExternalChatID   string `json:"external_chat_id"`
	ExternalThreadID string `json:"external_thread_id"`
}

// configureConversationExternalRef binds a conversation to an external reference.
//
//	@Summary		Configure a conversation's external reference
//	@Description	Binds a conversation to an external system reference (e.g. Feishu thread key) for dedupe / re-attach.
//	@Tags			conversations
//	@ID				configureDevConversationExternalRef
//	@Accept			json
//	@Produce		json
//	@Param			conversationID	path	string								true	"Conversation UUID"
//	@Param			body			body	configureConversationExternalRefBody	true	"External reference payload"
//	@Success		200 {object} map[string]interface{} "Updated conversation"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		404 {object} map[string]string "Conversation not found"
//	@Router			/api/v1/conversations/{conversationID}/external-ref [post]
func configureConversationExternalRef(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation mapping is disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conversation, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to load conversation")
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, conversation.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req configureConversationExternalRefBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Gateway) == "" {
			req.Gateway = "dev"
		}
		if strings.TrimSpace(req.ExternalChatID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "external_chat_id is required"})
			return
		}
		result, err := runtimeStore.ConfigureDevConversationExternalRef(r.Context(), store.ConfigureDevConversationExternalRefInput{
			ConversationID:   conversationID,
			Gateway:          req.Gateway,
			ExternalChatID:   req.ExternalChatID,
			ExternalThreadID: req.ExternalThreadID,
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownConversation) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure conversation external ref"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// getConversationTimeline returns the ordered timeline for a conversation.
//
//	@Summary		Get a conversation's timeline
//	@Description	Returns the ordered timeline (messages, tool calls, events) for a conversation.
//	@Tags			conversations
//	@ID				getDevConversationTimeline
//	@Produce		json
//	@Param			conversationID	path	string	true	"Conversation UUID"
//	@Success		200 {object} map[string]interface{} "Timeline entries"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller lacks permission"
//	@Failure		404 {object} map[string]string "Conversation not found"
//	@Router			/api/v1/conversations/{conversationID}/timeline [get]
func getConversationTimeline(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		// Timeline doesn't carry workspace_id, so reverse-lookup the
		// parent conversation to find the workspace to authorise against.
		conv, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to load conversation for rbac check")
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, conv.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		timeline, err := runtimeStore.GetConversationTimeline(r.Context(), conversationID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to get conversation timeline")
			return
		}
		writeJSON(w, http.StatusOK, timeline)
	}
}

type createConversationUserMessageBody struct {
	Content           string   `json:"content"`
	MentionedAgentIDs []string `json:"mentioned_agent_ids"`
}

func createConversationUserMessage(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation messages are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		var req createConversationUserMessageBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		content := strings.TrimSpace(req.Content)
		if content == "" || len(content) > 32000 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "content must be 1-32000 characters"})
			return
		}
		conversation, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, conversation.WorkspaceID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.SendUserMessageToConversation(r.Context(), store.SendUserMessageToConversationInput{
			ConversationID:    conversationID,
			UserID:            actorIDFromRequest(r),
			Content:           content,
			MentionedAgentIDs: req.MentionedAgentIDs,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownConversation):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownMention), errors.Is(err, store.ErrInvalidInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				log.Bg().Error("send conversation message failed",
					"error", err,
					"conversation_id", conversationID,
					"user_id", actorIDFromRequest(r))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to send conversation message"})
			}
			return
		}
		agentRunID := any(nil)
		if len(result.RunIDs) > 0 {
			agentRunID = result.RunIDs[0]
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"message":                result.Message,
			"agent_run_id":           agentRunID,
			"dispatched_agent_count": len(result.RunIDs),
		})
	}
}

type createWorkspaceConversationBody struct {
	Title    string         `json:"title"`
	Surface  string         `json:"surface"`
	Form     string         `json:"form"`
	AgentID  string         `json:"agent_id"`
	Metadata map[string]any `json:"metadata"`
}

func listWorkspaceConversations(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
		conversations, err := runtimeStore.ListWorkspaceConversations(r.Context(), workspaceID, agentFilter, parseLimit(r, 100))
		if err != nil {
			if errors.Is(err, store.ErrInvalidInput) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			writeReadError(w, err, "failed to list workspace conversations")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
	}
}

func createWorkspaceConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed conversation creation is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createWorkspaceConversationBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		conversation, err := runtimeStore.CreateWorkspaceConversation(r.Context(), store.CreateWorkspaceConversationInput{
			WorkspaceID:    workspaceID,
			Title:          req.Title,
			Surface:        req.Surface,
			Form:           req.Form,
			PrimaryAgentID: req.AgentID,
			Metadata:       req.Metadata,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownWorkspace):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownMention), errors.Is(err, store.ErrInvalidInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			}
			return
		}
		writeJSON(w, http.StatusCreated, conversation)
	}
}

func getConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conversation, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		// URL is keyed by conversation_id, so load before knowing
		// which workspace to authorise against.
		if err := requireWorkspaceMember(r, runtimeStore, conversation.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, conversation)
	}
}

// updateConversationTitleBody — PATCH /api/v1/conversations/{cid}; only title is editable.
type updateConversationTitleBody struct {
	Title string `json:"title"`
}

func updateConversationTitle(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed write APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		// Load row first so RBAC can gate on the resolved workspace.
		conv, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, conv.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateConversationTitleBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		if err := runtimeStore.UpdateConversationTitle(r.Context(), conversationID, req.Title); err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownConversation):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrInvalidInput):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update conversation"})
			}
			return
		}
		// Re-read so response shape matches GET.
		updated, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to read updated conversation")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func deleteConversation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed write APIs are disabled"})
			return
		}
		conversationID := strings.TrimSpace(chi.URLParam(r, "conversationID"))
		if !isUUID(conversationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation_id must be a valid uuid"})
			return
		}
		conv, err := runtimeStore.GetConversation(r.Context(), conversationID)
		if err != nil {
			writeReadError(w, err, "failed to get conversation")
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, conv.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		if err := runtimeStore.SoftDeleteConversation(r.Context(), conversationID); err != nil {
			if errors.Is(err, store.ErrUnknownConversation) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete conversation"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
