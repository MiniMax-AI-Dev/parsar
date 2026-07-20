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
	Supported    bool   `json:"supported"`
	Success      bool   `json:"success"`
	LatencyMS    int64  `json:"latency_ms"`
	Status       int    `json:"http_status,omitempty"`
	EndpointType string `json:"endpoint_type,omitempty"`
	Error        string `json:"error,omitempty"`
	Sample       string `json:"sample,omitempty"`
}

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
		"status":     status,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"latency_ms": result.LatencyMS,
		"supported":  result.Supported,
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
	return health
}

func probeModelConnectivity(ctx context.Context, runtimeStore RuntimeStore, workspaceID string, modelID string, callerUserID string) (connectivityTestResponse, error) {
	mr, err := runtimeStore.ResolveModelRuntimeForUser(ctx, modelID, callerUserID)
	if err != nil {
		if errors.Is(err, store.ErrModelDisabled) {
			return connectivityTestResponse{Supported: true, Success: false, Error: err.Error()}, nil
		}
		if errors.Is(err, store.ErrUnknownModel) {
			return connectivityTestResponse{}, err
		}
		return connectivityTestResponse{}, errors.New("failed to resolve model")
	}

	isOpenAIChatCompatible := isOpenAIChatCompletionsAdapter(mr.Adapter) || modelSupportsEndpointType(mr.ProviderConfig, "openai")
	isOpenAIResponsesCompatible := modelSupportsEndpointType(mr.ProviderConfig, "openai-response")
	isAnthropicCompatible := isAnthropicMessagesAdapter(mr.Adapter) || modelSupportsEndpointType(mr.ProviderConfig, "anthropic")
	if !isOpenAIChatCompatible && !isOpenAIResponsesCompatible && !isAnthropicCompatible {
		return connectivityTestResponse{
			Supported: false,
			Error:     "connectivity test only supports OpenAI chat-completions, OpenAI responses, and Anthropic messages compatible providers",
		}, nil
	}

	var encryptedPayload []byte
	if mr.CredentialMode == "credential_ref" {
		encryptedPayload = mr.EncryptedPayload
	} else {
		if mr.SecretID == "" {
			return connectivityTestResponse{Supported: true, Success: false, Error: "no API key bound to this model"}, nil
		}
		sp, err := runtimeStore.GetSecretPayload(ctx, workspaceID, mr.SecretID)
		if err != nil {
			return connectivityTestResponse{Supported: true, Success: false, Error: fmt.Sprintf("failed to fetch secret: %v", err)}, nil
		}
		encryptedPayload = sp.EncryptedPayload
	}

	secretService, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
	if err != nil {
		return connectivityTestResponse{}, errors.New("secrets service unavailable: " + err.Error())
	}
	payload, err := secretService.Decrypt(encryptedPayload)
	if err != nil {
		return connectivityTestResponse{Supported: true, Success: false, Error: "failed to decrypt credential: " + err.Error()}, nil
	}
	apiKey, _ := payload["api_key"].(string)
	if strings.TrimSpace(apiKey) == "" {
		if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return connectivityTestResponse{Supported: true, Success: false, Error: "credential payload missing api_key / value field"}, nil
	}

	endpointType := "openai"
	url := ""
	body := map[string]any{}
	if isAnthropicCompatible {
		endpointType = "anthropic"
		url = anthropicMessagesURL(endpointBaseURLFromConfig(mr.ProviderConfig, "anthropic", mr.BaseURL))
		body = map[string]any{
			"model":      mr.ModelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": 16,
		}
	} else if isOpenAIResponsesCompatible {
		endpointType = "openai-response"
		url = strings.TrimRight(endpointBaseURLFromConfig(mr.ProviderConfig, "openai-response", mr.BaseURL), "/") + "/responses"
		body = map[string]any{
			"model": mr.ModelKey,
			"input": "ping",
		}
	} else {
		url = strings.TrimRight(endpointBaseURLFromConfig(mr.ProviderConfig, "openai", mr.BaseURL), "/") + "/chat/completions"
		body = map[string]any{
			"model":      mr.ModelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": 16,
		}
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return connectivityTestResponse{Supported: true, Success: false, EndpointType: endpointType, Error: "failed to build request: " + err.Error()}, nil
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

	start := time.Now()
	resp, err := testModelHTTPClient.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return connectivityTestResponse{Supported: true, Success: false, LatencyMS: latency, EndpointType: endpointType, Error: "request failed: " + err.Error()}, nil
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)

	var parsed map[string]any
	_ = json.Unmarshal(respBytes, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || parsed["error"] != nil {
		msg := strings.TrimSpace(string(respBytes))
		if eo, ok := parsed["error"].(map[string]any); ok {
			if m, ok := eo["message"].(string); ok {
				msg = m
			}
		}
		if len(msg) > 500 {
			msg = msg[:500] + "…"
		}
		return connectivityTestResponse{Supported: true, Success: false, LatencyMS: latency, Status: resp.StatusCode, EndpointType: endpointType, Error: msg}, nil
	}

	sample := modelProbeSample(parsed, endpointType)
	return connectivityTestResponse{Supported: true, Success: true, LatencyMS: latency, Status: resp.StatusCode, EndpointType: endpointType, Sample: sample}, nil
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
