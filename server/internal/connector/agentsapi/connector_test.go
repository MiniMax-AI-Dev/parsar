package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentsclient "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type memoryStore struct {
	usage                 store.UsageInput
	mu                    sync.Mutex
	session               store.CoreSessionBinding
	run                   store.CoreRunBinding
	status                string
	metadata              map[string]any
	events                []store.AgentRunEventRead
	failSubmissionReceipt bool
}

func (s *memoryStore) GetConversation(context.Context, string) (store.ConversationRead, error) {
	return store.ConversationRead{Metadata: s.metadata}, nil
}
func (s *memoryStore) ClaimCoreRun(ctx context.Context, _ string) (context.Context, func(), error) {
	return ctx, func() {}, nil
}
func (s *memoryStore) GetCoreSession(context.Context, string) (store.CoreSessionBinding, error) {
	if s.session.ID == "" {
		return s.session, store.ErrUnknownAgentRun
	}
	return s.session, nil
}
func (s *memoryStore) EnsureCoreSession(_ context.Context, _ string, request json.RawMessage) (store.CoreSessionBinding, error) {
	if s.session.ID == "" {
		s.session = store.CoreSessionBinding{ID: "binding", Request: request}
	}
	return s.session, nil
}
func (s *memoryStore) BindCoreSession(_ context.Context, _, id string) error {
	s.session.SessionID = id
	s.run.SessionID = id
	return nil
}
func (s *memoryStore) EnsureCoreRun(_ context.Context, _, _ string, input json.RawMessage) error {
	if s.run.Input == nil {
		s.run.Input = input
	}
	return nil
}
func (s *memoryStore) GetCoreRun(context.Context, string) (store.CoreRunBinding, error) {
	return s.run, nil
}
func (s *memoryStore) SetCoreRunBaseline(_ context.Context, _, id string) error {
	s.run.PreviousTurnID = id
	s.run.BaselineSet = true
	return nil
}
func (s *memoryStore) MarkCoreRunSubmitted(context.Context, string) error {
	if s.failSubmissionReceipt {
		s.failSubmissionReceipt = false
		return context.DeadlineExceeded
	}
	s.run.Submitted = true
	return nil
}
func (s *memoryStore) MarkCoreRunAttempted(context.Context, string) error {
	s.run.Attempted = true
	return nil
}
func (s *memoryStore) BindCoreTurn(_ context.Context, _, id string) error {
	s.run.TurnID = id
	return nil
}
func (s *memoryStore) RecordCoreUsage(_ context.Context, _ string, usage store.UsageInput) error {
	s.usage = usage
	return nil
}
func (s *memoryStore) SettleCoreRun(context.Context, string) error { s.run.Settled = true; return nil }
func (s *memoryStore) GetCoreExecutionStatus(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, nil
}
func (s *memoryStore) CancelAgentRun(context.Context, string, string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = "cancelled"
	return true, nil
}
func (s *memoryStore) ListCoreExecutionEvents(_ context.Context, _ string, after int64) ([]store.AgentRunEventRead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.AgentRunEventRead
	for _, e := range s.events {
		if e.Sequence > after {
			out = append(out, e)
		}
	}
	return out, nil
}
func (*memoryStore) GetEnabledCapabilitiesForAgent(context.Context, string) ([]store.EnabledCapabilityRead, error) {
	return nil, nil
}

// protocolServer models durable admission: replaying a key never creates another turn.
type protocolServer struct {
	t             *testing.T
	mu            sync.Mutex
	sessions      int
	submissions   int
	turns         int
	cancels       int
	status        string
	loseResponse  bool
	rejectionCode string
	keys          map[string]bool
	requests      [][]byte
}

