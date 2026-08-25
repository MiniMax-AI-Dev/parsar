package enginehost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// downlinkBuffer is how many frames may queue before the reader blocks.
// Engine servers emit token-level deltas, so a small buffer smooths the
// gap between a burst and a slow consumer without hiding backpressure.
const downlinkBuffer = 256

// Downlink is a read-only WebSocket event stream from an engine server.
// Frames arrive as raw JSON so the adapter owns all decoding: the event
// vocabulary is engine-specific and this package must not grow a switch
// over it.
//
// Lifetime: Frames is closed exactly once, after which Err reports why.
// Close is idempotent and unblocks the reader.
type Downlink struct {
	frames   chan []byte
	conn     *websocket.Conn
	closed   chan struct{}
	readDone chan struct{}

	closeOnce sync.Once
	errMu     sync.Mutex
	err       error
}

// Dial opens a downlink on path (e.g. "/api/events.mux"). The handshake
// sends no Origin header, matching the loopback trust rule these engines
// enforce.
func (c *Client) Dial(ctx context.Context, path string) (*Downlink, error) {
	target, err := c.wsURL(path)
	if err != nil {
		return nil, err
	}
	dialer := &websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Proxy:            nil, // loopback: never route a downlink through a proxy
	}
	conn, resp, err := dialer.DialContext(ctx, target, http.Header{})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("enginehost: dial downlink %s (status %d): %w", path, status, err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	d := &Downlink{
		frames:   make(chan []byte, downlinkBuffer),
		conn:     conn,
		closed:   make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go d.read()
	return d, nil
}

// Frames yields every text frame the server sent, in order.
func (d *Downlink) Frames() <-chan []byte { return d.frames }

// Err returns the reason the stream ended. A clean server-side close
// reports nil.
func (d *Downlink) Err() error {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	return d.err
}

// Close tears the connection down. Idempotent.
func (d *Downlink) Close() {
	d.closeOnce.Do(func() {
		close(d.closed)
		_ = d.conn.Close()
	})
}

func (d *Downlink) read() {
	defer close(d.readDone)
	defer close(d.frames)
	for {
		msgType, payload, err := d.conn.ReadMessage()
		if err != nil {
			select {
			case <-d.closed:
				return
			default:
			}
			if !isCleanClose(err) {
				d.setErr(err)
			}
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		// Copying is required: gorilla reuses its read buffer, so handing
		// the slice to a consumer that outlives the next ReadMessage would
		// alias mutated bytes.
		frame := make([]byte, len(payload))
		copy(frame, payload)
		select {
		case d.frames <- frame:
		case <-d.closed:
			return
		}
	}
}

func (d *Downlink) setErr(err error) {
	d.errMu.Lock()
	defer d.errMu.Unlock()
	if d.err == nil {
		d.err = err
	}
}

// isCleanClose treats the shutdown paths that are not failures as clean:
// a normal/going-away close frame, and a local Close racing the reader.
func isCleanClose(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return true
	}
	return errors.Is(err, websocket.ErrCloseSent)
}
