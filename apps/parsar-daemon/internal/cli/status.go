package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runStatus prints a one-screen profile summary. Deliberately omits
// runner_credential — that's the wire identity and showing it in
// shell history / CI logs would be a foot-gun.
func runStatus(ctx *runContext, args []string) error {
	fs := newFlagSet("status")
	profile := fs.String("profile", paths.DefaultProfile, "profile name to inspect")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("status: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("status: %w", err)
	}

	dir, err := paths.ProfileDir(*profile)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	fmt.Fprintf(ctx.stdout, "profile      : %s\n", *profile)
	fmt.Fprintf(ctx.stdout, "state dir    : %s\n", dir)

	prof, err := auth.Load(*profile)
	switch {
	case errors.Is(err, auth.ErrNotPaired):
		fmt.Fprintln(ctx.stdout, "paired       : no legacy profile (use `parsar-daemon connect --url ... --token ...`)")
	case err != nil:
		fmt.Fprintf(ctx.stdout, "paired       : ERROR — %v\n", err)
	default:
		fmt.Fprintln(ctx.stdout, "paired       : yes")
		fmt.Fprintf(ctx.stdout, "server_url   : %s\n", prof.ServerURL)
		fmt.Fprintf(ctx.stdout, "runtime_id   : %s\n", prof.RuntimeID)
		if prof.DeviceName != "" {
			fmt.Fprintf(ctx.stdout, "device_name  : %s\n", prof.DeviceName)
		}
		if prof.Hostname != "" {
			fmt.Fprintf(ctx.stdout, "hostname     : %s\n", prof.Hostname)
		}
		if !prof.PairedAt.IsZero() {
			fmt.Fprintf(ctx.stdout, "paired_at    : %s\n", prof.PairedAt.Format("2006-01-02 15:04:05 MST"))
		}
	}

	// connect.pid existence is the cheap signal; the full liveness
	// check (kill -0) would be more accurate but a bare existence
	// check is honest enough for the "paired but not connected"
	// diagnosis.
	pidPath, err := paths.PIDFile(*profile)
	if err != nil {
		return fmt.Errorf("status: resolve pid path: %w", err)
	}
	if _, err := os.Stat(pidPath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(ctx.stdout, "background   : not started (no connect.pid)")
	} else if err != nil {
		fmt.Fprintf(ctx.stdout, "background   : ERROR — %v\n", err)
	} else {
		fmt.Fprintf(ctx.stdout, "background   : pidfile present at %s\n", pidPath)
	}
	return nil
}
