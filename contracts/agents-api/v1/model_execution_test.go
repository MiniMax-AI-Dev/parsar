package v1

import "testing"

func TestModelExecutionValidation(t *testing.T) {
	for _, tc := range []struct {
		protocol, harness string
		valid             bool
	}{{"anthropic", "claude_sdk", true}, {"anthropic", "mcode", true}, {"responses", "codex", true}, {"responses", "mcode", false}, {"anthropic", "codex", false}, {"responses", "", false}} {
		p := ModelProviderInput{Protocol: tc.protocol, BaseURL: "https://example.com/v1", APIKey: "secret", ContextWindow: 200000, MaxOutputTokens: 8000}
		if (p.ValidateHarness(tc.harness) == nil) != tc.valid {
			t.Fatalf("incorrect protocol validation: %s/%s", tc.protocol, tc.harness)
		}
	}
	for _, url := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com?key=secret", "https://example.com#secret", "https://"} {
		if (&ModelProviderInput{Protocol: "responses", BaseURL: url, APIKey: "secret"}).Validate() == nil {
			t.Fatal("unsafe provider URL accepted")
		}
	}
	if (&ModelProviderInput{Protocol: "anthropic", BaseURL: "https://example.com", APIKey: "secret"}).ValidateHarness("mcode") == nil {
		t.Fatal("MiniMax Code accepted unknown model limits")
	}
}
