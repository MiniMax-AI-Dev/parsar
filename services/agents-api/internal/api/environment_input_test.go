package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestPublicEnvironmentInputFailureMappings(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{execution.ErrEnvironmentInputExpired, http.StatusConflict, "environment_input_expired"},
		{execution.ErrEnvironmentInputCancelled, http.StatusConflict, "environment_input_cancelled"},
		{execution.ErrExecutionUnavailable, http.StatusServiceUnavailable, "execution_unavailable"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			recorder := &inputRecorder{err: fmt.Errorf("submission: %w", tc.err)}
			handler, _, _ := testHandler(t, WithExecution(recorder))
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session/events", strings.NewReader(`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"Start"}]}]}]}`))
			request.Header.Set("Authorization", "Bearer test-api-key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			request.Header.Set("Idempotency-Key", "retained-request")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status || !strings.Contains(response.Body.String(), `"code":"`+tc.code+`"`) || recorder.key != "retained-request" {
				t.Fatal("submission failure was not mapped", response.Code, response.Body.String(), recorder.key)
			}
		})
	}
}

type waitingEnvironmentInput struct {
	InputSubmitter
	entered chan struct{}
	release chan struct{}
}

func (s *waitingEnvironmentInput) SubmitInputs(ctx context.Context, _, _, _ string, _ []store.Input) ([]store.InputReceipt, error) {
	close(s.entered)
	select {
	case <-s.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSelfHostedInputWaitExtendsOnlyItsResponseDeadline(t *testing.T) {
	for _, environment := range []string{"none", "self_hosted"} {
		t.Run(environment, func(t *testing.T) {
			waiting := &waitingEnvironmentInput{entered: make(chan struct{}), release: make(chan struct{})}
			handler, fixture := environmentCreationHandler(t, "codex", WithExecution(waiting), WithEnvironmentRemoteURL(environmentOrigin))
			environmentJSON := `{"type":"none"}`
			if environment == "self_hosted" {
				environmentJSON = `{"type":"self_hosted","workspace_directory":"/remote/workspace"}`
			}
			create := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"MiniMax-M3"},"environment":`+environmentJSON+`}`))
			create.Header.Set("Authorization", "Bearer key")
			create.Header.Set("OpenAI-Beta", "agents=v1")
			created := httptest.NewRecorder()
			handler.ServeHTTP(created, create)
			if created.Code != http.StatusOK {
				t.Fatal("fixture creation failed", created.Code, created.Body.String())
			}
			server := httptest.NewUnstartedServer(handler)
			server.Config.WriteTimeout = 50 * time.Millisecond
			server.Start()
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/agents/sessions/"+fixture.session.ID+"/events", strings.NewReader(`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"Start"}]}]}]}`))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			type result struct {
				response *http.Response
				err      error
			}
			completed := make(chan result, 1)
			go func() {
				response, err := server.Client().Do(request)
				completed <- result{response, err}
			}()
			select {
			case <-waiting.entered:
			case <-ctx.Done():
				t.Fatal("submission did not enter its wait")
			}
			select {
			case outcome := <-completed:
				if outcome.response != nil {
					_ = outcome.response.Body.Close()
				}
				t.Fatal("input returned before admission", outcome.err)
			case <-time.After(100 * time.Millisecond):
			}
			close(waiting.release)
			select {
			case outcome := <-completed:
				if outcome.response != nil {
					defer outcome.response.Body.Close()
				}
				if environment == "self_hosted" {
					if outcome.err != nil || outcome.response.StatusCode != http.StatusNoContent {
						t.Fatal("self-hosted wait lost its response to the ordinary timeout", outcome.err)
					}
				} else if outcome.err == nil {
					t.Fatal("none input unexpectedly replaced the server write deadline", outcome.response.StatusCode)
				}
			case <-ctx.Done():
				t.Fatal("released input did not finish")
			}
		})
	}
}
