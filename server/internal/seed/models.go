// Package seed contains reusable dev-seeding routines shared between
// cmd/seeddev and integration tests.
package seed

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ModelRegistryResult reports the rows freshly inserted by this call;
// idempotent skips are NOT counted. CreatedProviders is always 0 (the
// providers table was inlined into models) and kept only for backwards
// compatibility with existing callers.
type ModelRegistryResult struct {
	CreatedProviders int
	CreatedModels    int
}

// SeedModelRegistry inserts a small, idempotent set of shared models
// into the org-global catalog (matched by model_key). workspaceID is
// ignored — the catalog is org-global — and createdBy is forwarded to
// models.created_by.
func SeedModelRegistry(ctx context.Context, st *store.Store, workspaceID, createdBy string) (ModelRegistryResult, error) {
	_ = workspaceID

	existing, err := st.ListModels(ctx, "", 200)
	if err != nil {
		return ModelRegistryResult{}, err
	}
	byModelKey := make(map[string]struct{}, len(existing))
	for _, m := range existing {
		byModelKey[m.ModelKey] = struct{}{}
	}

	createdModels := 0
	for _, ms := range modelSpecs {
		if _, ok := byModelKey[ms.ModelKey]; ok {
			continue
		}
		_, err := st.CreateModel(ctx, store.CreateModelInput{
			Name:               ms.Name,
			ProviderType:       ms.ProviderType,
			Adapter:            ms.Adapter,
			BaseURL:            ms.BaseURL,
			ModelKey:           ms.ModelKey,
			CredentialMode:     ms.CredentialMode,
			SecretID:           "",
			CredentialKindCode: ms.CredentialKindCode,
			Config: map[string]any{
				"capabilities": ms.Capabilities,
				"limits":       ms.Limits,
			},
			CreatedBy: createdBy,
		})
		if err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return ModelRegistryResult{CreatedModels: createdModels},
				fmt.Errorf("create model %q: %w", ms.ModelKey, err)
		}
		createdModels++
	}

	return ModelRegistryResult{CreatedModels: createdModels}, nil
}

type modelSpec struct {
	Name               string
	ProviderType       string
	Adapter            string
	BaseURL            string
	ModelKey           string
	CredentialMode     string // "inline_secret" or "credential_ref"
	CredentialKindCode string // only when CredentialMode == "credential_ref"
	Capabilities       map[string]any
	Limits             map[string]any
}

// modelSpecs is the canonical dev model set. Each is seeded as a
// credential_ref-mode shared model (no inline secret bound).
//
// `Adapter` MUST be the full npm package opencode loads at runtime
// (rendered into opencode.json as `npm: "<adapter>"`). Short names
// like "openai" make opencode look for the wrong package. The split
// between "@ai-sdk/openai" (uses /v1/responses) and
// "@ai-sdk/openai-compatible" (uses /v1/chat/completions) matters
// because most third-party gateways only implement the latter.
//
// `Limits` MUST follow opencode's `{ "context": <int>, "output": <int> }`
// schema (not strings like "200k") — opencode validates this on boot.
var modelSpecs = []modelSpec{
	{Name: "GPT-4o", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://api.openai.com/v1",
		ModelKey: "gpt-4o", CredentialMode: "credential_ref", CredentialKindCode: "openai_api_key",
		Capabilities: map[string]any{"chat": true, "tool_use": true, "vision": true},
		Limits:       map[string]any{"context": 128000, "output": 16384}},
	{Name: "GPT-4o mini", ProviderType: "openai", Adapter: "@ai-sdk/openai", BaseURL: "https://api.openai.com/v1",
		ModelKey: "gpt-4o-mini", CredentialMode: "credential_ref", CredentialKindCode: "openai_api_key",
		Capabilities: map[string]any{"chat": true, "tool_use": true},
		Limits:       map[string]any{"context": 128000, "output": 16384}},
	{Name: "Claude 3.5 Sonnet", ProviderType: "anthropic", Adapter: "@ai-sdk/anthropic", BaseURL: "https://api.anthropic.com",
		ModelKey: "claude-3-5-sonnet-20241022", CredentialMode: "credential_ref", CredentialKindCode: "anthropic_api_key",
		Capabilities: map[string]any{"chat": true, "tool_use": true, "vision": true},
		Limits:       map[string]any{"context": 200000, "output": 8192}},
	{Name: "Qwen 2.5 72B", ProviderType: "openai-compatible", Adapter: "@ai-sdk/openai-compatible", BaseURL: "https://llm.internal/v1",
		ModelKey: "qwen2.5-72b-instruct", CredentialMode: "credential_ref", CredentialKindCode: "internal_gw_api_key",
		Capabilities: map[string]any{"chat": true, "tool_use": true},
		Limits:       map[string]any{"context": 32768, "output": 8192}},
	{Name: "DeepSeek Chat", ProviderType: "openai-compatible", Adapter: "@ai-sdk/openai-compatible", BaseURL: "https://api.deepseek.com",
		ModelKey: "deepseek-chat", CredentialMode: "credential_ref", CredentialKindCode: "deepseek_api_key",
		Capabilities: map[string]any{"chat": true},
		Limits:       map[string]any{"context": 65536, "output": 8192}},
}

// ExpectedProviders / ExpectedModels are the canonical counts a fresh
// SeedModelRegistry run inserts. ExpectedProviders is always 0 (legacy
// shape) kept for backwards-compat with tests.
var (
	ExpectedProviders = 0
	ExpectedModels    = len(modelSpecs)
)

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx surfaces unique-violation as SQLSTATE 23505; string-match
	// avoids importing pgx here.
	if strings.Contains(err.Error(), "23505") ||
		strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		return true
	}
	return errors.Is(err, store.ErrUnknownModel)
}
