package deepseekharness

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeGateway is a stand-in for the dsh /api surface: the same envelope,
// the same method paths, and the same WebSocket downlink. Tests drive it
// instead of a real dsh so the adapter's wire handling is exercised
// without an engine install, while the shapes it emits are the ones
// captured from a live rc.7 server.
type fakeGateway struct {
	srv *httptest.Server

	mu        sync.Mutex
	created   []string
	prompts   []promptCall
	cancels   []string
	listErr   *rpcError
	promptErr *rpcError

	// nextSessionID is handed out by session.create.
	nextSessionID string

	conns   chan *websocket.Conn
	upgrade websocket.Upgrader
}

type promptCall struct {
	SessionID string
	Mode      string
	Content   []promptContentPart
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	g := &fakeGateway{
		nextSessionID: "session-fake-0001",
		conns:         make(chan *websocket.Conn, 4),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(eventsMuxPath, g.handleDownlink)
	mux.HandleFunc(apiPathPrefix+"/", g.handleUnary)
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGateway) handleDownlink(w http.ResponseWriter, r *http.Request) {
	conn, err := g.upgrade.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	g.conns <- conn
}

// conn waits for the adapter to attach its downlink. The adapter must
// attach before prompting, so a test that never sees a connection has
// found an ordering regression.
func (g *fakeGateway) conn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-g.conns:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("adapter never attached the event downlink")
		return nil
	}
}

func (g *fakeGateway) handleUnary(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, apiPathPrefix+"/")
	var req clientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if req.Type != clientRequestType {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}

	raw, _ := json.Marshal(req.Payload)
	g.mu.Lock()
	var (
		value any
		fail  *rpcError
	)
	switch method {
	case methodSessionList:
		fail = g.listErr
		value = sessionListValue{Items: []sessionListItem{}}
	case methodSessionCreate:
		g.created = append(g.created, string(raw))
		value = sessionCreateValue{SessionID: g.nextSessionID}
	case methodSessionPrompt:
		var payload struct {
			SessionID string              `json:"sessionId"`
			Mode      string              `json:"mode"`
			Content   []promptContentPart `json:"content"`
		}
		_ = json.Unmarshal(raw, &payload)
		g.prompts = append(g.prompts, promptCall{SessionID: payload.SessionID, Mode: payload.Mode, Content: payload.Content})
		fail = g.promptErr
		value = map[string]any{"accepted": true}
	case methodSessionCancel:
		var payload struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(raw, &payload)
		g.cancels = append(g.cancels, payload.SessionID)
		value = map[string]any{"accepted": true}
	default:
		fail = &rpcError{Code: "not-found", Message: "unknown method " + method}
	}
	g.mu.Unlock()

	resp := map[string]any{"type": serverResponseType, "rpcId": req.RPCID}
	if fail != nil {
		resp["result"] = map[string]any{"ok": false, "error": fail}
	} else {
		resp["result"] = map[string]any{"ok": true, "value": value}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *fakeGateway) promptCalls() []promptCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]promptCall{}, g.prompts...)
}

func (g *fakeGateway) createCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string{}, g.created...)
}

func (g *fakeGateway) cancelCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string{}, g.cancels...)
}

// emit pushes one session/event frame in the exact envelope a live dsh
// server sends: a server-request whose method names the frame family and
// whose payload nests the durable event.
func emitEvent(t *testing.T, conn *websocket.Conn, sessionID, eventType string, seq uint64, data any) {
	t.Helper()
	payload := map[string]any{
		"type":      frameMethodSessionEvent,
		"sessionId": sessionID,
		"event": map[string]any{
			"type": eventType,
			"seq":  seq,
			"time": time.Now().UnixMilli(),
			"data": data,
		},
	}
	frame := map[string]any{
		"type":    "server-request",
		"rpcId":   "rpc-" + eventType,
		"method":  frameMethodSessionEvent,
		"payload": payload,
	}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write frame %s: %v", eventType, err)
	}
}

// textDelta / reasoningDelta / usageChunk build assistant/chunk data.
func textDelta(index int, text string) map[string]any {
	return map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{"type": chunkTextDelta, "index": index, "text": text}}
}

func reasoningDelta(index int, text string) map[string]any {
	return map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{"type": chunkReasoningDelta, "index": index, "text": text}}
}

func usageChunk(in, out, cached int) map[string]any {
	return map[string]any{"turn": 1, "step": 1, "chunk": map[string]any{
		"type":  chunkUsage,
		"usage": map[string]any{"inputTokens": in, "outputTokens": out, "cacheReadTokens": cached},
	}}
}

func assistantMessage(blocks ...map[string]any) map[string]any {
	return map[string]any{"turn": 1, "step": 1, "message": map[string]any{"role": "assistant", "content": blocks}}
}

func textBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func reasoningBlock(text string) map[string]any {
	return map[string]any{"type": "reasoning", "text": text}
}

func turnEnd(kind string) map[string]any {
	return map[string]any{"turn": 1, "reason": map[string]any{"kind": kind}}
}
