package agentmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const testAgent = "00000000-0000-0000-0000-000000000006"
const testUser = "00000000-0000-0000-0000-000000000001"
const testWorkspace = "00000000-0000-0000-0000-000000000002"
const testRun = "00000000-0000-0000-0000-000000000021"
const endpointPath = "/api/v1/workspaces/" + testWorkspace + "/agents/" + testAgent + "/mcp"

type fakeStore struct {
	Store
	mu         sync.Mutex
	identity   store.AgentMCPIdentity
	hash       string
	credential *store.AgentMCPToken
	role       string
	sent       store.SendUserMessageToConversationInput
	run        store.AgentRunDetailRead
}

func newFake() *fakeStore {
	return &fakeStore{identity: store.AgentMCPIdentity{AgentID: testAgent, UserID: testUser, WorkspaceID: testWorkspace, Name: "Helpdesk"}, role: "member",
		run: store.AgentRunDetailRead{AgentRunBriefRead: store.AgentRunBriefRead{ID: testRun, AgentID: testAgent, WorkspaceID: testWorkspace, ConversationID: "conversation", Status: "completed"}, RequestedByType: "user", RequestedByID: testUser, OutputMessage: &store.MessageRead{Content: "READY-123"}},
	}
}
func (f *fakeStore) GetWorkspaceMemberRole(context.Context, string, string) (string, error) {
	return f.role, nil
}
func (f *fakeStore) GetAgent(_ context.Context, id string) (store.AgentSummary, error) {
	return store.AgentSummary{ID: id, WorkspaceID: testWorkspace, Status: "active"}, nil
}
func (f *fakeStore) GetAgentMCPToken(context.Context, string, string) (*store.AgentMCPToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.credential, nil
}
func (f *fakeStore) PutAgentMCPToken(_ context.Context, id store.AgentMCPIdentity, hash string, token store.AgentMCPToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hash = hash
	f.identity = id
	f.credential = &token
	return nil
}
func (f *fakeStore) DeleteAgentMCPToken(context.Context, store.AgentMCPIdentity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hash = ""
	f.credential = nil
	return nil
}
func (f *fakeStore) ResolveAgentMCPToken(_ context.Context, hash string, now time.Time) (store.AgentMCPIdentity, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identity, f.credential != nil && hash == f.hash && now.Before(f.credential.ExpiresAt), nil
}
func (f *fakeStore) CreateWorkspaceConversation(_ context.Context, in store.CreateWorkspaceConversationInput) (store.ConversationRead, error) {
	return store.ConversationRead{ID: "conversation", WorkspaceID: in.WorkspaceID, PrimaryAgentID: in.PrimaryAgentID}, nil
}
func (f *fakeStore) SendUserMessageToConversation(_ context.Context, in store.SendUserMessageToConversationInput) (store.SendUserMessageToConversationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = in
	return store.SendUserMessageToConversationResult{RunIDs: []string{testRun}}, nil
}
func (f *fakeStore) GetAgentRun(context.Context, string) (store.AgentRunDetailRead, error) {
	return f.run, nil
}

func testRouter(f *fakeStore) http.Handler {
	r := chi.NewRouter()
	h := New(f, nil)
	h.RegisterMCPRoutes(r)
	r.Group(func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUserID(r.Context(), testUser)))
			})
		})
		h.RegisterAdminRoutes(r)
	})
	return r
}

