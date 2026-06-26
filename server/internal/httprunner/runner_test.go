package httprunner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeStore struct {
	claims      []store.ClaimHTTPAgentRunResult
	invocations map[string]store.HTTPAgentRunInvocation
	completed   []store.CompleteAgentRunInput
	failed      []store.FailAgentRunInput
	models      map[string]store.ModelRuntime
	modelErr    error
}

func (s *fakeStore) ClaimNextQueuedHTTPAgentRun(ctx context.Context) (store.ClaimHTTPAgentRunResult, error) {
	if len(s.claims) == 0 {
		return store.ClaimHTTPAgentRunResult{Claimed: false}, nil
	}
	claim := s.claims[0]
	s.claims = s.claims[1:]
	return claim, nil
}

func (s *fakeStore) GetHTTPAgentRunInvocation(ctx context.Context, runID string) (store.HTTPAgentRunInvocation, error) {
	return s.invocations[runID], nil
}

func (s *fakeStore) CompleteAgentRun(ctx context.Context, input store.CompleteAgentRunInput) (store.CompleteAgentRunResult, error) {
	s.completed = append(s.completed, input)
	return store.CompleteAgentRunResult{RunID: input.RunID, Status: "completed"}, nil
}

func (s *fakeStore) FailAgentRun(ctx context.Context, input store.FailAgentRunInput) error {
	s.failed = append(s.failed, input)
	return nil
}

func (s *fakeStore) ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (store.ModelRuntime, error) {
	if s.modelErr != nil {
		return store.ModelRuntime{}, s.modelErr
	}
	if s.models == nil {
		return store.ModelRuntime{}, errors.New("model not found")
	}
	model, ok := s.models[modelID]
	if !ok {
		return store.ModelRuntime{}, errors.New("model not found")
	}
	return model, nil
}

func TestRunLoopStopsWhenNoQueuedRun(t *testing.T) {
	runtimeStore := &fakeStore{}
	result, err := RunLoop(context.Background(), runtimeStore, nil, LoopOptions{MaxRuns: 5})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempts != 1 || result.Completed != 0 || len(result.Results) != 1 || result.Results[0].Claimed {
		t.Fatalf("expected one no-op attempt, got %+v", result)
	}
}

func TestRunLoopHonorsMaxRuns(t *testing.T) {
	runtimeStore := &fakeStore{
		claims: []store.ClaimHTTPAgentRunResult{
			{RunID: "run-1", Claimed: true},
			{RunID: "run-2", Claimed: true},
			{RunID: "run-3", Claimed: true},
		},
		invocations: map[string]store.HTTPAgentRunInvocation{
			"run-1": invocation("run-1"),
			"run-2": invocation("run-2"),
			"run-3": invocation("run-3"),
		},
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"content":"ok","usage":{"provider":"test"}}`)), Header: make(http.Header)}, nil
	})}

	result, err := RunLoop(context.Background(), runtimeStore, client, LoopOptions{MaxRuns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempts != 2 || result.Completed != 2 || len(runtimeStore.completed) != 2 {
		t.Fatalf("expected exactly two completed runs, got result=%+v completed=%+v", result, runtimeStore.completed)
	}
}

func TestRunOnceMarksClaimedRunFailedWhenInvokeFails(t *testing.T) {
	runtimeStore := &fakeStore{
		claims:      []store.ClaimHTTPAgentRunResult{{RunID: "run-1", Claimed: true}},
		invocations: map[string]store.HTTPAgentRunInvocation{"run-1": invocation("run-1")},
	}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`bad gateway`)), Header: make(http.Header)}, nil
	})}

	result, err := RunOnce(context.Background(), runtimeStore, client)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Claimed || !result.Failed || result.Error != ErrNon2xx.Error() {
		t.Fatalf("expected failed claimed result, got %+v", result)
	}
	if len(runtimeStore.failed) != 1 || runtimeStore.failed[0].RunID != "run-1" || runtimeStore.failed[0].Source != "http_agent" {
		t.Fatalf("expected run failure persisted, got %+v", runtimeStore.failed)
	}
}

func TestInvokePostsHTTPAgentRequestAndCompletesRun(t *testing.T) {
	runtimeStore := &fakeStore{invocations: map[string]store.HTTPAgentRunInvocation{"run-1": invocation("run-1")}}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "http://agent.local/run" {
			t.Fatalf("unexpected request target: %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"run_id":"run-1"`, `"trigger_message_content":"please run"`} {
			if !strings.Contains(string(body), want) {
				t.Fatalf("request body missing %s: %s", want, string(body))
			}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"content":"done","usage":{"provider":"http-test","input_tokens":3}}`)), Header: make(http.Header)}, nil
	})}

	result, err := Invoke(context.Background(), runtimeStore, client, InvokeInput{RunID: "run-1"}, &Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentResponse.Content != "done" || result.Endpoint != "http://agent.local/run" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(runtimeStore.completed) != 1 || runtimeStore.completed[0].RunID != "run-1" || runtimeStore.completed[0].Source != "http_agent" {
		t.Fatalf("unexpected completion: %+v", runtimeStore.completed)
	}
}

func TestInvokeRejectsInvalidEndpoint(t *testing.T) {
	run := invocation("run-1")
	run.AgentConfig = map[string]any{"endpoint": "file:///tmp/not-http"}
	runtimeStore := &fakeStore{invocations: map[string]store.HTTPAgentRunInvocation{"run-1": run}}

	_, err := Invoke(context.Background(), runtimeStore, nil, InvokeInput{RunID: "run-1"})
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("expected ErrInvalidEndpoint, got %v", err)
	}
	if len(runtimeStore.completed) != 0 {
		t.Fatalf("invalid endpoint should not complete a run, got %+v", runtimeStore.completed)
	}
}

func TestInvokeFailClosedForMissingConfiguredModel(t *testing.T) {
	run := invocation("run-1")
	run.AgentConfig["model_id"] = "model-missing"
	runtimeStore := &fakeStore{invocations: map[string]store.HTTPAgentRunInvocation{"run-1": run}, modelErr: errors.New("disabled")}

	_, err := Invoke(context.Background(), runtimeStore, nil, InvokeInput{RunID: "run-1"})
	if err == nil || !strings.Contains(err.Error(), "model disabled or missing") {
		t.Fatalf("expected model fail-closed error, got %v", err)
	}
	if len(runtimeStore.completed) != 0 {
		t.Fatalf("missing model should not complete a run, got %+v", runtimeStore.completed)
	}
}

func invocation(runID string) store.HTTPAgentRunInvocation {
	return store.HTTPAgentRunInvocation{
		RunID:                 runID,
		WorkspaceID:           "workspace-1",
		ProjectID:             "project-1",
		ConversationID:        "conversation-1",
		ProjectAgentID:        "project-agent-1",
		AgentID:               "agent-1",
		AgentName:             "Backend Agent",
		AgentSlug:             "backend-agent",
		ConnectorType:         "http",
		Status:                "running",
		TriggerMessageContent: "please run",
		AgentConfig:           map[string]any{"endpoint": "http://agent.local/run"},
		ProjectAgentConfig:    map[string]any{},
	}
}
