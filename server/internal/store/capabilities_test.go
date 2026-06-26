package store

import "testing"

// normalizeCapabilityType is the gatekeeper that decides what string
// lands in capability.type. A missing case silently rewrites to "mcp"
// — system_prompt regressed exactly this way once, so pin every Kind
// declared in canonical.Kind here.
func TestNormalizeCapabilityType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"skill", "skill"},
		{"mcp", "mcp"},
		{"plugin", "plugin"},
		{"system_prompt", "system_prompt"},
		{" system_prompt ", "system_prompt"},
		{"", "mcp"},
		{"bogus", "mcp"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := normalizeCapabilityType(tc.in); got != tc.want {
				t.Fatalf("normalizeCapabilityType(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
