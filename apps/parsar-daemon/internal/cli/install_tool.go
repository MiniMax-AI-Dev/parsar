package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/tools"
)

func runInstallTool(ctx *runContext, args []string) error {
	if len(args) != 1 || args[0] != "codex" {
		return fmt.Errorf("install-tool: usage: parsar-daemon install-tool codex")
	}
	installCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	binary, version, err := tools.EnsureCodex(installCtx)
	if err != nil {
		return fmt.Errorf("install-tool: %w", err)
	}
	fmt.Fprintf(ctx.stdout, "codex installed: %s (%s)\n", binary, version)
	return nil
}
