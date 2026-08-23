package agentdaemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestResolveCapabilityAdditions_BundleSkillsInjected(t *testing.T) {
	t.Parallel()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindBundle,
		Bundle: &canonical.BundleSpec{
			Name:    "@internal/customer-service",
			Version: "1.0.0",
			Skills: []canonical.BundleSkill{
				{Slug: "greeting", Instruction: "Always greet warmly."},
				{Slug: "closing", Instruction: "End with a follow-up question."},
			},
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "cap-bundle-1",
		Name:          "customer-service",
		Type:          "bundle",
		CanonicalSpec: raw,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.SystemPrompts) != 2 {
		t.Fatalf("want 2 system_prompts from bundle skills, got %d: %+v", len(got.SystemPrompts), got.SystemPrompts)
	}
	// Verify first skill
	if got.SystemPrompts[0].Content != "Always greet warmly." {
		t.Fatalf("skill[0].Content = %q", got.SystemPrompts[0].Content)
	}
	if got.SystemPrompts[0].Mode != canonical.SystemPromptModeAppend {
		t.Fatalf("skill[0].Mode = %q, want append", got.SystemPrompts[0].Mode)
	}
	if got.SystemPrompts[0].Name != "bundle:@internal/customer-service/greeting" {
		t.Fatalf("skill[0].Name = %q", got.SystemPrompts[0].Name)
	}
	// Verify second skill
	if got.SystemPrompts[1].Content != "End with a follow-up question." {
		t.Fatalf("skill[1].Content = %q", got.SystemPrompts[1].Content)
	}
}

func TestResolveCapabilityAdditions_BundleEmptyCanonicalSpecSkipped(t *testing.T) {
	t.Parallel()
	row := store.EnabledCapabilityRead{
		CapabilityID:  "cap-bundle-empty",
		Name:          "ghost-bundle",
		Type:          "bundle",
		CanonicalSpec: nil, // empty
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.SystemPrompts) != 0 {
		t.Fatalf("expected no system_prompts, got %d", len(got.SystemPrompts))
	}
}

func TestResolveCapabilityAdditions_BundleKindMismatchErrors(t *testing.T) {
	t.Parallel()
	// Type=bundle but canonical_spec.kind=mcp — should error.
	mismatched := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP:           &canonical.MCPSpec{Servers: []canonical.MCPServer{{Name: "x", Command: "true"}}},
	}
	raw, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "bad-bundle",
		Name:          "bad",
		Type:          "bundle",
		CanonicalSpec: raw,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	_, err = c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil {
		t.Fatal("expected error for kind mismatch, got nil")
	}
}

func TestResolveCapabilityAdditions_BundleNoSkillsNoPrompts(t *testing.T) {
	t.Parallel()
	// A bundle with only server_entry (no skills) — Phase 0 produces no prompts.
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindBundle,
		Bundle: &canonical.BundleSpec{
			Name:        "@internal/tools-only",
			Version:     "1.0.0",
			ServerEntry: "./server/index.js",
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "cap-tools-only",
		Name:          "tools-only",
		Type:          "bundle",
		CanonicalSpec: raw,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got.SystemPrompts) != 0 {
		t.Fatalf("expected 0 prompts for tool-only bundle, got %d", len(got.SystemPrompts))
	}
}

func TestResolveCapabilityAdditions_BundleWorksOnAllEngines(t *testing.T) {
	t.Parallel()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindBundle,
		Bundle: &canonical.BundleSpec{
			Name:    "@internal/universal",
			Version: "1.0.0",
			Skills:  []canonical.BundleSkill{{Slug: "rule", Instruction: "Be helpful."}},
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "cap-u",
		Name:          "universal",
		Type:          "bundle",
		CanonicalSpec: raw,
	}
	for _, engine := range []string{"claude_code", "opencode", "codex", "pi"} {
		t.Run(engine, func(t *testing.T) {
			c := &Connector{
				capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
				log:          discardLogger(),
			}
			got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), engine)
			if err != nil {
				t.Fatalf("engine=%s: %v", engine, err)
			}
			if len(got.SystemPrompts) != 1 {
				t.Fatalf("engine=%s: want 1 prompt, got %d", engine, len(got.SystemPrompts))
			}
		})
	}
}
