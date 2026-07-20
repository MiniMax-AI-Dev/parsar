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
		workspaceID, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore)
		if !ok {
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
		model = persistModelHealth(r.Context(), runtimeStore, workspaceID, model, actorIDFromRequest(r))
		writeJSON(w, http.StatusCreated, model)
	}
}

// importProviderModelsBody is a backend-mediated batch import from an
// OpenAI-shaped GET {base_url}/v1/models endpoint. Dry-run returns the
// discovered ids and existing status without creating rows.
type importProviderModelsBody struct {
	ProviderType       string         `json:"provider_type"`
	Adapter            string         `json:"adapter"`
	BaseURL            string         `json:"base_url"`
	CredentialMode     string         `json:"credential_mode"`
	SecretID           string         `json:"secret_id"`
	CredentialKindCode string         `json:"credential_kind_code"`
	APIKey             string         `json:"api_key"`
	ModelIDs           []string       `json:"model_ids"`
	DryRun             bool           `json:"dry_run"`
	SkipExisting       *bool          `json:"skip_existing"`
	Capabilities       map[string]any `json:"capabilities"`
	Limits             map[string]any `json:"limits"`
	Config             map[string]any `json:"config"`
}

type discoveredProviderModel struct {
	ID                     string   `json:"id"`
	Exists                 bool     `json:"exists"`
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
}

type importProviderModelFailure struct {
	ModelKey string `json:"model_key"`
	Error    string `json:"error"`
}

type importProviderModelsResponse struct {
	Models  []discoveredProviderModel    `json:"models"`
	Created []store.ModelRead            `json:"created"`
	Skipped []string                     `json:"skipped"`
	Failed  []importProviderModelFailure `json:"failed"`
}

var importProviderModelsHTTPClient = &http.Client{Timeout: 15 * time.Second}

type providerModelDiscovery struct {
	ID                     string
	SupportedEndpointTypes []string
}

func modelImportSkipExisting(req importProviderModelsBody) bool {
	return req.SkipExisting == nil || *req.SkipExisting
}

func normalizeEndpointType(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "-", "_")
	v = strings.ReplaceAll(v, ".", "_")
	v = strings.ReplaceAll(v, "/", "_")
	switch v {
	case "openai", "chat", "chat_completion", "chat_completions", "openai_chat", "openai_chat_completions":
		return "openai"
	case "openai_response", "openai_responses", "response", "responses":
		return "openai-response"
	case "anthropic", "message", "messages", "anthropic_message", "anthropic_messages":
		return "anthropic"
	case "google", "google_generative_ai", "gemini":
		return "google_generative_ai"
	default:
		return v
	}
}

func normalizeEndpointTypes(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		normalized := normalizeEndpointType(item)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
	}
	return out
}

func endpointTypesFromAny(value any) []string {
	switch typed := value.(type) {
	case []any:
		raw := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				raw = append(raw, s)
			}
		}
		return normalizeEndpointTypes(raw)
	case []string:
		return normalizeEndpointTypes(typed)
	default:
		return nil
	}
}

func configWithSupportedEndpointTypes(config map[string]any, endpointTypes []string) map[string]any {
	merged := map[string]any{}
	for k, v := range config {
		merged[k] = v
	}
	normalized := normalizeEndpointTypes(endpointTypes)
	if len(normalized) > 0 {
		merged["supported_endpoint_types"] = normalized
	}
	return merged
}

func modelSupportsEndpointType(config map[string]any, endpointType string) bool {
	want := normalizeEndpointType(endpointType)
	for _, got := range endpointTypesFromAny(config["supported_endpoint_types"]) {
		if got == want {
			return true
		}
	}
	return false
}

