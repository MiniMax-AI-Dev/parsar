package render

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// mcpFixture exercises all env modes so renderer changes that touch the
// placeholder format are immediately visible.
func mcpFixture() canonical.Spec {
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{
				{
					Name:    "github",
					Command: "docker",
					Args:    []string{"run", "-i", "--rm", "ghcr.io/github/github-mcp-server"},
					Env: map[string]canonical.EnvValue{
						"GITHUB_HOST":                  {Mode: canonical.EnvModeLiteral, Literal: "https://api.github.com"},
						"GITHUB_PERSONAL_ACCESS_TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "github_pat"},
						"WORKSPACE_SECRET":             {Mode: canonical.EnvModeInlineSecret, SecretID: "00000000-0000-0000-0000-000000000001"},
					},
				},
			},
		},
	}
}

func skillFixture() canonical.Spec {
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        "writeup-reviewer",
			Title:       "Writeup Reviewer",
			Instruction: "You are a technical reviewer. Read carefully.",
		},
	}
}

func remoteMCPFixture() canonical.Spec {
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{
			Name:      "docs",
			Transport: canonical.MCPTransportStreamableHTTP,
			URL:       "https://docs.example.com/mcp",
		}}},
	}
}

// TestFor_KnownTargets catches "added a Target without wiring For()".
func TestFor_KnownTargets(t *testing.T) {
	for _, target := range []Target{TargetOpenCode, TargetClaudeCode, TargetCodex, TargetPi} {
		r, err := For(target)
		if err != nil {
			t.Fatalf("For(%q) error: %v", target, err)
		}
		if r.Target() != target {
			t.Fatalf("For(%q) returned renderer with Target()=%q", target, r.Target())
		}
	}
}

func TestFor_UnknownTarget(t *testing.T) {
	if _, err := For(Target("???")); err == nil {
		t.Fatal("expected error for unknown target")
	}
}

// TestOpenCodeRenderer_MCPGolden locks in the wire shape the OpenCode
// connector reads back. Changes here must coordinate with capability_runtime.go.
func TestOpenCodeRenderer_MCPGolden(t *testing.T) {
	out, err := openCodeRenderer{}.Render(context.Background(), mcpFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got openCodeMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	srv, ok := got.MCPServers["github"]
	if !ok {
		t.Fatalf("missing server 'github': got %+v", got)
	}
	if srv.Command != "docker" {
		t.Fatalf("command: want docker got %q", srv.Command)
	}
	if !srv.Enabled {
		t.Fatalf("server should default enabled=true")
	}
	if got, want := len(srv.Args), 4; got != want {
		t.Fatalf("args len: want %d got %d", want, got)
	}
	if srv.Args[0] != "run" {
		t.Fatalf("args[0]: want run got %q", srv.Args[0])
	}
	if got, want := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"], "${PARSAR_CREDENTIAL:github_pat}"; got != want {
		t.Fatalf("credential placeholder: want %q got %q", want, got)
	}
	if got, want := srv.Env["WORKSPACE_SECRET"], "${PARSAR_SECRET:00000000-0000-0000-0000-000000000001}"; got != want {
		t.Fatalf("secret placeholder: want %q got %q", want, got)
	}
	if got, want := srv.Env["GITHUB_HOST"], "https://api.github.com"; got != want {
		t.Fatalf("literal value: want %q got %q", want, got)
	}
}

