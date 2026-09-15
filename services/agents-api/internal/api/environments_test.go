package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type environmentResourceFixture struct {
	ResourceStore
	environment store.Environment
	err         error
	tenant, id  string
	calls       int
}

func (f *environmentResourceFixture) GetEnvironment(_ context.Context, tenant, id string) (store.Environment, error) {
	f.tenant, f.id = tenant, id
	f.calls++
	return f.environment, f.err
}

func environmentResourceHandler(t *testing.T) (http.Handler, *environmentResourceFixture) {
	t.Helper()
	f := &environmentResourceFixture{environment: store.Environment{
		ID: uuid.NewString(), TenantID: uuid.NewString(), SessionID: uuid.NewString(), Status: "pending",
		Configuration: json.RawMessage(`{"type":"self_hosted","workspace_directory":"/private/workspace"}`),
	}}
	auth, err := NewAuthenticator([]APIKey{{
		OrganizationID: "resource-org", ProjectID: "resource-project", SubjectKind: "user", SubjectID: "resource-reader",
		TokenSHA256: device.HashCredential("resource-key"), TenantID: f.environment.TenantID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(f, auth, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func TestEnvironmentResourceExactProjectionWithoutExecution(t *testing.T) {
	for _, status := range []string{"pending", "connected", "disconnected", "expired", "failed"} {
		t.Run(status, func(t *testing.T) {
			h, f := environmentResourceHandler(t)
			f.environment.Status = status
			request := httptest.NewRequest(http.MethodGet, "/v1/agents/environments/"+f.environment.ID, nil)
			request.Header.Set("Authorization", "Bearer resource-key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			request.Header.Set("X-Tenant-ID", "untrusted-tenant")
			request.Host = "untrusted.example"
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			var got map[string]any
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &got) != nil {
				t.Fatal("resource read failed", w.Code, w.Body)
			}
			want := map[string]any{
				"id": f.environment.ID, "object": "agent.environment", "type": "self_hosted", "status": status,
				"files": []any{}, "plugins": []any{}, "skills": []any{},
			}
			if !reflect.DeepEqual(got, want) || f.calls != 1 || f.tenant != f.environment.TenantID || f.id != f.environment.ID {
				t.Fatal("resource projection or authenticated lookup changed", got, f)
			}
		})
	}
	for _, capabilities := range []string{"", `,"capability_directories":null`, `,"capability_directories":[]`} {
		_, f := environmentResourceHandler(t)
		f.environment.Configuration = json.RawMessage(`{"type":"self_hosted","workspace_directory":"/workspace"` + capabilities + `}`)
		value, err := environmentResponse(f.environment)
		if err != nil || value.Files == nil || value.Plugins == nil || value.Skills == nil {
			t.Fatal("equivalent empty installation profile lost required arrays", value, err)
		}
	}
}

func TestEnvironmentResourceRejectsUnknownInventoryAndInvalidState(t *testing.T) {
	for name, configuration := range map[string]string{
		"missing":      `{}`,
		"none":         `{"type":"none"}`,
		"hosted":       `{"type":"openai_hosted","env":{"SECRET":"private-canary"}}`,
		"capabilities": `{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":["/skills"]}`,
		"files":        `{"type":"self_hosted","workspace_directory":"/workspace","files":[{"data":"private-canary"}]}`,
		"plugins":      `{"type":"self_hosted","workspace_directory":"/workspace","plugins":[]}`,
		"skills":       `{"type":"self_hosted","workspace_directory":"/workspace","skills":[]}`,
		"wrong type":   `{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":false}`,
		"invalid":      `{"type":"self_hosted","workspace_directory":`,
	} {
		t.Run(name, func(t *testing.T) {
			h, f := environmentResourceHandler(t)
			f.environment.Configuration = json.RawMessage(configuration)
			request := httptest.NewRequest(http.MethodGet, "/v1/agents/environments/"+f.environment.ID, nil)
			request.Header.Set("Authorization", "Bearer resource-key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			var response map[string]json.RawMessage
			if w.Code != 500 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response["error"] == nil || len(response) != 1 || strings.Contains(w.Body.String(), "private-canary") {
				t.Fatal("unsupported inventory became empty metadata or leaked configuration", w.Code, w.Body)
			}
		})
	}
	for _, status := range []string{"", "ready", "unknown"} {
		_, f := environmentResourceHandler(t)
		f.environment.Status = status
		if _, err := environmentResponse(f.environment); err == nil {
			t.Fatal("event or unknown status accepted as a resource status", status)
		}
	}
}

func TestEnvironmentResourceRequestAndStoreErrors(t *testing.T) {
	for _, test := range []struct {
		name, method, suffix, auth, beta string
		storeError                       error
		status, calls                    int
	}{
		{"no auth", "GET", "", "", "agents=v1", nil, 401, 0},
		{"wrong auth", "GET", "", "Bearer unknown", "agents=v1", nil, 401, 0},
		{"no beta", "GET", "", "Bearer resource-key", "", nil, 400, 0},
		{"wrong beta", "GET", "", "Bearer resource-key", "agents=v2", nil, 400, 0},
		{"query", "GET", "?tenant_id=foreign", "Bearer resource-key", "agents=v1", nil, 400, 0},
		{"method", "POST", "", "Bearer resource-key", "agents=v1", nil, 405, 0},
		{"not found", "GET", "", "Bearer resource-key", "agents=v1", store.ErrNotFound, 404, 1},
		{"invalid id", "GET", "", "Bearer resource-key", "agents=v1", store.ErrInvalidInput, 400, 1},
		{"backend", "GET", "", "Bearer resource-key", "agents=v1", errors.New("private-backend-canary"), 500, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, f := environmentResourceHandler(t)
			f.err = test.storeError
			request := httptest.NewRequest(test.method, "/v1/agents/environments/"+f.environment.ID+test.suffix, nil)
			request.Header.Set("Authorization", test.auth)
			request.Header.Set("OpenAI-Beta", test.beta)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			if w.Code != test.status || f.calls != test.calls || strings.Contains(w.Body.String(), "private-backend-canary") {
				t.Fatal("request or shared error handling changed", w.Code, w.Body, f.calls)
			}
		})
	}
}
