package httprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Deps is an empty placeholder kept for variadic backwards compatibility
// with older RunOnce / RunLoop / Invoke callers.
type Deps struct{}

type Store interface {
	ClaimNextQueuedHTTPAgentRun(ctx context.Context) (store.ClaimHTTPAgentRunResult, error)
	GetHTTPAgentRunInvocation(ctx context.Context, runID string) (store.HTTPAgentRunInvocation, error)
	CompleteAgentRun(ctx context.Context, input store.CompleteAgentRunInput) (store.CompleteAgentRunResult, error)
	FailAgentRun(ctx context.Context, input store.FailAgentRunInput) error
	ResolveModelRuntime(ctx context.Context, workspaceID string, modelID string) (store.ModelRuntime, error)
}

type InvokeInput struct {
	RunID    string
	Endpoint string
	Headers  map[string]string
}

type AgentRequest struct {
	RunID                 string         `json:"run_id"`
	WorkspaceID           string         `json:"workspace_id"`
	ProjectID             string         `json:"project_id"`
	ConversationID        string         `json:"conversation_id"`
	ProjectAgentID        string         `json:"project_agent_id"`
	AgentID               string         `json:"agent_id"`
	AgentName             string         `json:"agent_name"`
	AgentSlug             string         `json:"agent_slug"`
	TriggerMessageContent string         `json:"trigger_message_content"`
	AgentConfig           map[string]any `json:"agent_config"`
	ProjectAgentConfig    map[string]any `json:"project_agent_config"`
}

type AgentResponse struct {
	Content string           `json:"content"`
	Usage   store.UsageInput `json:"usage"`
}

type Result struct {
	Claimed       bool                         `json:"claimed"`
	Failed        bool                         `json:"failed,omitempty"`
	Error         string                       `json:"error,omitempty"`
	Endpoint      string                       `json:"endpoint,omitempty"`
	AgentResponse AgentResponse                `json:"agent_response,omitempty"`
	Completion    store.CompleteAgentRunResult `json:"completion,omitempty"`
}

type LoopOptions struct {
	Interval time.Duration
	MaxRuns  int
}

type LoopResult struct {
	Attempts  int      `json:"attempts"`
	Completed int      `json:"completed"`
	Results   []Result `json:"results"`
}

var ErrInvalidEndpoint = errors.New("endpoint must be an http(s) URL")
var ErrRequestFailed = errors.New("http agent request failed")
var ErrNon2xx = errors.New("http agent returned non-2xx status")
var ErrInvalidJSON = errors.New("http agent returned invalid json")

func RunOnce(ctx context.Context, runtimeStore Store, client *http.Client, deps ...*Deps) (Result, error) {
	claim, err := runtimeStore.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil {
		return Result{}, err
	}
	if !claim.Claimed {
		return Result{Claimed: false}, nil
	}

	result, err := Invoke(ctx, runtimeStore, client, InvokeInput{RunID: claim.RunID}, deps...)
	if err != nil {
		if failErr := runtimeStore.FailAgentRun(ctx, store.FailAgentRunInput{RunID: claim.RunID, Source: "http_agent", Reason: err.Error()}); failErr != nil {
			return Result{}, failErr
		}
		return Result{Claimed: true, Failed: true, Error: err.Error()}, nil
	}
	result.Claimed = true
	return result, nil
}

func RunLoop(ctx context.Context, runtimeStore Store, client *http.Client, options LoopOptions, deps ...*Deps) (LoopResult, error) {
	maxRuns := options.MaxRuns
	if maxRuns <= 0 {
		maxRuns = 1
	}
	var result LoopResult
	for result.Attempts < maxRuns {
		runResult, err := RunOnce(ctx, runtimeStore, client, deps...)
		if err != nil {
			return result, err
		}
		result.Attempts++
		result.Results = append(result.Results, runResult)
		if !runResult.Claimed {
			break
		}
		if !runResult.Failed {
			result.Completed++
		}
		if result.Attempts >= maxRuns {
			break
		}
		if options.Interval > 0 {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(options.Interval):
			}
		}
	}
	return result, nil
}

func Invoke(ctx context.Context, runtimeStore Store, client *http.Client, input InvokeInput, deps ...*Deps) (Result, error) {
	invocation, err := runtimeStore.GetHTTPAgentRunInvocation(ctx, input.RunID)
	if err != nil {
		return Result{}, err
	}
	if err := ensureConfiguredModelActive(ctx, runtimeStore, invocation); err != nil {
		return Result{}, err
	}

	endpoint := strings.TrimSpace(input.Endpoint)
	if endpoint == "" {
		endpoint = stringFromConfig(invocation.ProjectAgentConfig, "endpoint")
	}
	if endpoint == "" {
		endpoint = stringFromConfig(invocation.AgentConfig, "endpoint")
	}
	if !isSafeEndpoint(endpoint) {
		return Result{}, ErrInvalidEndpoint
	}

	body, err := json.Marshal(AgentRequest{
		RunID:                 invocation.RunID,
		WorkspaceID:           invocation.WorkspaceID,
		ProjectID:             invocation.ProjectID,
		ConversationID:        invocation.ConversationID,
		ProjectAgentID:        invocation.ProjectAgentID,
		AgentID:               invocation.AgentID,
		AgentName:             invocation.AgentName,
		AgentSlug:             invocation.AgentSlug,
		TriggerMessageContent: invocation.TriggerMessageContent,
		AgentConfig:           invocation.AgentConfig,
		ProjectAgentConfig:    invocation.ProjectAgentConfig,
	})
	if err != nil {
		return Result{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, ErrInvalidEndpoint
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for key, value := range input.Headers {
		if key = strings.TrimSpace(key); key != "" {
			httpReq.Header.Set(key, value)
		}
	}

	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(httpReq)
	if err != nil {
		return Result{}, ErrRequestFailed
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Result{}, ErrNon2xx
	}

	var agentResponse AgentResponse
	if err := json.NewDecoder(response.Body).Decode(&agentResponse); err != nil {
		return Result{}, ErrInvalidJSON
	}
	completed, err := runtimeStore.CompleteAgentRun(ctx, store.CompleteAgentRunInput{
		RunID:   input.RunID,
		Source:  "http_agent",
		Content: agentResponse.Content,
		Usage:   agentResponse.Usage,
	})
	if err != nil {
		return Result{}, err
	}

	return Result{Claimed: true, Endpoint: endpoint, AgentResponse: agentResponse, Completion: completed}, nil
}

func ensureConfiguredModelActive(ctx context.Context, runtimeStore Store, invocation store.HTTPAgentRunInvocation) error {
	modelID := stringFromConfig(invocation.AgentConfig, "model_id")
	if modelID == "" {
		return nil
	}
	if _, err := runtimeStore.ResolveModelRuntime(ctx, invocation.WorkspaceID, modelID); err != nil {
		return fmt.Errorf("model disabled or missing for run %s (model_id=%s): %w", invocation.RunID, modelID, err)
	}
	return nil
}

func stringFromConfig(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return strings.TrimSpace(value)
	}
	if nested, ok := config["http"].(map[string]any); ok {
		if value, ok := nested[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isSafeEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}
