package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// runInject emits the bundle hook scripts pipe into platform-specific
// formats (Claude additionalContext, OpenCode message parts, ...).
func runInject(ctx *runContext, args []string) error {
	if len(args) == 0 {
		printInjectHelp(ctx.stdout)
		return fmt.Errorf("inject: missing subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printInjectHelp(ctx.stdout)
		return nil
	}
	for _, sc := range injectSubcommands {
		if sc.name == args[0] {
			return sc.run(ctx, args[1:])
		}
	}
	printInjectHelp(ctx.stderr)
	return fmt.Errorf("inject: unknown subcommand %q", args[0])
}

var injectSubcommands = []command{
	{name: "snapshot", summary: "Print the SessionStart injection bundle as JSON", run: runInjectSnapshot},
	{name: "incremental", summary: "Print the per-turn memory delta as JSON", run: runInjectIncremental},
}

func printInjectHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: parsar inject <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, sc := range injectSubcommands {
		fmt.Fprintf(w, "  %-12s %s\n", sc.name, sc.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Hook scripts use these to fetch the bundle, then format it for")
	fmt.Fprintln(w, "the platform (Claude additionalContext, OpenCode message parts, ...).")
}

func runInjectSnapshot(ctx *runContext, args []string) error {
	fs := newFlagSet("inject snapshot")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("inject snapshot: parse flags: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("inject snapshot: %w", err)
	}
	snap, err := newClient(cfg).Snapshot(context.Background())
	if err != nil {
		return fmt.Errorf("inject snapshot: %w", err)
	}
	return emitJSON(ctx.stdout, snap)
}

func runInjectIncremental(ctx *runContext, args []string) error {
	fs := newFlagSet("inject incremental")
	sinceRaw := fs.String("since", "", "RFC3339 timestamp of the last delta cursor (required)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("inject incremental: parse flags: %w", err)
	}
	if strings.TrimSpace(*sinceRaw) == "" {
		return fmt.Errorf("inject incremental: --since (RFC3339) is required")
	}
	since, err := time.Parse(time.RFC3339, *sinceRaw)
	if err != nil {
		return fmt.Errorf("inject incremental: --since must be RFC3339: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("inject incremental: %w", err)
	}
	delta, err := newClient(cfg).Incremental(context.Background(), since)
	if err != nil {
		return fmt.Errorf("inject incremental: %w", err)
	}
	return emitJSON(ctx.stdout, delta)
}
