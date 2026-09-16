package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/placement"
)

func runPlacement(ctx *runContext, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintln(ctx.stdout, "Usage: parsar-daemon placement enroll --container <full-id> --owner <label-value> --workspace <absolute-path>")
		fmt.Fprintln(ctx.stdout, "       parsar-daemon placement retire --container <full-id>")
		fmt.Fprintln(ctx.stdout, "Explicit operator-managed local Linux/Docker only; normal harness release is unaffected.")
		fmt.Fprintln(ctx.stdout, "Enrollment requires label parsar.runtime.placement=<owner> and the qualified private profile.")
		return nil
	}
	action := args[0]
	if action != "enroll" && action != "retire" {
		return fmt.Errorf("unknown placement action %q", action)
	}
	fs := newFlagSet("placement " + action)
	id := fs.String("container", "", "full immutable container ID")
	var owner, workspace string
	if action == "enroll" {
		fs.StringVar(&owner, "owner", "", "operator-created placement label value")
		fs.StringVar(&workspace, "workspace", "", "retained absolute host workspace path")
	}
	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return runPlacement(ctx, []string{"--help"})
		}
		return err
	}
	if fs.NArg() != 0 || *id == "" {
		return fmt.Errorf("placement %s requires --container and no positional arguments", action)
	}
	controller, err := placement.New()
	if err != nil {
		return err
	}
	operation, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var receipt *placement.Receipt
	if action == "enroll" {
		receipt, err = controller.Enroll(operation, *id, owner, workspace)
	} else {
		receipt, err = controller.Retire(operation, *id)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(ctx.stdout).Encode(receipt)
}
