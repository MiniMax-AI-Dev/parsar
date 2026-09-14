package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func sessionCreationRequest(input sessionRequest, initial []store.Input) (json.RawMessage, error) {
	if input.AgentID == nil {
		return nil, nil
	}
	return json.Marshal(struct {
		AgentID       string                     `json:"agent_id"`
		Agent         map[string]json.RawMessage `json:"agent,omitempty"`
		Environment   *v1.Environment            `json:"environment"`
		Metadata      map[string]string          `json:"metadata,omitempty"`
		VaultIDs      []string                   `json:"vault_ids,omitempty"`
		InitialInputs []store.Input              `json:"initial_inputs,omitempty"`
	}{*input.AgentID, input.agentFields, input.Environment, input.Metadata, input.VaultIDs, initial})
}

func (h *Handler) recoverSessionCreation(w http.ResponseWriter, r *http.Request, key string, request json.RawMessage, stream bool) bool {
	if len(request) == 0 {
		return false
	}
	result, err := h.store.FindSessionCreation(r.Context(), tenantID(r), key, request, sessionCreator(r))
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		writeStoreError(w, r, err)
		return true
	}
	if stream {
		events, ok := h.store.(eventStore)
		if !ok {
			writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Live events are unavailable.")
			return true
		}
		h.respondSessionCreationStream(w, r, events, result)
	} else {
		session, err := h.store.GetSession(r.Context(), tenantID(r), result.Session.ID)
		if err != nil {
			writeStoreError(w, r, err)
		} else {
			h.respondSession(w, r, session)
		}
	}
	return true
}
