package render

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// claudeCodeRenderer emits three shapes discriminated by Spec.Kind:
//
//  1. KindMCP    → an mcpServers-shaped document. The wire shape MUST stay
//     in sync with apps/parsar-daemon/internal/agent/claudecode/options.go
//     writeMCPTempfile (consumed via --mcp-config). Differs from OpenCode
//     in lacking an "enabled" field — Claude treats every entry as enabled.
//  2. KindSkill  → {name, version, oss_key, sha256}. Daemon downloads
//     the zip, extracts to <workDir>/.claude/skills/<name>/. Claude Code
//     auto-registers from that directory; no CLI flag needed.
//  3. KindPlugin → {name, version, oss_key, sha256}. Mirrors KindSkill;
//     daemon then spawns Claude with --plugin-dir per entry.
type claudeCodeRenderer struct{}

func (claudeCodeRenderer) Target() Target { return TargetClaudeCode }

type claudeCodeMCPDocument struct {
	MCPServers map[string]claudeCodeMCPServer `json:"mcpServers"`
}

type claudeCodeMCPServer struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// claudeCodeSkillDocument mirrors claudeCodePluginDocument byte-for-byte
// so the daemon's generic zip installer decodes both with the same code.
type claudeCodeSkillDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OssKey  string `json:"oss_key"`
	SHA256  string `json:"sha256"`
}

type claudeCodePluginDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OssKey  string `json:"oss_key"`
	SHA256  string `json:"sha256"`
}

func (claudeCodeRenderer) Render(_ context.Context, spec canonical.Spec) (Output, error) {
	if err := spec.Validate(); err != nil {
		return Output{}, fmt.Errorf("claudecode render: invalid spec: %w", err)
	}
	switch spec.Kind {
	case canonical.KindMCP:
		return renderClaudeCodeMCP(spec.MCP)
	case canonical.KindSkill:
		return renderClaudeCodeSkill(spec.Skill)
	case canonical.KindPlugin:
		return renderClaudeCodePlugin(spec.Plugin)
	case canonical.KindSystemPrompt:
		return renderSystemPrompt(spec.SystemPrompt)
	default:
		return Output{}, fmt.Errorf("claudecode render: unknown kind %q", spec.Kind)
	}
}

func renderClaudeCodeMCP(s *canonical.MCPSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("claudecode render: nil mcp spec")
	}
	doc := claudeCodeMCPDocument{MCPServers: make(map[string]claudeCodeMCPServer, len(s.Servers))}
	for _, srv := range s.Servers {
		env, err := renderEnvMap(srv.Env)
		if err != nil {
			return Output{}, fmt.Errorf("claudecode render: server %q: %w", srv.Name, err)
		}
		var args []string
		if len(srv.Args) > 0 {
			args = append(args, srv.Args...)
		}
		doc.MCPServers[srv.Name] = claudeCodeMCPServer{
			Command: srv.Command,
			Args:    args,
			Env:     env,
		}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return Output{}, fmt.Errorf("claudecode render: marshal: %w", err)
	}
	return Output{Content: body}, nil
}

// renderClaudeCodeSkill emits the descriptor the dispatch layer expands
// into a full ResolvedSkill (by attaching a freshly-minted presigned
// download URL + filling version/oss_key/sha256 from capability_version
// columns). Pure: no I/O. Mirrors renderClaudeCodePlugin.
func renderClaudeCodeSkill(s *canonical.SkillSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("claudecode render: nil skill spec")
	}
	// Slug becomes the on-disk skill name so it matches SKILL.md's
	// frontmatter `name` and Claude Code's init.skills listing.
	doc := claudeCodeSkillDocument{Name: s.Slug}
	body, err := json.Marshal(doc)
	if err != nil {
		return Output{}, fmt.Errorf("claudecode render: marshal skill: %w", err)
	}
	return Output{Content: body}, nil
}

// renderClaudeCodePlugin emits the descriptor the dispatch layer expands
// into a full ResolvedPlugin (by attaching a freshly-minted presigned
// download URL). Pure: no I/O — SHA-256 shape is validated by
// canonical.PluginSpec.Validate, we do not re-hash here.
func renderClaudeCodePlugin(s *canonical.PluginSpec) (Output, error) {
	if s == nil {
		return Output{}, fmt.Errorf("claudecode render: nil plugin spec")
	}
	doc := claudeCodePluginDocument{
		Name:    s.Name,
		Version: s.Version,
		OssKey:  s.OssKey,
		SHA256:  s.SHA256,
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return Output{}, fmt.Errorf("claudecode render: marshal plugin: %w", err)
	}
	return Output{Content: body}, nil
}
