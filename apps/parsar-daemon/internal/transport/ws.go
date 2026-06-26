package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// DialOptions is the input to Dial. HTTPHeader is optional.
type DialOptions struct {
	// WSURL is the absolute ws://... or wss://... URL.
	WSURL string

	// DeviceID is the runtime row id stamped at pair time. Sent as
	// device_id query param; the gateway uses it as the session key.
	DeviceID string

	// Credential is the bearer compared against runner_credential_hash.
	// Sent as the token query param (not as a header) because some
	// HTTP middleware strips Authorization on upgrade requests.
	Credential string

	// DaemonVersion is the X.Y.Z string used for
	// proto.VersionCompatible. Mismatches close at upgrade time (4126).
	DaemonVersion string

	// HandshakeTimeout caps WS upgrade wait. Zero → 10s.
	HandshakeTimeout time.Duration

	// HTTPHeader is appended to the upgrade request. Useful in tests.
	HTTPHeader http.Header
}

// Conn is the daemon-side WebSocket. One read goroutine + one write
// goroutine; callers Send synchronously and Recv off a channel.
//
// Conn does NOT own the heartbeat loop — call StartHeartbeats once
// Dial returns. Splitting keeps transport free of "what's a live
// request count" knowledge.
type Conn struct {
	ws       *websocket.Conn
	deviceID string

	recvCh chan proto.Envelope
	sendCh chan envelopeWithAck

	closeOnce sync.Once
	closed    chan struct{}

	errMu sync.Mutex
	err   error

	hbCancel context.CancelFunc
}

// envelopeWithAck pairs an outbound frame with a done channel so Send
// blocks until the frame is flushed (or the write goroutine errors).
// Single-writer serialisation point gorilla/websocket requires.
type envelopeWithAck struct {
	env proto.Envelope
	ack chan error
}

// Dial opens the reverse WebSocket. Read/write goroutines are live
// on return; caller MUST eventually Close to avoid goroutine leaks.
func Dial(ctx context.Context, opts DialOptions) (*Conn, error) {
	if opts.WSURL == "" || opts.DeviceID == "" || opts.Credential == "" || opts.DaemonVersion == "" {
		return nil, fmt.Errorf("transport.Dial: WSURL, DeviceID, Credential, DaemonVersion are all required")
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}
	dialURL, err := withQueryParams(opts.WSURL, map[string]string{
		"device_id": opts.DeviceID,
		"token":     opts.Credential,
		"version":   opts.DaemonVersion,
	})
	if err != nil {
		return nil, err
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = opts.HandshakeTimeout

	dialCtx, cancel := context.WithTimeout(ctx, opts.HandshakeTimeout)
	defer cancel()
	wsConn, resp, err := dialer.DialContext(dialCtx, dialURL, opts.HTTPHeader)
	if err != nil {
		// 401/403/426 are operator-fixable and MUST NOT be retried —
		// the gateway will keep rejecting until credential / device /
		// daemon version is fixed. Mark them ErrPermanent so Reconnect
		// bails. Everything else stays transient.
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden, http.StatusUpgradeRequired:
				return nil, fmt.Errorf("transport.Dial: ws upgrade rejected with %s: %w: %w", resp.Status, err, ErrPermanent)
			}
			return nil, fmt.Errorf("transport.Dial: ws upgrade rejected with %s: %w", resp.Status, err)
		}
		return nil, fmt.Errorf("transport.Dial: ws upgrade: %w", err)
	}
	// Bound a single inbound frame so a misbehaving server can't OOM
	// us. Matches the gateway's 4 MiB outbound ceiling.
	wsConn.SetReadLimit(4 * 1024 * 1024)

	c := &Conn{
		ws:       wsConn,
		deviceID: opts.DeviceID,
		recvCh:   make(chan proto.Envelope, 64),
		sendCh:   make(chan envelopeWithAck, 64),
		closed:   make(chan struct{}),
	}
	go c.writeLoop()
	go c.readLoop()
	return c, nil
}

// Recv returns the inbound envelope channel. Closes when the connection
// terminates; Err() returns the cause after.
func (c *Conn) Recv() <-chan proto.Envelope { return c.recvCh }

// Done closes when the connection has shut down.
func (c *Conn) Done() <-chan struct{} { return c.closed }

// Err returns the first fatal error from either loop, or nil on clean
// shutdown via Close.
func (c *Conn) Err() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	return c.err
}

func (c *Conn) DeviceID() string { return c.deviceID }

// Send marshals env, queues it, and blocks until written (or ctx fires
// / conn closes). Safe for concurrent callers — the write goroutine is
// the only thing touching the underlying ws write path.
func (c *Conn) Send(ctx context.Context, env proto.Envelope) error {
	ack := make(chan error, 1)
	wrapped := envelopeWithAck{env: env, ack: ack}
	select {
	case c.sendCh <- wrapped:
	case <-c.closed:
		return errClosedOrErr(c)
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-ack:
		return err
	case <-c.closed:
		return errClosedOrErr(c)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close tears down the conn. Safe to call concurrently; idempotent.
// The courteous CloseMessage frame is NOT written from here —
// writeLoop owns single-writer access and emits it from its defer.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		if c.hbCancel != nil {
			c.hbCancel()
		}
		close(c.closed)
	})
	return nil
}

