package agentdaemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

var errStubBuiltinLookup = errors.New("stub: builtin flag lookup failed")

// fixedSigner mints a deterministic token so tests can assert the connector
// threads the signer output into the injected env verbatim.
func fixedSigner(conversationID string) string { return "tok-" + conversationID }

func enabledConnector() *Connector {
	return &Connector{
		imHistoryEndpoint: "https://parsar.example/internal/im/history",
		imHistoryToken:    fixedSigner,
		log:               discardLogger(),
	}
}

// TestIMHistoryEnv_BuildsEnv: a configured connector produces the three env
// vars carrying the endpoint, minted token, and conversation id.
func TestIMHistoryEnv_BuildsEnv(t *testing.T) {
	c := enabledConnector()
	env, ok := c.imHistoryEnv("conv-1")
	if !ok {
		t.Fatal("expected env to be produced")
	}
	if env["PARSAR_IM_HISTORY_URL"] != "https://parsar.example/internal/im/history" {
		t.Fatalf("url env = %q", env["PARSAR_IM_HISTORY_URL"])
	}
	if env["PARSAR_IM_HISTORY_TOKEN"] != "tok-conv-1" {
		t.Fatalf("token env = %q", env["PARSAR_IM_HISTORY_TOKEN"])
	}
	if env["PARSAR_CONVERSATION_ID"] != "conv-1" {
		t.Fatalf("conv env = %q", env["PARSAR_CONVERSATION_ID"])
	}
}

// TestIMHistoryEnv_Disabled: any missing precondition disables the tool so no
// half-wired env (with an empty token or unroutable URL) ships.
func TestIMHistoryEnv_Disabled(t *testing.T) {
	cases := map[string]*Connector{
		"no endpoint":     {imHistoryToken: fixedSigner, log: discardLogger()},
		"no signer":       {imHistoryEndpoint: "https://x/y", log: discardLogger()},
		"empty token out": {imHistoryEndpoint: "https://x/y", imHistoryToken: func(string) string { return "" }, log: discardLogger()},
	}
	for name, c := range cases {
		if _, ok := c.imHistoryEnv("conv-1"); ok {
			t.Fatalf("%s: expected disabled", name)
		}
	}
	// Empty conversation id also disables (nothing to scope the token to).
	if _, ok := enabledConnector().imHistoryEnv(""); ok {
		t.Fatal("empty conversation id must disable")
	}
}

// TestApplyIMHistoryPromptInjection_Injects: the instruction is appended to
// system_prompt and the three env vars are merged into opts["env"], preserving
// any pre-existing system_prompt and env keys. Works for every agent kind
// (the injection is agent-kind agnostic).
func TestApplyIMHistoryPromptInjection_Injects(t *testing.T) {
	c := enabledConnector() // capabilities store nil => built-in defaults ON
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}
	opts := map[string]any{
		"system_prompt": "base prompt",
		"env":           map[string]any{"EXISTING": "1"},
	}
	c.applyIMHistoryPromptInjection(context.Background(), opts, in)

	sp := stringFromMap(opts, "system_prompt")
	if !strings.HasPrefix(sp, "base prompt\n\n") {
		t.Fatalf("system_prompt did not preserve base: %q", sp)
	}
	if !strings.Contains(sp, imHistoryInstruction) {
		t.Fatal("system_prompt missing instruction")
	}
	env, _ := opts["env"].(map[string]any)
	if env["EXISTING"] != "1" {
		t.Fatalf("existing env key dropped: %#v", env)
	}
	if env["PARSAR_IM_HISTORY_TOKEN"] != "tok-conv-1" {
		t.Fatalf("token env = %v", env["PARSAR_IM_HISTORY_TOKEN"])
	}
	if env["PARSAR_IM_HISTORY_URL"] != "https://parsar.example/internal/im/history" {
		t.Fatalf("url env = %v", env["PARSAR_IM_HISTORY_URL"])
	}
	if env["PARSAR_CONVERSATION_ID"] != "conv-1" {
		t.Fatalf("conv env = %v", env["PARSAR_CONVERSATION_ID"])
	}
}

// TestApplyIMHistoryPromptInjection_EmptyBasePrompt: with no pre-existing
// system_prompt the instruction becomes the whole prompt (no leading blank).
func TestApplyIMHistoryPromptInjection_EmptyBasePrompt(t *testing.T) {
	c := enabledConnector()
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}
	opts := map[string]any{}
	c.applyIMHistoryPromptInjection(context.Background(), opts, in)
	if stringFromMap(opts, "system_prompt") != imHistoryInstruction {
		t.Fatalf("system_prompt = %q", stringFromMap(opts, "system_prompt"))
	}
}

// TestApplyIMHistoryPromptInjection_SkipsOnOverride: an override_system_prompt
// fully replaces the system prompt, so the injection (both instruction and env)
// is skipped — mirroring applySpecMemoryInjection's override guard.
func TestApplyIMHistoryPromptInjection_SkipsOnOverride(t *testing.T) {
	c := enabledConnector()
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}
	opts := map[string]any{"override_system_prompt": "replace everything"}
	c.applyIMHistoryPromptInjection(context.Background(), opts, in)
	if _, ok := opts["system_prompt"]; ok {
		t.Fatal("system_prompt must not be set when override is present")
	}
	if _, ok := opts["env"]; ok {
		t.Fatal("env must not be injected when override is present")
	}
}

// TestApplyIMHistoryPromptInjection_GatedByBuiltinFlag: the per-agent built-in
// flag governs injection. OFF suppresses; ON (or no row) injects; a lookup
// error defaults to injecting (never block on bookkeeping).
func TestApplyIMHistoryPromptInjection_GatedByBuiltinFlag(t *testing.T) {
	in := connector.PromptInput{AgentID: "pa-1", ConversationID: "conv-1"}

	t.Run("disabled suppresses injection", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{
			builtinDisabled: map[string]bool{imHistoryServerName: true},
		}
		opts := map[string]any{}
		c.applyIMHistoryPromptInjection(context.Background(), opts, in)
		if _, ok := opts["system_prompt"]; ok {
			t.Fatal("must not inject when built-in flag is OFF")
		}
		if _, ok := opts["env"]; ok {
			t.Fatal("must not inject env when built-in flag is OFF")
		}
	})

	t.Run("enabled keeps injection", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{} // no disabled entries => default ON
		opts := map[string]any{}
		c.applyIMHistoryPromptInjection(context.Background(), opts, in)
		if stringFromMap(opts, "system_prompt") != imHistoryInstruction {
			t.Fatal("must inject when built-in flag is ON")
		}
	})

	t.Run("flag lookup error defaults to injecting", func(t *testing.T) {
		c := enabledConnector()
		c.capabilities = stubCapabilityStore{builtinErr: errStubBuiltinLookup}
		opts := map[string]any{}
		c.applyIMHistoryPromptInjection(context.Background(), opts, in)
		if stringFromMap(opts, "system_prompt") != imHistoryInstruction {
			t.Fatal("must inject when the flag lookup fails (never block on bookkeeping)")
		}
	})
}
