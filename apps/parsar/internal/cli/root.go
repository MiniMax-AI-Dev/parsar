// Package cli is the parsar command dispatch table.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Version is overridden at build time via -ldflags so release binaries
// embed the git tag.
var Version = "0.0.0-dev"

type command struct {
	name    string
	summary string
	run     func(ctx *runContext, args []string) error
}

type runContext struct {
	stdout io.Writer
	stderr io.Writer
	// configLoader is the test seam for env-load; nil = real loader.
	configLoader func() (Config, error)
}

func defaultRunContext() *runContext {
	return &runContext{stdout: os.Stdout, stderr: os.Stderr}
}

func (ctx *runContext) resolveConfig() (Config, error) {
	if ctx.configLoader != nil {
		return ctx.configLoader()
	}
	return loadConfigFromEnv()
}

var commands = []command{
	{name: "spec", summary: "Manage workspace spec fragments (list / add / edit / rm)", run: runSpec},
	{name: "memory", summary: "Manage user / project memories (list / add / edit / rm)", run: runMemory},
	{name: "inject", summary: "Print the injection bundle hook scripts stitch into the prompt", run: runInject},
	{name: "sync", summary: "Human-readable dump of the current injection snapshot (debug)", run: runSync},
	{name: "version", summary: "Print the CLI version and exit", run: runVersion},
}

func Execute(argv []string) error {
	return execute(defaultRunContext(), argv)
}

func execute(ctx *runContext, argv []string) error {
	if len(argv) == 0 || argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help" {
		printRootHelp(ctx.stdout)
		if len(argv) == 0 {
			return fmt.Errorf("missing subcommand")
		}
		return nil
	}
	// Hook scripts and container probes invoke `parsar --version` / `-v`.
	if argv[0] == "--version" || argv[0] == "-v" {
		return runVersion(ctx, nil)
	}
	name := argv[0]
	for _, c := range commands {
		if c.name == name {
			return c.run(ctx, argv[1:])
		}
	}
	printRootHelp(ctx.stderr)
	return fmt.Errorf("unknown subcommand %q", name)
}

func printRootHelp(w io.Writer) {
	fmt.Fprintln(w, "parsar — Parsar in-sandbox companion CLI")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage: parsar <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, c := range commands {
		fmt.Fprintf(w, "  %-9s %s\n", c.name, c.summary)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `parsar <subcommand> --help` for subcommand-specific flags.")
	fmt.Fprintln(w, "Environment: PARSAR_SERVER_URL and PARSAR_RUNNER_TOKEN are required.")
}

// newFlagSet returns a FlagSet that suppresses its own stderr output;
// errors surface via the run() return so stderr stays predictable for
// callers piping `parsar`.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func runVersion(ctx *runContext, _ []string) error {
	fmt.Fprintln(ctx.stdout, Version)
	return nil
}
