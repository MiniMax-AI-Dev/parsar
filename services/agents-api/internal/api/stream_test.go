package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type streamFixture struct {
	SessionStore
	session store.Session
	mu      sync.Mutex
	changes []store.SessionChange
	gap     bool
	cursors []int64
}

func (f *streamFixture) GetSession(_ context.Context, tenant, id string) (store.Session, error) {
	if tenant != f.session.TenantID || id != f.session.ID {
		return store.Session{}, store.ErrNotFound
	}
	return f.session, nil
}

func (f *streamFixture) SessionEventCursor(context.Context, string, string) (int64, error) {
	return 10, nil
}

func (f *streamFixture) ListSessionEvents(_ context.Context, _, _ string, cursor int64) ([]store.SessionChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors = append(f.cursors, cursor)
	if f.gap {
		return nil, store.ErrStreamGap
	}
	changes := f.changes
	f.changes = nil
	return changes, nil
}

func TestLiveStreamAuthDisconnectRecoveryAndServerDeadline(t *testing.T) {
	f := &streamFixture{session: store.Session{ID: uuid.NewString(), TenantID: uuid.NewString(), CreatedAt: time.Now(), Metadata: map[string]string{},
		Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"model","tools":[]},"environment":{"type":"none"}}`)}}
	auth, err := NewAuthenticator([]APIKey{{TokenSHA256: device.HashCredential("key"), TenantID: f.session.TenantID}, {TokenSHA256: device.HashCredential("foreign"), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(f, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}, 8)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { done <- struct{}{} }()
		h.ServeHTTP(w, r)
	}))
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	request := func(key string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/agents/sessions/"+f.session.ID+"/events", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Last-Event-ID", "old-history")
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, pair := range []struct {
		key    string
		status int
	}{{"invalid", 401}, {"foreign", 404}} {
		response := request(pair.key)
		_ = response.Body.Close()
		if response.StatusCode != pair.status || response.Header.Get("Content-Type") == "text/event-stream" {
			t.Fatal("authentication happened after stream headers", response.StatusCode)
		}
		<-done
	}
	response := request("key")
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
		t.Fatal(line, err)
	}
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	f.gap = true
	f.mu.Unlock()
	rest, err := io.ReadAll(reader)
	_ = response.Body.Close()
	if err != nil || !strings.Contains(string(rest), `"code":"stream_interrupted"`) || !strings.Contains(string(rest), "event: error") {
		t.Fatal("deadline or gap handling failed", string(rest), err)
	}
	<-done
	f.mu.Lock()
	f.gap = false
	cursors := append([]int64(nil), f.cursors...)
	f.mu.Unlock()
	if len(cursors) == 0 || cursors[0] != 10 {
		t.Fatal("stream replayed history", cursors)
	}
	response = request("key")
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not release the handler")
	}

	// An unread large event must hit a write deadline instead of retaining a handler indefinitely.
	response = request("key")
	f.mu.Lock()
	text := strings.Repeat("x", 16*1024*1024)
	f.changes = []store.SessionChange{{Sequence: 11, Event: v1.SessionEvent{Type: "agent.session.turn.output_text.delta", EventID: "large", SessionID: f.session.ID, Delta: &text}}}
	f.mu.Unlock()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Error("slow reader retained the handler")
	}
	_ = response.Body.Close()
}
