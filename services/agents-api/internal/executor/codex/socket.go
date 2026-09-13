package codex

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const heartbeatInterval = 5 * time.Second

// @Summary Attach an executor using a registration-scoped connection capability
// @Tags Native executor registry
// @Param environment path string true "Environment ID"
// @Param registration path string true "Registration ID"
// @Param ticket query string true "Private connection capability"
// @Success 101 {string} string "WebSocket upgrade"
// @Failure 401,404,409,503 {object} RegistryError
// @Router /cloud/environment/{environment}/executor/{registration} [get]
func (r *Registry) connectExecutor(w http.ResponseWriter, req *http.Request) {
	environment, id := req.PathValue("environment"), req.PathValue("registration")
	ticket := sha256.Sum256([]byte(req.URL.Query().Get("ticket")))
	r.mu.Lock()
	reg := r.registrations[environment]
	valid := !r.closed && reg != nil && reg.id == id && time.Now().Before(reg.expires) && subtle.ConstantTimeCompare(ticket[:], reg.ticket[:]) == 1
	r.mu.Unlock()
	if !valid {
		writeError(w, http.StatusUnauthorized)
		return
	}
	if !r.check(w, req, reg.key) {
		return
	}
	// Upgrade only the current registration, and never replace an established socket by replaying its URL.
	r.mu.Lock()
	if r.closed || r.registrations[environment] != reg || reg.socket != nil {
		r.mu.Unlock()
		writeError(w, http.StatusConflict)
		return
	}
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, ReadBufferSize: 1024, WriteBufferSize: 1024}
	socket, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		r.mu.Unlock()
		return
	}
	reg.socket = socket
	r.mu.Unlock()
	defer r.disconnected(environment, reg, socket)
	socket.SetReadLimit(64 * 1024)
	_ = socket.SetReadDeadline(time.Now().Add(3 * heartbeatInterval))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(3 * heartbeatInterval)) })
	done := make(chan struct{})
	defer close(done)
	go r.heartbeat(environment, reg, socket, done)
	for {
		kind, _, err := socket.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.BinaryMessage || kind == websocket.TextMessage {
			// Data forwarding is deliberately unavailable until harness grants and routing are implemented.
			_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "harness relay unavailable"), time.Now().Add(time.Second))
			return
		}
	}
}

func (r *Registry) disconnected(environment string, reg *registration, socket *websocket.Conn) {
	_ = socket.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.registrations[environment] == reg && reg.socket == socket {
		reg.socket = nil
	}
}

func (r *Registry) heartbeat(environment string, reg *registration, socket *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			err := r.authorized(ctx, reg.key)
			cancel()
			r.mu.Lock()
			current := !r.closed && r.registrations[environment] == reg && reg.socket == socket
			r.mu.Unlock()
			if err != nil || !current {
				_ = socket.Close()
				return
			}
			if err := socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				_ = socket.Close()
				return
			}
		}
	}
}
