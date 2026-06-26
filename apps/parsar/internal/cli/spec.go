package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

func runSpec(ctx *runContext, args []string) error {
	if len(args) == 0 {
		printSpecHelp(ctx.stdout)
		return fmt.Errorf("spec: missing subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printSpecHelp(ctx.stdout)
		return nil
	}
	for _, sc := range specSubcommands {
		if sc.name == args[0] {
			return sc.run(ctx, args[1:])
		}
	}
	printSpecHelp(ctx.stderr)
	return fmt.Errorf("spec: unknown subcommand %q", args[0])
}

var specSubcommands = []command{
	{name: "list", summary: "List spec fragments in the workspace", run: runSpecList},
	{name: "add", summary: "Create a new spec fragment", run: runSpecAdd},
	{name: "edit", summary: "Replace title/body/tags on an existing fragment", run: runSpecEdit},
	{name: "rm", summary: "Soft-delete a fragment", run: runSpecRm},
}

func printSpecHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: parsar spec <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, sc := range specSubcommands {
		fmt.Fprintf(w, "  %-7s %s\n", sc.name, sc.summary)
	}
}

// ----- spec list ------------------------------------------------------------

func runSpecList(ctx *runContext, args []string) error {
	fs := newFlagSet("spec list")
	tagCSV := fs.String("tag", "", "comma-separated tags to filter on")
	source := fs.String("source", "", "source filter: manual | agent | import | user | auto-review")
	limit := fs.Int("limit", 0, "max rows (0 = server default)")
	jsonOut := fs.Bool("json", false, "emit JSON instead of the table")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("spec list: parse flags: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("spec list: %w", err)
	}
	rows, err := newClient(cfg).ListFragments(context.Background(), splitTags(*tagCSV), *source, *limit)
	if err != nil {
		return fmt.Errorf("spec list: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(ctx.stdout, "(no spec fragments)")
		return nil
	}
	tw := tabwriter.NewWriter(ctx.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tSOURCE\tTAGS\tUPDATED")
	for _, f := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			f.ID,
			truncate(f.Title, 60),
			f.Source,
			strings.Join(f.Tags, ","),
			f.UpdatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

// ----- spec add -------------------------------------------------------------

func runSpecAdd(ctx *runContext, args []string) error {
	fs := newFlagSet("spec add")
	title := fs.String("title", "", "fragment title (required)")
	body := fs.String("body", "", "fragment body (required; use - to read from stdin)")
	tagCSV := fs.String("tag", "", "comma-separated tags")
	jsonOut := fs.Bool("json", false, "emit JSON of the created fragment")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("spec add: parse flags: %w", err)
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("spec add: --title is required")
	}
	resolvedBody, err := resolveBody(*body)
	if err != nil {
		return fmt.Errorf("spec add: %w", err)
	}
	if resolvedBody == "" {
		return fmt.Errorf("spec add: --body is required")
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("spec add: %w", err)
	}
	frag, err := newClient(cfg).CreateFragment(context.Background(), *title, resolvedBody, splitTags(*tagCSV))
	if err != nil {
		return fmt.Errorf("spec add: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, frag)
	}
	fmt.Fprintf(ctx.stdout, "created fragment %s\n", frag.ID)
	return nil
}

// ----- spec edit ------------------------------------------------------------

func runSpecEdit(ctx *runContext, args []string) error {
	fs := newFlagSet("spec edit")
	title := fs.String("title", "", "new title (empty = keep current)")
	body := fs.String("body", "", "new body (- = read from stdin; empty = keep current)")
	tagCSV := fs.String("tag", "", "new tag set (overrides; empty string = keep)")
	clearTags := fs.Bool("clear-tags", false, "set tags to []")
	jsonOut := fs.Bool("json", false, "emit JSON of the updated fragment")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("spec edit: parse flags: %w", err)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("spec edit: expected exactly one positional <id>")
	}
	id := strings.TrimSpace(fs.Arg(0))
	if id == "" {
		return fmt.Errorf("spec edit: <id> must not be empty")
	}
	resolvedBody, err := resolveBody(*body)
	if err != nil {
		return fmt.Errorf("spec edit: %w", err)
	}
	// Server treats empty title/body as "keep current". For tags,
	// --clear-tags forces []; bare --tag "" keeps; --tag "a,b" replaces.
	var tags []string
	switch {
	case *clearTags:
		tags = []string{}
	case strings.TrimSpace(*tagCSV) != "":
		tags = splitTags(*tagCSV)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("spec edit: %w", err)
	}
	frag, err := newClient(cfg).UpdateFragment(context.Background(), id, *title, resolvedBody, tags)
	if err != nil {
		return fmt.Errorf("spec edit: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, frag)
	}
	fmt.Fprintf(ctx.stdout, "updated fragment %s\n", frag.ID)
	return nil
}

// ----- spec rm --------------------------------------------------------------

func runSpecRm(ctx *runContext, args []string) error {
	fs := newFlagSet("spec rm")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("spec rm: parse flags: %w", err)
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("spec rm: expected exactly one positional <id>")
	}
	id := strings.TrimSpace(fs.Arg(0))
	if id == "" {
		return fmt.Errorf("spec rm: <id> must not be empty")
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("spec rm: %w", err)
	}
	if err := newClient(cfg).DeleteFragment(context.Background(), id); err != nil {
		return fmt.Errorf("spec rm: %w", err)
	}
	fmt.Fprintf(ctx.stdout, "deleted fragment %s\n", id)
	return nil
}
