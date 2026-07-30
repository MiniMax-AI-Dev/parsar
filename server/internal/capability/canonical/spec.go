// Package canonical defines the schema-versioned, scaffold-agnostic
// description of a capability that lives in capability_version.canonical_spec.
//
// Any scaffold-specific shape (OpenCode's `environment` vs Claude Code's
// `env`, Codex TOML, etc.) is built lazily from this struct by the render
// package.
//
// Stability: the JSON wire shape is anchored by capability_version.schema_version.
// Bumping SchemaVersionCurrent means writing a new branch in parser/renderer
// rather than mutating the existing JSON shape.
package canonical

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SchemaVersionCurrent is the canonical_spec schema version written by
// new code paths. Stored as capability_version.schema_version (smallint).
const SchemaVersionCurrent int16 = 1

type Kind string

const (
	KindMCP          Kind = "mcp"
	KindSkill        Kind = "skill"
	KindPlugin       Kind = "plugin"
	KindSystemPrompt Kind = "system_prompt"
)

// Spec is the top-level canonical capability description. Exactly one of
// MCP / Skill / Plugin / SystemPrompt is non-nil; the populated branch must
// match Kind.
type Spec struct {
	SchemaVersion int16             `json:"schema_version"`
	Kind          Kind              `json:"kind"`
	MCP           *MCPSpec          `json:"mcp,omitempty"`
	Skill         *SkillSpec        `json:"skill,omitempty"`
	Plugin        *PluginSpec       `json:"plugin,omitempty"`
	SystemPrompt  *SystemPromptSpec `json:"system_prompt,omitempty"`
}

// Validate performs structural sanity checks. It does NOT consult external
// state (no DB lookups), so it is safe to call from preview handlers before
// credentials are resolved. Errors wrap a package-level sentinel via %w.
func (s Spec) Validate() error {
	if s.SchemaVersion <= 0 {
		return fmt.Errorf("%w: schema_version must be > 0", ErrInvalidSpec)
	}
	switch s.Kind {
	case KindMCP:
		if s.MCP == nil {
			return fmt.Errorf("%w: kind=mcp but mcp body is nil", ErrInvalidSpec)
		}
		if s.Skill != nil || s.Plugin != nil || s.SystemPrompt != nil {
			return fmt.Errorf("%w: kind=mcp but another body is set", ErrInvalidSpec)
		}
		return s.MCP.Validate()
	case KindSkill:
		if s.Skill == nil {
			return fmt.Errorf("%w: kind=skill but skill body is nil", ErrInvalidSpec)
		}
		if s.MCP != nil || s.Plugin != nil || s.SystemPrompt != nil {
			return fmt.Errorf("%w: kind=skill but another body is set", ErrInvalidSpec)
		}
		return s.Skill.Validate()
	case KindPlugin:
		if s.Plugin == nil {
			return fmt.Errorf("%w: kind=plugin but plugin body is nil", ErrInvalidSpec)
		}
		if s.MCP != nil || s.Skill != nil || s.SystemPrompt != nil {
			return fmt.Errorf("%w: kind=plugin but another body is set", ErrInvalidSpec)
		}
		return s.Plugin.Validate()
	case KindSystemPrompt:
		if s.SystemPrompt == nil {
			return fmt.Errorf("%w: kind=system_prompt but system_prompt body is nil", ErrInvalidSpec)
		}
		if s.MCP != nil || s.Skill != nil || s.Plugin != nil {
			return fmt.Errorf("%w: kind=system_prompt but another body is set", ErrInvalidSpec)
		}
		return s.SystemPrompt.Validate()
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidSpec, s.Kind)
	}
}

// specWire is the decode-time shape: deferring the body unmarshal lets us
// reject specs with a missing schema_version up front instead of silently
// defaulting to 0 and tripping Validate later.
type specWire struct {
	SchemaVersion int16           `json:"schema_version"`
	Kind          Kind            `json:"kind"`
	MCP           json.RawMessage `json:"mcp,omitempty"`
	Skill         json.RawMessage `json:"skill,omitempty"`
	Plugin        json.RawMessage `json:"plugin,omitempty"`
	SystemPrompt  json.RawMessage `json:"system_prompt,omitempty"`
}

