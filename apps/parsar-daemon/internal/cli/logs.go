package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runLogs tails ~/.parsar/parsar-daemon/<profile>/connect.log. -f streams
// new bytes; -n sets trailing-line history (default 100). Missing log
// file surfaces an actionable hint instead of a path error.
func runLogs(ctx *runContext, args []string) error {
	fs := newFlagSet("logs")
	var (
		profile = fs.String("profile", paths.DefaultProfile, "profile name whose log to tail")
		follow  = fs.Bool("f", false, "follow the log (like `tail -f`)")
		lines   = fs.Int("n", 100, "print the last N lines before optionally following")
	)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("logs: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("logs: %w", err)
	}

	logPath, err := paths.LogFile(*profile)
	if err != nil {
		return fmt.Errorf("logs: %w", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(ctx.stderr, "parsar-daemon: no log file yet at %s\n", logPath)
			fmt.Fprintln(ctx.stderr, "  Start the daemon with `parsar-daemon connect -b` first.")
			return fmt.Errorf("logs: log file does not exist")
		}
		return fmt.Errorf("logs: stat: %w", err)
	}

	// Wire SIGINT so Ctrl-C exits the follow loop cleanly.
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-rootCtx.Done():
		}
		signal.Stop(sigCh)
	}()

	return daemonize.Tail(logPath, daemonize.TailOptions{
		LastLines: *lines,
		Follow:    *follow,
	}, rootCtx.Done(), ctx.stdout)
}
