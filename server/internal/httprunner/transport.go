package httprunner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Send performs the HTTP protocol without persisting a run. The caller owns
// completion, so both the legacy runner and the conversation dispatcher can use it.
func Send(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, input AgentRequest) (AgentResponse, error) {
	if !store.ValidHTTPAgentEndpoint(endpoint) {
		return AgentResponse{}, ErrInvalidEndpoint
	}
	body, err := json.Marshal(input)
	if err != nil {
		return AgentResponse{}, ErrRequestFailed
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return AgentResponse{}, ErrInvalidEndpoint
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if client == nil {
		client = http.DefaultClient
	}
	localClient := *client
	// Never forward endpoint credentials through a redirect, including same-host redirects.
	localClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := localClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return AgentResponse{}, ctx.Err()
		}
		return AgentResponse{}, ErrRequestFailed
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AgentResponse{}, fmt.Errorf("%w (%d)", ErrNon2xx, response.StatusCode)
	}
	const maxResponseBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return AgentResponse{}, ErrRequestFailed
	}
	if len(data) > maxResponseBytes {
		return AgentResponse{}, fmt.Errorf("%w: response exceeds 4 MiB", ErrInvalidJSON)
	}
	var result AgentResponse
	if err := json.Unmarshal(data, &result); err != nil || strings.TrimSpace(result.Content) == "" {
		return AgentResponse{}, ErrInvalidJSON
	}
	return result, nil
}
