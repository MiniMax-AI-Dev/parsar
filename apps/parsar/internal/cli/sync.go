package cli

import (
	"context"
	"fmt"
	"strings"
)

// runSync prints a human-readable dump of the current SessionStart
// snapshot, framed with `--- section ---` headers for visual skimming.
func runSync(ctx *runContext, args []string) error {
	fs := newFlagSet("sync")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("sync: parse flags: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	snap, err := newClient(cfg).Snapshot(context.Background())
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	fmt.Fprintf(ctx.stdout, "server       : %s\n", cfg.ServerURL)
	if cfg.RuntimeID != "" {
		fmt.Fprintf(ctx.stdout, "runtime_id   : %s\n", cfg.RuntimeID)
	}
	if cfg.WorkspaceID != "" {
		fmt.Fprintf(ctx.stdout, "workspace_id : %s\n", cfg.WorkspaceID)
	}
	if cfg.UserID != "" {
		fmt.Fprintf(ctx.stdout, "user_id      : %s\n", cfg.UserID)
	}
	if cfg.ProjectID != "" {
		fmt.Fprintf(ctx.stdout, "project_id   : %s\n", cfg.ProjectID)
	}
	fmt.Fprintln(ctx.stdout)
	printSection(ctx, "spec", snap.SpecBlock)
	printSection(ctx, "memory", snap.MemoryBlock)
	printSection(ctx, "memory-write-guide", snap.MemoryWriteGuide)
	return nil
}

// printSection emits a labelled block; empty bodies render as
// "(empty)" so an operator can tell missing data from a render bug.
func printSection(ctx *runContext, name, body string) {
	fmt.Fprintf(ctx.stdout, "--- %s ---\n", name)
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(ctx.stdout, "(empty)")
	} else {
		fmt.Fprintln(ctx.stdout, body)
	}
	fmt.Fprintln(ctx.stdout)
}
