package execution

import "testing"

func TestRuntimeProviderSelectionDoesNotFallBack(t *testing.T) {
	config := RuntimeProviders{DefaultProvider: "original", EngineProviders: map[string]string{"codex": "codex-image", "mcode": "mcode-image"}}
	if config.ProviderForEngine("mcode") != "mcode-image" || config.ProviderForEngine("claude_sdk") != "" {
		t.Fatal("selected an unrelated runtime")
	}
	if (RuntimeProviders{DefaultProvider: "original"}).ProviderForEngine("codex") != "original" {
		t.Fatal("changed legacy default")
	}
}
