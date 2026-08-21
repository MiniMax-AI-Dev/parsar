// Package binpath resolves which executable each agent adapter should
// probe and spawn.
//
// By default every engine is looked up by bare name on PATH ("claude",
// "codex", ...). That breaks in images where PATH is not under our
// control: e2b's base image, for instance, ships its own
// /usr/local/bin entries that can shadow the ones we install, and a
// bare-name lookup then resolves to the wrong (or no) binary. The
// symptom is the worst kind — `parsar-daemon connect` reports
// "no supported agent CLI available" and the device never dials in,
// with no indication of which lookup failed.
//
// The env overrides below let an image or operator pin an absolute path
// instead. They are read in ONE place so the version probe
// (CheckCLIAvailable) and the run-time spawn (sessionConfig) can never
// disagree: a probe that succeeds against /custom/claude while the run
// spawns PATH's `claude` would advertise a capability the daemon cannot
// actually honour.
package binpath

import (
	"os"
	"strings"
)

// Env var names for the per-engine executable overrides. Empty or unset
// means "look up the default name on PATH".
const (
	EnvClaudeCode = "PARSAR_CLAUDE_BIN"
	EnvCodex      = "PARSAR_CODEX_BIN"
	EnvPi         = "PARSAR_PI_BIN"
	EnvOpenCode   = "PARSAR_OPENCODE_BIN"
)

// Default executable names, used when the matching env var is unset.
const (
	DefaultClaudeCode = "claude"
	DefaultCodex      = "codex"
	DefaultPi         = "pi"
	DefaultOpenCode   = "opencode"
)

// resolve returns the trimmed env override when set, else fallback.
func resolve(envVar, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		return v
	}
	return fallback
}

// ClaudeCode returns the claude executable to probe and spawn.
func ClaudeCode() string { return resolve(EnvClaudeCode, DefaultClaudeCode) }

// Codex returns the codex executable to probe and spawn.
func Codex() string { return resolve(EnvCodex, DefaultCodex) }

// Pi returns the pi executable to probe and spawn.
func Pi() string { return resolve(EnvPi, DefaultPi) }

// OpenCode returns the opencode executable to probe and spawn.
func OpenCode() string { return resolve(EnvOpenCode, DefaultOpenCode) }
