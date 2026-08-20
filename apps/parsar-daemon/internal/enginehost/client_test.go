package enginehost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPostJSONRoundTripsAndRejectsNonSuccess(t *testing.T) {
	var gotPath, gotBody, gotOrigin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotOrigin = r.Header.Get("Origin")
		buf := make([]byte, 256)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		if r.URL.Path == "/api/denied" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("forbidden"))
			return
		}
		_, _ = w.Write([]byte(`{"echo":"pong"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)

	var out struct{ Echo string }
	if err := c.PostJSON(context.Background(), "api/ping", map[string]string{"ping": "1"}, &out); err != nil {
		t.Fatalf("PostJSON: %v", err)
	}
	if out.Echo != "pong" {
		t.Fatalf("decoded %+v", out)
	}
	if gotPath != "/api/ping" {
		t.Fatalf("path not normalised, got %q", gotPath)
	}
	if gotBody != `{"ping":"1"}` {
		t.Fatalf("body %q", gotBody)
	}
	// The loopback trust rule these engines apply rejects a mismatched
	// Origin, so the client must not invent one.
	if gotOrigin != "" {
		t.Fatalf("client sent an Origin header: %q", gotOrigin)
	}

	err := c.PostJSON(context.Background(), "/api/denied", struct{}{}, nil)
	if err == nil {
		t.Fatal("expected an error for a 403")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error should carry status and body, got %v", err)
	}
}

func TestDialStreamsFramesAndClosesCleanly(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for _, frame := range []string{`{"seq":1}`, `{"seq":2}`, `{"seq":3}`} {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
				return
			}
		}
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	down, err := c.Dial(context.Background(), "/api/events.mux")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer down.Close()

	var got []string
	for frame := range down.Frames() {
		got = append(got, string(frame))
	}
	if strings.Join(got, ",") != `{"seq":1},{"seq":2},{"seq":3}` {
		t.Fatalf("frames %v", got)
	}
	// A server-side normal close is a clean end of stream, not a failure.
	if err := down.Err(); err != nil {
		t.Fatalf("clean close reported an error: %v", err)
	}
}

func TestDialFailsOnNonUpgradePath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	if _, err := c.Dial(context.Background(), "/api/events.mux"); err == nil {
		t.Fatal("expected a dial failure")
	} else if !strings.Contains(err.Error(), "404") {
		t.Fatalf("error should report the status, got %v", err)
	}
}

func TestWSURLDerivesSchemeFromBase(t *testing.T) {
	for base, want := range map[string]string{
		"http://127.0.0.1:1234":  "ws://127.0.0.1:1234/api/events.mux",
		"https://127.0.0.1:1234": "wss://127.0.0.1:1234/api/events.mux",
	} {
		got, err := NewClient(base, 0).wsURL("/api/events.mux")
		if err != nil {
			t.Fatalf("wsURL(%s): %v", base, err)
		}
		if got != want {
			t.Fatalf("wsURL(%s) = %q, want %q", base, got, want)
		}
	}
}
