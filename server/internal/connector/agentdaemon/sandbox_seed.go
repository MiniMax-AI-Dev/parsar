// Sandbox seed step — write platform-specific runtime config into a
// fresh sandbox before the agent CLI inside it ever boots.
//
// settings.json content is per-sandbox (it points at hook scripts that
// need to read the runtime credentials we mint), but the hook scripts
// themselves are baked into the image. Splitting the static layer from
// the runtime layer keeps the image cacheable while still giving each
// sandbox a config pointing at the right binaries.
package agentdaemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// SandboxConnector tags which agent CLI is going to run inside the
// sandbox. The seed step picks a different config-file shape per
// platform: Claude wants ~/.claude/settings.json, OpenCode wants a
// ~/.config/opencode/config.toml, Codex wants ~/.codex/AGENTS.md
// (Codex has no per-turn hook, so its "config" is the full spec+memory
// bundle rendered into a markdown file at boot).
type SandboxConnector string

const (
	SandboxConnectorClaude   SandboxConnector = "claude"
	SandboxConnectorOpenCode SandboxConnector = "opencode"
	SandboxConnectorCodex    SandboxConnector = "codex"
	SandboxConnectorPi       SandboxConnector = "pi"
)

// In-image absolute paths to the hook scripts baked by
// infra/sandbox/Dockerfile. Keeping
// them as constants here means a Dockerfile move forces an update on
// the Go side too.
const (
	claudeHookSessionStart     = "/opt/parsar/hooks/claude/session-start.py"
	claudeHookUserPromptSubmit = "/opt/parsar/hooks/claude/user-prompt-submit.py"

	// Sandbox runs as root (IS_SANDBOX=1 in the image so Claude Code
	// accepts bypassPermissions), so $HOME resolves to /root.
	claudeSettingsPath = "/root/.claude/settings.json"

	// Hook subprocess wall-clock timeouts (seconds). SessionStart
	// fires once at boot before the agent waits for user input — 10s
	// is forgiving. UserPromptSubmit gates every turn so we hold it
	// at 5s, matching the Python hook's subprocess.run timeout.
	claudeSessionStartTimeoutSec     = 10
	claudeUserPromptSubmitTimeoutSec = 5
)

// seedPlatformConfigTimeout caps the per-call file-write Exec.
var seedPlatformConfigTimeout = 10 * time.Second

// claudeSettings is the JSON we write to claudeSettingsPath. Shape
// matches Claude Code's documented hook spec — top-level "hooks" map
// keyed by event name, value is a list of matcher + command entries.
type claudeSettings struct {
	Hooks map[string][]claudeHookMatcher `json:"hooks"`
}

type claudeHookMatcher struct {
	Matcher string          `json:"matcher"`
	Hooks   []claudeHookCmd `json:"hooks"`
}

type claudeHookCmd struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// renderClaudeSettings produces the bytes for claudeSettingsPath. Pure
// (no I/O) so tests can assert byte-for-byte. The matcher "*" matches
// every prompt — the hooks are cheap (5s timeout, fail-open) so scoping
// would double the config surface for no benefit.
func renderClaudeSettings() ([]byte, error) {
	settings := claudeSettings{
		Hooks: map[string][]claudeHookMatcher{
			"SessionStart": {{
				Matcher: "*",
				Hooks: []claudeHookCmd{{
					Type:    "command",
					Command: claudeHookSessionStart,
					Timeout: claudeSessionStartTimeoutSec,
				}},
			}},
			"UserPromptSubmit": {{
				Matcher: "*",
				Hooks: []claudeHookCmd{{
					Type:    "command",
					Command: claudeHookUserPromptSubmit,
					Timeout: claudeUserPromptSubmitTimeoutSec,
				}},
			}},
		},
	}
	return json.MarshalIndent(settings, "", "  ")
}

// ConnectorForAgentKind maps the per-agent agent_kind string (from
// AgentConfig) to the SandboxConnector enum used by the seed step.
// Unknown kinds default to Claude — the daemon's heartbeat validation
// catches unsupported kinds at prompt time, so the seed step does not
// need to be the gatekeeper.
func ConnectorForAgentKind(agentKind string) SandboxConnector {
	switch strings.TrimSpace(agentKind) {
	case "codex":
		return SandboxConnectorCodex
	case "opencode":
		return SandboxConnectorOpenCode
	case "pi":
		return SandboxConnectorPi
	default:
		// claude_code, "", and anything unknown → Claude
		return SandboxConnectorClaude
	}
}

