// Package runtime connects execution devices without product dependencies.
package runtime

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
)

type DeviceStore interface {
	gateway.RuntimeStore
	gateway.HeartbeatTouch
}

// NewGateway exposes the existing internal daemon protocol. It does not implement
// the public Agents API self_hosted executor contract or grant Session API access.
func NewGateway(s DeviceStore, publicWSURL string) (http.Handler, *gateway.Registry, error) {
	u, err := url.Parse(publicWSURL)
	if err != nil || s == nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/v1/agent-daemon/ws" {
		return nil, nil, errors.New("daemon URL must be an absolute ws(s) URL ending in /api/v1/agent-daemon/ws")
	}
	registry := gateway.NewRegistry()
	h := gateway.NewHandler(gateway.HandlerConfig{
		Authenticator: gateway.NewAuthenticator(s), Registry: registry,
		Heartbeat: s, PublicWSURL: publicWSURL,
	})
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) { gateway.RegisterRoutes(r, h) })
	return r, registry, nil
}

// CloseConnections releases upgraded WebSockets, which http.Server.Shutdown
// does not close. Call after stopping new HTTP upgrades.
func CloseConnections(registry *gateway.Registry) {
	for _, id := range registry.Devices() {
		if session, err := registry.LookupDevice(id); err == nil {
			session.Close("execution service shutting down")
		}
	}
}
