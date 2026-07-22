package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type agentInteractionRuntimeStore interface {
	ListWorkspaceAgentInteractions(ctx context.Context, workspaceID, statusGroup string, limit int32) ([]store.AgentInteractionRead, error)
}

type listAgentInteractionsResponse struct {
	Interactions []store.AgentInteractionRead `json:"interactions"`
}

type resolveAgentInteractionBody struct {
	Approved  *bool               `json:"approved,omitempty"`
	Note      string              `json:"note,omitempty"`
	Answers   map[string][]string `json:"answers,omitempty"`
	Cancelled bool                `json:"cancelled,omitempty"`
}

type resolveAgentInteractionResponse struct {
	Interaction     store.AgentInteractionRead `json:"interaction"`
	Applied         bool                       `json:"applied"`
	AlreadyResolved bool                       `json:"already_resolved"`
}

// listAgentInteractions returns durable human requests for the workspace.
//
//	@Summary		List workspace approval and user-question requests
//	@Description	Returns durable permission and AskUserQuestion requests. status accepts pending, decided, or expired.
//	@Tags			interactions
//	@ID				listAgentInteractions
//	@Produce		json
//	@Param			workspaceID	path		string	true	"Workspace UUID"
//	@Param			status		query		string	false	"pending, decided, or expired"
//	@Param			limit		query		int		false	"Maximum rows (1-200)"
//	@Success		200			{object}	listAgentInteractionsResponse
//	@Failure		400			{object}	map[string]string	"Invalid status or limit"
//	@Failure		403			{object}	map[string]string	"Caller is not a workspace member"
//	@Failure		503			{object}	map[string]string	"Interaction store unavailable"
//	@Router			/api/v1/workspaces/{workspaceID}/interactions [get]
func listAgentInteractions(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		interactions, ok := runtimeStore.(agentInteractionRuntimeStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "interaction store is unavailable"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		if status != "" && status != "pending" && status != "decided" && status != "expired" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status must be pending, decided, or expired"})
			return
		}
		limit := parseLimit(r, 100)
		if limit > 200 {
			limit = 200
		}
		rows, err := interactions.ListWorkspaceAgentInteractions(r.Context(), workspaceID, status, limit)
		if err != nil {
			writeReadError(w, err, "failed to list interactions")
			return
		}
		writeJSON(w, http.StatusOK, listAgentInteractionsResponse{Interactions: rows})
	}
}

// resolveAgentInteraction resolves a pending request through the canonical service.
//
//	@Summary		Resolve a pending approval or user question
//	@Description	Permission requests require approved. AskUserQuestion requests require an answers map keyed by stable question ID, or cancelled=true.
//	@Tags			interactions
//	@ID				resolveAgentInteraction
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path		string					true	"Workspace UUID"
//	@Param			interactionID	path		string					true	"Interaction UUID"
//	@Param			body			body		resolveAgentInteractionBody	true	"Human decision"
//	@Success		200				{object}	resolveAgentInteractionResponse
//	@Failure		400				{object}	map[string]string	"Invalid request body"
//	@Failure		403				{object}	map[string]string	"Caller is not a writable workspace member"
//	@Failure		404				{object}	map[string]string	"Interaction not found"
//	@Failure		409				{object}	map[string]string	"Interaction is currently being resolved"
//	@Failure		410				{object}	map[string]string	"Interaction expired or runtime request ended"
//	@Failure		503				{object}	map[string]string	"Runtime temporarily unavailable"
//	@Router			/api/v1/workspaces/{workspaceID}/interactions/{interactionID}/resolve [post]
func resolveAgentInteraction(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil || cfg.interactionService == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "interaction service is unavailable"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		interactionID := strings.TrimSpace(chi.URLParam(r, "interactionID"))
		if !isUUID(interactionID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "interaction_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, workspaceID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		var body resolveAgentInteractionBody
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		answers := make([]interaction.QuestionAnswer, 0, len(body.Answers))
		for questionID, values := range body.Answers {
			answers = append(answers, interaction.QuestionAnswer{QuestionID: questionID, Answers: values})
		}
		actorID := actorIDFromRequest(r)
		result, err := cfg.interactionService.Resolve(r.Context(), interaction.ResolveRequest{
			WorkspaceID: workspaceID, InteractionID: interactionID,
			Actor:    interaction.Actor{UserID: actorID, ActorID: actorID, Source: store.AgentInteractionSourceWeb, ActorType: audit.ActorTypeUser},
			Decision: interaction.Decision{Approved: body.Approved, Note: body.Note, QuestionAnswers: answers, Cancelled: body.Cancelled},
		})
		if err != nil {
			writeInteractionError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, resolveAgentInteractionResponse{
			Interaction: result.Interaction, Applied: result.Applied, AlreadyResolved: result.AlreadyResolved,
		})
	}
}

func writeInteractionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, interaction.ErrInvalidDecision):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, interaction.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "interaction not found"})
	case errors.Is(err, interaction.ErrAlreadyResolving):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, interaction.ErrExpired), errors.Is(err, interaction.ErrRuntimeGone):
		writeJSON(w, http.StatusGone, map[string]string{"error": err.Error()})
	case errors.Is(err, interaction.ErrRuntimeUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "runtime temporarily unavailable"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve interaction"})
	}
}
