package agentdaemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// TestRenderClaudeSettings: byte-level pin on the JSON we ship into
// every sandbox. The structure has to match Claude Code's hook contract
// EXACTLY (top-level "hooks" map keyed by event name, value is a list
// of matcher + command entries) — if anything drifts, hooks silently
// stop firing inside the sandbox and spec/memory injection breaks with
// no surfaced error. Asserting against parsed fields rather than raw
// bytes keeps the test resilient to incidental whitespace changes from
// json.MarshalIndent.
func TestRenderClaudeSettings(t *testing.T) {
	raw, err := renderClaudeSettings()
	if err != nil {
		t.Fatalf("renderClaudeSettings: %v", err)
	}

	var parsed claudeSettings
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("output must be valid JSON: %v\nraw:\n%s", err, raw)
	}

	want := map[string]struct {
		command string
		timeout int
	}{
		"SessionStart":     {claudeHookSessionStart, claudeSessionStartTimeoutSec},
		"UserPromptSubmit": {claudeHookUserPromptSubmit, claudeUserPromptSubmitTimeoutSec},
	}
	if len(parsed.Hooks) != len(want) {
		t.Fatalf("hook event count = %d, want %d (have %v)", len(parsed.Hooks), len(want), parsed.Hooks)
	}
	for event, w := range want {
		matchers, ok := parsed.Hooks[event]
		if !ok {
			t.Fatalf("missing hook event %q", event)
		}
		if len(matchers) != 1 || matchers[0].Matcher != "*" {
			t.Fatalf("%s: expected single matcher='*', got %+v", event, matchers)
		}
		if len(matchers[0].Hooks) != 1 {
			t.Fatalf("%s: expected exactly one hook command, got %+v", event, matchers[0].Hooks)
		}
		cmd := matchers[0].Hooks[0]
		if cmd.Type != "command" {
			t.Errorf("%s: hook type = %q, want \"command\"", event, cmd.Type)
		}
		if cmd.Command != w.command {
			t.Errorf("%s: hook command = %q, want %q", event, cmd.Command, w.command)
		}
		if cmd.Timeout != w.timeout {
			t.Errorf("%s: hook timeout = %d, want %d", event, cmd.Timeout, w.timeout)
		}
	}

	// Sanity: file is human-greppable inside a running sandbox. Indented
	// output gives operators something readable when they exec in to
	// debug; this is also documented in renderClaudeSettings godoc.
	if !strings.Contains(string(raw), "\n  ") {
		t.Errorf("expected indented JSON, got compact form:\n%s", raw)
	}
}

