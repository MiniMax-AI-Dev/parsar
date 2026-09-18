package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type environmentCreationFixture struct {
	streamFixture
	input store.CreateSessionInput
}

func (f *environmentCreationFixture) FindSessionCreation(context.Context, string, string, json.RawMessage, identity.Subject) (store.SessionCreation, error) {
	return store.SessionCreation{}, store.ErrNotFound
}

func (f *environmentCreationFixture) CreateSession(_ context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = input
	f.session = store.Session{ID: uuid.NewString(), TenantID: tenant, Configuration: input.Configuration, Metadata: input.Metadata, CreatedAt: time.Unix(1700000000, 0)}
	var snapshot struct {
		Environment json.RawMessage `json:"environment"`
	}
	if err := json.Unmarshal(input.Configuration, &snapshot); err != nil {
		return store.Session{}, err
	}
	f.session.Environment = &store.Environment{
		ID: uuid.NewString(), SessionID: f.session.ID, TenantID: tenant, Status: "pending", Configuration: snapshot.Environment,
	}
	return f.session, nil
}

func (f *environmentCreationFixture) CreateSessionStream(ctx context.Context, tenant string, input store.CreateSessionInput) (store.SessionCreation, error) {
	session, err := f.CreateSession(ctx, tenant, input)
	return store.SessionCreation{Session: session, Created: true}, err
}

func environmentCreationHandler(t *testing.T, engine string, options ...Option) (http.Handler, *environmentCreationFixture) {
	t.Helper()
	fixture := &environmentCreationFixture{}
	auth, err := NewAuthenticator([]APIKey{{
		OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner",
		TokenSHA256: device.HashCredential("key"), TenantID: uuid.NewString(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(fixture, auth, engine, options...)
	if err != nil {
		t.Fatal(err)
	}
	return handler, fixture
}

func TestSelfHostedEmptyCreationAndStream(t *testing.T) {
	var canonical string
	for _, capability := range []string{"", `,"capability_directories":null`, `,"capability_directories":[]`} {
		for _, input := range []string{"", `,"input":null`} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", capability, input, stream), func(t *testing.T) {
					handler, fixture := environmentCreationHandler(t, "codex", WithExecution(&inputRecorder{}), WithEnvironmentRemoteURL(environmentOrigin))
					server := httptest.NewServer(handler)
					defer server.Close()
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					body := fmt.Sprintf(`{"agent":{"model":"MiniMax-M3"},"environment":{"type":"self_hosted","workspace_directory":"/remote/workspace"%s},"stream":%t%s}`, capability, stream, input)
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/agents/sessions", strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					request.Host = "forged.example"
					request.Header.Set("X-Forwarded-Host", "forged.example")
					request.Header.Set("Authorization", "Bearer key")
					request.Header.Set("OpenAI-Beta", "agents=v1")
					request.Header.Set("Idempotency-Key", "empty-environment")
					response, err := server.Client().Do(request)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if response.StatusCode != http.StatusOK {
						t.Fatal("empty creation rejected", response.StatusCode)
					}
					var session v1.Session
					if stream {
						if response.Header.Get("Content-Type") != "text/event-stream" {
							t.Fatal("streamed creation returned a non-stream response")
						}
						scanner := bufio.NewScanner(response.Body)
						for scanner.Scan() {
							if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
								var event v1.SessionEvent
								if json.Unmarshal([]byte(data), &event) != nil || event.Type != "agent.session.created" || event.Session == nil {
									t.Fatal("invalid creation event", data)
								}
								session = *event.Session
								break
							}
						}
					} else if err := json.NewDecoder(response.Body).Decode(&session); err != nil {
						t.Fatal(err)
					}
					fixture.mu.Lock()
					created, persisted := fixture.session, fixture.input
					fixture.mu.Unlock()
					if session.ID != created.ID || session.Status != "idle" || session.Error != nil || session.RequiredActions == nil || len(session.RequiredActions) != 0 || len(persisted.InitialInputs) != 0 || created.LastTurn != nil {
						t.Fatal("empty creation invented execution activity", session, persisted)
					}
					if persisted.Engine != "codex" || persisted.Creator.Kind != "service_account" || persisted.Creator.ID != "test-runner" || persisted.IdempotencyKey != "empty-environment" {
						t.Fatal("creation lost engine or authenticated identity", persisted)
					}
					if session.Environment.ID != created.Environment.ID || session.Environment.RemoteURL != environmentOrigin || session.Environment.WorkspaceDirectory != "/remote/workspace" || session.Environment.CapabilityDirectories == nil || *session.Environment.CapabilityDirectories == nil || len(*session.Environment.CapabilityDirectories) != 0 {
						t.Fatal("creation did not use the owned Environment and configured origin", session.Environment)
					}
					value := string(created.Environment.Configuration)
					if canonical == "" {
						canonical = value
					}
					if value != canonical {
						t.Fatal("omitted/null/empty capability defaults changed retry configuration", value, canonical)
					}
				})
			}
		}
	}
}