// seedPlatformConfig writes the runtime config file the agent CLI
// inside the sandbox reads on boot. Called from coldStart after
// e2b.Create succeeds and BEFORE parsar-daemon connect — Claude Code
// reads settings.json the moment it spawns.
//
// An unknown connector returns an error rather than silently no-op'ing
// — better to fail acquire loudly than ship a sandbox with no spec/memory
// injection wired up.
func seedPlatformConfig(ctx context.Context, client E2BClient, sb e2b.Sandbox, conn SandboxConnector, envdURL string) error {
	switch conn {
	case SandboxConnectorClaude, "":
		// Empty defaults to Claude because the only template currently
		// boots Claude (parsar-daemon-claudecode).
		return seedClaudeConfig(ctx, client, sb, envdURL)
	case SandboxConnectorOpenCode:
		// TODO: wire spec/memory injection via OpenCode hook scripts
		// at /opt/parsar/hooks/opencode/. CLI binary is available in
		// the image; daemon discovers and registers it via heartbeat.
		return nil
	case SandboxConnectorCodex:
		// TODO: Codex has no hook surface — render spec+memory into
		// ~/.codex/AGENTS.md at boot. CLI binary is available in the
		// image; daemon discovers and registers it via heartbeat.
		return nil
	case SandboxConnectorPi:
		// TODO: wire spec/memory injection for pi. CLI binary is
		// available in the image; daemon discovers and registers it
		// via heartbeat.
		return nil
	default:
		return fmt.Errorf("sandbox_seed: unknown connector %q", conn)
	}
}

// seedClaudeConfig is the Claude-specific arm of seedPlatformConfig,
// split out so the unit test can call it directly.
func seedClaudeConfig(ctx context.Context, client E2BClient, sb e2b.Sandbox, envdURL string) error {
	body, err := renderClaudeSettings()
	if err != nil {
		return fmt.Errorf("sandbox_seed claude: render: %w", err)
	}
	return writeRemoteFile(ctx, client, sb, claudeSettingsPath, body, envdURL)
}

// connectorTagFor maps the SandboxConnector enum to the string value
// the agent CLI inside the sandbox expects on PARSAR_CONNECTOR. The
// empty-default-to-claude mirrors seedPlatformConfig.
func connectorTagFor(conn SandboxConnector) string {
	if conn == "" {
		return string(SandboxConnectorClaude)
	}
	return string(conn)
}

// writeRemoteFile drops a byte payload onto the sandbox filesystem via
// RunCommand. Base64-encode the payload then shell-decode into the
// target path — sidesteps every shell-quoting landmine a literal heredoc
// would step on (JSON contains $, backticks, single quotes, and a
// heredoc <<'EOF' still chokes on a literal EOF in the data).
//
// The mkdir + redirect + chmod chain is one shell pipeline so a failure
// at any step yields a non-zero exit.
func writeRemoteFile(ctx context.Context, client E2BClient, sb e2b.Sandbox, path string, body []byte, envdURL string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("sandbox_seed write: path must be absolute, got %q", path)
	}
	idx := strings.LastIndex(path, "/")
	parent := path[:idx]
	if parent == "" {
		// Writing to "/foo" — parent "/" is a no-op mkdir.
		parent = "/"
	}

	encoded := base64.StdEncoding.EncodeToString(body)
	cmd := fmt.Sprintf("mkdir -p %s && printf '%%s' '%s' | base64 -d > %s && chmod 0644 %s",
		shellSingleQuote(parent), encoded, shellSingleQuote(path), shellSingleQuote(path))

	timeoutCtx, cancel := context.WithTimeout(ctx, seedPlatformConfigTimeout)
	defer cancel()
	res, err := client.RunCommand(timeoutCtx, e2b.RunCommandInput{
		Sandbox: sb,
		Command: cmd,
		Timeout: seedPlatformConfigTimeout,
		EnvdURL: envdURL,
	})
	if err != nil {
		return fmt.Errorf("sandbox_seed write %s: %w", path, err)
	}
	if !res.Exited || res.Status != "0" {
		return fmt.Errorf("sandbox_seed write %s: exit=%q stderr=%q",
			path, res.Status, strings.TrimSpace(res.Stderr))
	}
	return nil
}
