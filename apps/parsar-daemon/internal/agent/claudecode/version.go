package claudecode

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe"
)

// InstallURL points to the official Claude Code install instructions.
// Surfaced by `parsar-daemon connect` when the CLI is missing so the user
// has a clear next step instead of an opaque "exec: no such file".
const InstallURL = "https://docs.anthropic.com/claude/docs/claude-code"

// ErrCLINotFound is returned by CheckCLIAvailable when the binary
// cannot be located on PATH. Callers use errors.Is to distinguish
// "install Claude Code" from "Claude Code is broken".
var ErrCLINotFound = errors.New("claude CLI not found")

// CheckCLIAvailable runs `<binary> --version` and returns the trimmed
// first line. Empty binary defaults to "claude". On missing binary the
// error wraps ErrCLINotFound; on other failures the wrapped error
// keeps the raw stderr.
func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	return versionprobe.Check(ctx, binary, versionprobe.Config{
		Name:          "claude",
		DefaultBinary: "claude",
		MissingError:  ErrCLINotFound,
	})
}
