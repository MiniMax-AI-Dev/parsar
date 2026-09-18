package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
)

func (h *Handler) setEnvironmentInputWriteDeadline(w http.ResponseWriter, r *http.Request, sessionID string) error {
	session, err := h.store.GetSession(r.Context(), tenantID(r), sessionID)
	if err != nil {
		return err
	}
	var snapshot configuration
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		return err
	}
	if snapshot.Environment.Type != "self_hosted" && snapshot.Environment.Type != "openai_hosted" {
		return nil
	}
	// This read selects only the HTTP budget; admission and its deadline remain under the Session lock.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(6 * time.Minute)); err != nil {
		return execution.ErrExecutionUnavailable
	}
	return nil
}