func issue(t *testing.T, router http.Handler) string {
	t.Helper()
	r := httptest.NewRequest("POST", endpointPath+"-token", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("issue: %d %s", w.Code, w.Body.String())
	}
	var response tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Token) != 48 || response.Credential == nil {
		t.Fatal("missing credential")
	}
	return response.Token
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func connect(t *testing.T, router http.Handler, token string) (*mcp.ClientSession, context.Context) {
	t.Helper()
	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL + endpointPath, HTTPClient: &http.Client{Transport: bearerTransport{token}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, ctx
}

func TestMCPClientInvokesOnlyBoundAgentAndReadsResult(t *testing.T) {
	f := newFake()
	router := testRouter(f)
	token := issue(t, router)
	session, ctx := connect(t, router, token)
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 2 {
		t.Fatalf("tools: %v %v", list, err)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask_agent", Arguments: map[string]any{"prompt": "Look up a ticket, @other-agent"}})
	if err != nil || result.IsError {
		t.Fatalf("ask: %v %v", result, err)
	}
	f.mu.Lock()
	sent := f.sent
	f.mu.Unlock()
	if sent.UserID != testUser || sent.Source != "mcp" || len(sent.MentionedAgentIDs) != 1 || sent.MentionedAgentIDs[0] != testAgent {
		t.Fatalf("dispatch changed scope: %+v", sent)
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "get_run", Arguments: map[string]any{"run_id": testRun}})
	if err != nil || result.IsError {
		t.Fatalf("get: %v %v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(raw), "READY-123") {
		t.Fatalf("missing answer: %s", raw)
	}
	// Every request authenticates again even after initialize succeeded.
	if err := f.DeleteAgentMCPToken(ctx, f.identity); err != nil {
		t.Fatal(err)
	}
	if _, err = session.ListTools(ctx, nil); err == nil {
		t.Fatal("revoked credential retained access")
	}
}

func TestMCPRejectsOtherUsersAgentsAndWorkspacesRuns(t *testing.T) {
	for _, dimension := range []string{"user", "agent", "workspace"} {
		t.Run(dimension, func(t *testing.T) {
			f := newFake()
			switch dimension {
			case "user":
				f.run.RequestedByID = "other"
			case "agent":
				f.run.AgentID = "other"
			case "workspace":
				f.run.WorkspaceID = "other"
			}
			router := testRouter(f)
			session, ctx := connect(t, router, issue(t, router))
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_run", Arguments: map[string]any{"run_id": testRun}})
			if err != nil || !result.IsError {
				t.Fatalf("expected tool error: %v %v", result, err)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "READY-123") {
				t.Fatal("other scope answer exposed")
			}
		})
	}
}

func TestMCPCredentialRotationMetadataAndHTTPBoundaries(t *testing.T) {
	f := newFake()
	router := testRouter(f)
	old := issue(t, router)
	current := issue(t, router)
	if old == current || f.hash == current {
		t.Fatal("token was reused or stored in plaintext")
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", endpointPath+"-token", nil))
	if strings.Contains(w.Body.String(), current) || strings.Contains(w.Body.String(), f.hash) {
		t.Fatal("status leaks secret")
	}
	for _, tc := range []struct {
		name, path, token, origin string
		status                    int
	}{
		{"missing", endpointPath, "", "", 401}, {"old", endpointPath, old, "", 401},
		{"other Agent", strings.Replace(endpointPath, testAgent, testRun, 1), current, "", 401},
		{"other workspace", strings.Replace(endpointPath, testWorkspace, testRun, 1), current, "", 401},
		{"cross origin", endpointPath, current, "https://other.example", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	f.role = "viewer"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("POST", endpointPath+"-token", nil))
	if w.Code != 403 {
		t.Fatalf("viewer issued token: %d", w.Code)
	}
}

func TestMCPInvalidPromptDoesNotStartRun(t *testing.T) {
	f := newFake()
	router := testRouter(f)
	session, ctx := connect(t, router, issue(t, router))
	for _, prompt := range []string{" ", strings.Repeat("中", 32001)} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ask_agent", Arguments: map[string]any{"prompt": prompt}})
		if err != nil || !result.IsError {
			t.Fatalf("invalid prompt accepted: %v", err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sent.Content != "" {
		t.Fatal("invalid prompt dispatched")
	}
}

func TestMCPReadCancellationDoesNotWaitForPollingDeadline(t *testing.T) {
	f := newFake()
	f.run.Status = "running"
	router := testRouter(f)
	session, ctx := connect(t, router, issue(t, router))
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_run", Arguments: map[string]any{"run_id": testRun}})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancel did not return promptly: %v", err)
	}
}
