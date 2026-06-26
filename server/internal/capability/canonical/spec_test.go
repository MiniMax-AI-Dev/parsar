package canonical

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestSpec_RoundTrip verifies the JSON encoding round-trips for both kinds
// without losing fields. The wire shape matters because canonical_spec is a
// jsonb column — any change here is an implicit schema break.
func TestSpec_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Spec
	}{
		{
			name: "mcp with mixed env modes",
			in: Spec{
				SchemaVersion: SchemaVersionCurrent,
				Kind:          KindMCP,
				MCP: &MCPSpec{
					Servers: []MCPServer{{
						Name:    "github",
						Command: "docker",
						Args:    []string{"run", "-i", "--rm", "ghcr.io/github/github-mcp-server"},
						Env: map[string]EnvValue{
							"GITHUB_HOST":                  {Mode: EnvModeLiteral, Literal: "https://api.github.com"},
							"INLINE":                       {Mode: EnvModeInlineSecret, SecretID: "00000000-0000-0000-0000-000000000001"},
							"GITHUB_PERSONAL_ACCESS_TOKEN": {Mode: EnvModeCredentialRef, CredentialKindCode: "github_pat"},
						},
						StartupTimeoutSec: 30,
					}},
				},
			},
		},
		{
			name: "skill",
			in: Spec{
				SchemaVersion: SchemaVersionCurrent,
				Kind:          KindSkill,
				Skill: &SkillSpec{
					Slug:        "writeup-reviewer",
					Title:       "Writeup Reviewer",
					Description: "Reviews technical writeups",
					Instruction: "You are a technical reviewer. Read carefully.",
					Trigger:     "when reviewing markdown writeups",
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Spec
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("validate decoded spec: %v", err)
			}
			// Re-encode and compare for stability.
			re, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if string(re) != string(encoded) {
				t.Fatalf("round-trip diverged:\norig=%s\n got=%s", encoded, re)
			}
		})
	}
}

// TestSpec_UnmarshalRejectsMissingKind ensures we don't silently accept
// payloads that would default Kind to "" and pass downstream.
func TestSpec_UnmarshalRejectsMissingKind(t *testing.T) {
	var got Spec
	err := json.Unmarshal([]byte(`{"schema_version":1}`), &got)
	if err == nil {
		t.Fatal("expected error decoding spec without kind")
	}
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec, got %v", err)
	}
}

// TestSpec_UnmarshalRejectsWrongBody ensures a kind=mcp payload without an mcp
// body fails fast at decode rather than reaching Validate.
func TestSpec_UnmarshalRejectsWrongBody(t *testing.T) {
	var got Spec
	err := json.Unmarshal([]byte(`{"schema_version":1,"kind":"mcp"}`), &got)
	if err == nil {
		t.Fatal("expected error for kind=mcp without mcp body")
	}
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec, got %v", err)
	}
}

// TestEnvValue_ValidateModeFieldExclusivity locks in the rule that each Mode
// pairs with exactly one field family — see EnvValue.Validate.
func TestEnvValue_ValidateModeFieldExclusivity(t *testing.T) {
	cases := []struct {
		name    string
		ev      EnvValue
		wantErr bool
	}{
		{"literal ok", EnvValue{Mode: EnvModeLiteral, Literal: "hello"}, false},
		{"literal carries secret_id", EnvValue{Mode: EnvModeLiteral, Literal: "x", SecretID: "s"}, true},
		{"inline_secret needs secret_id", EnvValue{Mode: EnvModeInlineSecret}, true},
		{"inline_secret ok", EnvValue{Mode: EnvModeInlineSecret, SecretID: "s"}, false},
		{"inline_secret carries literal", EnvValue{Mode: EnvModeInlineSecret, SecretID: "s", Literal: "y"}, true},
		{"credential_ref needs kind", EnvValue{Mode: EnvModeCredentialRef}, true},
		{"credential_ref ok", EnvValue{Mode: EnvModeCredentialRef, CredentialKindCode: "github_pat"}, false},
		{"credential_ref carries secret_id", EnvValue{Mode: EnvModeCredentialRef, CredentialKindCode: "k", SecretID: "s"}, true},
		{"unknown mode rejected", EnvValue{Mode: "random"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ev.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

// TestMCPSpec_ValidateDetectsDuplicateName protects the renderer assumption
// that server names within a spec are unique (some scaffolds key on name).
func TestMCPSpec_ValidateDetectsDuplicateName(t *testing.T) {
	s := MCPSpec{Servers: []MCPServer{
		{Name: "a", Command: "x"},
		{Name: "a", Command: "y"},
	}}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate name error, got %v", err)
	}
}