func endpointBaseURLFromConfig(config map[string]any, endpointType, fallback string) string {
	want := normalizeEndpointType(endpointType)
	switch raw := config["endpoint_base_urls"].(type) {
	case map[string]any:
		for k, v := range raw {
			if normalizeEndpointType(k) != want {
				continue
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	case map[string]string:
		for k, s := range raw {
			if normalizeEndpointType(k) == want && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return inferredEndpointBaseURL(want, fallback)
}

func inferredEndpointBaseURL(endpointType, fallback string) string {
	base := strings.TrimRight(strings.TrimSpace(fallback), "/")
	if endpointType == "openai" || endpointType == "openai-response" {
		if strings.HasSuffix(base, "/anthropic/v1") {
			return strings.TrimSuffix(base, "/anthropic/v1") + "/v1"
		}
		if strings.HasSuffix(base, "/anthropic") {
			return strings.TrimSuffix(base, "/anthropic") + "/v1"
		}
	}
	return base
}

func providerModelsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func stringHeaders(raw map[string]any) map[string]string {
	out := map[string]string{}
	if hdrs, ok := raw["headers"].(map[string]any); ok {
		for k, v := range hdrs {
			if s, ok := v.(string); ok && strings.TrimSpace(k) != "" && s != "" {
				out[k] = s
			}
		}
	}
	return out
}

func modelImportAuthScheme(req importProviderModelsBody) string {
	if s, ok := req.Config["auth_scheme"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	if isAnthropicMessagesAdapter(req.Adapter) {
		return "api-key"
	}
	return "bearer"
}

func apiKeyFromPayload(payload map[string]any) string {
	apiKey, _ := payload["api_key"].(string)
	if strings.TrimSpace(apiKey) == "" {
		if v, _ := payload["value"].(string); strings.TrimSpace(v) != "" {
			apiKey = v
		}
	}
	return strings.TrimSpace(apiKey)
}

func importProviderModelIDs(ctx context.Context, req importProviderModelsBody, apiKey string) ([]providerModelDiscovery, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, providerModelsURL(req.BaseURL), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build upstream request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	for k, v := range stringHeaders(req.Config) {
		httpReq.Header.Set(k, v)
	}
	if apiKey != "" {
		if modelImportAuthScheme(req) == "api-key" {
			httpReq.Header.Set("x-api-key", apiKey)
		} else {
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	resp, err := importProviderModelsHTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upstream /models request failed: %w", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(bodyBytes))
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return nil, fmt.Errorf("upstream /models returned %d: %s", resp.StatusCode, msg)
	}
	var body struct {
		Data []struct {
			ID                     string   `json:"id"`
			SupportedEndpointTypes []string `json:"supported_endpoint_types"`
		} `json:"data"`
	}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return nil, fmt.Errorf("failed to parse upstream /models response: %w", err)
	}
	seen := map[string]bool{}
	models := make([]providerModelDiscovery, 0, len(body.Data))
	for _, item := range body.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, providerModelDiscovery{
			ID:                     id,
			SupportedEndpointTypes: normalizeEndpointTypes(item.SupportedEndpointTypes),
		})
	}
	return models, nil
}

func existingModelKeys(models []store.ModelRead) map[string]bool {
	out := map[string]bool{}
	for _, model := range models {
		key := strings.TrimSpace(model.ModelKey)
		if key != "" {
			out[key] = true
		}
	}
	return out
}

func emptyImportProviderModelsResponse(models []discoveredProviderModel) importProviderModelsResponse {
	return importProviderModelsResponse{
		Models:  models,
		Created: []store.ModelRead{},
		Skipped: []string{},
		Failed:  []importProviderModelFailure{},
	}
}

func selectedImportModelKeys(discovered []providerModelDiscovery, selected []string) []string {
	if len(selected) == 0 {
		out := make([]string, 0, len(discovered))
		for _, model := range discovered {
			out = append(out, model.ID)
		}
		return out
	}
	discoveredSet := map[string]bool{}
	for _, model := range discovered {
		discoveredSet[model.ID] = true
	}
	out := make([]string, 0, len(selected))
	seen := map[string]bool{}
	for _, raw := range selected {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] || !discoveredSet[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func discoveredEndpointTypesByID(discovered []providerModelDiscovery) map[string][]string {
	out := map[string][]string{}
	for _, model := range discovered {
		out[model.ID] = model.SupportedEndpointTypes
	}
	return out
}

func importSecretID(runtimeStore RuntimeStore, r *http.Request, workspaceID string, req importProviderModelsBody) (string, error) {
	if strings.TrimSpace(req.SecretID) != "" {
		return strings.TrimSpace(req.SecretID), nil
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return "", errors.New("api_key or secret_id is required for inline_secret mode")
	}
	serverMasterKey := os.Getenv("PARSAR_MASTER_KEY")
	if serverMasterKey == "" {
		return "", errors.New("server has no PARSAR_MASTER_KEY configured; refusing to create a secret")
	}
	secretService, err := secrets.New(serverMasterKey)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"api_key": apiKey}
	encrypted, err := secretService.Encrypt(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt secret: %w", err)
	}
	secret, err := runtimeStore.CreateSecret(r.Context(), store.CreateSecretInput{
		WorkspaceID: workspaceID,
		Name:        "model-key-" + time.Now().UTC().Format("20060102150405"),
		Kind:        "model_provider",
		Provider:    req.ProviderType,
		AuthType:    "api_key",
		Payload:     payload,
		Masked:      secrets.MaskPayload(payload),
	}, encrypted)
	if err != nil {
		return "", err
	}
	return secret.ID, nil
}

func importDiscoveryAPIKey(runtimeStore RuntimeStore, r *http.Request, workspaceID string, req importProviderModelsBody) (string, error) {
	if apiKey := strings.TrimSpace(req.APIKey); apiKey != "" {
		return apiKey, nil
	}
	if strings.TrimSpace(req.SecretID) == "" {
		return "", nil
	}
	serverMasterKey := os.Getenv("PARSAR_MASTER_KEY")
	if serverMasterKey == "" {
		return "", errors.New("server has no PARSAR_MASTER_KEY configured; refusing to decrypt the selected secret")
	}
	secretPayload, err := runtimeStore.GetSecretPayload(r.Context(), workspaceID, strings.TrimSpace(req.SecretID))
	if err != nil {
		return "", err
	}
	secretService, err := secrets.New(serverMasterKey)
	if err != nil {
		return "", err
	}
	payload, err := secretService.Decrypt(secretPayload.EncryptedPayload)
	if err != nil {
		return "", err
	}
	return apiKeyFromPayload(payload), nil
}

// importProviderModels discovers model ids from an upstream /v1/models endpoint
// and optionally creates model rows for selected ids.
//
//	@Summary		Import models from provider /models
//	@Description	Fetches model ids from base_url + /v1/models and batch-creates model rows using the existing model catalog schema. Owner/admin only.
//	@Tags			models
//	@ID				importDevProviderModels
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string						true	"Workspace UUID"
//	@Param			body		body	importProviderModelsBody	true	"Model import payload"
//	@Success		200 {object} importProviderModelsResponse "Import preview or result"
//	@Failure		400 {object} map[string]string "Invalid body, UUID, provider response, or credentials"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/models/import [post]
func importProviderModels(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore)
		if !ok {
			return
		}
		var req importProviderModelsBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		req.ProviderType = strings.TrimSpace(req.ProviderType)
		req.Adapter = strings.TrimSpace(req.Adapter)
		req.BaseURL = strings.TrimSpace(req.BaseURL)
		if req.ProviderType == "" || req.Adapter == "" || req.BaseURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider_type, adapter, and base_url are required"})
			return
		}
		mode := strings.TrimSpace(req.CredentialMode)
		if mode == "" {
			mode = "inline_secret"
		}
		if mode != "inline_secret" && mode != "credential_ref" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential_mode must be inline_secret or credential_ref"})
			return
		}
		req.CredentialMode = mode

		apiKey, err := importDiscoveryAPIKey(runtimeStore, r, workspaceID, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		discoveredModels, err := importProviderModelIDs(r.Context(), req, apiKey)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		existing, err := runtimeStore.ListModels(r.Context(), workspaceID, 1000)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list existing models"})
			return
		}
		existingKeys := existingModelKeys(existing)
		discovered := make([]discoveredProviderModel, 0, len(discoveredModels))
		for _, model := range discoveredModels {
			discovered = append(discovered, discoveredProviderModel{
				ID:                     model.ID,
				Exists:                 existingKeys[model.ID],
				SupportedEndpointTypes: model.SupportedEndpointTypes,
			})
		}
		if req.DryRun {
			writeJSON(w, http.StatusOK, emptyImportProviderModelsResponse(discovered))
			return
		}

		response := emptyImportProviderModelsResponse(discovered)
		endpointTypesByID := discoveredEndpointTypesByID(discoveredModels)
		modelKeys := make([]string, 0, len(discoveredModels))
		for _, modelKey := range selectedImportModelKeys(discoveredModels, req.ModelIDs) {
			if modelImportSkipExisting(req) && existingKeys[modelKey] {
				response.Skipped = append(response.Skipped, modelKey)
				continue
			}
			modelKeys = append(modelKeys, modelKey)
		}
		if len(modelKeys) == 0 {
			writeJSON(w, http.StatusOK, response)
			return
		}
		if mode == "credential_ref" && strings.TrimSpace(req.CredentialKindCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential_kind_code is required for credential_ref mode"})
			return
		}
		secretID := ""
		if mode == "inline_secret" {
			secretID, err = importSecretID(runtimeStore, r, workspaceID, req)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		for _, modelKey := range modelKeys {
			model, err := runtimeStore.CreateModel(r.Context(), store.CreateModelInput{
				Name:               modelKey,
				ProviderType:       req.ProviderType,
				Adapter:            req.Adapter,
				BaseURL:            req.BaseURL,
				ModelKey:           modelKey,
				CredentialMode:     mode,
				SecretID:           secretID,
				CredentialKindCode: strings.TrimSpace(req.CredentialKindCode),
				Config:             foldModelConfig(configWithSupportedEndpointTypes(req.Config, endpointTypesByID[modelKey]), req.Capabilities, req.Limits),
				CreatedBy:          actorIDFromRequest(r),
			})
			if err != nil {
				response.Failed = append(response.Failed, importProviderModelFailure{
					ModelKey: modelKey,
					Error:    err.Error(),
				})
				continue
			}
			model = persistModelHealth(r.Context(), runtimeStore, workspaceID, model, actorIDFromRequest(r))
			response.Created = append(response.Created, model)
			existingKeys[modelKey] = true
		}
		writeJSON(w, http.StatusOK, response)
	}
}

type detectProviderModelEndpointsBody struct {
	BaseURL            string         `json:"base_url"`
	ModelKey           string         `json:"model_key"`
	APIKey             string         `json:"api_key"`
	SecretID           string         `json:"secret_id"`
	Config             map[string]any `json:"config"`
	CredentialKindCode string         `json:"credential_kind_code"`
}

type detectProviderModelEndpointsResponse struct {
	SupportedEndpointTypes []string `json:"supported_endpoint_types"`
}

func endpointProbeRequest(ctx context.Context, baseURL, modelKey, apiKey, endpointType string, config map[string]any) (*http.Request, error) {
	body := map[string]any{}
	url := ""
	switch normalizeEndpointType(endpointType) {
	case "anthropic":
		url = anthropicMessagesURL(endpointBaseURLFromConfig(config, "anthropic", baseURL))
		body = map[string]any{
			"model":      modelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": 1,
		}
	case "openai":
		url = strings.TrimRight(endpointBaseURLFromConfig(config, "openai", baseURL), "/") + "/chat/completions"
		body = map[string]any{
			"model":      modelKey,
			"messages":   []map[string]any{{"role": "user", "content": "ping"}},
			"max_tokens": 1,
		}
	case "openai-response":
		url = strings.TrimRight(endpointBaseURLFromConfig(config, "openai-response", baseURL), "/") + "/responses"
		body = map[string]any{
			"model": modelKey,
			"input": "ping",
		}
	default:
		return nil, fmt.Errorf("unsupported endpoint probe type %q", endpointType)
	}
	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if normalizeEndpointType(endpointType) == "anthropic" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range stringHeaders(config) {
		req.Header.Set(k, v)
	}
	return req, nil
}

func probeProviderEndpoint(ctx context.Context, baseURL, modelKey, apiKey, endpointType string, config map[string]any) bool {
	req, err := endpointProbeRequest(ctx, baseURL, modelKey, apiKey, endpointType, config)
	if err != nil {
		return false
	}
	resp, err := testModelHTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var parsed map[string]any
	_ = json.Unmarshal(bodyBytes, &parsed)
	return resp.StatusCode >= 200 && resp.StatusCode < 300 && parsed["error"] == nil
}

func detectEndpointTypes(ctx context.Context, baseURL, modelKey, apiKey string, config map[string]any) []string {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(modelKey) == "" || strings.TrimSpace(apiKey) == "" {
		return nil
	}
	candidates := []string{"anthropic", "openai", "openai-response"}
	supported := make([]string, 0, len(candidates))
	for _, endpointType := range candidates {
		if probeProviderEndpoint(ctx, baseURL, modelKey, apiKey, endpointType, config) {
			supported = append(supported, endpointType)
		}
	}
	return supported
}

// detectProviderModelEndpoints probes common provider endpoint families for a
// model key so manual model creation can store all supported protocols instead
// of asking the user to choose one.
//
//	@Summary		Detect supported model endpoint types
//	@Description	Probes common OpenAI/Anthropic endpoint families for a base URL and model key. Owner/admin only.
//	@Tags			models
//	@ID				detectDevModelEndpoints
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string								true	"Workspace UUID"
//	@Param			body		body	detectProviderModelEndpointsBody	true	"Endpoint detection payload"
//	@Success		200 {object} detectProviderModelEndpointsResponse "Detected endpoint families"
//	@Failure		400 {object} map[string]string "Invalid body, UUID, or credentials"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/models/detect-endpoints [post]
func detectProviderModelEndpoints(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed model registry is disabled"})
			return
		}
		workspaceID, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore)
		if !ok {
			return
		}
		var req detectProviderModelEndpointsBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		req.BaseURL = strings.TrimSpace(req.BaseURL)
		req.ModelKey = strings.TrimSpace(req.ModelKey)
		if req.BaseURL == "" || req.ModelKey == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_url and model_key are required"})
			return
		}
		apiKey, err := importDiscoveryAPIKey(runtimeStore, r, workspaceID, importProviderModelsBody{
			APIKey:   req.APIKey,
			SecretID: req.SecretID,
		})
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if strings.TrimSpace(apiKey) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api_key or secret_id is required to detect endpoint types"})
			return
		}
		writeJSON(w, http.StatusOK, detectProviderModelEndpointsResponse{
			SupportedEndpointTypes: detectEndpointTypes(r.Context(), req.BaseURL, req.ModelKey, apiKey, req.Config),
		})
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
		workspaceID, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore)
		if !ok {
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
		if _, ok := requireWorkspaceOwnerOrAdminRequest(w, r, runtimeStore); !ok {
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
