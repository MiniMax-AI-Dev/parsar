package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestHostedEnvironmentDefaultsAndExplicitGaps(t *testing.T) {
	for _, raw := range []string{
		`{"type":"openai_hosted"}`,
		`{"type":"openai_hosted","network":null,"env":null,"files":null,"packages":null,"plugins":null,"skills":null,"setup_commands":null,"capability_directories":null}`,
		`{"type":"openai_hosted","network":{"access":"enabled","allowed_domains":null},"env":{},"files":[],"packages":{"npm":[],"python":null,"system":[]},"plugins":[],"skills":[],"setup_commands":[],"capability_directories":[]}`,
	} {
		got, err := decodeSessionEnvironment(json.RawMessage(raw))
		if err != nil || got.Network == nil || got.Network.Access != "enabled" {
			t.Fatal("hosted defaults changed", got, err)
		}
	}
	got, err := decodeSessionEnvironment(json.RawMessage(`{"type":"openai_hosted","network":{"access":"disabled"}}`))
	if err != nil || got.Network.Access != "disabled" {
		t.Fatal("explicit policy changed", got, err)
	}
	for _, field := range []string{
		`"network":{}`, `"network":{"access":null}`, `"network":{"access":"restricted"}`,
		`"network":{"access":"disabled","allowed_domains":["example.com"]}`, `"network":{"access":"enabled","unknown":true}`,
		`"env":{"SECRET":"value"}`, `"files":[{}]`, `"packages":{"npm":["package"]}`,
		`"packages":{"unknown":[]}`, `"plugins":[{}]`, `"skills":[{}]`, `"setup_commands":["echo test"]`,
		`"capability_directories":["/workspace"]`, `"template_id":"template"`, `"workspace_directory":"/workspace"`,
		`"files":{}`, `"env":[]`, `"packages":[]`, `"network":[]`, `"unknown":null`,
	} {
		if _, err := decodeSessionEnvironment(json.RawMessage(`{"type":"openai_hosted",` + field + `}`)); err == nil {
			t.Fatal("unsupported installation or invalid input accepted", field)
		}
	}
}

func TestHostedEnvironmentResponseHasPinnedShapeAndNoConnectionAction(t *testing.T) {
	s := environmentSession()
	s.Configuration = json.RawMessage(`{"agent":{"id":"agent","model":"model"},"environment":{"type":"openai_hosted"}}`)
	s.Environment.Configuration = json.RawMessage(`{"type":"openai_hosted"}`)
	s.EnvironmentInputActivity = nil
	result, err := sessionResponse(s, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "idle" || len(result.RequiredActions) != 0 {
		t.Fatal("hosted preparation requires a caller connection", result)
	}
	raw, err := json.Marshal(result.Environment)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if json.Unmarshal(raw, &got) != nil {
		t.Fatal("invalid environment JSON")
	}
	expected := map[string]any{"id": "environment", "type": "openai_hosted", "capability_directories": []any{}, "files": []any{}, "plugins": []any{}, "skills": []any{}, "packages": map[string]any{"npm": []any{}, "python": []any{}, "system": []any{}}, "network": map[string]any{"access": "enabled", "allowed_domains": []any{}}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("hosted response shape changed", string(raw))
	}
	for _, mutate := range []func(*store.Environment){
		func(e *store.Environment) { e.TenantID = "foreign" },
		func(e *store.Environment) { e.SessionID = "other" },
		func(e *store.Environment) {
			e.Configuration = json.RawMessage(`{"type":"self_hosted","workspace_directory":"/workspace"}`)
		},
	} {
		invalid := s
		environment := *s.Environment
		invalid.Environment = &environment
		mutate(invalid.Environment)
		if _, err := sessionResponse(invalid, ""); err == nil {
			t.Fatal("hosted association mismatch accepted")
		}
	}
}

// The execution owner must see idle creation as well as initial-input creation.
// Resource persistence alone cannot validate the configured managed deployment.
func TestHostedCreationUsesExecutionAdmissionWithoutInitialInput(t *testing.T) {
	for _, stream := range []bool{false, true} {
		recorder := &hostedCreationRecorder{}
		handler, fixture := environmentCreationHandler(t, "codex", WithHostedEnvironments(), WithExecution(recorder))
		body := fmt.Sprintf(`{"agent":{"model":"model"},"environment":{"type":"openai_hosted"},"stream":%t}`, stream)
		request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer key")
		request.Header.Set("OpenAI-Beta", "agents=v1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		// The recorder deliberately rejects both creation methods. Its rejection
		// proves admission was used; the resource fixture must remain untouched.
		if recorder.calls != 1 || fixture.input.Engine != "" || fixture.session.ID != "" || response.Code != http.StatusBadRequest {
			t.Fatal("idle hosted creation bypassed execution admission", stream, response.Code, response.Body.String())
		}
	}
}

type hostedCreationRecorder struct {
	inputRecorder
	calls int
}

func (r *hostedCreationRecorder) CreateSession(context.Context, string, store.CreateSessionInput) (store.Session, error) {
	r.calls++
	return store.Session{}, store.ErrInvalidInput
}
func (r *hostedCreationRecorder) CreateSessionStream(context.Context, string, store.CreateSessionInput) (store.SessionCreation, error) {
	r.calls++
	return store.SessionCreation{}, store.ErrInvalidInput
}
