// Command parsar-bootstrap provisions the first workspace owner on a
// freshly-installed Parsar without going through HTTP. Operators who can
// run the binary against the database directly prefer this path because
// there is no shared-secret token to leak.
//
// Usage:
//
//	export DATABASE_URL="postgres://..."
//	go run ./cmd/parsar-bootstrap \
//	    --email=admin@example.com \
//	    --name="First Admin" \
//	    --workspace="Acme Corp"
//
// Refuses to run when any active workspace owner already exists
// (store.ErrBootstrapClosed → exit 2). Reset is a manual DB-level
// operation; there is no `--force` flag. On success, IDs and slug are
// printed as JSON to stdout. Writes nothing to disk.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	exitOK      = 0
	exitUsage   = 1
	exitClosed  = 2 // bootstrap already complete
	exitInvalid = 3 // operator-supplied input rejected
	exitDB      = 4 // database connect / commit failure
	exitUnknown = 5
)

func main() {
	var (
		email       string
		userName    string
		workspace   string
		databaseURL string
	)
	flag.StringVar(&email, "email", "", "owner email address (required)")
	flag.StringVar(&userName, "name", "", "owner display name (defaults to local-part of email)")
	flag.StringVar(&workspace, "workspace", "", "workspace display name (required)")
	flag.StringVar(&databaseURL, "database-url", "",
		"Postgres connection string (defaults to $DATABASE_URL)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `parsar-bootstrap — provision the first workspace owner.

Usage: parsar-bootstrap --email=... --workspace=... [--name=...] [--database-url=...]

Exit codes:
  0  success (printed JSON describes the created user + workspace)
  1  usage error (bad flags)
  2  bootstrap already complete (an active workspace owner exists)
  3  invalid operator input (rejected by the store)
  4  database connection / commit failure
  5  unknown error

This command refuses to overwrite an existing install. Recovery from
a lost owner is a manual DB operation; there is no --force flag.
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if strings.TrimSpace(databaseURL) == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	if strings.TrimSpace(databaseURL) == "" {
		fmt.Fprintln(os.Stderr, "parsar-bootstrap: DATABASE_URL is required (set env or pass --database-url)")
		os.Exit(exitUsage)
	}
	if strings.TrimSpace(email) == "" {
		fmt.Fprintln(os.Stderr, "parsar-bootstrap: --email is required")
		os.Exit(exitUsage)
	}
	if strings.TrimSpace(workspace) == "" {
		fmt.Fprintln(os.Stderr, "parsar-bootstrap: --workspace is required")
		os.Exit(exitUsage)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.OpenPool(ctx, databaseURL)
	if err != nil {
		log.Bg().Error("parsar-bootstrap: database connect failed", "error", err)
		os.Exit(exitDB)
	}
	defer pool.Close()

	st := store.New(pool)
	res, err := st.ProvisionFirstOwner(ctx, store.ProvisionFirstOwnerInput{
		Email:         email,
		Name:          userName,
		WorkspaceName: workspace,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrBootstrapClosed):
			fmt.Fprintln(os.Stderr, "parsar-bootstrap: bootstrap already complete (an active workspace owner exists)")
			os.Exit(exitClosed)
		case errors.Is(err, store.ErrInvalidWorkspaceInput):
			fmt.Fprintln(os.Stderr, "parsar-bootstrap: invalid input:", err)
			os.Exit(exitInvalid)
		default:
			log.Bg().Error("parsar-bootstrap: provisioning failed", "error", err)
			os.Exit(exitUnknown)
		}
	}

	out := map[string]any{
		"user_id":        res.UserID,
		"user_created":   res.UserCreated,
		"workspace_id":   res.WorkspaceID,
		"workspace_slug": res.WorkspaceSlug,
		"workspace_name": res.WorkspaceName,
		"member_id":      res.MemberID,
		"setup_complete": true,
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		// stdout encoding only fails on closed stdout — fatal so the
		// operator notices.
		log.Bg().Error("parsar-bootstrap: stdout encode failed", "error", err)
		os.Exit(exitUnknown)
	}
	os.Exit(exitOK)
}
