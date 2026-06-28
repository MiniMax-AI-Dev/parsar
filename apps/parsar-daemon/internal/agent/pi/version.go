package pi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// InstallURL points operators at the pi documentation when the daemon
// can see the adapter but not the CLI binary.
const InstallURL = "https://github.com/earendil-works/pi"

const defaultBinary = "pi"

// ErrCLINotFound is returned by CheckCLIAvailable when the binary cannot
// be located on PATH. Callers use errors.Is to distinguish an install
// problem from a present-but-broken CLI.
var ErrCLINotFound = errors.New("pi CLI not found")

// CheckCLIAvailable runs `<binary> --version` and returns the trimmed
// first line. The empty binary name defaults to "pi".
func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = defaultBinary
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
		return "", fmt.Errorf("pi --version failed: %s", msg)
	}
	out := strings.TrimSpace(stdout.String())
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	if out == "" {
		return "", fmt.Errorf("pi --version returned empty output")
	}
	return out, nil
}
