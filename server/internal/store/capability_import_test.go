package store

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestPatchInlineSecretID_HappyPath(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "github",
				Command: "docker",
				Env: map[string]canonical.EnvValue{
					"GITHUB_PAT": {Mode: canonical.EnvModeInlineSecret},
				},
			}},
		},
	}
	if err := patchInlineSecretID(&spec, "github", "GITHUB_PAT", "secret-uuid-123"); err != nil {
		t.Fatalf("patch: %v", err)
	}
	got := spec.MCP.Servers[0].Env["GITHUB_PAT"]
	if got.SecretID != "secret-uuid-123" {
		t.Fatalf("want secret-uuid-123, got %q", got.SecretID)
	}
	if got.Mode != canonical.EnvModeInlineSecret {
		t.Fatalf("mode should still be inline_secret, got %q", got.Mode)
	}
}

// Silently overwriting a literal entry would destroy the user's value, so
// patching a non-inline_secret slot must fail.
func TestPatchInlineSecretID_RejectsWrongMode(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name: "x",
				Env: map[string]canonical.EnvValue{
					"K": {Mode: canonical.EnvModeLiteral, Literal: "hello"},
				},
			}},
		},
	}
	err := patchInlineSecretID(&spec, "x", "K", "secret-uuid-123")
	if err == nil {
		t.Fatalf("expected error patching a literal entry")
	}
	if !strings.Contains(err.Error(), "not in inline_secret mode") {
		t.Fatalf("error should mention mode mismatch, got: %v", err)
	}
}

// Duplicate patches indicate an orchestrator bug; failing loudly beats
// silently overwriting the prior secret_id.
func TestPatchInlineSecretID_RejectsAlreadyResolved(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name: "x",
				Env: map[string]canonical.EnvValue{
					"K": {Mode: canonical.EnvModeInlineSecret, SecretID: "existing-id"},
				},
			}},
		},
	}
	if err := patchInlineSecretID(&spec, "x", "K", "new-id"); err == nil {
		t.Fatalf("expected error patching already-resolved slot")
	}
}

func TestPatchInlineSecretID_UnknownServer(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP:  &canonical.MCPSpec{Servers: []canonical.MCPServer{{Name: "real-server"}}},
	}
	err := patchInlineSecretID(&spec, "ghost-server", "K", "x")
	if err == nil {
		t.Fatalf("expected error for missing server")
	}
	if !strings.Contains(err.Error(), "ghost-server") {
		t.Fatalf("error should name the missing server, got: %v", err)
	}
}

func TestPatchInlineSecretID_UnknownEnvKey(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name: "x",
				Env:  map[string]canonical.EnvValue{"REAL_KEY": {Mode: canonical.EnvModeInlineSecret}},
			}},
		},
	}
	err := patchInlineSecretID(&spec, "x", "WRONG_KEY", "id")
	if err == nil {
		t.Fatalf("expected error for missing env key")
	}
}

func TestAssertInlineSecretsResolved_AllResolved(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name: "x",
				Env: map[string]canonical.EnvValue{
					"A": {Mode: canonical.EnvModeLiteral, Literal: "v"},
					"B": {Mode: canonical.EnvModeInlineSecret, SecretID: "sid"},
					"C": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "github_pat"},
				},
			}},
		},
	}
	if err := assertInlineSecretsResolved(spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAssertInlineSecretsResolved_DetectsUnresolved(t *testing.T) {
	spec := canonical.Spec{
		Kind: canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name: "x",
				Env: map[string]canonical.EnvValue{
					"BAD": {Mode: canonical.EnvModeInlineSecret},
				},
			}},
		},
	}
	err := assertInlineSecretsResolved(spec)
	if err == nil {
		t.Fatalf("expected error for unresolved inline_secret")
	}
	if !strings.Contains(err.Error(), "BAD") {
		t.Fatalf("error should reference the offending env key, got: %v", err)
	}
}

func TestAssertInlineSecretsResolved_SkillSpecIsOK(t *testing.T) {
	spec := canonical.Spec{
		Kind:  canonical.KindSkill,
		Skill: &canonical.SkillSpec{Slug: "x", Instruction: "y"},
	}
	if err := assertInlineSecretsResolved(spec); err != nil {
		t.Fatalf("skill spec should pass through: %v", err)
	}
}

// The pre-commit gate accepts empty SecretID on inline_secret entries (filled
// in inside the tx); strict canonical.Spec.Validate() would reject the same input.
func TestValidateImportSpecPreCommit_AllowsEmptyInlineSecretID(t *testing.T) {
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "github",
				Command: "docker",
				Env: map[string]canonical.EnvValue{
					"PAT": {Mode: canonical.EnvModeInlineSecret /* SecretID intentionally empty */},
				},
			}},
		},
	}
	if err := spec.Validate(); err == nil {
		t.Fatalf("sanity check: strict Validate() must still reject empty SecretID")
	}
	if err := validateImportSpecPreCommit(spec); err != nil {
		t.Fatalf("pre-commit validate must allow unresolved inline_secret: %v", err)
	}
}

func TestValidateImportSpecPreCommit_StillRejectsStructuralIssues(t *testing.T) {
	cases := []struct {
		name string
		spec canonical.Spec
		want string
	}{
		{
			name: "missing schema_version",
			spec: canonical.Spec{Kind: canonical.KindMCP, MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{Name: "x", Command: "c"}}}},
			want: "schema_version",
		},
		{
			name: "empty command",
			spec: canonical.Spec{SchemaVersion: 1, Kind: canonical.KindMCP, MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{Name: "x"}}}},
			want: "command is required",
		},
		{
			name: "literal mode with stray secret_id",
			spec: canonical.Spec{
				SchemaVersion: 1, Kind: canonical.KindMCP,
				MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{
					Name: "x", Command: "c",
					Env: map[string]canonical.EnvValue{"K": {Mode: canonical.EnvModeLiteral, SecretID: "leak"}},
				}}},
			},
			want: "literal mode must not set",
		},
		{
			name: "credential_ref without code",
			spec: canonical.Spec{
				SchemaVersion: 1, Kind: canonical.KindMCP,
				MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{
					Name: "x", Command: "c",
					Env: map[string]canonical.EnvValue{"K": {Mode: canonical.EnvModeCredentialRef}},
				}}},
			},
			want: "credential_ref mode requires credential_kind_code",
		},
		{
			name: "duplicate server names",
			spec: canonical.Spec{
				SchemaVersion: 1, Kind: canonical.KindMCP,
				MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{
					{Name: "dup", Command: "c"},
					{Name: "dup", Command: "c"},
				}},
			},
			want: "duplicate server name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImportSpecPreCommit(tc.spec)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got: %v", tc.want, err)
			}
		})
	}
}
