package gateway

import (
	"github.com/go-chi/chi/v5"
)

// RegisterRoutes mounts the agent_daemon HTTP / WebSocket endpoints
// onto a chi router.
//
//	GET  /agent-daemon/ws            — daemon dial-in (WS upgrade)
//	POST /agent-daemon/bootstrap     — daemon first-call to fetch wsUrl + heartbeat cadence
//	GET  /agent-daemon/device-status — daemon self-check
//
// All three accept the runtime credential issued via the
// runtimes/pairings flow with type='agent_daemon'.
func RegisterRoutes(r chi.Router, h *Handler) {
	if h == nil {
		panic("agentdaemon gateway: RegisterRoutes called with nil handler")
	}
	r.Route("/agent-daemon", func(r chi.Router) {
		r.Get("/ws", h.WS)
		r.Post("/bootstrap", h.Bootstrap)
		r.Get("/device-status", h.DeviceStatus)
	})
}