func TestOpenCodeRenderer_SkillUnsupported(t *testing.T) {
	_, err := openCodeRenderer{}.Render(context.Background(), skillFixture())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestClaudeCodeRenderer_MCPGolden mirrors the opencode test but asserts
// the Claude Code shape must NOT carry opencode's "enabled" field.
func TestClaudeCodeRenderer_MCPGolden(t *testing.T) {
	out, err := claudeCodeRenderer{}.Render(context.Background(), mcpFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got claudeCodeMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	srv := got.MCPServers["github"]
	if srv.Command != "docker" {
		t.Fatalf("command: want docker got %q", srv.Command)
	}
	if strings.Contains(string(out.Content), "\"enabled\"") {
		t.Fatalf("claudecode payload should not include 'enabled' field: %s", out.Content)
	}
	if got, want := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"], "${PARSAR_CREDENTIAL:github_pat}"; got != want {
		t.Fatalf("credential placeholder: want %q got %q", want, got)
	}
	if got, want := srv.Env["WORKSPACE_SECRET"], "${PARSAR_SECRET:00000000-0000-0000-0000-000000000001}"; got != want {
		t.Fatalf("secret placeholder: want %q got %q", want, got)
	}
}

func TestClaudeCodeRenderer_StreamableHTTP(t *testing.T) {
	out, err := claudeCodeRenderer{}.Render(context.Background(), remoteMCPFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got claudeCodeMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatal(err)
	}
	srv := got.MCPServers["docs"]
	if srv.Type != "http" || srv.URL != "https://docs.example.com/mcp" || srv.Command != "" {
		t.Fatalf("server = %+v", srv)
	}
}

func TestClaudeCodeRenderer_SkillGolden(t *testing.T) {
	out, err := claudeCodeRenderer{}.Render(context.Background(), skillFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got claudeCodeSkillDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	// Renderer copies SkillSpec.Slug into Name. version / oss_key /
	// sha256 come from capability_version columns at the connector
	// layer (mirrors plugin renderer behaviour), so renderer-level
	// values are empty.
	if got.Name == "" {
		t.Fatalf("name should be populated from SkillSpec.Slug, got empty: %s", out.Content)
	}
	// Wire-shape parity with plugin: same field names so the daemon
	// can use the same descriptor decoder.
	for _, key := range []string{`"name"`, `"version"`, `"oss_key"`, `"sha256"`} {
		if !strings.Contains(string(out.Content), key) {
			t.Fatalf("payload missing %s key: %s", key, out.Content)
		}
	}
}

// TestClaudeCodeRenderer_SkillRequiresInstruction asserts the renderer
// surfaces a Validate-level error rather than producing an empty
// append_system_prompt (which would silently no-op at the agent).
func TestClaudeCodeRenderer_SkillRequiresInstruction(t *testing.T) {
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        "empty-body",
			Instruction: "   ",
		},
	}
	if _, err := (claudeCodeRenderer{}).Render(context.Background(), spec); err == nil {
		t.Fatal("expected error for empty instruction body")
	}
}

// TestCodexRenderer_MCPGolden locks the Codex MCP wire shape. The shape
// is intentionally identical to claudecode so the connector's
// claudeCodeMCPDocument unmarshal in capability_runtime.go can decode
// either target's output without per-engine branching.
func TestCodexRenderer_MCPGolden(t *testing.T) {
	out, err := codexRenderer{}.Render(context.Background(), mcpFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got codexMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	srv, ok := got.MCPServers["github"]
	if !ok {
		t.Fatalf("missing server 'github': got %+v", got)
	}
	if srv.Command != "docker" {
		t.Fatalf("command: want docker got %q", srv.Command)
	}
	if got, want := len(srv.Args), 4; got != want {
		t.Fatalf("args len: want %d got %d", want, got)
	}
	// The "enabled" field is an opencode-ism; codex must not carry it.
	if strings.Contains(string(out.Content), "\"enabled\"") {
		t.Fatalf("codex payload should not include 'enabled' field: %s", out.Content)
	}
	if got, want := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"], "${PARSAR_CREDENTIAL:github_pat}"; got != want {
		t.Fatalf("credential placeholder: want %q got %q", want, got)
	}
	if got, want := srv.Env["WORKSPACE_SECRET"], "${PARSAR_SECRET:00000000-0000-0000-0000-000000000001}"; got != want {
		t.Fatalf("secret placeholder: want %q got %q", want, got)
	}
}

func TestCodexRenderer_StreamableHTTP(t *testing.T) {
	out, err := codexRenderer{}.Render(context.Background(), remoteMCPFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got codexMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatal(err)
	}
	srv := got.MCPServers["docs"]
	if srv.Type != "http" || srv.URL != "https://docs.example.com/mcp" || srv.Command != "" {
		t.Fatalf("server = %+v", srv)
	}
}

