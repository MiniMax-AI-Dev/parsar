package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// testModelHTTPClient is overridable by tests so the unit tests can point the
// connectivity check at a httptest.Server instead of reaching the real upstream.
var testModelHTTPClient = &http.Client{Timeout: 15 * time.Second}

func isOpenAIChatCompletionsAdapter(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case "openai", "openai_compatible", "openai-compatible", "@ai-sdk/openai", "@ai-sdk/openai-compatible":
		return true
	default:
		return false
	}
}

func isAnthropicMessagesAdapter(adapter string) bool {
	switch strings.TrimSpace(adapter) {
	case "anthropic", "anthropic_compatible", "anthropic-compatible", "@ai-sdk/anthropic":
		return true
	default:
		return false
	}
}

func anthropicMessagesURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(base, "/v1/messages"), strings.HasSuffix(base, "/messages"):
		return base
	case strings.HasSuffix(base, "/v1"):
		return base + "/messages"
	default:
		return base + "/v1/messages"
	}
}

type connectivityTestResponse struct {
	Supported    bool                       `json:"supported"`
	Success      bool                       `json:"success"`
	LatencyMS    int64                      `json:"latency_ms"`
	Status       int                        `json:"http_status,omitempty"`
	EndpointType string                     `json:"endpoint_type,omitempty"`
	Error        string                     `json:"error,omitempty"`
	Sample       string                     `json:"sample,omitempty"`
	HealthyCount int                        `json:"healthy_count"`
	TotalCount   int                        `json:"total_count"`
	Results      []connectivityEndpointTest `json:"results"`
}

type connectivityEndpointTest struct {
	EndpointType string                   `json:"endpoint_type"`
	Supported    bool                     `json:"supported"`
	Success      bool                     `json:"success"`
	LatencyMS    int64                    `json:"latency_ms"`
	Status       int                      `json:"http_status,omitempty"`
	FailureStage string                   `json:"failure_stage,omitempty"`
	Error        string                   `json:"error,omitempty"`
	Sample       string                   `json:"sample,omitempty"`
	Request      connectivityHTTPRequest  `json:"request"`
	Response     connectivityHTTPResponse `json:"response,omitempty"`
}

type connectivityHTTPRequest struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    map[string]any    `json:"body,omitempty"`
}

