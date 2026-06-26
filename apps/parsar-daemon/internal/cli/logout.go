package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// runLogout removes the credential file for a profile. Idempotent
// so CI scripts can re-run it on every step.
//
// Logout is local-only: revoking the runtime on the server is the
// admin UI's disable-runtime button (which also kicks any live WS).
func runLogout(ctx *runContext, args []string) error {
	fs := newFlagSet("logout")
	profile := fs.String("profile", paths.DefaultProfile, "profile name to forget")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("logout: parse flags: %w", err)
	}
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("logout: %w", err)
	}

	// Stat first so we emit a distinct message for "nothing to
	// remove" vs. "removed".
	authPath, err := paths.AuthFile(*profile)
	if err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	_, statErr := os.Stat(authPath)
	missing := errors.Is(statErr, os.ErrNotExist)

	if err := auth.Delete(*profile); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	if missing {
		fmt.Fprintf(ctx.stdout, "Profile %q already had no credential — nothing to forget.\n", *profile)
	} else {
		fmt.Fprintf(ctx.stdout, "Forgot credential for profile %q (removed %s).\n", *profile, authPath)
	}
	fmt.Fprintln(ctx.stdout, "Note: this only removes local state. To revoke the runtime on the server, disable it in the admin UI.")
	return nil
}
