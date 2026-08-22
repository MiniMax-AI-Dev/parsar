package binpath

import "testing"

func TestResolvers(t *testing.T) {
	tests := []struct {
		name       string
		envVar     string
		fallback   string
		resolve    func() string
		override   string
		wantPinned string
	}{
		{name: "claude", envVar: EnvClaudeCode, fallback: DefaultClaudeCode, resolve: ClaudeCode, override: "  /opt/agents/claude  ", wantPinned: "/opt/agents/claude"},
		{name: "codex", envVar: EnvCodex, fallback: DefaultCodex, resolve: Codex, override: "  /opt/agents/codex  ", wantPinned: "/opt/agents/codex"},
		{name: "pi", envVar: EnvPi, fallback: DefaultPi, resolve: Pi, override: "  /opt/agents/pi  ", wantPinned: "/opt/agents/pi"},
		{name: "opencode", envVar: EnvOpenCode, fallback: DefaultOpenCode, resolve: OpenCode, override: "  /opt/agents/opencode  ", wantPinned: "/opt/agents/opencode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envVar, "")
			if got := tt.resolve(); got != tt.fallback {
				t.Fatalf("unset override: got %q, want %q", got, tt.fallback)
			}

			t.Setenv(tt.envVar, tt.override)
			if got := tt.resolve(); got != tt.wantPinned {
				t.Fatalf("explicit override: got %q, want %q", got, tt.wantPinned)
			}

			t.Setenv(tt.envVar, " \t ")
			if got := tt.resolve(); got != tt.fallback {
				t.Fatalf("blank override: got %q, want %q", got, tt.fallback)
			}
		})
	}
}