func TestOpenCodeRenderer_StreamableHTTP(t *testing.T) {
	out, err := openCodeRenderer{}.Render(context.Background(), remoteMCPFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got openCodeMCPDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatal(err)
	}
	srv := got.MCPServers["docs"]
	if srv.Type != "remote" || srv.URL != "https://docs.example.com/mcp" || !srv.Enabled {
		t.Fatalf("server = %+v", srv)
	}
}

// TestCodexRenderer_SkillAndPluginUnsupported pins the soft-degrade
// contract — codex must return ErrUnsupported for Skill and Plugin so
// the agentdaemon connector skips them with a Disabled notice instead
// of hard-failing the prompt. Don't bundle MCP here: that case is
// covered by TestCodexRenderer_MCPGolden and would re-regress this
// test back into the old "always unsupported" shape if accidentally
// edited.
func TestCodexRenderer_SkillAndPluginUnsupported(t *testing.T) {
	cases := []canonical.Spec{
		skillFixture(),
		{
			SchemaVersion: canonical.SchemaVersionCurrent,
			Kind:          canonical.KindPlugin,
			Plugin: &canonical.PluginSpec{
				Name:         "my-plugin",
				Version:      "1.0.0",
				Description:  "x",
				OssKey:       "capabilities/plugins/u1/my-plugin.zip",
				SHA256:       "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
				UploadSource: canonical.UploadSourceZip,
			},
		},
	}
	for _, spec := range cases {
		_, err := codexRenderer{}.Render(context.Background(), spec)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("expected ErrUnsupported for kind=%s, got %v", spec.Kind, err)
		}
	}
}

// TestCodexRenderer_MCPByteEqualsClaudeCode locks the contract reviewer
// flagged: the connector's claudeCodeMCPDocument unmarshal currently
// decodes BOTH codex and claudecode renderer output. If a future field
// is added to one shape and not the other, the unmarshal will keep
// working (silent forwards-compat) but capability_runtime.go's mcp_servers
// will quietly diverge per engine.
//
// Comparing through canonicalize() rather than raw bytes so map-key
// ordering jitter from encoding/json isn't a false positive — the actual
// invariant we want is "same keys, same values on every server entry."
func TestCodexRenderer_MCPByteEqualsClaudeCode(t *testing.T) {
	codexOut, err := codexRenderer{}.Render(context.Background(), mcpFixture())
	if err != nil {
		t.Fatalf("codex render: %v", err)
	}
	claudeOut, err := claudeCodeRenderer{}.Render(context.Background(), mcpFixture())
	if err != nil {
		t.Fatalf("claudecode render: %v", err)
	}
	codexCanon, err := canonicalizeJSON(codexOut.Content)
	if err != nil {
		t.Fatalf("canonicalize codex: %v\nraw=%s", err, codexOut.Content)
	}
	claudeCanon, err := canonicalizeJSON(claudeOut.Content)
	if err != nil {
		t.Fatalf("canonicalize claudecode: %v\nraw=%s", err, claudeOut.Content)
	}
	if codexCanon != claudeCanon {
		t.Fatalf("codex and claudecode MCP shapes diverged.\ncodex     = %s\nclaudecode= %s",
			codexCanon, claudeCanon)
	}
}

