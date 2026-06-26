package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runStop sends SIGTERM to the pid in ~/.parsar/parsar-daemon/<profile>/
// connect.pid, escalates to SIGKILL after killTimeout, then removes
// the pidfile. Idempotent: missing / stale pidfile cleans up and
// exits 0 — the user's goal is "no background daemon" and that holds
// either way.
func runStop(ctx *runContext, args []string) error {
	fs := newFlagSet("stop")
	profile := fs.String("profile", paths.DefaultProfile, "profile name to stop")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("stop: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pidPath, err := paths.PIDFile(*profile)
	if err != nil {
		return fmt.Errorf("stop: %w", err)
	}

	pid, err := daemonize.ReadPIDFile(pidPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintln(ctx.stdout, "parsar-daemon: no background daemon running (no pidfile)")
		return nil
	case errors.Is(err, daemonize.ErrStaleOrCorrupt):
		fmt.Fprintf(ctx.stdout, "parsar-daemon: stale pidfile detected (%v); removing\n", err)
		if rmErr := daemonize.RemovePIDFile(pidPath); rmErr != nil {
			return fmt.Errorf("stop: %w", rmErr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("stop: read pidfile: %w", err)
	}

	fmt.Fprintf(ctx.stdout, "parsar-daemon: sending SIGTERM to pid=%d\n", pid)
	if err := daemonize.SignalAndWait(pid, killTimeout); err != nil {
		return fmt.Errorf("stop: signal: %w", err)
	}
	if err := daemonize.RemovePIDFile(pidPath); err != nil {
		return fmt.Errorf("stop: remove pidfile: %w", err)
	}
	fmt.Fprintln(ctx.stdout, "parsar-daemon: stopped")
	return nil
}