type connectivityHTTPResponse struct {
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      any               `json:"body,omitempty"`
	RawBody   string            `json:"raw_body,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

const connectivityResponseBodyLimit = 8 * 1024
const modelProbeMaxTokens = 2

// testModelConnectivity sends a minimal request to the upstream provider so the
// admin can verify base_url + api_key + custom headers + model_key without
// driving a full Agent Run.
//
//	@Summary		Test a model's connectivity
//	@Description	Runs a live probe against the model's configured provider and credentials. Owner/admin only.
//	@Tags			models
//	@ID				testDevModelConnectivity
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			modelID		path	string	true	"Model UUID"
//	@Success		200 {object} map[string]interface{} "Probe result"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Model not found"
//	@Router			/api/v1/workspaces/{workspaceID}/models/{modelID}/test [post]
func testModelConnectivity(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore)
		if !ok {
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		result, err := probeModelConnectivity(r.Context(), runtimeStore, workspaceID, modelID, actorIDFromRequest(r))
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if _, err := runtimeStore.UpdateModelHealth(r.Context(), modelID, modelHealthFromProbe(result)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to persist model health"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func persistModelHealth(ctx context.Context, runtimeStore RuntimeStore, workspaceID string, model store.ModelRead, callerUserID string) store.ModelRead {
	result, err := probeModelConnectivity(ctx, runtimeStore, workspaceID, model.ID, callerUserID)
	if err != nil {
		return model
	}
	updated, err := runtimeStore.UpdateModelHealth(ctx, model.ID, modelHealthFromProbe(result))
	if err != nil {
		return model
	}
	return updated
}

func modelHealthFromProbe(result connectivityTestResponse) map[string]any {
	status := "failed"
	if result.Success {
		status = "healthy"
	} else if !result.Supported {
		status = "unsupported"
	}
	health := map[string]any{
		"status":        status,
		"checked_at":    time.Now().UTC().Format(time.RFC3339),
		"latency_ms":    result.LatencyMS,
		"supported":     result.Supported,
		"healthy_count": result.HealthyCount,
		"total_count":   result.TotalCount,
	}
	if result.Status != 0 {
		health["http_status"] = result.Status
	}
	if result.EndpointType != "" {
		health["endpoint_type"] = result.EndpointType
	}
	if result.Error != "" {
		health["error"] = result.Error
	}
	if result.Sample != "" {
		health["sample"] = result.Sample
	}
	if len(result.Results) > 0 {
		summaries := make([]map[string]any, 0, len(result.Results))
		for _, item := range result.Results {
			summary := map[string]any{
				"endpoint_type": item.EndpointType,
				"supported":     item.Supported,
				"success":       item.Success,
				"latency_ms":    item.LatencyMS,
			}
			if item.Status != 0 {
				summary["http_status"] = item.Status
			}
			if item.FailureStage != "" {
				summary["failure_stage"] = item.FailureStage
			}
			if item.Error != "" {
				summary["error"] = item.Error
			}
			summaries = append(summaries, summary)
		}
		health["results_summary"] = summaries
	}
	return health
}

func probeModelConnectivity(ctx context.Context, runtimeStore RuntimeStore, workspaceID string, modelID string, callerUserID string) (connectivityTestResponse, error) {
	mr, err := runtimeStore.ResolveModelRuntimeForUser(ctx, modelID, callerUserID)
	if err != nil {
		if errors.Is(err, store.ErrModelDisabled) {
			return aggregateConnectivityResults([]connectivityEndpointTest{{
				Supported:    true,
				Success:      false,
				FailureStage: "credential",
				Error:        err.Error(),
			}}), nil
		}
		if errors.Is(err, store.ErrUnknownModel) {
			return connectivityTestResponse{}, err
		}
		return connectivityTestResponse{}, errors.New("failed to resolve model")
	}

	endpointTypes := modelProbeEndpointTypes(mr)
	if len(endpointTypes) == 0 {
		return aggregateConnectivityResults([]connectivityEndpointTest{{
			Supported:    false,
			Success:      false,
			FailureStage: "unsupported",
			Error:        "connectivity test only supports OpenAI chat-completions, OpenAI responses, and Anthropic messages compatible providers",
		}}), nil
	}

	var encryptedPayload []byte
	if mr.CredentialMode == "credential_ref" {
		encryptedPayload = mr.EncryptedPayload
	} else {
		if mr.SecretID == "" {
			return aggregateCredentialFailure(endpointTypes, "no API key bound to this model"), nil
		}
		sp, err := runtimeStore.GetSecretPayload(ctx, workspaceID, mr.SecretID)
		if err != nil {
			return aggregateCredentialFailure(endpointTypes, fmt.Sprintf("failed to fetch secret: %v", err)), nil
		}
		encryptedPayload = sp.EncryptedPayload
	}

	secretService, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
	if err != nil {
		return connectivityTestResponse{}, errors.New("secrets service unavailable: " + err.Error())
	}
	payload, err := secretService.Decrypt(encryptedPayload)
	if err != nil {
		return aggregateCredentialFailure(endpointTypes, "failed to decrypt credential: "+err.Error()), nil
	}
	apiKey, _ := payload["api_key"].(string)
	if strings.TrimSpace(apiKey) == "" {
		if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return aggregateCredentialFailure(endpointTypes, "credential payload missing api_key / value field"), nil
	}

	results := make([]connectivityEndpointTest, 0, len(endpointTypes))
	for _, endpointType := range endpointTypes {
		results = append(results, probeModelEndpoint(ctx, mr, endpointType, apiKey))
	}
	return aggregateConnectivityResults(results), nil
}

func modelProbeEndpointTypes(mr store.ModelRuntime) []string {
	raw := endpointTypesFromAny(mr.ProviderConfig["supported_endpoint_types"])
	if len(raw) == 0 {
		switch {
		case isAnthropicMessagesAdapter(mr.Adapter):
			raw = []string{"anthropic"}
		case isOpenAIChatCompletionsAdapter(mr.Adapter):
			raw = []string{"openai"}
		}
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, endpointType := range raw {
		normalized := normalizeEndpointType(endpointType)
		switch normalized {
		case "openai", "openai-response", "anthropic":
			if !seen[normalized] {
				seen[normalized] = true
				out = append(out, normalized)
			}
		}
	}
	return out
}

func aggregateCredentialFailure(endpointTypes []string, msg string) connectivityTestResponse {
	results := make([]connectivityEndpointTest, 0, len(endpointTypes))
	for _, endpointType := range endpointTypes {
		results = append(results, connectivityEndpointTest{
			EndpointType: endpointType,
			Supported:    true,
			Success:      false,
			FailureStage: "credential",
			Error:        msg,
		})
	}
	return aggregateConnectivityResults(results)
}

func aggregateConnectivityResults(results []connectivityEndpointTest) connectivityTestResponse {
	out := connectivityTestResponse{Results: results, TotalCount: len(results)}
	for _, result := range results {
		out.LatencyMS += result.LatencyMS
		if result.Supported {
			out.Supported = true
		}
		if result.Success {
			out.Success = true
			out.HealthyCount++
			if out.EndpointType == "" {
				out.EndpointType = result.EndpointType
				out.Status = result.Status
				out.Sample = result.Sample
			}
			continue
		}
		if out.Error == "" && result.Error != "" {
			out.EndpointType = result.EndpointType
			out.Status = result.Status
			out.Error = result.Error
		}
	}
	if out.Error == "" && !out.Success && len(results) > 0 {
		out.Error = results[0].Error
		out.EndpointType = results[0].EndpointType
		out.Status = results[0].Status
	}
	return out
}

func probeModelEndpoint(ctx context.Context, mr store.ModelRuntime, endpointType, apiKey string) connectivityEndpointTest {
	url, body := modelProbeRequestSpec(mr, endpointType)
	result := connectivityEndpointTest{
		EndpointType: endpointType,
		Supported:    true,
		Request: connectivityHTTPRequest{
			Method: http.MethodPost,
			URL:    url,
			Body:   body,
		},
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		result.FailureStage = "request_build"
		result.Error = "failed to build request: " + err.Error()
		return result
	}
	req.Header.Set("Content-Type", "application/json")
	if endpointType == "anthropic" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if hdrs, ok := mr.ProviderConfig["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}
	result.Request.Headers = maskedHeaderMap(req.Header)

	start := time.Now()
	resp, err := testModelHTTPClient.Do(req)
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.FailureStage = "network"
		result.Error = "request failed: " + err.Error()
		return result
	}
	defer resp.Body.Close()

	respBytes, truncated := readLimitedResponseBody(resp.Body)
	parsed, parsedOK := parseJSONBody(respBytes)
	result.Status = resp.StatusCode
	result.Response = connectivityHTTPResponse{
		Status:    resp.StatusCode,
		Headers:   selectedResponseHeaders(resp.Header),
		RawBody:   string(respBytes),
		Truncated: truncated,
	}
	if parsedOK {
		result.Response.Body = parsed
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed["error"] != nil {
		msg := responseErrorMessage(respBytes, parsed)
		if truncated {
			msg += "…"
		}
		result.FailureStage = "upstream_http"
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			result.FailureStage = "upstream_body"
		}
		result.Error = msg
		return result
	}

	result.Success = true
	result.Sample = modelProbeSample(parsed, endpointType)
	return result
}

func modelProbeRequestSpec(mr store.ModelRuntime, endpointType string) (string, map[string]any) {
	switch endpointType {
	case "anthropic":
		return anthropicMessagesURL(endpointBaseURLFromConfig(mr.ProviderConfig, "anthropic", mr.BaseURL)), map[string]any{
			"model":      mr.ModelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": modelProbeMaxTokens,
		}
	case "openai-response":
		return strings.TrimRight(endpointBaseURLFromConfig(mr.ProviderConfig, "openai-response", mr.BaseURL), "/") + "/responses", map[string]any{
			"model":             mr.ModelKey,
			"input":             "ping",
			"max_output_tokens": modelProbeMaxTokens,
		}
	default:
		return strings.TrimRight(endpointBaseURLFromConfig(mr.ProviderConfig, "openai", mr.BaseURL), "/") + "/chat/completions", map[string]any{
			"model":      mr.ModelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": modelProbeMaxTokens,
		}
	}
}

func readLimitedResponseBody(body io.Reader) ([]byte, bool) {
	respBytes, _ := io.ReadAll(io.LimitReader(body, connectivityResponseBodyLimit+1))
	if len(respBytes) > connectivityResponseBodyLimit {
		return respBytes[:connectivityResponseBodyLimit], true
	}
	return respBytes, false
}

func parseJSONBody(body []byte) (map[string]any, bool) {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return map[string]any{}, false
	}
	return parsed, true
}

func responseErrorMessage(respBytes []byte, parsed map[string]any) string {
	msg := strings.TrimSpace(string(respBytes))
	if eo, ok := parsed["error"].(map[string]any); ok {
		if m, ok := eo["message"].(string); ok {
			msg = m
		}
	}
	if msg == "" {
		msg = "upstream returned an empty response body"
	}
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}

func maskedHeaderMap(headers http.Header) map[string]string {
	out := map[string]string{}
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		if isSensitiveHeader(key) {
			out[key] = maskSecret(values[0])
			continue
		}
		out[key] = values[0]
	}
	return out
}

func selectedResponseHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"Content-Type", "X-Request-Id", "Request-Id"} {
		if value := headers.Get(key); value != "" {
			out[key] = value
		}
	}
	return out
}

func isSensitiveHeader(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "authorization") ||
		strings.Contains(k, "api-key") ||
		strings.Contains(k, "token") ||
		strings.Contains(k, "secret") ||
		k == "x-api-key"
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "bearer ") {
		return "Bearer " + maskSecret(strings.TrimSpace(value[7:]))
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func modelProbeSample(parsed map[string]any, endpointType string) string {
	sample := ""
	if endpointType == "anthropic" {
		if content, ok := parsed["content"].([]any); ok {
			for _, part := range content {
				if p, ok := part.(map[string]any); ok {
					if text, ok := p["text"].(string); ok && strings.TrimSpace(text) != "" {
						sample = strings.TrimSpace(text)
						break
					}
				}
			}
		} else if content, ok := parsed["content"].(string); ok {
			sample = strings.TrimSpace(content)
		}
	} else if endpointType == "openai-response" {
		if output, ok := parsed["output_text"].(string); ok {
			sample = strings.TrimSpace(output)
		}
	} else if choices, ok := parsed["choices"].([]any); ok && len(choices) > 0 {
		if first, ok := choices[0].(map[string]any); ok {
			if msg, ok := first["message"].(map[string]any); ok {
				if c, ok := msg["content"].(string); ok {
					sample = strings.TrimSpace(c)
				}
			}
		}
	}
	if len(sample) > 200 {
		sample = sample[:200] + "…"
	}
	return sample
}