// StartHeartbeats kicks off a ticker that calls payloadFn every
// interval and Sends the resulting HeartbeatPayload. Returns
// immediately. Caller-controlled because the heartbeat carries fields
// (active_requests, claude_available) only the agent layer knows.
// Nil logger falls back to log.Bg().
func (c *Conn) StartHeartbeats(parentCtx context.Context, interval time.Duration, payloadFn func() proto.HeartbeatPayload, logger *slog.Logger) {
	if interval <= 0 || payloadFn == nil {
		return
	}
	if logger == nil {
		logger = log.Bg()
	}
	ctx, cancel := context.WithCancel(parentCtx)
	c.hbCancel = cancel
	go func() {
		defer cancel()
		sendHeartbeat := func(sendTimeout time.Duration) {
			env, err := proto.NewEnvelope(proto.TypeHeartbeat, "", payloadFn())
			if err != nil {
				logger.Error("marshal heartbeat envelope", "err", err)
				return
			}
			// Per-send deadline so a wedged peer doesn't pile up
			// heartbeats on sendCh.
			sendCtx, sendCancel := context.WithTimeout(ctx, sendTimeout)
			if err := c.Send(sendCtx, env); err != nil && !errors.Is(err, context.Canceled) {
				logger.Warn("send heartbeat", "err", err)
			}
			sendCancel()
		}

		sendHeartbeat(interval)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.closed:
				return
			case <-t.C:
				sendHeartbeat(interval)
			}
		}
	}()
}

// ---------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------

// writeLoop owns every write to the underlying websocket. Single-
// writer invariant: this goroutine is the ONLY place that touches the
// ws write path — including the final courteous Close frame, emitted
// from the defer rather than from Conn.Close() so gorilla/websocket's
// internal write state isn't raced.
func (c *Conn) writeLoop() {
	defer func() {
		// Best-effort close frame so the peer sees code 1000 rather
		// than EOF.
		_ = c.ws.SetWriteDeadline(time.Now().Add(time.Second))
		_ = c.ws.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closing"))
		_ = c.ws.Close()
	}()
	for {
		select {
		case <-c.closed:
			return
		case msg := <-c.sendCh:
			raw, err := json.Marshal(msg.env)
			if err != nil {
				msg.ack <- fmt.Errorf("transport: marshal envelope: %w", err)
				continue
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.ws.WriteMessage(websocket.TextMessage, raw); err != nil {
				wrapped := fmt.Errorf("transport: write envelope (type=%s): %w", msg.env.Type, err)
				msg.ack <- wrapped
				c.fail(wrapped)
				return
			}
			msg.ack <- nil
		}
	}
}

// readLoop pulls frames, decodes, pushes onto recvCh. Bounded recv
// buffer means a stuck consumer eventually backpressures into the WS
// read deadline — better to disconnect than balloon memory.
func (c *Conn) readLoop() {
	defer func() {
		// Closing recvCh signals consumers ("drain me then check
		// Err()"); closing closed signals everyone else.
		c.closeOnce.Do(func() {
			if c.hbCancel != nil {
				c.hbCancel()
			}
			_ = c.ws.Close()
			close(c.closed)
		})
		close(c.recvCh)
	}()
	for {
		_, raw, err := c.ws.ReadMessage()
		if err != nil {
			if isCleanClose(err) {
				return
			}
			if isPermanentClose(err) {
				c.fail(fmt.Errorf("transport: server closed connection (runtime deleted): %w", ErrPermanent))
				return
			}
			c.fail(fmt.Errorf("transport: read frame: %w", err))
			return
		}
		log.Bg().Debug("transport: received WS frame", "bytes", len(raw))
		var env proto.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			// Skip malformed frames rather than tearing down — server
			// might have shipped a new event type we don't understand
			// yet, and dropping is friendlier than disconnecting.
			log.Bg().Warn("transport: dropping malformed WS frame",
				"bytes", len(raw), "err", err.Error(), "head", string(raw[:min(len(raw), 200)]))
			continue
		}
		log.Bg().Info("transport: dispatching envelope", "type", env.Type, "id", env.ID)
		select {
		case c.recvCh <- env:
		case <-c.closed:
			return
		}
	}
}

func (c *Conn) fail(err error) {
	c.errMu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.errMu.Unlock()
}

func errClosedOrErr(c *Conn) error {
	if e := c.Err(); e != nil {
		return e
	}
	return ErrConnClosed
}

// ErrConnClosed is returned by Send on clean shutdown.
var ErrConnClosed = errors.New("transport: connection closed")

// isCleanClose reports peer-initiated graceful close codes.
func isCleanClose(err error) bool {
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
	) {
		return true
	}
	return false
}

// CloseRuntimeDeleted is the custom WS close code the server sends
// when the runtime has been deleted by an admin — permanent error.
const CloseRuntimeDeleted = 4001

// isPermanentClose reports server-sent close codes that must NOT be
// retried.
func isPermanentClose(err error) bool {
	return websocket.IsCloseError(err, CloseRuntimeDeleted)
}

// withQueryParams appends params, preserving any existing query string.
func withQueryParams(raw string, params map[string]string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("transport: parse ws url %q: %w", raw, err)
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
