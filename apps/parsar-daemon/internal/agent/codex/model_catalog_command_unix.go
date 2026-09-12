//go:build unix

package codex

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// SupportsTextVerbosity requires bounded cancellation of the catalog probe.
const SupportsTextVerbosity = true

func modelCatalogCommand(ctx context.Context, binary string, args ...string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	return cmd, nil
}
