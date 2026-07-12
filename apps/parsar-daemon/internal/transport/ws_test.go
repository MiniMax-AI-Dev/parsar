package transport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// fakeGateway is a minimal stand-in for the server-side
// /agent-daemon/ws handler. Echoes inbound frames and records dial
// query params.
type fakeGateway struct {
	upgrader websocket.Upgrader

	mu      sync.Mutex
	dialURL string
	frames  []proto.Envelope

	connCh chan *websocket.Conn
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		connCh:   make(chan *websocket.Conn, 1),
	}
}

func (g *fakeGateway) handler(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.dialURL = r.URL.String()
	g.mu.Unlock()

	conn, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	g.connCh <- conn

	// Read until client closes; record every frame.
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			_ = conn.Close()
			return
		}
		var env proto.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		g.mu.Lock()
		g.frames = append(g.frames, env)
		g.mu.Unlock()
	}
}

func (g *fakeGateway) recordedFrames() []proto.Envelope {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]proto.Envelope, len(g.frames))
	copy(out, g.frames)
	return out
}

func (g *fakeGateway) recordedDialURL() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dialURL
}

// httpToWS rewrites httptest server URL into the ws:// equivalent.
func httpToWS(s string) string { return "ws" + strings.TrimPrefix(s, "http") }

func TestDialPassesAuthInQueryParams(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "rt_abc",
		Credential:    "shh-secret",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	dialURL := gw.recordedDialURL()
	if !strings.Contains(dialURL, "device_id=rt_abc") {
		t.Errorf("dial URL %q missing device_id", dialURL)
	}
	if !strings.Contains(dialURL, "token=shh-secret") {
		t.Errorf("dial URL %q missing token", dialURL)
	}
	if !strings.Contains(dialURL, "version=0.1.0") {
		t.Errorf("dial URL %q missing version", dialURL)
	}
	if got := conn.DeviceID(); got != "rt_abc" {
		t.Errorf("Conn.DeviceID = %q, want rt_abc", got)
	}
}

