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
const maxRelayMessageSize = 256 * 1024
const relayWriteTimeout = 5 * time.Second

var nativeUpgrader = websocket.Upgrader{HandshakeTimeout: 5 * time.Second, ReadBufferSize: 1024, WriteBufferSize: 1024}

type connection struct {
	socket *websocket.Conn
	peer   *connection
}

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
	if !r.check(w, req, reg.key) || !r.checkCurrentExecutor(w, req, reg.key) {
		return
	}
	r.mu.Lock()
	if r.closed || r.registrations[environment] != reg || !time.Now().Before(reg.expires) || reg.socket != nil {
		r.mu.Unlock()
		writeError(w, http.StatusConflict)
		return
	}
	socket, err := nativeUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.mu.Unlock()
		return
	}
	c := &connection{socket: socket}
	reg.socket = c
	r.mu.Unlock()
	r.serveConnection(environment, reg, c)
}

func (r *Registry) serveConnection(environment string, reg *registration, c *connection) {
	defer func() {
		r.mu.Lock()
		r.closeConnectionLocked(reg, c)
		r.mu.Unlock()
	}()
	socket := c.socket
	socket.SetReadLimit(maxRelayMessageSize)
	_ = socket.SetReadDeadline(time.Now().Add(3 * heartbeatInterval))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(3 * heartbeatInterval)) })
	done := make(chan struct{})
	defer close(done)
	go r.heartbeat(environment, reg, c, done)
	for {
		kind, data, err := socket.ReadMessage()
		if err != nil {
			return
		}
		r.mu.Lock()
		peer := c.peer
		current := !r.closed && r.registrations[environment] == reg && (reg.socket == c || reg.socket == peer)
		r.mu.Unlock()
		if kind != websocket.BinaryMessage || peer == nil {
			_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "binary paired relay required"), time.Now().Add(time.Second))
			return
		}
		if !current {
			return
		}
		// Each peer has exactly one data writer. Blocking bounds buffering to one native frame per direction.
		if err := peer.socket.SetWriteDeadline(time.Now().Add(relayWriteTimeout)); err != nil {
			return
		}
		if err := peer.socket.WriteMessage(websocket.BinaryMessage, data); err != nil {
			return
		}
	}
}

func (r *Registry) closeConnectionLocked(reg *registration, c *connection) {
	if c == nil {
		return
	}
	_ = c.socket.Close()
	if c.peer != nil {
		_ = c.peer.socket.Close()
	}
	// Close both physical peers so the native executor detaches its virtual Session before reconnecting.
	if reg.socket == c || reg.socket == c.peer {
		reg.socket = nil
		clear(reg.grants)
	}
}

func (r *Registry) heartbeat(environment string, reg *registration, c *connection, done <-chan struct{}) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			err := r.executorAuthorized(ctx, reg.key)
			cancel()
			r.mu.Lock()
			current := !r.closed && r.registrations[environment] == reg && (reg.socket == c || (reg.socket != nil && reg.socket.peer == c))
			r.mu.Unlock()
			if err != nil || !current {
				_ = c.socket.Close()
				return
			}
			if err := c.socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				_ = c.socket.Close()
				return
			}
		}
	}
}
