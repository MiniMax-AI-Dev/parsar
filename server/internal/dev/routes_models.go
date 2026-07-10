package dev

import (
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

// createModelBody is the request body for POST /models in the new
// shared catalog. Provider info is inlined (no more model_providers
// table). Credential binding is one-of:
//   - credential_mode="inline_secret" + secret_id  → shared credential
//   - credential_mode="credential_ref" + credential_kind_code → per-user
//
// Capabilities/limits are accepted as optional top-level convenience
// fields and folded into config server-side.
type createModelBody struct {
	Name               string         `json:"name"`
	ProviderType       string         `json:"provider_type"`
	Adapter            string         `json:"adapter"`
	BaseURL            string         `json:"base_url"`
	ModelKey           string         `json:"model_key"`
	CredentialMode     string         `json:"credential_mode"`
	SecretID           string         `json:"secret_id"`
	CredentialKindCode string         `json:"credential_kind_code"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	Config             map[string]any `json:"config"`
}

// updateModelBody is the request body for PATCH /models/{id}.
// CredentialMode / ProviderType / Adapter are NOT editable here — to
// change them, create a new model.
type updateModelBody struct {
	Name               string         `json:"name"`
	ModelKey           string         `json:"model_key"`
	BaseURL            string         `json:"base_url"`
	SecretID           string         `json:"secret_id"`
	CredentialKindCode string         `json:"credential_kind_code"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	Config             map[string]any `json:"config"`
}

// foldModelConfig merges capabilities / limits into the config bag. Empty
// inputs are skipped so a caller that already nested them under config (or
// omitted them) is not clobbered with empty objects.
func foldModelConfig(config, capabilities, limits map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range config {
		merged[k] = v
	}
	if len(capabilities) > 0 {
		merged["capabilities"] = capabilities
	}
	if len(limits) > 0 {
		merged["limits"] = limits
	}
	return merged
}

// ============================================================
// createModel creates a model row in a workspace.
//
//	@Summary		Create a model in a workspace
//	@Description	Creates a new model row bound to a provider and credential. Owner/admin only.
//	@Tags			models
//	@ID				createDevModel
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string			true	"Workspace UUID"
//	@Param			body		body	createModelBody	true	"Model create payload"
//	@Success		201 {object} map[string]interface{} "Created model"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/models [post]
func createModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		// Model catalog is org-global; URL workspaceID is only used
		// for RBAC. The created model is NOT scoped to it.
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createModelBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		mode := strings.TrimSpace(req.CredentialMode)
		if mode == "" {
			mode = "inline_secret"
		}
		model, err := runtimeStore.CreateModel(r.Context(), store.CreateModelInput{
			Name:               req.Name,
			ProviderType:       req.ProviderType,
			Adapter:            req.Adapter,
			BaseURL:            req.BaseURL,
			ModelKey:           req.ModelKey,
			CredentialMode:     mode,
			SecretID:           req.SecretID,
			CredentialKindCode: req.CredentialKindCode,
			Config:             foldModelConfig(req.Config, req.Capabilities, req.Limits),
			CreatedBy:          actorIDFromRequest(r),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create model"})
			return
		}
		writeJSON(w, http.StatusCreated, model)
	}
}

// disableModel marks a model row as disabled.
//
//	@Summary		Disable a model
//	@Description	Marks the model as disabled. Owner/admin only. Disabled models are hidden from selectors.
//	@Tags			models
//	@ID				disableDevModel
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			modelID		path	string	true	"Model UUID"
//	@Success		200 {object} map[string]interface{} "Disabled model"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Model not found"
//	@Router			/api/v1/workspaces/{workspaceID}/models/{modelID}/disable [post]
func disableModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		model, err := runtimeStore.DisableModel(r.Context(), workspaceID, modelID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to disable model"})
			return
		}
		writeJSON(w, http.StatusOK, model)
	}
}

// updateModel applies a partial update to a model row.
//
//	@Summary		Update a model
//	@Description	Partially updates a model's mutable fields. Owner/admin only.
//	@Tags			models
//	@ID				updateDevModel
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string			true	"Workspace UUID"
//	@Param			modelID		path	string			true	"Model UUID"
//	@Param			body		body	updateModelBody	true	"Model update payload"
//	@Success		200 {object} map[string]interface{} "Updated model"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Model not found"
//	@Router			/api/v1/workspaces/{workspaceID}/models/{modelID} [patch]
func updateModel(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}
		var req updateModelBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if strings.TrimSpace(req.ModelKey) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_key is required"})
			return
		}
		model, err := runtimeStore.UpdateModel(r.Context(), store.UpdateModelInput{
			ModelID:            modelID,
			Name:               req.Name,
			ModelKey:           req.ModelKey,
			BaseURL:            req.BaseURL,
			SecretID:           req.SecretID,
			CredentialKindCode: req.CredentialKindCode,
			Config:             foldModelConfig(req.Config, req.Capabilities, req.Limits),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update model"})
			return
		}
		writeJSON(w, http.StatusOK, model)
	}
}

// testModelHTTPClient is overridable by tests so the unit tests can
// point the connectivity check at a httptest.Server instead of
// reaching the real upstream.
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
	Supported bool   `json:"supported"`
	Success   bool   `json:"success"`
	LatencyMS int64  `json:"latency_ms"`
	Status    int    `json:"http_status,omitempty"`
	Error     string `json:"error,omitempty"`
	Sample    string `json:"sample,omitempty"`
}

