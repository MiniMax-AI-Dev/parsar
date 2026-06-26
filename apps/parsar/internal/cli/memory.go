package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

func runMemory(ctx *runContext, args []string) error {
	if len(args) == 0 {
		printMemoryHelp(ctx.stdout)
		return fmt.Errorf("memory: missing subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printMemoryHelp(ctx.stdout)
		return nil
	}
	for _, sc := range memorySubcommands {
		if sc.name == args[0] {
			return sc.run(ctx, args[1:])
		}
	}
	printMemoryHelp(ctx.stderr)
	return fmt.Errorf("memory: unknown subcommand %q", args[0])
}

var memorySubcommands = []command{
	{name: "list", summary: "List user / project memories", run: runMemoryList},
	{name: "add", summary: "Create a new memory", run: runMemoryAdd},
	{name: "edit", summary: "Replace title/body/why/tags on an existing memory", run: runMemoryEdit},
	{name: "rm", summary: "Soft-delete a memory", run: runMemoryRm},
}

func printMemoryHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: parsar memory <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, sc := range memorySubcommands {
		fmt.Fprintf(w, "  %-7s %s\n", sc.name, sc.summary)
	}
}

// ----- memory list ----------------------------------------------------------

func runMemoryList(ctx *runContext, args []string) error {
	fs := newFlagSet("memory list")
	scope := fs.String("scope", "user", "scope: user | project")
	mtype := fs.String("type", "", "memory_type filter: user | feedback | project | reference")
	tagCSV := fs.String("tag", "", "comma-separated tags")
	limit := fs.Int("limit", 0, "max rows (0 = server default)")
	jsonOut := fs.Bool("json", false, "emit JSON instead of the table")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("memory list: parse flags: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("memory list: %w", err)
	}
	rows, err := newClient(cfg).ListMemories(context.Background(), *scope, *mtype, splitTags(*tagCSV), *limit)
	if err != nil {
		return fmt.Errorf("memory list: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(ctx.stdout, "(no memories)")
		return nil
	}
	tw := tabwriter.NewWriter(ctx.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tBODY\tTAGS\tUPDATED")
	for _, m := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			m.ID,
			m.MemoryType,
			truncate(m.Body, 80),
			strings.Join(m.Tags, ","),
			m.UpdatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

// ----- memory add -----------------------------------------------------------

func runMemoryAdd(ctx *runContext, args []string) error {
	fs := newFlagSet("memory add")
	scope := fs.String("scope", "user", "scope: user | project")
	mtype := fs.String("type", "", "memory_type: user | feedback | project | reference (required)")
	title := fs.String("title", "", "optional short title")
	body := fs.String("body", "", "memory body (required; use - to read from stdin)")
	why := fs.String("why", "", "rationale (recommended for feedback / project types)")
	tagCSV := fs.String("tag", "", "comma-separated tags")
	jsonOut := fs.Bool("json", false, "emit JSON of the created memory")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("memory add: parse flags: %w", err)
	}
	if strings.TrimSpace(*mtype) == "" {
		return fmt.Errorf("memory add: --type is required")
	}
	resolvedBody, err := resolveBody(*body)
	if err != nil {
		return fmt.Errorf("memory add: %w", err)
	}
	if resolvedBody == "" {
		return fmt.Errorf("memory add: --body is required")
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("memory add: %w", err)
	}
	mem, err := newClient(cfg).CreateMemory(context.Background(), *scope, *mtype, *title, resolvedBody, *why, splitTags(*tagCSV))
	if err != nil {
		return fmt.Errorf("memory add: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, mem)
	}
	fmt.Fprintf(ctx.stdout, "created memory %s\n", mem.ID)
	return nil
}

// ----- memory edit ----------------------------------------------------------

func runMemoryEdit(ctx *runContext, args []string) error {
	fs := newFlagSet("memory edit")
	title := fs.String("title", "", "new title (empty = keep current)")
	body := fs.String("body", "", "new body (- = read from stdin; empty = keep current)")
	why := fs.String("why", "", "new why (empty = keep current)")
	tagCSV := fs.String("tag", "", "new tag set (overrides; empty string = keep)")
	clearTags := fs.Bool("clear-tags", false, "set tags to []")
	jsonOut := fs.Bool("json", false, "emit JSON of the updated memory")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("memory edit: parse flags: %w", err)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("memory edit: expected exactly one positional <id>")
	}
	id := strings.TrimSpace(fs.Arg(0))
	if id == "" {
		return fmt.Errorf("memory edit: <id> must not be empty")
	}
	resolvedBody, err := resolveBody(*body)
	if err != nil {
		return fmt.Errorf("memory edit: %w", err)
	}
	var tags []string
	switch {
	case *clearTags:
		tags = []string{}
	case strings.TrimSpace(*tagCSV) != "":
		tags = splitTags(*tagCSV)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("memory edit: %w", err)
	}
	mem, err := newClient(cfg).UpdateMemory(context.Background(), id, *title, resolvedBody, *why, tags)
	if err != nil {
		return fmt.Errorf("memory edit: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, mem)
	}
	fmt.Fprintf(ctx.stdout, "updated memory %s\n", mem.ID)
	return nil
}

// ----- memory rm ------------------------------------------------------------

func runMemoryRm(ctx *runContext, args []string) error {
	fs := newFlagSet("memory rm")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("memory rm: parse flags: %w", err)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("memory rm: expected exactly one positional <id>")
	}
	id := strings.TrimSpace(fs.Arg(0))
	if id == "" {
		return fmt.Errorf("memory rm: <id> must not be empty")
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("memory rm: %w", err)
	}
	if err := newClient(cfg).DeleteMemory(context.Background(), id); err != nil {
		return fmt.Errorf("memory rm: %w", err)
	}
	fmt.Fprintf(ctx.stdout, "deleted memory %s\n", id)
	return nil
}
