package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
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
	if binary == "" {
		binary = "claude"
	}

	if _, lookErr := exec.LookPath(binary); lookErr != nil {
		return "", fmt.Errorf("%w: %s", ErrCLINotFound, binary)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, "--version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("claude --version failed: %s", msg)
	}

	out := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if out == "" {
		return "", fmt.Errorf("claude --version returned empty output")
	}
	return out, nil
}