// UnmarshalJSON only decodes the body matching Kind so a malformed inactive
// branch doesn't fail the parse of an otherwise valid spec.
func (s *Spec) UnmarshalJSON(data []byte) error {
	var w specWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	s.SchemaVersion = w.SchemaVersion
	s.Kind = w.Kind
	switch w.Kind {
	case KindMCP:
		if len(w.MCP) == 0 {
			return fmt.Errorf("%w: kind=mcp but no mcp body", ErrInvalidSpec)
		}
		var body MCPSpec
		if err := json.Unmarshal(w.MCP, &body); err != nil {
			return fmt.Errorf("decode mcp body: %w", err)
		}
		s.MCP = &body
	case KindSkill:
		if len(w.Skill) == 0 {
			return fmt.Errorf("%w: kind=skill but no skill body", ErrInvalidSpec)
		}
		var body SkillSpec
		if err := json.Unmarshal(w.Skill, &body); err != nil {
			return fmt.Errorf("decode skill body: %w", err)
		}
		s.Skill = &body
	case KindPlugin:
		if len(w.Plugin) == 0 {
			return fmt.Errorf("%w: kind=plugin but no plugin body", ErrInvalidSpec)
		}
		var body PluginSpec
		if err := json.Unmarshal(w.Plugin, &body); err != nil {
			return fmt.Errorf("decode plugin body: %w", err)
		}
		s.Plugin = &body
	case KindSystemPrompt:
		if len(w.SystemPrompt) == 0 {
			return fmt.Errorf("%w: kind=system_prompt but no system_prompt body", ErrInvalidSpec)
		}
		var body SystemPromptSpec
		if err := json.Unmarshal(w.SystemPrompt, &body); err != nil {
			return fmt.Errorf("decode system_prompt body: %w", err)
		}
		s.SystemPrompt = &body
	case "":
		return fmt.Errorf("%w: missing kind", ErrInvalidSpec)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidSpec, w.Kind)
	}
	return nil
}

// EnvMode is the discriminator for how a single MCP env value is materialized
// at runtime.
//
// The user explicitly chooses the mode in the import UI; the system NEVER
// auto-promotes a literal into inline_secret/credential_ref.
type EnvMode string

const (
	EnvModeLiteral EnvMode = "literal"

	// EnvModeInlineSecret: value lives in the workspace `secrets` table,
	// referenced by EnvValue.SecretID. Cleartext is never persisted in the Spec.
	EnvModeInlineSecret EnvMode = "inline_secret"

	// EnvModeCredentialRef: value resolves per-user from `user_credentials`
	// at runtime via EnvValue.CredentialKindCode. The Spec only knows the kind.
	EnvModeCredentialRef EnvMode = "credential_ref"
)

// EnvValue describes one entry in an MCPServer.Env map. The active fields
// depend on Mode; see EnvMode docs.
type EnvValue struct {
	Mode EnvMode `json:"mode"`

	Prefix string `json:"prefix,omitempty"`

	// Literal is set iff Mode == EnvModeLiteral.
	Literal string `json:"literal,omitempty"`

	// SecretID is a `secrets.id` (uuid) set iff Mode == EnvModeInlineSecret.
	SecretID string `json:"secret_id,omitempty"`

	// CredentialKindCode is a `credential_kinds.code` set iff
	// Mode == EnvModeCredentialRef.
	CredentialKindCode string `json:"credential_kind_code,omitempty"`
}

// Validate checks structural consistency between Mode and the per-mode fields.
func (v EnvValue) Validate() error {
	switch v.Mode {
	case EnvModeLiteral:
		if v.SecretID != "" || v.CredentialKindCode != "" || v.Prefix != "" {
			return fmt.Errorf("%w: literal mode must not set secret_id/credential_kind_code/prefix", ErrInvalidEnvValue)
		}
	case EnvModeInlineSecret:
		if strings.TrimSpace(v.SecretID) == "" {
			return fmt.Errorf("%w: inline_secret mode requires secret_id", ErrInvalidEnvValue)
		}
		if v.Literal != "" || v.CredentialKindCode != "" {
			return fmt.Errorf("%w: inline_secret mode must not set literal/credential_kind_code", ErrInvalidEnvValue)
		}
	case EnvModeCredentialRef:
		if strings.TrimSpace(v.CredentialKindCode) == "" {
			return fmt.Errorf("%w: credential_ref mode requires credential_kind_code", ErrInvalidEnvValue)
		}
		if v.Literal != "" || v.SecretID != "" {
			return fmt.Errorf("%w: credential_ref mode must not set literal/secret_id", ErrInvalidEnvValue)
		}
	default:
		return fmt.Errorf("%w: unknown env mode %q", ErrInvalidEnvValue, v.Mode)
	}
	return nil
}
