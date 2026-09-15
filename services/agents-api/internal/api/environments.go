package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve an execution Environment
// @Description Returns durable connection status and safe installed metadata for the supported self_hosted profile. Empty files/plugins/skills describe the absence of API-managed installations, not the contents or discovered capabilities of the caller's machine. Unsupported installation configurations remain implementation gaps. This read does not prepare execution, start compute or require an enabled execution worker. Session deletion removes the associated Environment from public reads; project-shared read authorization is unchanged. Connection status does not prove native readiness or process quiescence.
// @Tags Environments
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param environment_id path string true "Environment ID"
// @Success 200 {object} v1.EnvironmentInfo
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/environments/{environment_id} [get]
func (h *Handler) getEnvironment(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Environment retrieval does not accept query parameters.")
		return
	}
	environment, err := h.store.GetEnvironment(r.Context(), tenantID(r), chi.URLParam(r, "environment_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := environmentResponse(environment)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func environmentResponse(environment store.Environment) (v1.EnvironmentInfo, error) {
	configuration, err := decodeSessionEnvironment(environment.Configuration)
	if err != nil || configuration.Type != "self_hosted" || len(configuration.CapabilityDirectories) != 0 || environment.ID == "" {
		return v1.EnvironmentInfo{}, errors.New("unsupported stored environment metadata configuration")
	}
	switch environment.Status {
	case "pending", "connected", "disconnected", "expired", "failed":
	default:
		return v1.EnvironmentInfo{}, errors.New("unsupported stored environment resource status")
	}
	// This closed configuration has no API-installed resources; it is not host inventory.
	return v1.EnvironmentInfo{
		ID: environment.ID, Object: "agent.environment", Type: configuration.Type, Status: environment.Status,
		Files: []json.RawMessage{}, Plugins: []json.RawMessage{}, Skills: []json.RawMessage{},
	}, nil
}
