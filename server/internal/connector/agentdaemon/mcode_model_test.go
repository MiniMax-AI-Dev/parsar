package agentdaemon

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"testing"
)

func TestMCodeManagedModelUsesConfiguredProvider(t *testing.T) {
	mr := store.ModelRuntime{ProviderType: "minimax", ModelKey: "MiniMax-M3", Adapter: "@ai-sdk/anthropic", BaseURL: "https://wrong.example.test", ProviderConfig: map[string]any{"endpoint_base_urls": map[string]any{"anthropic": "https://api.example.test/anthropic"}, "options": map[string]any{"headers": map[string]any{"X-Test": "fixture"}}}, Limits: map[string]any{"context": 64000, "output": 4096}}
	opts := map[string]any{}
	if err := injectMCodeManagedModel(opts, "model-1", mr, "test-api-key"); err != nil {
		t.Fatal(err)
	}
	provider := opts["mcode_provider"].(map[string]any)
	options := provider["options"].(map[string]any)
	if provider["kind"] != "custom" || provider["enabled"] != true || provider["npm"] != "@ai-sdk/anthropic" {
		t.Fatalf("provider=%v", provider)
	}
	if options["apiKey"] != "test-api-key" || options["baseURL"] != "https://api.example.test/anthropic/v1" {
		t.Fatalf("options=%v", options)
	}
	if opts["model"] != "MiniMax-M3" || provider["models"].(map[string]any)["MiniMax-M3"] == nil {
		t.Fatal("model selection lost")
	}
	if options["headers"].(map[string]any)["X-Test"] != "fixture" {
		t.Fatal("provider headers lost")
	}
}

func TestMCodeManagedModelRejectsIncompleteMetadata(t *testing.T) {
	if err := injectMCodeManagedModel(map[string]any{}, "missing", store.ModelRuntime{}, "test-key"); err == nil {
		t.Fatal("incomplete model accepted")
	}
}
