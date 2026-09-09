package agentdaemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/render"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// resolveCapabilityMCPContent picks the right content source for an MCP
// capability and renders it via the Claude Code renderer.
//
// Precedence:
//  1. CanonicalSpec (M1+ imports) → render through TargetClaudeCode
//  2. Content (legacy hand-authored) → returned verbatim
func resolveCapabilityMCPContent(cap store.EnabledCapabilityRead, renderer render.Renderer) ([]byte, error) {
	if len(cap.CanonicalSpec) > 0 {
		var spec canonical.Spec
		if err := json.Unmarshal(cap.CanonicalSpec, &spec); err != nil {
			return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec decode: %w", cap.CapabilityID, err)
		}
		out, err := renderer.Render(context.Background(), spec)
		if err != nil {
			return nil, fmt.Errorf("agent_daemon: capability %s render: %w", cap.CapabilityID, err)
		}
		return out.Content, nil
	}
	return cap.Content, nil
}

// resolveSystemPromptCapability decodes a capability_version.canonical_spec
// into a ResolvedSystemPrompt. The render call is kept for wire-shape
// consistency only (mirrors resolveSkillCapability); the prompt text is
// read straight from the canonical spec because the daemon consumes
// agent_options.system_prompt / override_system_prompt, not a rendered
// capability blob.
func (c *Connector) resolveSystemPromptCapability(
	ctx context.Context,
	cap store.EnabledCapabilityRead,
	renderer render.Renderer,
) (*ResolvedSystemPrompt, error) {
	resolved := resolveVersionFields(cap)
	if len(resolved.CanonicalSpec) == 0 {
		c.log.Warn("agent_daemon: system_prompt capability has empty canonical_spec, skipping",
			"capability_id", cap.CapabilityID,
			"capability_name", cap.Name)
		return nil, nil
	}
	var spec canonical.Spec
	if err := json.Unmarshal(resolved.CanonicalSpec, &spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: system_prompt capability %s canonical_spec decode: %w", cap.CapabilityID, err)
	}
	if spec.Kind != canonical.KindSystemPrompt {
		return nil, fmt.Errorf("agent_daemon: capability %s has type=system_prompt but canonical_spec.kind=%q", cap.CapabilityID, spec.Kind)
	}
	if spec.SystemPrompt == nil {
		return nil, fmt.Errorf("agent_daemon: capability %s canonical_spec.system_prompt is nil", cap.CapabilityID)
	}
	if _, err := renderer.Render(ctx, spec); err != nil {
		return nil, fmt.Errorf("agent_daemon: render system_prompt %s: %w", cap.CapabilityID, err)
	}
	return &ResolvedSystemPrompt{
		Name:    strings.TrimSpace(cap.Name),
		Mode:    spec.SystemPrompt.ResolvedMode(),
		Content: spec.SystemPrompt.Prompt,
	}, nil
}
