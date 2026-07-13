package pi

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe"
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
	return versionprobe.Check(ctx, binary, versionprobe.Config{
		Name:           "pi",
		DefaultBinary:  defaultBinary,
		MissingError:   ErrCLINotFound,
		TrimBinary:     true,
		StderrFallback: true,
	})
}
