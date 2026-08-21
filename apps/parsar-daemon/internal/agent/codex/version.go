package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/binpath"
)

// InstallURL points operators at the Codex install instructions when
// the daemon can see the adapter but not the CLI binary.
const InstallURL = "https://github.com/openai/codex"

// defaultBinary is the executable to probe and spawn: binpath.Codex()
// honours the PARSAR_CODEX_BIN override so a bare-name PATH lookup can
// be bypassed in images where PATH is not under our control. A function
// rather than a const so the env is read at call time.
func defaultBinary() string { return binpath.Codex() }

// ErrCLINotFound is returned by CheckCLIAvailable when the binary
// cannot be located on PATH. Callers use errors.Is to distinguish an
// install problem from a present-but-broken CLI.
var ErrCLINotFound = errors.New("codex CLI not found")

// CheckCLIAvailable runs `<binary> --version` and returns the trimmed
// first line. The empty binary name defaults to defaultBinary(). Matches
// the CheckCLIAvailable signature of the claudecode and opencode adapters
// so connect.go's preflight loop treats every engine uniformly.
func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = defaultBinary()
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
		return "", fmt.Errorf("codex --version failed: %s", msg)
	}
	out := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if out == "" {
		return "", fmt.Errorf("codex --version returned empty output")
	}
	return out, nil
}
