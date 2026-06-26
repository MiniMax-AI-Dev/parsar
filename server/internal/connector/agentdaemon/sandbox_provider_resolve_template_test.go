package agentdaemon

import (
	"log/slog"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// newProviderForResolveTest builds a bare E2BSandboxProvider with just
// the Templates map / DefaultSize / Template fields populated — that's
// all resolveTemplate touches. We don't go through NewE2BSandboxProvider
// because that requires Client/Store/Registry/Binder which would force
// us to stub four interfaces for a pure-logic test.
func newProviderForResolveTest(cfg E2BProviderConfig) *E2BSandboxProvider {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &E2BSandboxProvider{
		cfg:      cfg,
		cache:    map[string]*sandboxEntry{},
		inflight: map[string]*acquirePromise{},
	}
}

func TestResolveTemplate_DefaultsToStandardWhenNoSizeConfigured(t *testing.T) {
	p := newProviderForResolveTest(E2BProviderConfig{
		Template:    "parsar-daemon",
		Templates:   map[string]string{"standard": "parsar-daemon"},
		DefaultSize: "standard",
	})

	size, tmpl := p.resolveTemplate(connector.PromptInput{})

	if size != "standard" {
		t.Errorf("size = %q, want %q", size, "standard")
	}
	if tmpl != "parsar-daemon" {
		t.Errorf("template = %q, want %q", tmpl, "parsar-daemon")
	}
}

func TestResolveTemplate_HonorsProjectAgentXL(t *testing.T) {
	p := newProviderForResolveTest(E2BProviderConfig{
		Template: "parsar-daemon",
		Templates: map[string]string{
			"standard": "parsar-daemon",
			"xl":       "parsar-daemon-xl",
		},
		DefaultSize: "standard",
	})

	in := connector.PromptInput{
		ProjectAgentConfig: map[string]any{"sandbox_size": "xl"},
	}
	size, tmpl := p.resolveTemplate(in)

	if size != "xl" {
		t.Errorf("size = %q, want %q", size, "xl")
	}
	if tmpl != "parsar-daemon-xl" {
		t.Errorf("template = %q, want %q", tmpl, "parsar-daemon-xl")
	}
}

func TestResolveTemplate_ProjectAgentOverridesAgentDefault(t *testing.T) {
	// AgentConfig says xl, ProjectAgentConfig says standard — project
	// override wins. Mirrors the precedence used by injectClaudeManagedModel
	// for model_id (project > agent > default).
	p := newProviderForResolveTest(E2BProviderConfig{
		Template: "parsar-daemon",
		Templates: map[string]string{
			"standard": "parsar-daemon",
			"xl":       "parsar-daemon-xl",
		},
		DefaultSize: "standard",
	})

	in := connector.PromptInput{
		AgentConfig:        map[string]any{"sandbox_size": "xl"},
		ProjectAgentConfig: map[string]any{"sandbox_size": "standard"},
	}
	size, tmpl := p.resolveTemplate(in)

	if size != "standard" {
		t.Errorf("size = %q, want project override %q", size, "standard")
	}
	if tmpl != "parsar-daemon" {
		t.Errorf("template = %q, want %q", tmpl, "parsar-daemon")
	}
}

func TestResolveTemplate_FallsBackToTemplateWhenXLNotConfigured(t *testing.T) {
	// Agent asks for xl, but the deployment didn't register an xl entry
	// in Templates (e.g. AGENT_DAEMON_SANDBOX_TEMPLATE_XL env unset).
	// Should degrade to the canonical Template rather than fail acquire.
	p := newProviderForResolveTest(E2BProviderConfig{
		Template:    "parsar-daemon",
		Templates:   map[string]string{"standard": "parsar-daemon"},
		DefaultSize: "standard",
	})

	in := connector.PromptInput{
		ProjectAgentConfig: map[string]any{"sandbox_size": "xl"},
	}
	size, tmpl := p.resolveTemplate(in)

	if size != "standard" {
		t.Errorf("fallback size = %q, want %q", size, "standard")
	}
	if tmpl != "parsar-daemon" {
		t.Errorf("fallback template = %q, want %q", tmpl, "parsar-daemon")
	}
}

func TestResolveTemplate_WhitespaceSizeTreatedAsUnset(t *testing.T) {
	p := newProviderForResolveTest(E2BProviderConfig{
		Template:    "parsar-daemon",
		Templates:   map[string]string{"standard": "parsar-daemon"},
		DefaultSize: "standard",
	})

	in := connector.PromptInput{
		ProjectAgentConfig: map[string]any{"sandbox_size": "   "},
	}
	size, tmpl := p.resolveTemplate(in)

	if size != "standard" {
		t.Errorf("size = %q, want %q (whitespace should be ignored)", size, "standard")
	}
	if tmpl != "parsar-daemon" {
		t.Errorf("template = %q, want %q", tmpl, "parsar-daemon")
	}
}