func (p *protocolServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-core-key" {
		p.t.Error("Core caller credential missing")
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/v1/agents/sessions")
	switch {
	case path == "" && r.Method == "POST":
		p.sessions++
		if r.Header.Get("Idempotency-Key") != "parsar-session-binding" {
			p.t.Error("session key is not stable")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		agent := body["agent"].(map[string]any)
		if agent["model"] != "test-model" || agent["instructions"] != "Be concise" {
			p.t.Errorf("incorrect session config: %v", agent)
		}
		_, _ = w.Write([]byte(`{"id":"session","object":"agent.session"}`))
	case path == "/session/events" && r.Method == "POST":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		events := body["events"].([]any)
		if events[0].(map[string]any)["type"] == "agent.session.input.cancel" {
			p.cancels++
			p.status = "cancelled"
			w.WriteHeader(204)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "parsar-input-run" {
			p.t.Errorf("input key = %q", key)
		}
		p.submissions++
		if p.rejectionCode != "" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": p.rejectionCode, "message": "input rejected"}})
			return
		}
		if !p.keys[key] {
			p.keys[key] = true
			p.turns++
		}
		if p.loseResponse {
			p.loseResponse = false
			w.WriteHeader(502)
			_, _ = w.Write([]byte(`{"error":{"message":"lost response"}}`))
			return
		}
		w.WriteHeader(204)
	case path == "/session/turns":
		if p.turns == 0 {
			_, _ = w.Write([]byte(`{"data":[],"has_more":false}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "turn", "status": p.status}}, "has_more": false})
	case path == "/session/turns/turn":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "turn", "status": p.status, "usage": map[string]any{"input_tokens": 12, "output_tokens": 3, "total_tokens": 15}})
	case path == "/session/items":
		_, _ = w.Write([]byte(`{"data":[{"id":"item","turn_id":"turn","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from Core"}]}],"has_more":false}`))
	default:
		p.t.Errorf("unexpected Core request: %s %s", r.Method, r.URL)
		w.WriteHeader(404)
	}
}
func newTestConnector(t *testing.T, st *memoryStore, upstream *protocolServer) *Connector {
	t.Helper()
	upstream.t = t
	upstream.keys = map[string]bool{}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	c, err := New(agentsclient.Config{BaseURL: server.URL + "/v1", APIKey: "test-core-key"}, st)
	if err != nil {
		t.Fatal(err)
	}
	c.poll = time.Millisecond
	return c
}
func testInput() connector.PromptInput {
	return connector.PromptInput{RunID: "run", WorkspaceID: "workspace", ConversationID: "conversation", AgentID: "agent", TriggerMessageContent: "Hello", AgentConfig: map[string]any{"model": "test-model", "system_prompt": "Be concise"}}
}
func drain(t *testing.T, ctx context.Context, c *Connector, st *memoryStore) []connector.PromptEvent {
	t.Helper()
	events, err := c.StreamPrompt(ctx, testInput())
	if err != nil {
		t.Fatal(err)
	}
	var got []connector.PromptEvent
	for e := range events {
		got = append(got, e)
		st.mu.Lock()
		if e.Type == connector.EventDelta {
			st.events = append(st.events, store.AgentRunEventRead{Sequence: int64(len(st.events) + 1), EventKind: "message.delta", Payload: map[string]any{"delta": e.Delta, "sequence": float64(e.Sequence)}})
		}
		st.mu.Unlock()
		e.Persisted <- nil
	}
	return got
}
func TestDurableSubmissionReplayAndRecoveredOutput(t *testing.T) {
	st := &memoryStore{status: "running", failSubmissionReceipt: true}
	api := &protocolServer{status: "completed", loseResponse: true}
	c := newTestConnector(t, st, api)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	interrupted := drain(t, ctx, c, st)
	if len(interrupted) != 0 || api.turns != 1 || api.submissions != 2 {
		t.Fatalf("receipt interruption did not preserve accepted work: %+v", interrupted)
	}
	first := drain(t, ctx, c, st)
	if api.turns != 1 || api.submissions != 3 || api.sessions != 1 {
		t.Fatalf("admission not idempotent: turns=%d submissions=%d sessions=%d", api.turns, api.submissions, api.sessions)
	}
	last := first[len(first)-1]
	if last.Final == nil || last.Final.Content != "Hello from Core" || last.Final.Usage.InputTokens != 12 {
		t.Fatalf("bad completion: %+v", last)
	}
	// A crash after persisting output but before the product completion transaction must not replay text.
	second := drain(t, ctx, c, st)
	for _, e := range second {
		if e.Type == connector.EventDelta {
			t.Fatal("persisted delta replayed")
		}
	}
	if api.turns != 1 || api.submissions != 3 {
		t.Fatal("recovery submitted another turn")
	}
}
func TestCancellationWaitsForOwningObserver(t *testing.T) {
	st := &memoryStore{status: "running"}
	api := &protocolServer{status: "in_progress"}
	c := newTestConnector(t, st, api)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := c.StreamPrompt(ctx, testInput())
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Type == connector.EventDelta {
			if err := c.Abort(ctx, connector.AbortInput{RunID: "run"}); err != nil {
				t.Fatal(err)
			}
			if api.cancels != 0 {
				t.Fatal("request handler sent a session-wide cancel")
			}
		}
		event.Persisted <- nil
	}
	if api.cancels != 1 || !st.run.Settled {
		t.Fatalf("cancel did not settle: %d %+v", api.cancels, st.run)
	}
	if err := c.Abort(ctx, connector.AbortInput{RunID: "run"}); err != nil {
		t.Fatal(err)
	}
	if api.cancels != 1 {
		t.Fatal("late abort cancelled a subsequent turn")
	}
}
func TestEventPersistenceFailureLeavesRunRecoverable(t *testing.T) {
	st := &memoryStore{status: "running"}
	api := &protocolServer{status: "completed"}
	c := newTestConnector(t, st, api)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := c.StreamPrompt(ctx, testInput())
	if err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.Type != connector.EventDelta {
		t.Fatalf("first event=%s", event.Type)
	}
	event.Persisted <- errors.New("database temporarily unavailable")
	if event, ok := <-events; ok {
		t.Fatalf("unexpected terminal event after lost persistence: %+v", event)
	}
	if st.run.Settled {
		t.Fatal("unpersisted output marked settled")
	}
	got := drain(t, ctx, c, st)
	if len(got) != 2 || got[0].Delta != "Hello from Core" || api.turns != 1 {
		t.Fatalf("incorrect recovery: %+v", got)
	}
}

func TestCompleteAgentConfigurationPreservesNullAndCollections(t *testing.T) {
	st := &memoryStore{}
	c := &Connector{store: st}
	request, err := c.sessionRequest(context.Background(), connector.PromptInput{AgentConfig: map[string]any{
		"model": "test-model", "system_prompt": "Be concise", "service_tier": "priority",
		"tools":     []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}},
		"reasoning": map[string]any{"effort": "high", "summary": "auto"}, "text": nil,
		"multi_agent": map[string]any{"enabled": true, "max_concurrent_subagents": float64(4)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	agent := got["agent"].(map[string]any)
	if text, present := agent["text"]; !present || text != nil {
		t.Fatalf("null semantics lost: %s", raw)
	}
	for _, key := range []string{"model", "instructions", "service_tier", "tools", "reasoning", "multi_agent"} {
		if _, ok := agent[key]; !ok {
			t.Errorf("field %s missing: %s", key, raw)
		}
	}
}

func TestSessionEnvironmentSelectorsReachCore(t *testing.T) {
	for _, selection := range []string{
		`{"type":"openai_hosted","environment_template_id":"tmpl-research"}`,
		`{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":["/skills"]}`,
		`{"type":"none"}`,
	} {
		t.Run(selection, func(t *testing.T) {
			st := &memoryStore{metadata: map[string]any{"core_environment": json.RawMessage(selection)}}
			request, err := (&Connector{store: st}).sessionRequest(context.Background(), connector.PromptInput{AgentConfig: map[string]any{"model": "test-model"}})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Environment map[string]any `json:"environment"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(selection), &want); err != nil {
				t.Fatal(err)
			}
			for field, value := range want {
				g, _ := json.Marshal(got.Environment[field])
				w, _ := json.Marshal(value)
				if string(g) != string(w) {
					t.Errorf("lost environment field %s: %s", field, raw)
				}
			}
		})
	}
}

func TestTerminalUsageAndFailedRecovery(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "completed"} {
		t.Run(status, func(t *testing.T) {
			st := &memoryStore{status: "running", events: []store.AgentRunEventRead{{Sequence: 1, EventKind: "run.failed"}}}
			c := newTestConnector(t, st, &protocolServer{status: status})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			events := drain(t, ctx, c, st)
			final := events[len(events)-1]
			if !final.Recovered || final.Final.Metadata["error"] == nil || st.usage.InputTokens != 12 || st.usage.OutputTokens != 3 || !st.run.Settled {
				t.Fatalf("lost terminal outcome/usage: %+v %+v", final, st.usage)
			}
		})
	}
}