func TestDialRoundTripsEnvelope(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	env, err := proto.NewEnvelope(proto.TypeDelta, "run_1", proto.DeltaPayload{Delta: "hello", Sequence: 1})
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Send(ctx, env); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Poll because the read happens in a goroutine and recording is
	// post-decode.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frames := gw.recordedFrames()
		if len(frames) > 0 {
			if frames[0].Type != proto.TypeDelta || frames[0].ID != "run_1" {
				t.Errorf("recorded frame = %+v, want type=delta id=run_1", frames[0])
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake gateway never recorded the sent frame")
}

func TestRecvDeliversServerSentFrames(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	// Server pushes a prompt_request down to the daemon.
	serverConn := <-gw.connCh
	pushed, _ := proto.NewEnvelope("prompt_request", "run_abc", map[string]string{"prompt": "hi"})
	raw, _ := json.Marshal(pushed)
	if err := serverConn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("server write: %v", err)
	}

	select {
	case env := <-conn.Recv():
		if env.Type != "prompt_request" || env.ID != "run_abc" {
			t.Errorf("received %+v, want prompt_request/run_abc", env)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("never received the server-pushed frame")
	}
}

func TestCloseTerminatesRecvAndUnblocksSend(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Wait for server-side accept.
	<-gw.connCh
	_ = conn.Close()

	select {
	case _, ok := <-conn.Recv():
		if ok {
			// Drain — close should be followed by close-of-recv-channel.
			<-conn.Recv()
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Recv channel was not closed after Close")
	}

	// Done() must be closed too.
	select {
	case <-conn.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() never closed after Close")
	}

	// A post-close Send must return immediately rather than hang.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	env, _ := proto.NewEnvelope(proto.TypeDelta, "x", proto.DeltaPayload{Delta: "x"})
	if err := conn.Send(ctx, env); err == nil {
		t.Fatal("Send after Close returned nil error")
	}
}

func TestStartHeartbeatsTicks(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-gw.connCh

	var calls atomic.Int32
	conn.StartHeartbeats(context.Background(), 30*time.Millisecond, func() proto.HeartbeatPayload {
		calls.Add(1)
		return proto.HeartbeatPayload{Timestamp: time.Now().Unix(), DaemonVersion: "0.0.0-dev"}
	}, nil)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		// Look for at least two heartbeat frames at the server end.
		var seen int
		for _, f := range gw.recordedFrames() {
			if f.Type == proto.TypeHeartbeat {
				seen++
			}
		}
		if seen >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected ≥2 heartbeat frames, got payload calls=%d, frames=%+v", calls.Load(), gw.recordedFrames())
}

func TestStartHeartbeatsSendsImmediately(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-gw.connCh

	var calls atomic.Int32
	hbCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn.StartHeartbeats(hbCtx, time.Hour, func() proto.HeartbeatPayload {
		calls.Add(1)
		return proto.HeartbeatPayload{Timestamp: time.Now().Unix(), DaemonVersion: "0.0.0-dev"}
	}, nil)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, f := range gw.recordedFrames() {
			if f.Type == proto.TypeHeartbeat {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected immediate heartbeat before first interval tick; payload calls=%d frames=%+v", calls.Load(), gw.recordedFrames())
}

func TestDialBubblesUpgradeError(t *testing.T) {
	// Server that 401s the upgrade. We have to write the rejection
	// before any WS handshake completes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("Dial returned nil error on 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("Dial error %q missing status code", err.Error())
	}
}

// Regression: WS used to retry forever even when the server returned
// 426 incompatible_version. 401 / 403 / 426 must wrap with
// ErrPermanent so Reconnect bails.
func TestDialMarksOperatorFixableUpgradeRejectionsAsPermanent(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		{"unauthorized", http.StatusUnauthorized},
		{"forbidden", http.StatusForbidden},
		{"upgrade_required", http.StatusUpgradeRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(`{"error":"x","detail":"y"}`))
			}))
			defer srv.Close()
			_, err := transport.Dial(context.Background(), transport.DialOptions{
				WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
				DeviceID:      "d",
				Credential:    "c",
				DaemonVersion: "0.1.0",
			})
			if err == nil {
				t.Fatalf("Dial returned nil for status %d", tc.code)
			}
			if !errors.Is(err, transport.ErrPermanent) {
				t.Errorf("Dial err %v does not wrap ErrPermanent (status %d)", err, tc.code)
			}
		})
	}
}

// Guards against ErrPermanent classification swallowing transient
// gateway hiccups — 5xx must stay retryable so a deployment / restart
// recovers on its own.
func TestDialDoesNotMarkServerErrorsAsPermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err == nil {
		t.Fatal("Dial returned nil for 500")
	}
	if errors.Is(err, transport.ErrPermanent) {
		t.Errorf("Dial err %v marks 500 as permanent (must stay transient)", err)
	}
}

func TestDialValidatesRequiredOptions(t *testing.T) {
	_, err := transport.Dial(context.Background(), transport.DialOptions{})
	if err == nil {
		t.Fatal("Dial accepted empty options")
	}
}

func TestSendRespectsContextCancel(t *testing.T) {
	gw := newFakeGateway()
	srv := httptest.NewServer(http.HandlerFunc(gw.handler))
	defer srv.Close()

	conn, err := transport.Dial(context.Background(), transport.DialOptions{
		WSURL:         httpToWS(srv.URL) + "/agent-daemon/ws",
		DeviceID:      "d",
		Credential:    "c",
		DaemonVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	<-gw.connCh

	// Build an env, then call Send with an already-cancelled ctx.
	env, _ := proto.NewEnvelope(proto.TypeDelta, "r", proto.DeltaPayload{Delta: "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := conn.Send(ctx, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send with cancelled ctx = %v, want context.Canceled", err)
	}
}
