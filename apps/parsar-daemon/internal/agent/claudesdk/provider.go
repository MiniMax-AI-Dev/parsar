package claudesdk

import (
	"fmt"
	"net/url"
	"strings"
)

// Provider options are transient operator input, never public Agent fields or
// native tool environment. The workspace sandbox removes these variables.
func providerEnvironment(value any) ([]string, error) {
	fail := func() ([]string, error) { return nil, fmt.Errorf("claudesdk: invalid provider configuration") }
	options, ok := value.(map[string]any)
	if !ok || len(options) != 2 {
		return fail()
	}
	base, ok := options["base_url"].(string)
	if !ok {
		return fail()
	}
	token, ok := options["bearer_token"].(string)
	if !ok || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\x00\r\n") {
		return fail()
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fail()
	}
	return []string{"ANTHROPIC_BASE_URL=" + base, "ANTHROPIC_AUTH_TOKEN=" + token}, nil
}

func withProvider(env, provider []string) []string {
	if provider == nil {
		return env
	}
	out := make([]string, 0, len(env)+len(provider))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key != "ANTHROPIC_API_KEY" && key != "ANTHROPIC_AUTH_TOKEN" && key != "ANTHROPIC_BASE_URL" {
			out = append(out, entry)
		}
	}
	return append(out, provider...)
}