func TestSelfHostedCreationRejectsBeforePersistence(t *testing.T) {
	validEnvironment := `{"type":"self_hosted","workspace_directory":"/remote/workspace"}`
	for _, tc := range []struct {
		name, environment, agentFields, input, engine string
	}{
		{name: "missing environment", environment: ""},
		{name: "null environment", environment: "null"},
		{name: "array environment", environment: "[]"},
		{name: "missing type", environment: `{"workspace_directory":"/remote/workspace"}`},
		{name: "null type", environment: `{"type":null,"workspace_directory":"/remote/workspace"}`},
		{name: "missing workspace", environment: `{"type":"self_hosted"}`},
		{name: "null workspace", environment: `{"type":"self_hosted","workspace_directory":null}`},
		{name: "numeric workspace", environment: `{"type":"self_hosted","workspace_directory":1}`},
		{name: "relative workspace", environment: `{"type":"self_hosted","workspace_directory":"relative"}`},
		{name: "tilde workspace", environment: `{"type":"self_hosted","workspace_directory":"~/project"}`},
		{name: "nul workspace", environment: `{"type":"self_hosted","workspace_directory":"/remote/\u0000"}`},
		{name: "newline workspace placement", environment: `{"type":"self_hosted","workspace_directory":"/remote/\n"}`},
		{name: "carriage return workspace placement", environment: `{"type":"self_hosted","workspace_directory":"/remote/\r"}`},
		{name: "backslash workspace placement", environment: `{"type":"self_hosted","workspace_directory":"/remote/\\"}`},
		{name: "capabilities", environment: `{"type":"self_hosted","workspace_directory":"/remote/workspace","capability_directories":["/remote/skills"]}`},
		{name: "null capability entry", environment: `{"type":"self_hosted","workspace_directory":"/remote/workspace","capability_directories":[null]}`},
		{name: "scalar capabilities", environment: `{"type":"self_hosted","workspace_directory":"/remote/workspace","capability_directories":"/remote/skills"}`},
		{name: "output field", environment: `{"type":"self_hosted","workspace_directory":"/remote/workspace","remote_url":"https://forged.example"}`},
		{name: "none extra field", environment: `{"type":"none","workspace_directory":null}`},
		{name: "initial image", environment: validEnvironment, input: `,"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/image.png"}]}]`},
		{name: "initial assistant message", environment: validEnvironment, input: `,"input":[{"role":"assistant","content":[{"type":"input_text","text":"start"}]}]`},
		{name: "deferred functions", environment: validEnvironment, agentFields: `,"tools":[{"type":"function","name":"lookup","description":"Find a value","parameters":{"type":"object"},"defer_loading":true}]`},
		{name: "Claude SDK placement", environment: validEnvironment, engine: "claude_sdk"},
		{name: "Claude Code placement", environment: validEnvironment, engine: "claude_code"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				engine := tc.engine
				if engine == "" {
					engine = "codex"
				}
				handler, fixture := environmentCreationHandler(t, engine, WithExecution(&inputRecorder{}), WithEnvironmentRemoteURL(environmentOrigin))
				environment := ""
				if tc.environment != "" {
					environment = `,"environment":` + tc.environment
				}
				body := fmt.Sprintf(`{"agent":{"model":"MiniMax-M3"%s},"stream":%t%s%s}`, tc.agentFields, stream, environment, tc.input)
				request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer key")
				request.Header.Set("OpenAI-Beta", "agents=v1")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest || fixture.input.Engine != "" || fixture.session.ID != "" {
					t.Fatal("unsupported configuration reached persistence", response.Code, response.Body.String(), fixture.input)
				}
			})
		}
	}
}

func TestSelfHostedCreationRequiresOperatorExecution(t *testing.T) {
	for _, options := range [][]Option{nil, {WithExecution(&inputRecorder{})}, {WithEnvironmentRemoteURL(environmentOrigin)}} {
		for _, stream := range []bool{false, true} {
			handler, fixture := environmentCreationHandler(t, "codex", options...)
			body := fmt.Sprintf(`{"agent":{"model":"MiniMax-M3"},"environment":{"type":"self_hosted","workspace_directory":"/remote/workspace"},"stream":%t}`, stream)
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var failure v1.ErrorResponse
			if response.Code != http.StatusServiceUnavailable || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code != "execution_unavailable" || fixture.input.Engine != "" {
				t.Fatal("operator prerequisites did not fail before persistence", response.Code, response.Body.String(), fixture.input)
			}
		}
	}
}

func TestHostedCreationRequiresOperatorExecution(t *testing.T) {
	for _, stream := range []bool{false, true} {
		handler, fixture := environmentCreationHandler(t, "codex", WithExecution(&inputRecorder{}), WithEnvironmentRemoteURL(environmentOrigin))
		body := fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"stream":%t}`, stream)
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("OpenAI-Beta", "agents=v1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var failure v1.ErrorResponse
		if response.Code != http.StatusServiceUnavailable || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code != "execution_unavailable" || fixture.input.Engine != "" {
			t.Fatal("hosted configuration bypassed operator prerequisites", response.Code, response.Body.String())
		}
	}
}