// TestConnectorTagFor: the env-block builder calls this once per
// Acquire; if it dropped its empty-default behaviour, every existing
// deployment (which leaves Connector zero-value because only one
// template exists today) would get an empty PARSAR_CONNECTOR and the hook
// scripts would either error out or pick the wrong inject contract.
func TestConnectorTagFor(t *testing.T) {
	cases := map[SandboxConnector]string{
		"":                       "claude",
		SandboxConnectorClaude:   "claude",
		SandboxConnectorOpenCode: "opencode",
		SandboxConnectorCodex:    "codex",
		SandboxConnectorPi:       "pi",
	}
	for in, want := range cases {
		if got := connectorTagFor(in); got != want {
			t.Errorf("connectorTagFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestConnectorForAgentKind: dispatch table from the free-form agent_kind
// string stamped in AgentConfig to the typed SandboxConnector. An
// unknown/empty kind defaults to Claude — daemon heartbeat validation is
// the real gate for unsupported kinds, so this function is a normaliser
// rather than a validator. Locking in the mapping here means a rename
// (e.g. "opencode" → "open_code") would have to touch this test too.
func TestConnectorForAgentKind(t *testing.T) {
	cases := map[string]SandboxConnector{
		"":            SandboxConnectorClaude,
		"claude_code": SandboxConnectorClaude,
		"codex":       SandboxConnectorCodex,
		"opencode":    SandboxConnectorOpenCode,
		"pi":          SandboxConnectorPi,
		"  pi  ":      SandboxConnectorPi, // TrimSpace applied
		"bogus":       SandboxConnectorClaude,
	}
	for in, want := range cases {
		if got := ConnectorForAgentKind(in); got != want {
			t.Errorf("ConnectorForAgentKind(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSeedPlatformConfig_DispatchTable: per-connector branching in the
// switch. Claude (and empty default) writes a file via the fake client;
// OpenCode/Codex/Pi are explicit no-ops until their templates land; an
// unknown connector fails loudly to avoid shipping a sandbox without
// spec/memory wiring.
func TestSeedPlatformConfig_DispatchTable(t *testing.T) {
	type scenario struct {
		name         string
		conn         SandboxConnector
		wantRunCalls int
		wantErr      bool
	}
	cases := []scenario{
		{"empty defaults to claude", "", 1, false},
		{"claude writes settings", SandboxConnectorClaude, 1, false},
		{"opencode noop until template exists", SandboxConnectorOpenCode, 0, false},
		{"codex noop until template exists", SandboxConnectorCodex, 0, false},
		{"pi noop until template exists", SandboxConnectorPi, 0, false},
		{"unknown connector errors", SandboxConnector("totally-bogus"), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFakeE2BClient()
			err := seedPlatformConfig(context.Background(), fc, e2b.Sandbox{SandboxID: "sbx-1"}, tc.conn, "")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for connector %q", tc.conn)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := len(fc.runCommands); got != tc.wantRunCalls {
				t.Fatalf("RunCommand calls = %d, want %d (commands: %v)", got, tc.wantRunCalls, fc.runCommands)
			}
		})
	}
}

// TestWriteRemoteFile_CommandShape: the actual shell command we send
// through e2b. The shape matters because (1) any drift in the mkdir/
// base64/chmod chain would silently corrupt or skip the settings.json
// write, and (2) the JSON we're injecting contains $, `, ', and {} —
// every literal-form alternative (heredoc, double-quoted echo, single-
// quoted printf) trips over at least one of those. The base64 wrapper
// is the only known-safe form; keeping a regex pin here makes drift
// obvious in code review.
func TestWriteRemoteFile_CommandShape(t *testing.T) {
	fc := newFakeE2BClient()
	body := []byte(`{"hooks": {"SessionStart": []}}`)
	if err := writeRemoteFile(context.Background(), fc, e2b.Sandbox{SandboxID: "sbx-1"},
		"/root/.claude/settings.json", body, ""); err != nil {
		t.Fatalf("writeRemoteFile: %v", err)
	}
	if len(fc.runCommands) != 1 {
		t.Fatalf("expected 1 RunCommand call, got %d", len(fc.runCommands))
	}
	cmd := fc.runCommands[0]
	encoded := base64.StdEncoding.EncodeToString(body)

	// mkdir parent directory.
	if !strings.Contains(cmd, "mkdir -p /root/.claude") {
		t.Errorf("missing mkdir -p clause: %q", cmd)
	}
	// base64 payload appears verbatim and is piped into base64 -d.
	if !strings.Contains(cmd, encoded) {
		t.Errorf("encoded payload missing from command")
	}
	if !strings.Contains(cmd, "base64 -d > /root/.claude/settings.json") {
		t.Errorf("missing base64-decode-into-target clause: %q", cmd)
	}
	// chmod sets sane perms so a non-root agent process can read it
	// (we currently run as root in sandbox, but 0644 is forward-safe).
	if !strings.Contains(cmd, "chmod 0644 /root/.claude/settings.json") {
		t.Errorf("missing chmod 0644 clause: %q", cmd)
	}
	// All three steps joined by && so a partial-failure can't ship a
	// half-written file.
	chain := regexp.MustCompile(`mkdir.+&&.+base64 -d.+&&.+chmod`)
	if !chain.MatchString(cmd) {
		t.Errorf("command should chain steps with &&, got: %q", cmd)
	}
}

// TestWriteRemoteFile_RoundTrip: feeds the recorded shell command
// through Go's own base64 decoder so we catch any payload corruption
// before the shell ever sees it. Cheaper than spinning up a real
// sandbox to verify, and the failure mode here (silent settings.json
// truncation) is exactly the one a textual-only assertion would miss.
func TestWriteRemoteFile_RoundTrip(t *testing.T) {
	fc := newFakeE2BClient()
	// Payload chosen to exercise the shell-hostile characters that
	// motivated the base64 approach: dollar, backtick, single quote,
	// double quote, embedded "EOF" (kills naive heredocs), newline.
	body := []byte("{\"k\": \"$VAR `cmd` 'q' \\\"q\\\" EOF\nx\"}")
	if err := writeRemoteFile(context.Background(), fc, e2b.Sandbox{SandboxID: "sbx-1"},
		"/tmp/test.json", body, ""); err != nil {
		t.Fatalf("writeRemoteFile: %v", err)
	}
	cmd := fc.runCommands[0]

	// Extract the base64 chunk: it's the single-quoted blob between
	// `printf '%s' '` and `' | base64 -d`.
	re := regexp.MustCompile(`printf '%s' '([A-Za-z0-9+/=]+)' \| base64 -d`)
	m := re.FindStringSubmatch(cmd)
	if len(m) != 2 {
		t.Fatalf("could not find base64 payload in command: %q", cmd)
	}
	decoded, err := base64.StdEncoding.DecodeString(m[1])
	if err != nil {
		t.Fatalf("recovered payload is not valid base64: %v", err)
	}
	if string(decoded) != string(body) {
		t.Errorf("payload round-trip mismatch:\nwant: %q\ngot:  %q", body, decoded)
	}
}

// TestWriteRemoteFile_RequiresAbsolutePath: the parent-dir mkdir uses
// strings.LastIndex to find the boundary; a relative path would compute
// the wrong parent and write to a working dir we don't control. Cheaper
// to reject up front than to debug a misplaced file inside a sandbox.
func TestWriteRemoteFile_RequiresAbsolutePath(t *testing.T) {
	fc := newFakeE2BClient()
	err := writeRemoteFile(context.Background(), fc, e2b.Sandbox{SandboxID: "sbx-1"},
		"relative/path.json", []byte("x"), "")
	if err == nil {
		t.Fatalf("expected error for relative path")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("error should mention 'absolute': %v", err)
	}
	if len(fc.runCommands) != 0 {
		t.Errorf("must not RunCommand for invalid path, got %d", len(fc.runCommands))
	}
}

// TestWriteRemoteFile_PropagatesNonZeroExit: the fake client mirrors
// the real one — non-zero status returns CommandResult{Exited:true,
// Status:"1"} with no Go-level error. writeRemoteFile must surface
// that as an error so the caller (coldStart) can Kill the half-built
// sandbox instead of declaring success.
func TestWriteRemoteFile_PropagatesNonZeroExit(t *testing.T) {
	fc := newFakeE2BClient()
	// Any substring match will trigger the error path; we use mkdir
	// because it appears in every writeRemoteFile invocation.
	fc.runErrs["mkdir"] = errors.New("permission denied")
	err := writeRemoteFile(context.Background(), fc, e2b.Sandbox{SandboxID: "sbx-1"},
		"/root/.claude/settings.json", []byte("x"), "")
	if err == nil {
		t.Fatalf("expected error on non-zero exit")
	}
	if !strings.Contains(err.Error(), "exit=") {
		t.Errorf("error should include exit status: %v", err)
	}
}