// testModelConnectivity sends a minimal request to the upstream
// provider so the admin can verify base_url + api_key + custom headers
// + model_key without driving a full Agent Run. OpenAI-shaped adapters
// use chat-completions; Anthropic-shaped use Messages. Other protocols
// return supported=false.
// testModelConnectivity issues a live probe against a model row's credentials.
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
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		modelID := strings.TrimSpace(chi.URLParam(r, "modelID"))
		if !isUUID(modelID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "model_id must be a valid uuid"})
			return
		}

		// ResolveModelRuntimeForUser handles both modes in one shot.
		// For credential_ref mode passing "" returns ErrModelDisabled,
		// which we surface as supported=false below.
		callerUserID := actorIDFromRequest(r)
		if callerUserID == "" {
			callerUserID = ""
		}
		mr, err := runtimeStore.ResolveModelRuntimeForUser(r.Context(), modelID, callerUserID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownModel) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			if errors.Is(err, store.ErrModelDisabled) {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     err.Error(),
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve model"})
			return
		}

		isOpenAICompatible := isOpenAIChatCompletionsAdapter(mr.Adapter)
		isAnthropicCompatible := isAnthropicMessagesAdapter(mr.Adapter)

		if !isOpenAICompatible && !isAnthropicCompatible {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: false,
				Error:     "connectivity test only supports OpenAI chat-completions and Anthropic messages compatible providers",
			})
			return
		}

		// Pick the encrypted payload:
		//   inline_secret  → fetched via secret_id below.
		//   credential_ref → filled from the caller's user_credentials.
		var encryptedPayload []byte
		if mr.CredentialMode == "credential_ref" {
			encryptedPayload = mr.EncryptedPayload
		} else {
			if mr.SecretID == "" {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     "no API key bound to this model",
				})
				return
			}
			sp, err := runtimeStore.GetSecretPayload(r.Context(), workspaceID, mr.SecretID)
			if err != nil {
				writeJSON(w, http.StatusOK, connectivityTestResponse{
					Supported: true,
					Success:   false,
					Error:     fmt.Sprintf("failed to fetch secret: %v", err),
				})
				return
			}
			encryptedPayload = sp.EncryptedPayload
		}

		secretService, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "secrets service unavailable: " + err.Error()})
			return
		}
		payload, err := secretService.Decrypt(encryptedPayload)
		if err != nil {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "failed to decrypt credential: " + err.Error(),
			})
			return
		}
		// Two payload shapes coexist:
		//   `secrets` rows (inline_secret) carry {api_key: "..."}
		//   `user_credentials` rows (credential_ref) carry {value: "..."}
		// Both encode an upstream-provider API key; accept either.
		apiKey, _ := payload["api_key"].(string)
		if strings.TrimSpace(apiKey) == "" {
			if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
				apiKey = v
			}
		}
		if strings.TrimSpace(apiKey) == "" {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "credential payload missing api_key / value field",
			})
			return
		}

		// Build a minimal protocol-shaped request.
		url := ""
		body := map[string]any{}
		if isAnthropicCompatible {
			url = anthropicMessagesURL(mr.BaseURL)
			body = map[string]any{
				"model":      mr.ModelKey,
				"messages":   []map[string]any{{"role": "user", "content": "ping"}},
				"max_tokens": 16,
			}
		} else {
			url = strings.TrimRight(mr.BaseURL, "/") + "/chat/completions"
			body = map[string]any{
				"model":      mr.ModelKey,
				"messages":   []map[string]any{{"role": "user", "content": "ping"}},
				"max_tokens": 16,
			}
		}
		bodyBytes, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, strings.NewReader(string(bodyBytes)))
		if err != nil {
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				Error:     "failed to build request: " + err.Error(),
			})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if isAnthropicCompatible {
			req.Header.Set("x-api-key", apiKey)
			req.Header.Set("anthropic-version", "2023-06-01")
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		// Provider-level custom headers (e.g. X-Sub-Module for an internal gateway).
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
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				LatencyMS: latency,
				Error:     "request failed: " + err.Error(),
			})
			return
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)

		var parsed map[string]any
		_ = json.Unmarshal(respBytes, &parsed)

		// HTTP 200 != business success — many gateways respond 200 with
		// an `error` object inside the body. Treat non-2xx OR
		// `error` field in body as failure.
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
			writeJSON(w, http.StatusOK, connectivityTestResponse{
				Supported: true,
				Success:   false,
				LatencyMS: latency,
				Status:    resp.StatusCode,
				Error:     msg,
			})
			return
		}

		// Pull a sample first message content for the UI to display.
		sample := ""
		if isAnthropicCompatible {
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
		writeJSON(w, http.StatusOK, connectivityTestResponse{
			Supported: true,
			Success:   true,
			LatencyMS: latency,
			Status:    resp.StatusCode,
			Sample:    sample,
		})
	}
}

// listModels lists model rows for a workspace.
//
//	@Summary		List workspace models
//	@Description	Returns model rows for the workspace. Caller must be a workspace member.
//	@Tags			models
//	@ID				listDevModels
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Model list"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Router			/api/v1/workspaces/{workspaceID}/models [get]
func listModels(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		models, err := runtimeStore.ListModels(r.Context(), workspaceID, parseLimit(r, 100))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list models"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
	}
}