// TestPiRenderer_SkillGolden mirrors the claudecode skill golden: pi
// reuses Claude Code's skill descriptor verbatim so the daemon's generic
// zip installer decodes pi's --skill payload with one code path.
func TestPiRenderer_SkillGolden(t *testing.T) {
	out, err := piRenderer{}.Render(context.Background(), skillFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got claudeCodeSkillDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	if got.Name == "" {
		t.Fatalf("name should be populated from SkillSpec.Slug, got empty: %s", out.Content)
	}
	for _, key := range []string{`"name"`, `"version"`, `"oss_key"`, `"sha256"`} {
		if !strings.Contains(string(out.Content), key) {
			t.Fatalf("payload missing %s key: %s", key, out.Content)
		}
	}
}

// TestPiRenderer_SkillByteEqualsClaudeCode is the parity guard the daemon
// relies on: pi and claudecode skill descriptors decode through the same
// claudeCodeSkillDocument unmarshal, so any field that lands on one shape
// and not the other silently breaks pi skill installs. Compared through
// canonicalize() so map-key ordering jitter isn't a false positive.
func TestPiRenderer_SkillByteEqualsClaudeCode(t *testing.T) {
	piOut, err := piRenderer{}.Render(context.Background(), skillFixture())
	if err != nil {
		t.Fatalf("pi render: %v", err)
	}
	claudeOut, err := claudeCodeRenderer{}.Render(context.Background(), skillFixture())
	if err != nil {
		t.Fatalf("claudecode render: %v", err)
	}
	piCanon, err := canonicalizeJSON(piOut.Content)
	if err != nil {
		t.Fatalf("canonicalize pi: %v\nraw=%s", err, piOut.Content)
	}
	claudeCanon, err := canonicalizeJSON(claudeOut.Content)
	if err != nil {
		t.Fatalf("canonicalize claudecode: %v\nraw=%s", err, claudeOut.Content)
	}
	if piCanon != claudeCanon {
		t.Fatalf("pi and claudecode skill shapes diverged.\npi        = %s\nclaudecode= %s",
			piCanon, claudeCanon)
	}
}

// TestPiRenderer_MCPAndPluginUnsupported pins pi's soft-degrade contract:
// managed MCP and plugin delivery are out of scope, so both return
// ErrUnsupported and the agentdaemon connector skips them with a Disabled
// notice instead of hard-failing the prompt. pi is the mirror of codex —
// it supports Skill (which codex rejects) and rejects MCP (which codex
// renders) — so keep this distinct from the codex unsupported test.
func TestPiRenderer_MCPAndPluginUnsupported(t *testing.T) {
	cases := []canonical.Spec{
		mcpFixture(),
		{
			SchemaVersion: canonical.SchemaVersionCurrent,
			Kind:          canonical.KindPlugin,
			Plugin: &canonical.PluginSpec{
				Name:         "my-plugin",
				Version:      "1.0.0",
				Description:  "x",
				OssKey:       "capabilities/plugins/u1/my-plugin.zip",
				SHA256:       "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
				UploadSource: canonical.UploadSourceZip,
			},
		},
	}
	for _, spec := range cases {
		_, err := piRenderer{}.Render(context.Background(), spec)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("expected ErrUnsupported for kind=%s, got %v", spec.Kind, err)
		}
	}
}

// TestPiRenderer_SystemPromptShared confirms pi rides the shared
// renderSystemPrompt path (system prompt injection is in scope), emitting
// the {prompt, mode} document every scaffold uses.
func TestPiRenderer_SystemPromptShared(t *testing.T) {
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSystemPrompt,
		SystemPrompt: &canonical.SystemPromptSpec{
			Prompt: "Stay terse.",
			Mode:   canonical.SystemPromptModeAppend,
		},
	}
	out, err := piRenderer{}.Render(context.Background(), spec)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var got systemPromptDocument
	if err := json.Unmarshal(out.Content, &got); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, out.Content)
	}
	if got.Prompt != "Stay terse." {
		t.Fatalf("prompt = %q, want 'Stay terse.'", got.Prompt)
	}
}

// canonicalizeJSON re-emits raw with map keys sorted recursively, so two
// payloads that differ only in encoding/json's map iteration order
// compare equal.
func canonicalizeJSON(raw []byte) (string, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", err
	}
	out, err := json.Marshal(sortKeys(v))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func sortKeys(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		// Stable sort for determinism across go versions.
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
				keys[j-1], keys[j] = keys[j], keys[j-1]
			}
		}
		out := make([]any, 0, len(keys)*2)
		for _, k := range keys {
			out = append(out, k, sortKeys(x[k]))
		}
		return out
	case []any:
		for i, el := range x {
			x[i] = sortKeys(el)
		}
		return x
	}
	return v
}

// TestEnvValueToString_UnknownMode guards against silently rendering a
// newly-added EnvMode as an empty string.
func TestEnvValueToString_UnknownMode(t *testing.T) {
	_, err := envValueToString(canonical.EnvValue{Mode: canonical.EnvMode("magic")})
	if err == nil {
		t.Fatal("expected error for unknown env mode")
	}
}
